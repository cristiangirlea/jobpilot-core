package jobpilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeAPI is the smallest JobPilot REST surface the tools depend on. It serves
// canned data and records write payloads so tests can assert what was sent.
type fakeAPI struct {
	mu      sync.Mutex
	patches []map[string]string
	posts   []string
}

const acceptedJob = `{"job":{"id":"job-a","title":"Senior Backend Engineer","company":"Fresh Systems GmbH","location":"Bucharest, Romania","remote_type":"remote","salary_min":95000,"salary_max":110000,"currency":"EUR","employment_type":"full-time","industry":"Software","description":"Long description A about Go and PostgreSQL.","apply_url":"https://example.test/apply-a","posted_at":"2026-09-10T00:00:00Z","created_at":"2026-09-11T00:00:00Z","source_name":"Remotive feed","source_provider":"remotive"},"match":{"rejected":false,"rejection_reasons":[],"warnings":["No salary band"],"score":82,"reasons":["The job title aligns with a desired role."],"matched_skills":["Go","PostgreSQL"],"missing_skills":["Kafka"],"red_flags":[],"breakdown":{"title_match":25,"skill_match":30}},"match_id":"m-a"}`
const rejectedJob = `{"job":{"id":"job-b","title":"Backend Engineer","company":"CurrentCo LLC","location":"Romania","remote_type":"remote","description":"Long description B.","apply_url":"https://example.test/apply-b","created_at":"2026-09-11T00:00:00Z"},"match":{"rejected":true,"rejection_reasons":["Company matches your current employer."],"warnings":[],"score":40,"reasons":[],"matched_skills":[],"missing_skills":[],"red_flags":[],"breakdown":{}},"match_id":"m-b"}`
const weakJob = `{"job":{"id":"job-c","title":"Platform Engineer","company":"Other Ltd","location":"Remote","remote_type":"remote","description":"Long description C.","apply_url":"https://example.test/apply-c","created_at":"2026-09-11T00:00:00Z"},"match":{"rejected":false,"rejection_reasons":[],"warnings":[],"score":65,"reasons":[],"matched_skills":["Go"],"missing_skills":[],"red_flags":[],"breakdown":{}},"match_id":"m-c"}`
const application = `{"id":"app-1","job_id":"job-a","status":"ready","notes":"keep me","created_at":"2026-09-12T00:00:00Z","updated_at":"2026-09-12T00:00:00Z","job":{"id":"job-a","title":"Senior Backend Engineer","company":"Fresh Systems GmbH","location":"Bucharest, Romania","remote_type":"remote","description":"Long description A about Go and PostgreSQL.","apply_url":"https://example.test/apply-a","created_at":"2026-09-11T00:00:00Z"}}`
const appliedApplication = `{"id":"app-2","job_id":"job-c","status":"applied","notes":"","created_at":"2026-09-12T00:00:00Z","updated_at":"2026-09-13T00:00:00Z","job":{"id":"job-c","title":"Platform Engineer","company":"Other Ltd","location":"Remote","remote_type":"remote","description":"Long description C.","apply_url":"https://example.test/apply-c","created_at":"2026-09-11T00:00:00Z"}}`
const documents = `{"recruiter_message":"Hello recruiter","cover_letter":"Dear Fresh Systems","candidate_summary":"Summary","answers":{"Tell us about yourself":"I build backends."}}`
const discoveryRun = `{"id":"run-1","status":"completed","jobs_seen":12,"jobs_added":3,"jobs_rejected":2,"applications_prepared":1,"duplicates_skipped":6,"started_at":"2026-09-14T00:00:00Z","completed_at":"2026-09-14T00:01:00Z"}`

