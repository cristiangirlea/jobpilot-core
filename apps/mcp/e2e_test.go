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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Compare the set, not the count, so adding a tool is a deliberate edit
	// here rather than a number nobody can interpret.
	want := map[string]bool{
		"get_profile": true, "list_jobs": true, "get_job": true, "list_applications": true,
		"get_application": true, "get_dashboard": true, "list_discovery_runs": true,
		"get_filters": true, "list_discovery_sources": true, "validate_discovery_source": true,
		"generate_documents": true, "create_application": true, "update_application_status": true,
		"run_discovery": true, "import_job": true, "add_discovery_source": true,
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
	if match, _ := out["match"].(map[string]any); match["breakdown"] == nil {
		t.Fatalf("get_job must include the breakdown: %s", text)
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

	out, text, isErr = call("get_dashboard", nil)
	if isErr || out["applications_applied"] != float64(1) || out["jobs_imported"] != float64(2) {
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
}
