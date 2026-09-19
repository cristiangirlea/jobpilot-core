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
	req, err := http.NewRequest(method, r.base+path, reader)
	if err != nil {
		r.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := http.DefaultClient.Do(req)
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
	r := &rest{t: t, base: api}
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
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "JOBPILOT_API_URL="+api, "JOBPILOT_TOKEN=", "JOBPILOT_EMAIL="+email, "JOBPILOT_PASSWORD="+password)
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
	if len(tools.Tools) != 11 {
		t.Fatalf("expected 11 tools, got %d", len(tools.Tools))
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
}