func (f *fakeAPI) handler() http.Handler {
	m := http.NewServeMux()
	send := func(w http.ResponseWriter, status int, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
	m.HandleFunc("GET /api/profile", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, `{"name":"Octavian","current_title":"Senior Backend Engineer","desired_titles":["Backend Engineer"],"seniorities":["senior"],"years_experience":9,"skills":[{"name":"Go","level":"advanced","years":6}],"experiences":[{"company":"CurrentCo","title":"Senior Backend Engineer","current":true}],"projects":[]}`)
	})
	m.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, "["+acceptedJob+","+weakJob+","+rejectedJob+"]")
	})
	m.HandleFunc("GET /api/matches", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, "["+acceptedJob+","+weakJob+"]")
	})
	m.HandleFunc("GET /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") {
		case "job-a":
			send(w, 200, acceptedJob)
		case "job-b":
			send(w, 200, rejectedJob)
		default:
			send(w, 404, `{"error":"Record not found."}`)
		}
	})
	m.HandleFunc("POST /api/jobs/{id}/generate", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "job-b" {
			send(w, 409, `{"error":"application actions are blocked for this rejected job"}`)
			return
		}
		send(w, 200, documents)
	})
	m.HandleFunc("GET /api/applications", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, "["+application+","+appliedApplication+"]")
	})
	m.HandleFunc("POST /api/applications", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.posts = append(f.posts, body["job_id"])
		f.mu.Unlock()
		if body["job_id"] == "job-b" {
			send(w, 409, `{"error":"application actions are blocked for this rejected job"}`)
			return
		}
		send(w, 201, `{"id":"app-new","job_id":"`+body["job_id"]+`","status":"draft","notes":"","created_at":"2026-09-15T00:00:00Z","updated_at":"2026-09-15T00:00:00Z"}`)
	})
	m.HandleFunc("GET /api/applications/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "app-1" {
			send(w, 404, `{"error":"Record not found."}`)
			return
		}
		send(w, 200, `{"application":`+application+`,"documents":`+documents+`,"blocked":false,"block_reasons":[]}`)
	})
	m.HandleFunc("PATCH /api/applications/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.patches = append(f.patches, body)
		f.mu.Unlock()
		send(w, 200, `{"id":"app-1","job_id":"job-a","status":"`+body["status"]+`","notes":"`+body["notes"]+`","created_at":"2026-09-12T00:00:00Z","updated_at":"2026-09-16T00:00:00Z"}`)
	})
	m.HandleFunc("GET /api/dashboard", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, `{"profile_completeness":75,"jobs_imported":3,"jobs_rejected_blacklist":1,"jobs_rejected_filters":0,"good_matches":1,"applications_drafted":1,"applications_applied":1,"top_jobs":[`+acceptedJob+`],"next_actions":["Review your strongest job matches"]}`)
	})
	m.HandleFunc("GET /api/discovery/runs", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, "["+discoveryRun+"]")
	})
	m.HandleFunc("POST /api/discovery/run", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, discoveryRun)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			send(w, 401, `{"error":"unauthorized"}`)
			return
		}
		m.ServeHTTP(w, r)
	})
}

