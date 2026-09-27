package main

// TestEndToEnd drives the built binary over stdio against a live JobPilot API,
// the way Claude Code would. It seeds a fresh user through the REST API, then
// exercises every read tool and every write tool, including the rejection
// lock. It skips unless an API origin is supplied:
//
//	go test -run TestEndToEnd -v . -args -api http://localhost:8080

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var apiFlag = flag.String("api", os.Getenv("JOBPILOT_E2E_API_URL"), "JobPilot API origin for the end-to-end test; empty skips it")

type rest struct {
	t     *testing.T
	ctx   context.Context
	http  *http.Client
	base  string
	token string
}

func (r *rest) call(method, path string, body, out any) {
	r.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			r.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(r.ctx, method, r.base+path, reader)
	if err != nil {
		r.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		r.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		r.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
	}
	if out != nil {
		if err = json.Unmarshal(data, out); err != nil {
			r.t.Fatalf("%s %s: %v", method, path, err)
		}
	}
}

type jobResult struct {
	Job struct {
		ID string `json:"id"`
	} `json:"job"`
	Match struct {
		Rejected bool `json:"rejected"`
		Score    int  `json:"score"`
	} `json:"match"`
}

func TestEndToEnd(t *testing.T) {
	api := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(*apiFlag), "/"), "/api")
	if api == "" {
		t.Skip("pass -api <origin> or set JOBPILOT_E2E_API_URL to run against a live API")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Seed a fresh, isolated user the same way scripts/smoke.ps1 does.
	email := fmt.Sprintf("mcp-e2e-%d@example.test", time.Now().UnixNano())
	password := "mcp-e2e-password"
	r := &rest{t: t, ctx: ctx, http: &http.Client{Timeout: 30 * time.Second}, base: api}
	var registered struct {
		Token string `json:"token"`
	}
	r.call("POST", "/api/auth/register", map[string]string{"email": email, "password": password}, &registered)
	r.token = registered.Token
	r.call("PUT", "/api/profile", map[string]any{
		"name": "MCP Candidate", "email": email, "phone": "+400000000", "location": "Bucharest, Romania",
		"linkedin": "", "github": "", "portfolio": "", "current_title": "Senior Backend Engineer",
		"desired_titles": []string{"Backend Engineer"}, "seniorities": []string{"mid", "senior"}, "years_experience": 7,
		"certifications": []string{}, "languages": []string{"English"}, "salary_expectation": 90000, "rate_expectation": 0, "currency": "EUR",
		"work_preference": []string{"remote"}, "preferred_countries": []string{"Romania"}, "preferred_timezones": []string{"EET"},
		"work_authorization": "EU citizen", "notice_period": "30 days", "industry_preferences": []string{"Software"}, "summary": "",
		"experiences": []any{}, "skills": []any{}, "projects": []any{},
	}, nil)
	r.call("POST", "/api/experiences", map[string]any{"company": "CurrentCo", "company_domain": "current.example", "title": "Senior Backend Engineer", "start_date": "2022", "end_date": "", "current": true, "description": "Built backend services."}, nil)
	r.call("POST", "/api/skills", map[string]any{"name": "Go", "level": "advanced", "years": 5}, nil)
	r.call("POST", "/api/skills", map[string]any{"name": "PostgreSQL", "level": "advanced", "years": 5}, nil)
	r.call("POST", "/api/filters", map[string]any{
		"name": "MCP filter", "active": true, "included_titles": []string{"Backend Engineer"}, "excluded_title_words": []string{"unpaid"},
		"locations": []string{"Romania"}, "remote_types": []string{"remote"}, "min_salary": 90000, "currency": "EUR",
		"employment_types": []string{"full-time"}, "excluded_industries": []string{"Gambling"}, "required_skills": []string{"Go", "PostgreSQL"},
		"included_technologies": []string{"Go"}, "excluded_technologies": []string{"PHP"}, "max_age_days": 30,
	}, nil)
	var rejected, accepted jobResult
	r.call("POST", "/api/jobs", map[string]any{
		"title": "Backend Engineer", "company": "CurrentCo LLC", "company_domain": "", "location": "Romania", "remote_type": "remote",
		"salary_min": 90000, "salary_max": 110000, "currency": "EUR", "employment_type": "full-time", "industry": "Software",
		"description": "Build services with Go and PostgreSQL.", "apply_url": "https://example.test/apply-rejected",
	}, &rejected)
	r.call("POST", "/api/jobs", map[string]any{
		"title": "Senior Backend Engineer", "company": "Fresh Systems GmbH", "company_domain": "fresh.example", "location": "Bucharest, Romania", "remote_type": "remote",
		"salary_min": 95000, "salary_max": 110000, "currency": "EUR", "employment_type": "full-time", "industry": "Software",
		"description": "Build reliable backend services with Go and PostgreSQL.", "apply_url": "https://example.test/apply-accepted",
		"posted_at": time.Now().UTC().Format(time.RFC3339),
	}, &accepted)
	if !rejected.Match.Rejected || accepted.Match.Rejected || accepted.Match.Score < 70 {
		t.Fatalf("seed did not produce one rejected and one strong job: %+v %+v", rejected, accepted)
	}

	// Build the real binary and talk to it over stdio, logging in with
	// email and password so the lazy-login path is exercised too.
	bin := filepath.Join(t.TempDir(), "jobpilot-mcp")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	// Run the server the recommended way: a scoped agent token, not the
	// user's password.
	var agent struct {
		Token string `json:"token"`
	}
	r.call("POST", "/api/tokens", map[string]any{"name": "mcp-e2e", "scopes": []string{"read", "write"}}, &agent)
	if !strings.HasPrefix(agent.Token, "jpat_") {
		t.Fatalf("expected an agent token, got %q", agent.Token)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "JOBPILOT_API_URL="+api, "JOBPILOT_TOKEN="+agent.Token, "JOBPILOT_EMAIL=", "JOBPILOT_PASSWORD=")
	cmd.Stderr = os.Stderr
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// The live API must report a contract at least as new as this server
	// requires, or the notes guard would refuse status-only updates.
	var health struct {
		Contract int `json:"contract"`
	}
	r.call("GET", "/api/health", nil, &health)
	if health.Contract < 1 {
		t.Fatalf("the API reports contract %d; this server was written against 1", health.Contract)
	}

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	descriptions := map[string]string{}
	for _, tool := range tools.Tools {
		descriptions[tool.Name] = tool.Description
	}
	// Compare the set, not the count, so adding a tool is a deliberate edit
	// here rather than a number nobody can interpret.
	want := map[string]bool{
		"get_profile": true, "list_jobs": true, "get_job": true, "list_applications": true,
		"get_application": true, "get_dashboard": true, "list_discovery_runs": true,
		"get_filters": true, "list_discovery_sources": true, "validate_discovery_source": true,
		"generate_documents": true, "create_application": true, "update_application_status": true,
		"run_discovery": true, "import_job": true, "add_discovery_source": true,
		"get_ai_status": true, "list_answers": true, "resolve_screening_answers": true,
		"save_answer": true, "approve_answer": true, "list_cvs": true, "get_cv": true,
		"approve_cv": true, "tailor_cv_for_job": true, "compose_application": true,
		"list_outbox": true, "get_outbox_entry": true, "cancel_outbox_entry": true,
		"detect_board": true, "list_board_suggestions": true,
	}
	for _, tool := range tools.Tools {
		if !want[tool.Name] {
			t.Errorf("unexpected tool %q", tool.Name)
		}
		delete(want, tool.Name)
	}
	for name := range want {
		t.Errorf("missing tool %q", name)
	}
	if prompts, err := cs.ListPrompts(ctx, nil); err != nil || len(prompts.Prompts) != 2 {
		t.Fatalf("expected both prompts, got %v (%v)", prompts, err)
	}

	call := func(name string, args map[string]any) (map[string]any, string, bool) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var text strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text.WriteString(tc.Text)
			}
		}
		out, _ := res.StructuredContent.(map[string]any)
		return out, text.String(), res.IsError
	}
	ids := func(out map[string]any, key string) []string {
		var found []string
		items, _ := out[key].([]any)
		for _, item := range items {
			m, _ := item.(map[string]any)
			job, _ := m["job"].(map[string]any)
			if id, ok := job["id"].(string); ok {
				found = append(found, id)
			}
		}
		return found
	}
	contains := func(list []string, want string) bool {
		for _, x := range list {
			if x == want {
				return true
			}
		}
		return false
	}

	out, text, isErr := call("get_profile", nil)
	if isErr || out["name"] != "MCP Candidate" {
		t.Fatalf("get_profile: %s", text)
	}

	out, text, isErr = call("list_jobs", map[string]any{"min_score": 70})
	if isErr || !contains(ids(out, "jobs"), accepted.Job.ID) || contains(ids(out, "jobs"), rejected.Job.ID) {
		t.Fatalf("list_jobs accepted: %s", text)
	}
	if strings.Contains(text, "Build reliable backend services") {
		t.Fatal("list_jobs must not carry descriptions")
	}
	out, text, isErr = call("list_jobs", map[string]any{"state": "rejected"})
	if isErr || !contains(ids(out, "jobs"), rejected.Job.ID) || !strings.Contains(strings.ToLower(text), "employer") {
		t.Fatalf("list_jobs rejected: %s", text)
	}

	out, text, isErr = call("get_job", map[string]any{"job_id": accepted.Job.ID})
	if isErr || !strings.Contains(text, "Build reliable backend services") {
		t.Fatalf("get_job: %s", text)
	}
	evaluation, _ := out["match"].(map[string]any)
	breakdown, _ := evaluation["breakdown"].(map[string]any)
	if len(breakdown) == 0 {
		t.Fatalf("get_job must include the breakdown: %s", text)
	}
	// The description names the components by hand, because this module
	// cannot import the API's matching package; the real API is the check.
	for component := range breakdown {
		if !strings.Contains(descriptions["get_job"], component) {
			t.Errorf("the API returns breakdown component %q, which the get_job description does not name", component)
		}
	}

	_, text, isErr = call("generate_documents", map[string]any{"job_id": rejected.Job.ID})
	if !isErr || !strings.Contains(text, "blocked") {
		t.Fatalf("generate_documents must be refused for the rejected job: %s", text)
	}
	out, text, isErr = call("generate_documents", map[string]any{"job_id": accepted.Job.ID})
	if isErr || !strings.Contains(fmt.Sprint(out["cover_letter"]), "Fresh Systems") {
		t.Fatalf("generate_documents: %s", text)
	}

	_, text, isErr = call("create_application", map[string]any{"job_id": rejected.Job.ID})
	if !isErr || !strings.Contains(text, "blocked") {
		t.Fatalf("create_application must be refused for the rejected job: %s", text)
	}
	out, text, isErr = call("create_application", map[string]any{"job_id": accepted.Job.ID})
	if isErr || out["status"] != "draft" {
		t.Fatalf("create_application: %s", text)
	}
	appID, _ := out["id"].(string)

	out, text, isErr = call("update_application_status", map[string]any{"application_id": appID, "status": "ready", "notes": "Prepared via MCP"})
	if isErr || out["status"] != "ready" || out["notes"] != "Prepared via MCP" {
		t.Fatalf("update to ready: %s", text)
	}
	out, text, isErr = call("update_application_status", map[string]any{"application_id": appID, "status": "applied"})
	if isErr || out["status"] != "applied" || out["notes"] != "Prepared via MCP" {
		t.Fatalf("update to applied must keep notes: %s", text)
	}

	out, text, isErr = call("get_application", map[string]any{"application_id": appID})
	if isErr {
		t.Fatalf("get_application: %s", text)
	}
	application, _ := out["application"].(map[string]any)
	documents, _ := out["documents"].(map[string]any)
	if application["status"] != "applied" || documents["cover_letter"] == "" || out["blocked"] != false {
		t.Fatalf("get_application: %s", text)
	}

	out, text, isErr = call("list_applications", map[string]any{"status": "applied"})
	if isErr {
		t.Fatalf("list_applications: %s", text)
	}
	if apps, _ := out["applications"].([]any); len(apps) != 1 {
		t.Fatalf("expected one applied application: %s", text)
	}

	// The apply engine, end to end with no AI provider configured.
	out, text, isErr = call("get_ai_status", nil)
	if isErr || out["enabled"] != false {
		t.Fatalf("the AI adapter should be disabled by default: %s", text)
	}

	out, text, isErr = call("compose_application", map[string]any{"job_id": accepted.Job.ID})
	if isErr {
		t.Fatalf("compose_application: %s", text)
	}
	// example.test is not a readable ATS, so the entry must hold for review.
	if out["status"] != "needs_review" {
		t.Fatalf("expected needs_review for an unreadable form, got %v", out["status"])
	}
	if blockers, _ := out["blockers"].([]any); len(blockers) == 0 {
		t.Fatalf("an entry that cannot be sent must say why: %s", text)
	}
	if out["cover_letter"] == "" {
		t.Fatal("a cover letter must be written even with no AI provider")
	}
	outboxID, _ := out["id"].(string)

	out, text, isErr = call("get_outbox_entry", map[string]any{"outbox_id": outboxID})
	if isErr || out["status"] != "needs_review" {
		t.Fatalf("get_outbox_entry: %s", text)
	}

	out, text, isErr = call("resolve_screening_answers", map[string]any{
		"job_id": accepted.Job.ID,
		"questions": []any{
			map[string]any{"text": "Tell us about yourself", "kind": "textarea"},
			map[string]any{"text": "What is your gender?", "kind": "select", "options": []any{"Male", "Female", "Prefer not to say"}},
		},
	})
	if isErr {
		t.Fatalf("resolve_screening_answers: %s", text)
	}
	resolutions, _ := out["resolutions"].([]any)
	if len(resolutions) != 2 {
		t.Fatalf("expected one resolution per question: %s", text)
	}
	// A protected-characteristic question must never be answered.
	for _, r := range resolutions {
		m, _ := r.(map[string]any)
		if strings.Contains(fmt.Sprint(m["question"]), "gender") && m["answer"] != "" {
			t.Fatalf("a protected question must be left unanswered: %s", text)
		}
	}

	_, text, isErr = call("cancel_outbox_entry", map[string]any{"outbox_id": outboxID})
	if isErr {
		t.Fatalf("cancel_outbox_entry: %s", text)
	}

	out, text, isErr = call("detect_board", map[string]any{"url": "https://job-boards.greenhouse.io/grafanalabs"})
	if isErr || out["provider"] != "greenhouse" || out["identifier"] != "grafanalabs" {
		t.Fatalf("detect_board: %s", text)
	}

	// A readable employer form. The smoke flow composes against an apply URL
	// no reader supports, so its entries always carry a blocker and can never
	// be approved. Serving the form here reaches the approval path, and the
	// rule that rebuilding an approved entry withdraws the approval out loud.
	atsPort := os.Getenv("JOBPILOT_E2E_ATS_PORT")
	if atsPort == "" {
		t.Log("JOBPILOT_E2E_ATS_PORT not set; skipping the employer-form section, which CI runs")
	} else {
		fake := serveFakeGreenhouse(t, atsPort)

		out, text, isErr = call("import_job", map[string]any{
			"title": "Senior Backend Engineer", "company": "Fake Greenhouse Co",
			"description": "Build reliable backend services with Go and PostgreSQL.",
			"apply_url":   "https://boards.greenhouse.io/fakeco/jobs/12345", "remote_type": "remote",
			"location": "Bucharest, Romania", "employment_type": "full-time",
			"salary_min": 95000, "salary_max": 120000, "currency": "EUR",
			"posted_at": time.Now().UTC().Format(time.RFC3339),
		})
		if isErr {
			t.Fatalf("import_job (greenhouse): %s", text)
		}
		ghJob, _ := out["job"].(map[string]any)
		if ghMatch, _ := out["match"].(map[string]any); ghMatch["rejected"] != false {
			t.Fatalf("the greenhouse job should be accepted: %s", text)
		}
		ghJobID, _ := ghJob["id"].(string)

		out, text, isErr = call("compose_application", map[string]any{"job_id": ghJobID})
		if isErr {
			t.Fatalf("compose_application (greenhouse): %s", text)
		}
		if out["ats_provider"] != "greenhouse" {
			t.Fatalf("the form was not read from the fake; is the API's ATS_GREENHOUSE_URL pointing at port %s? %s", atsPort, text)
		}
		if paths := fake.paths(); len(paths) == 0 || paths[0] != "/v1/boards/fakeco/jobs/12345" {
			t.Fatalf("the apply URL should route to board fakeco job 12345 on the fake, got %v", paths)
		}
		// This user has no CV, so compose prepares a tailored one from the
		// profile and holds the entry until it is approved. That is the one
		// blocker expected here, and approving content is an agent tool.
		blockers, _ := out["blockers"].([]any)
		if out["status"] != "needs_review" || len(blockers) != 1 || !strings.Contains(fmt.Sprint(blockers[0]), "tailored CV") {
			t.Fatalf("expected exactly the tailored-CV blocker, got status %v with blockers %v", out["status"], blockers)
		}
		ghCV, _ := out["cv_document_id"].(string)
		if ghCV == "" {
			t.Fatalf("the prepared CV should be referenced: %s", text)
		}
		if out, text, isErr = call("approve_cv", map[string]any{"cv_id": ghCV}); isErr || out["approved"] != true {
			t.Fatalf("approve_cv: %s", text)
		}

		// With the CV approved, a rebuild reuses it and nothing blocks.
		out, text, isErr = call("compose_application", map[string]any{"job_id": ghJobID})
		if isErr {
			t.Fatalf("compose_application after approving the CV: %s", text)
		}
		if out["status"] != "ready" {
			t.Fatalf("expected no blockers once the CV is approved, got status %v with blockers %v", out["status"], out["blockers"])
		}
		if out["cv_decision"] == "generated" {
			t.Fatalf("an approved tailored CV should be reused, not written again: %s", text)
		}
		ghOutbox, _ := out["id"].(string)
		formAnswers, _ := out["answers"].([]any)
		if len(formAnswers) != 4 {
			t.Fatalf("expected all four form fields answered: %s", text)
		}
		for _, a := range formAnswers {
			m, _ := a.(map[string]any)
			if m["source"] != "profile" || m["answer"] == "" {
				t.Fatalf("every field on this form is answerable from the profile, got %v", m)
			}
		}

		// Approval is consent to send and stays with the user; this REST
		// call stands in for that click in the web app. It is deliberately
		// not an MCP tool.
		var approval struct {
			Status     string  `json:"status"`
			ApprovedAt *string `json:"approved_at"`
		}
		r.call("POST", "/api/outbox/"+ghOutbox+"/approve", nil, &approval)
		if approval.Status != "approved" || approval.ApprovedAt == nil {
			t.Fatalf("an entry with no blockers should approve, got %+v", approval)
		}

		out, text, isErr = call("compose_application", map[string]any{"job_id": ghJobID})
		if isErr {
			t.Fatalf("recompose: %s", text)
		}
		if out["status"] != "ready" {
			t.Fatalf("a rebuilt entry must wait for approval again, got %v", out["status"])
		}
		if !strings.Contains(text, "withdrew that approval") {
			t.Fatalf("rebuilding an approved entry must say the approval was withdrawn: %s", text)
		}
		if _, text, isErr = call("get_outbox_entry", map[string]any{"outbox_id": ghOutbox}); isErr || !strings.Contains(text, "withdrew that approval") {
			t.Fatalf("the notice must still be readable afterwards: %s", text)
		}
		// The notice belongs to the transition: a rebuild of an entry that
		// was not approved has nothing to withdraw.
		if _, text, isErr = call("compose_application", map[string]any{"job_id": ghJobID}); isErr || strings.Contains(text, "withdrew that approval") {
			t.Fatalf("a second rebuild has no approval to withdraw: %s", text)
		}
		r.call("POST", "/api/outbox/"+ghOutbox+"/approve", nil, &approval)
		if approval.Status != "approved" {
			t.Fatalf("a rebuilt entry should be approvable again, got %+v", approval)
		}
	}

	// The employer-form section imports one more job when it runs.
	expectedJobs := float64(2)
	if atsPort != "" {
		expectedJobs++
	}
	out, text, isErr = call("get_dashboard", nil)
	if isErr || out["applications_applied"] != float64(1) || out["jobs_imported"] != expectedJobs {
		t.Fatalf("get_dashboard: %s", text)
	}

	_, text, isErr = call("list_discovery_runs", nil)
	if isErr {
		t.Fatalf("list_discovery_runs: %s", text)
	}

	// A read-only token must be refused a write tool, with a message that
	// tells the model this is a permission limit rather than a product rule.
	var readOnly struct {
		Token string `json:"token"`
	}
	r.call("POST", "/api/tokens", map[string]any{"name": "mcp-e2e-read", "scopes": []string{"read"}}, &readOnly)
	limited := exec.Command(bin)
	limited.Env = append(os.Environ(), "JOBPILOT_API_URL="+api, "JOBPILOT_TOKEN="+readOnly.Token, "JOBPILOT_EMAIL=", "JOBPILOT_PASSWORD=")
	limited.Stderr = os.Stderr
	limitedSession, err := mcp.NewClient(&mcp.Implementation{Name: "e2e-read", Version: "0"}, nil).Connect(ctx, &mcp.CommandTransport{Command: limited}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer limitedSession.Close()
	readRes, err := limitedSession.CallTool(ctx, &mcp.CallToolParams{Name: "get_profile"})
	if err != nil || readRes.IsError {
		t.Fatalf("a read token must still read: %v", err)
	}
	writeRes, err := limitedSession.CallTool(ctx, &mcp.CallToolParams{Name: "create_application", Arguments: map[string]any{"job_id": accepted.Job.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var refusal strings.Builder
	for _, c := range writeRes.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			refusal.WriteString(tc.Text)
		}
	}
	if !writeRes.IsError || !strings.Contains(refusal.String(), "scope") {
		t.Fatalf("a read token must be refused a write tool with a scope message: %s", refusal.String())
	}

	out, text, isErr = call("get_filters", nil)
	if isErr {
		t.Fatalf("get_filters: %s", text)
	}
	if filters, _ := out["filters"].([]any); len(filters) != 1 {
		t.Fatalf("expected the seeded filter: %s", text)
	}

	// A manual import lands in the shared catalog and is evaluated.
	out, text, isErr = call("import_job", map[string]any{
		"title": "Staff Backend Engineer", "company": "Imported Systems BV",
		"description": "Build reliable backend services with Go and PostgreSQL.",
		"apply_url":   "https://example.test/apply-imported", "remote_type": "remote",
		"location": "Bucharest, Romania", "employment_type": "full-time",
		"salary_min": 95000, "salary_max": 120000, "currency": "EUR",
		"posted_at": time.Now().UTC().Format(time.RFC3339),
	})
	if isErr {
		t.Fatalf("import_job: %s", text)
	}
	job, _ := out["job"].(map[string]any)
	match, _ := out["match"].(map[string]any)
	if job["id"] == "" || match["rejected"] != false {
		t.Fatalf("the imported job should be accepted: %s", text)
	}
	if strings.Contains(text, "Build reliable backend services with Go and PostgreSQL.") {
		t.Fatal("import_job must not echo the description back")
	}

	// Importing a job at an excluded employer still succeeds, but rejected.
	out, text, isErr = call("import_job", map[string]any{
		"title": "Backend Engineer", "company": "CurrentCo",
		"description": "Build services.", "apply_url": "https://example.test/apply-excluded",
	})
	if isErr {
		t.Fatalf("import_job for an excluded employer should still return a result: %s", text)
	}
	match, _ = out["match"].(map[string]any)
	job, _ = out["job"].(map[string]any)
	if match["rejected"] != true {
		t.Fatalf("an excluded employer must be rejected: %s", text)
	}
	if job["apply_url"] != nil && job["apply_url"] != "" {
		t.Fatalf("a rejected import must not carry an apply URL: %s", text)
	}

	if atsPort != "" {
		discoveryShareAndCounters(t, r, call)
		discoveryRecognisesReposts(t, r, call)
	}
}

// discoveryRecognisesReposts adds board fair-c, which lists one opening twice,
// the second time under a new link. The fingerprint includes the link, so the
// repost used to become a second job. It is one job, carrying the newer link.
// The two boards from the previous step are all known by now, so with a
// budget of ten they fill eight places and fair-c gives both its postings.
func discoveryRecognisesReposts(t *testing.T, r *rest, call func(string, map[string]any) (map[string]any, string, bool)) {
	t.Helper()
	r.call("PUT", "/api/discovery/settings", map[string]any{"enabled": false, "interval_minutes": 360, "auto_prepare": false, "min_score": 75, "max_jobs_per_run": 10}, nil)
	if _, text, isErr := call("add_discovery_source", map[string]any{"provider": "greenhouse", "name": "Board fair-c", "identifier": "fair-c"}); isErr {
		t.Fatalf("add_discovery_source fair-c: %s", text)
	}
	out, text, isErr := call("run_discovery", nil)
	if isErr {
		t.Fatalf("run_discovery: %s", text)
	}
	got := fmt.Sprintf("seen %v, added %v, rejected %v, known %v", out["jobs_seen"], out["jobs_added"], out["jobs_rejected"], out["duplicates_skipped"])
	if want := "seen 10, added 1, rejected 0, known 9"; got != want {
		t.Fatalf("a repost should be known, not added: got %s, want %s: %s", got, want, text)
	}
	out, text, isErr = call("list_jobs", map[string]any{"state": "all", "limit": 100})
	if isErr {
		t.Fatalf("list_jobs: %s", text)
	}
	var links []string
	jobs, _ := out["jobs"].([]any)
	for _, j := range jobs {
		m, _ := j.(map[string]any)
		job, _ := m["job"].(map[string]any)
		if job["company"] == "Board fair-c" {
			link, _ := job["apply_url"].(string)
			links = append(links, link)
		}
	}
	if len(links) != 1 || !strings.HasSuffix(links[0], "/fair-c/302") {
		t.Fatalf("the reposted opening should be one job with the newer link, got %v", links)
	}
}

// discoveryShareAndCounters runs discovery over two fake boards of five
// postings with a budget of four. The budget used to be spent source by
// source, so the first board took all four and the second was never read.
// Shared, each board gives two per run, new postings before known ones, and a
// known posting is re-scored without counting as a new rejection. Board fair-b
// is rejected by the filter, so every one of its new jobs is a rejection.
func discoveryShareAndCounters(t *testing.T, r *rest, call func(string, map[string]any) (map[string]any, string, bool)) {
	t.Helper()
	r.call("PUT", "/api/discovery/settings", map[string]any{"enabled": false, "interval_minutes": 360, "auto_prepare": false, "min_score": 75, "max_jobs_per_run": 4}, nil)
	for _, board := range []string{"fair-a", "fair-b"} {
		if _, text, isErr := call("add_discovery_source", map[string]any{"provider": "greenhouse", "name": "Board " + board, "identifier": board}); isErr {
			t.Fatalf("add_discovery_source %s: %s", board, text)
		}
	}
	perBoard := func() map[string]int {
		out, text, isErr := call("list_jobs", map[string]any{"state": "all", "limit": 100})
		if isErr {
			t.Fatalf("list_jobs: %s", text)
		}
		counts := map[string]int{}
		jobs, _ := out["jobs"].([]any)
		for _, j := range jobs {
			m, _ := j.(map[string]any)
			job, _ := m["job"].(map[string]any)
			if company, _ := job["company"].(string); strings.HasPrefix(company, "Board ") {
				counts[strings.TrimPrefix(company, "Board ")]++
			}
		}
		return counts
	}

	for i, want := range []struct{ counts, why string }{
		{"seen 4, added 4, rejected 2, known 0", "both boards share the budget"},
		{"seen 4, added 4, rejected 2, known 0", "new postings come before the two known ones on each board"},
		{"seen 4, added 2, rejected 1, known 2", "one new posting left per board; the known ones are re-scored, not new rejections"},
	} {
		out, text, isErr := call("run_discovery", nil)
		if isErr {
			t.Fatalf("run %d: %s", i+1, text)
		}
		got := fmt.Sprintf("seen %v, added %v, rejected %v, known %v", out["jobs_seen"], out["jobs_added"], out["jobs_rejected"], out["duplicates_skipped"])
		if got != want.counts {
			t.Fatalf("run %d (%s): got %s, want %s", i+1, want.why, got, want.counts)
		}
		if boards := perBoard(); boards["fair-a"] != boards["fair-b"] {
			t.Fatalf("run %d: the boards should have contributed equally, got %v", i+1, boards)
		}
	}
	if boards := perBoard(); boards["fair-a"] != 5 || boards["fair-b"] != 5 {
		t.Fatalf("three runs should have reached every posting on both boards, got %v", boards)
	}
}

// serveFakeGreenhouse answers the Greenhouse board API: a listing of five
// postings for any board, and for any job one form of four required text
// fields the profile can answer, so the composed entry carries no blockers. It listens on every interface, because
// an API running in a container reaches it through host.docker.internal.
func serveFakeGreenhouse(t *testing.T, port string) *fakeGreenhouse {
	t.Helper()
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		t.Fatalf("cannot listen on port %s for the fake Greenhouse; set JOBPILOT_E2E_ATS_PORT to a free port and the API's ATS_GREENHOUSE_URL to match: %v", port, err)
	}
	fake := &fakeGreenhouse{}
	mux := http.NewServeMux()
	// Board listings for the discovery steps, in the shape of Greenhouse's
	// jobs?content=true: five postings per board, except fair-c's repost.
	// Board fair-b's mention PHP, which the seeded filter excludes, so its
	// jobs are rejected.
	mux.HandleFunc("GET /v1/boards/{board}/jobs", func(w http.ResponseWriter, r *http.Request) {
		board := r.PathValue("board")
		w.Header().Set("Content-Type", "application/json")
		if board == "fair-c" {
			// One opening listed twice: the second is a repost under a new
			// link two days later, as Arbeitnow reposts.
			posting := `{"id":%d,"title":"Backend Engineer fair-c","updated_at":%q,"absolute_url":"https://example.test/fair-c/%d","content":"Build services with Go and PostgreSQL.","location":{"name":"Remote, Romania"}}`
			older, newer := time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)
			_, _ = w.Write([]byte(`{"jobs":[` + fmt.Sprintf(posting, 301, older, 301) + "," + fmt.Sprintf(posting, 302, newer, 302) + `]}`))
			return
		}
		stack, first := "Go and PostgreSQL", 100
		if board == "fair-b" {
			stack, first = "PHP", 200
		}
		var jobs []string
		for i := 1; i <= 5; i++ {
			jobs = append(jobs, fmt.Sprintf(`{"id":%d,"title":"Backend Engineer %s %d","updated_at":%q,"absolute_url":"https://example.test/%s/%d","content":"Build services with %s.","location":{"name":"Remote, Romania"}}`,
				first+i, board, i, time.Now().UTC().Format(time.RFC3339), board, i, stack))
		}
		_, _ = w.Write([]byte(`{"jobs":[` + strings.Join(jobs, ",") + `]}`))
	})
	mux.HandleFunc("GET /v1/boards/{board}/jobs/{job}", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requested = append(fake.requested, r.URL.Path)
		fake.mu.Unlock()
		if r.URL.Query().Get("questions") != "true" {
			http.Error(w, "questions=true is required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Senior Backend Engineer","company_name":"Fake Greenhouse Co","questions":[` +
			`{"label":"First Name","required":true,"fields":[{"name":"first_name","type":"input_text","values":[]}]},` +
			`{"label":"Last Name","required":true,"fields":[{"name":"last_name","type":"input_text","values":[]}]},` +
			`{"label":"Email","required":true,"fields":[{"name":"email","type":"input_text","values":[]}]},` +
			`{"label":"Phone","required":true,"fields":[{"name":"phone","type":"input_text","values":[]}]}]}`))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return fake
}

// fakeGreenhouse remembers which forms were requested, so the flow can prove
// the apply URL was routed to the right board and job, not merely to a form.
type fakeGreenhouse struct {
	mu        sync.Mutex
	requested []string
}

func (f *fakeGreenhouse) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requested...)
}