// session wires a real MCP client to the server over in-memory transports so
// every assertion goes through the same JSON the host would see.
func session(t *testing.T) (*mcp.ClientSession, *fakeAPI) {
	t.Helper()
	fake := &fakeAPI{}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client, err := NewClient(Options{BaseURL: srv.URL, Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(client, "test")
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err = server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-host", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, fake
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return res, text.String()
}

func structured[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode structured content: %v\n%s", err, raw)
	}
	return out
}

func TestToolCatalog(t *testing.T) {
	cs, _ := session(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	readOnly := map[string]bool{
		"get_profile": true, "list_jobs": true, "get_job": true, "list_applications": true, "get_application": true,
		"get_dashboard": true, "list_discovery_runs": true,
		"generate_documents": false, "create_application": false, "update_application_status": false, "run_discovery": false,
	}
	seen := map[string]bool{}
	for _, tool := range res.Tools {
		want, known := readOnly[tool.Name]
		if !known {
			t.Errorf("unexpected tool %q", tool.Name)
			continue
		}
		seen[tool.Name] = true
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != want {
			t.Errorf("%s: readOnlyHint should be %v", tool.Name, want)
		}
		if tool.Description == "" {
			t.Errorf("%s: missing description", tool.Name)
		}
		if strings.Contains(tool.Name, "outbound") || strings.Contains(strings.ToLower(tool.Description), "submit the application") {
			t.Errorf("%s: must not expose submission or outbound attribution", tool.Name)
		}
	}
	for name := range readOnly {
		if !seen[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestListJobsFiltersAndSummarizes(t *testing.T) {
	cs, _ := session(t)
	type listing struct {
		Total int            `json:"total"`
		Jobs  []MatchSummary `json:"jobs"`
	}
	res, text := call(t, cs, "list_jobs", map[string]any{"min_score": 70})
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[listing](t, res)
	if got.Total != 1 || len(got.Jobs) != 1 || got.Jobs[0].Job.ID != "job-a" || got.Jobs[0].Score != 82 {
		t.Fatalf("unexpected listing: %s", text)
	}
	if strings.Contains(text, "Long description") {
		t.Fatal("list output must not carry full descriptions")
	}
	if got.Jobs[0].Job.Source != "Remotive feed (remotive)" {
		t.Fatalf("source not summarized: %+v", got.Jobs[0].Job)
	}

	res, text = call(t, cs, "list_jobs", map[string]any{"state": "all"})
	got = structured[listing](t, res)
	if got.Total != 3 || len(got.Jobs) != 3 {
		t.Fatalf("expected every evaluated job, got %s", text)
	}
	var rejected *MatchSummary
	for i := range got.Jobs {
		if got.Jobs[i].Job.ID == "job-b" {
			rejected = &got.Jobs[i]
		}
	}
	if rejected == nil || !rejected.Rejected || len(rejected.RejectionReasons) != 1 {
		t.Fatalf("rejected job must keep its reasons: %s", text)
	}

	res, text = call(t, cs, "list_jobs", map[string]any{"state": "rejected"})
	got = structured[listing](t, res)
	if got.Total != 1 || got.Jobs[0].Job.ID != "job-b" {
		t.Fatalf("expected only the rejected job, got %s", text)
	}

	res, text = call(t, cs, "list_jobs", map[string]any{"limit": 1})
	got = structured[listing](t, res)
	if got.Total != 2 || len(got.Jobs) != 1 || got.Jobs[0].Job.ID != "job-a" {
		t.Fatalf("limit must cap the page but report the total: %s", text)
	}

	res, text = call(t, cs, "list_jobs", map[string]any{"state": "bogus"})
	if !res.IsError || !strings.Contains(text, "state") {
		t.Fatalf("invalid state must be a tool error: %s", text)
	}
}

func TestGetJobIncludesDescriptionAndBreakdown(t *testing.T) {
	cs, _ := session(t)
	res, text := call(t, cs, "get_job", map[string]any{"job_id": "job-a"})
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[JobWithMatch](t, res)
	if got.Job.Description != "Long description A about Go and PostgreSQL." || got.Match.Breakdown["skill_match"] != 30 || got.Match.MissingSkills[0] != "Kafka" {
		t.Fatalf("unexpected detail: %s", text)
	}
	res, text = call(t, cs, "get_job", map[string]any{"job_id": "nope"})
	if !res.IsError || !strings.Contains(text, "404") {
		t.Fatalf("missing job must surface the API error: %s", text)
	}
}

func TestBlockedActionsSurfaceAsToolErrors(t *testing.T) {
	cs, fake := session(t)
	res, text := call(t, cs, "generate_documents", map[string]any{"job_id": "job-b"})
	if !res.IsError || !strings.Contains(text, "blocked") {
		t.Fatalf("expected the 409 message, got %s", text)
	}
	res, text = call(t, cs, "create_application", map[string]any{"job_id": "job-b"})
	if !res.IsError || !strings.Contains(text, "blocked") {
		t.Fatalf("expected the 409 message, got %s", text)
	}
	res, text = call(t, cs, "create_application", map[string]any{"job_id": "job-a"})
	if res.IsError {
		t.Fatal(text)
	}
	if got := structured[Application](t, res); got.ID != "app-new" || got.Status != "draft" {
		t.Fatalf("unexpected application: %s", text)
	}
	if len(fake.posts) != 2 {
		t.Fatalf("expected two create calls, got %v", fake.posts)
	}
}

func TestGenerateDocumentsReturnsAllFour(t *testing.T) {
	cs, _ := session(t)
	res, text := call(t, cs, "generate_documents", map[string]any{"job_id": "job-a"})
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[Documents](t, res)
	if got.CoverLetter != "Dear Fresh Systems" || got.Answers["Tell us about yourself"] == "" {
		t.Fatalf("unexpected documents: %s", text)
	}
}

func TestUpdateStatusPreservesNotesUnlessGiven(t *testing.T) {
	cs, fake := session(t)
	res, text := call(t, cs, "update_application_status", map[string]any{"application_id": "app-1", "status": "applied"})
	if res.IsError {
		t.Fatal(text)
	}
	res, text = call(t, cs, "update_application_status", map[string]any{"application_id": "app-1", "status": "interview", "notes": "Phone screen booked"})
	if res.IsError {
		t.Fatal(text)
	}
	if len(fake.patches) != 2 {
		t.Fatalf("expected two PATCH calls, got %d", len(fake.patches))
	}
	if fake.patches[0]["status"] != "applied" || fake.patches[0]["notes"] != "keep me" {
		t.Fatalf("omitting notes must preserve the stored notes, sent %v", fake.patches[0])
	}
	if fake.patches[1]["status"] != "interview" || fake.patches[1]["notes"] != "Phone screen booked" {
		t.Fatalf("explicit notes must be sent, got %v", fake.patches[1])
	}
	res, text = call(t, cs, "update_application_status", map[string]any{"application_id": "app-1", "status": "hired"})
	if !res.IsError || !strings.Contains(text, "status") {
		t.Fatalf("invalid status must be rejected before any request: %s", text)
	}
	if len(fake.patches) != 2 {
		t.Fatal("invalid status must not reach the API")
	}
}

func TestApplicationsListAndDetail(t *testing.T) {
	cs, _ := session(t)
	type listing struct {
		Applications []ApplicationSummary `json:"applications"`
	}
	res, text := call(t, cs, "list_applications", nil)
	got := structured[listing](t, res)
	if len(got.Applications) != 2 || got.Applications[0].Job == nil || got.Applications[0].Job.Title != "Senior Backend Engineer" {
		t.Fatalf("unexpected applications: %s", text)
	}
	if strings.Contains(text, "Long description") {
		t.Fatal("application listing must not carry job descriptions")
	}
	res, text = call(t, cs, "list_applications", map[string]any{"status": "applied"})
	got = structured[listing](t, res)
	if len(got.Applications) != 1 || got.Applications[0].ID != "app-2" {
		t.Fatalf("status filter failed: %s", text)
	}
	res, text = call(t, cs, "get_application", map[string]any{"application_id": "app-1"})
	if res.IsError {
		t.Fatal(text)
	}
	detail := structured[ApplicationView](t, res)
	if detail.Application.ID != "app-1" || detail.Documents.RecruiterMessage != "Hello recruiter" || detail.Blocked {
		t.Fatalf("unexpected detail: %s", text)
	}
}

func TestDashboardAndDiscovery(t *testing.T) {
	cs, _ := session(t)
	res, text := call(t, cs, "get_dashboard", nil)
	if res.IsError {
		t.Fatal(text)
	}
	dash := structured[DashboardView](t, res)
	if dash.GoodMatches != 1 || len(dash.TopJobs) != 1 || dash.TopJobs[0].Job.ID != "job-a" || len(dash.NextActions) != 1 {
		t.Fatalf("unexpected dashboard: %s", text)
	}
	if strings.Contains(text, "Long description") {
		t.Fatal("dashboard top jobs must be summaries")
	}
	res, text = call(t, cs, "list_discovery_runs", nil)
	type runs struct {
		Runs []DiscoveryRun `json:"runs"`
	}
	if got := structured[runs](t, res); len(got.Runs) != 1 || got.Runs[0].JobsAdded != 3 {
		t.Fatalf("unexpected runs: %s", text)
	}
	res, text = call(t, cs, "run_discovery", nil)
	if res.IsError {
		t.Fatal(text)
	}
	if got := structured[DiscoveryRun](t, res); got.ID != "run-1" || got.Status != "completed" {
		t.Fatalf("unexpected run: %s", text)
	}
	res, text = call(t, cs, "get_profile", nil)
	if got := structured[Profile](t, res); got.Name != "Octavian" || len(got.Skills) != 1 {
		t.Fatalf("unexpected profile: %s", text)
	}
}
