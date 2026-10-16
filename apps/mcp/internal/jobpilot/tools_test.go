package jobpilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeAPI is the smallest JobPilot REST surface the tools depend on. It serves
// canned data and records write payloads so tests can assert what was sent.
type fakeAPI struct {
	mu            sync.Mutex
	patches       []map[string]any
	posts         []string
	sources       []map[string]any
	imports       []map[string]any
	answers       []map[string]any
	answerPatches []map[string]any
	resolves      []map[string]any
	cvApprovals   []map[string]any
	cvUploads     []cvUpload
	// detailGets counts reads of one application, so a test can prove the
	// status tool no longer needs a read-back round trip.
	detailGets atomic.Int32
	// noContract makes the fake look like an API from before the health
	// endpoint reported a contract version.
	noContract bool
	// pages makes the job lists behave like an API that pages: it records
	// the query, answers with one job, and reports a total of 42.
	pages      atomic.Bool
	jobQueries []string
	// reports records which jobs were reported as no longer available.
	reports []string
	// filterSaves records each filter preset save as "METHOD path" and body.
	filterSaves  []string
	filterBodies []map[string]any
}

func (f *fakeAPI) lastJobQuery() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.jobQueries) == 0 {
		return ""
	}
	return f.jobQueries[len(f.jobQueries)-1]
}

const acceptedJob = `{"job":{"id":"job-a","title":"Senior Backend Engineer","company":"Fresh Systems GmbH","location":"Bucharest, Romania","remote_type":"remote","salary_min":95000,"salary_max":110000,"currency":"EUR","employment_type":"full-time","industry":"Software","description":"Long description A about Go and PostgreSQL.","apply_url":"https://example.test/apply-a","posted_at":"2026-09-10T00:00:00Z","created_at":"2026-09-11T00:00:00Z","source_name":"Remotive feed","source_provider":"remotive"},"match":{"rejected":false,"rejection_reasons":[],"warnings":["No salary band"],"score":82,"reasons":["The job title aligns with a desired role."],"matched_skills":["Go","PostgreSQL"],"missing_skills":["Kafka"],"red_flags":[],"breakdown":{"title_match":25,"skill_match":30}},"match_id":"m-a"}`
const rejectedJob = `{"job":{"id":"job-b","title":"Backend Engineer","company":"CurrentCo LLC","location":"Romania","remote_type":"remote","description":"Long description B.","apply_url":"https://example.test/apply-b","created_at":"2026-09-11T00:00:00Z"},"match":{"rejected":true,"rejection_reasons":["Company matches your current employer."],"warnings":[],"score":40,"reasons":[],"matched_skills":[],"missing_skills":[],"red_flags":[],"breakdown":{}},"match_id":"m-b"}`
const closedJob = `{"job":{"id":"job-d","title":"Staff Backend Engineer","company":"Gone GmbH","location":"Remote","remote_type":"remote","description":"Long description D.","apply_url":"https://example.test/apply-d","created_at":"2026-09-11T00:00:00Z","closed_at":"2026-09-20T00:00:00Z","closed_reason":"its link now returns 404 Not Found"},"match":{"rejected":false,"rejection_reasons":[],"warnings":[],"score":90,"reasons":[],"matched_skills":["Go"],"missing_skills":[],"red_flags":[],"breakdown":{}},"match_id":"m-d"}`
const weakJob = `{"job":{"id":"job-c","title":"Platform Engineer","company":"Other Ltd","location":"Remote","remote_type":"remote","description":"Long description C.","apply_url":"https://example.test/apply-c","created_at":"2026-09-11T00:00:00Z"},"match":{"rejected":false,"rejection_reasons":[],"warnings":[],"score":65,"reasons":[],"matched_skills":["Go"],"missing_skills":[],"red_flags":[],"breakdown":{}},"match_id":"m-c"}`
const application = `{"id":"app-1","job_id":"job-a","status":"ready","notes":"keep me","created_at":"2026-09-12T00:00:00Z","updated_at":"2026-09-12T00:00:00Z","job":{"id":"job-a","title":"Senior Backend Engineer","company":"Fresh Systems GmbH","location":"Bucharest, Romania","remote_type":"remote","description":"Long description A about Go and PostgreSQL.","apply_url":"https://example.test/apply-a","created_at":"2026-09-11T00:00:00Z"}}`
const appliedApplication = `{"id":"app-2","job_id":"job-c","status":"applied","notes":"","created_at":"2026-09-12T00:00:00Z","updated_at":"2026-09-13T00:00:00Z","job":{"id":"job-c","title":"Platform Engineer","company":"Other Ltd","location":"Remote","remote_type":"remote","description":"Long description C.","apply_url":"https://example.test/apply-c","created_at":"2026-09-11T00:00:00Z"}}`
const blockedApplication = `{"id":"app-blocked","job_id":"job-b","status":"ready","notes":"","created_at":"2026-09-12T00:00:00Z","updated_at":"2026-09-12T00:00:00Z","job":{"id":"job-b","title":"Backend Engineer","company":"CurrentCo LLC","location":"Romania","remote_type":"remote","description":"Long description B.","apply_url":"https://example.test/apply-b","created_at":"2026-09-11T00:00:00Z"}}`
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
	// Without pages, the lists are whole and uncounted, like an API from
	// before paging, which the tools must still filter themselves.
	paged := func(w http.ResponseWriter, r *http.Request) bool {
		if !f.pages.Load() {
			return false
		}
		f.mu.Lock()
		f.jobQueries = append(f.jobQueries, r.URL.Path+"?"+r.URL.RawQuery)
		f.mu.Unlock()
		w.Header().Set("X-Total-Count", "42")
		if r.URL.Query().Get("open") == "true" {
			send(w, 200, "["+rejectedJob+"]")
		} else {
			send(w, 200, "["+closedJob+"]")
		}
		return true
	}
	m.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		if !paged(w, r) {
			send(w, 200, "["+acceptedJob+","+weakJob+","+rejectedJob+","+closedJob+"]")
		}
	})
	m.HandleFunc("GET /api/matches", func(w http.ResponseWriter, r *http.Request) {
		if !paged(w, r) {
			send(w, 200, "["+acceptedJob+","+weakJob+","+closedJob+"]")
		}
	})
	m.HandleFunc("POST /api/jobs/{id}/report", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id != "job-a" {
			send(w, 404, `{"error":"Job not found."}`)
			return
		}
		f.mu.Lock()
		f.reports = append(f.reports, id)
		f.mu.Unlock()
		send(w, 200, `{"closed":false,"hidden_for_you":true,"message":"Hidden from your lists. Its link does not confirm it, so it stays open for others until the link does or someone else reports it too."}`)
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
		f.detailGets.Add(1)
		switch r.PathValue("id") {
		case "app-1":
			send(w, 200, `{"application":`+application+`,"documents":`+documents+`,"blocked":false,"block_reasons":[]}`)
		case "app-blocked":
			send(w, 200, `{"application":`+blockedApplication+`,"documents":`+documents+`,"blocked":true,"block_reasons":["Company matches your current employer."]}`)
		default:
			send(w, 404, `{"error":"Record not found."}`)
		}
	})
	m.HandleFunc("PATCH /api/applications/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.patches = append(f.patches, body)
		f.mu.Unlock()
		status, _ := body["status"].(string)
		// The API keeps the stored notes when the field is absent.
		notes := "keep me"
		if sent, ok := body["notes"].(string); ok {
			notes = sent
		}
		send(w, 200, `{"id":"app-1","job_id":"job-a","status":"`+status+`","notes":"`+notes+`","created_at":"2026-09-12T00:00:00Z","updated_at":"2026-09-16T00:00:00Z"}`)
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
	m.HandleFunc("POST /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.imports = append(f.imports, body)
		f.mu.Unlock()
		if title, _ := body["title"].(string); title == "" {
			send(w, 422, `{"error":"Title, company, and job description are required."}`)
			return
		}
		if company, _ := body["company"].(string); strings.Contains(company, "CurrentCo") {
			send(w, 201, rejectedJob)
			return
		}
		send(w, 201, acceptedJob)
	})
	m.HandleFunc("GET /api/filters", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, `[{"id":"f-1","name":"Backend remote","active":true,"included_titles":["Backend Engineer"],"excluded_title_words":["unpaid"],"locations":["Romania"],"remote_types":["remote"],"min_salary":90000,"currency":"EUR","employment_types":["full-time"],"excluded_industries":["Gambling"],"required_skills":["Go"],"included_technologies":["Go"],"excluded_technologies":["PHP"],"max_age_days":30}]`)
	})
	saveFilter := func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.filterSaves = append(f.filterSaves, r.Method+" "+r.URL.Path)
		f.filterBodies = append(f.filterBodies, body)
		f.mu.Unlock()
		if name, _ := body["name"].(string); name == "Refused" {
			send(w, 422, `{"error":"Filter name is required and numeric limits cannot be negative."}`)
			return
		}
		saved := map[string]any{"id": "f-new"}
		for k, v := range body {
			saved[k] = v
		}
		raw, _ := json.Marshal(saved)
		send(w, map[string]int{http.MethodPost: 201, http.MethodPut: 200}[r.Method], string(raw))
	}
	m.HandleFunc("POST /api/filters", saveFilter)
	m.HandleFunc("PUT /api/filters/{id}", saveFilter)
	m.HandleFunc("GET /api/discovery/sources", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, `[{"id":"s-1","provider":"remotive","name":"Remote Go","identifier":"","search_query":"go backend","excluded_search_query":"","active":true,"created_at":"2026-09-11T00:00:00Z"}]`)
	})
	m.HandleFunc("POST /api/discovery/validate", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if provider, _ := body["provider"].(string); provider == "greenhouse" {
			send(w, 422, `{"error":"Source could not be read: board not found"}`)
			return
		}
		send(w, 200, `{"count":12,"sample_title":"Senior Backend Engineer"}`)
	})
	m.HandleFunc("POST /api/discovery/sources", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		duplicate := false
		for _, existing := range f.sources {
			if existing["name"] == body["name"] {
				duplicate = true
			}
		}
		f.sources = append(f.sources, body)
		f.mu.Unlock()
		if duplicate {
			send(w, 409, `{"error":"a discovery source with the same provider, identifier, and query already exists"}`)
			return
		}
		send(w, 201, `{"id":"s-new","provider":"`+fmt.Sprint(body["provider"])+`","name":"`+fmt.Sprint(body["name"])+`","identifier":"","search_query":"","excluded_search_query":"","active":true,"created_at":"2026-09-20T00:00:00Z"}`)
	})
	f.applyRoutes(m, send)
	f.notificationRoutes(m, send)
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		if f.noContract {
			send(w, 200, `{"status":"ok"}`)
			return
		}
		send(w, 200, `{"status":"ok","contract":6}`)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			m.ServeHTTP(w, r)
			return
		}
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
	return sessionWithToken(t, "test-token")
}

func sessionWithToken(t *testing.T, token string) (*mcp.ClientSession, *fakeAPI) {
	t.Helper()
	return connect(t, token, nil, nil)
}

// sessionWithLogger wires a server whose middleware writes to the given logger.
func sessionWithLogger(t *testing.T, logger *slog.Logger) (*mcp.ClientSession, *fakeAPI) {
	t.Helper()
	return connect(t, "test-token", logger, nil)
}

// sessionWithClient lets a test supply its own client, for example to collect
// progress notifications.
func sessionWithClient(t *testing.T, client *mcp.Client) *mcp.ClientSession {
	t.Helper()
	cs, _ := connect(t, "test-token", nil, client)
	return cs
}

func connect(t *testing.T, token string, logger *slog.Logger, host *mcp.Client) (*mcp.ClientSession, *fakeAPI) {
	t.Helper()
	return connectWith(t, token, logger, host, false, true)
}

// sessionAgainstOldAPI wires a server whose startup contract check saw an API
// from before the health endpoint reported a version.
func sessionAgainstOldAPI(t *testing.T) (*mcp.ClientSession, *fakeAPI) {
	t.Helper()
	return connectWith(t, "test-token", nil, nil, true, true)
}

// sessionUnchecked wires a server whose startup contract check never
// succeeded, as when the API was down when the server started.
func sessionUnchecked(t *testing.T, oldAPI bool) (*mcp.ClientSession, *fakeAPI) {
	t.Helper()
	return connectWith(t, "test-token", nil, nil, oldAPI, false)
}

func connectWith(t *testing.T, token string, logger *slog.Logger, host *mcp.Client, oldAPI, check bool) (*mcp.ClientSession, *fakeAPI) {
	t.Helper()
	return connectOptions(t, Options{Token: token}, logger, host, oldAPI, check)
}

// connectOptions is connectWith for a client that needs more settings, such
// as a CV folder; BaseURL is filled in with the fake API's.
func connectOptions(t *testing.T, o Options, logger *slog.Logger, host *mcp.Client, oldAPI, check bool) (*mcp.ClientSession, *fakeAPI) {
	t.Helper()
	fake := &fakeAPI{noContract: oldAPI}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	o.BaseURL = srv.URL
	client, err := NewClient(o)
	if err != nil {
		t.Fatal(err)
	}
	if check {
		if _, err = client.CheckContract(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	server := NewServer(client, "test", logger)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err = server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	if host == nil {
		host = mcp.NewClient(&mcp.Implementation{Name: "test-host", Version: "0"}, nil)
	}
	cs, err := host.Connect(ctx, ct, nil)
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
	if rejected.Job.ApplyURL != "" || got.Jobs[0].Job.ApplyURL != "https://example.test/apply-a" {
		t.Fatalf("apply_url must be blanked only for rejected jobs: %s", text)
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

	// A closed posting cannot be applied to, so it is left out unless asked
	// for, and then says it is closed.
	res, text = call(t, cs, "list_jobs", map[string]any{"state": "all", "include_closed": true})
	got = structured[listing](t, res)
	if got.Total != 4 {
		t.Fatalf("include_closed should add the closed job, got %s", text)
	}
	for _, job := range got.Jobs {
		if closed := job.Job.ID == "job-d"; job.Job.Closed != closed || (closed && job.Job.ClosedReason == "") {
			t.Fatalf("only the closed job should say it is closed, and why: %s", text)
		}
	}
}

// Found in early use: listing rejected or all jobs downloaded every
// job, 14.4 MB on a full account and 45% of the response cap, to keep one
// page. An API that pages is asked for the page, as summaries, and its total
// is reported.
func TestListJobsAsksTheServerForOnePage(t *testing.T) {
	cs, fake := session(t)
	fake.pages.Store(true)
	type listing struct {
		Total int            `json:"total"`
		Jobs  []MatchSummary `json:"jobs"`
	}
	res, text := call(t, cs, "list_jobs", map[string]any{"state": "rejected", "min_score": 30, "limit": 5})
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[listing](t, res)
	if got.Total != 42 || len(got.Jobs) != 1 || got.Jobs[0].Job.ID != "job-b" {
		t.Fatalf("the page and the server's total should come back as served, got %s", text)
	}
	if want := "/api/jobs?fields=summary&limit=5&min_score=30&open=true&state=rejected"; fake.lastJobQuery() != want {
		t.Fatalf("asked for %s, want %s", fake.lastJobQuery(), want)
	}

	// Accepted jobs keep their own list, and closed postings can be asked for.
	res, text = call(t, cs, "list_jobs", map[string]any{"include_closed": true})
	if res.IsError {
		t.Fatal(text)
	}
	if want := "/api/matches?fields=summary&limit=20"; fake.lastJobQuery() != want {
		t.Fatalf("asked for %s, want %s", fake.lastJobQuery(), want)
	}
	if got = structured[listing](t, res); len(got.Jobs) != 1 || !got.Jobs[0].Job.Closed {
		t.Fatalf("a closed job should say so: %s", text)
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
	res, text = call(t, cs, "get_job", map[string]any{"job_id": "job-b"})
	if got := structured[JobWithMatch](t, res); !got.Match.Rejected || got.Job.ApplyURL != "" {
		t.Fatalf("rejected job detail must not carry an apply URL: %s", text)
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
	if _, sent := fake.patches[0]["notes"]; sent {
		t.Fatalf("omitting notes must send no notes field at all, sent %v", fake.patches[0])
	}
	if fake.patches[0]["status"] != "applied" {
		t.Fatalf("status not sent, got %v", fake.patches[0])
	}
	if fake.patches[1]["status"] != "interview" || fake.patches[1]["notes"] != "Phone screen booked" {
		t.Fatalf("explicit notes must be sent, got %v", fake.patches[1])
	}
	if got := fake.detailGets.Load(); got != 0 {
		t.Fatalf("the API keeps omitted notes, so no read-back is needed; got %d reads", got)
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
	if detail.Application.ID != "app-1" || detail.Documents.RecruiterMessage != "Hello recruiter" || detail.Blocked || detail.Application.Job.ApplyURL == "" {
		t.Fatalf("unexpected detail: %s", text)
	}
	res, text = call(t, cs, "get_application", map[string]any{"application_id": "app-blocked"})
	detail = structured[ApplicationView](t, res)
	if !detail.Blocked || len(detail.BlockReasons) != 1 || detail.Application.Job == nil || detail.Application.Job.ApplyURL != "" {
		t.Fatalf("blocked application must not carry an apply URL: %s", text)
	}
}

func TestUnauthorizedSurfacesTokenHint(t *testing.T) {
	cs, _ := sessionWithToken(t, "expired-token")
	res, text := call(t, cs, "get_profile", nil)
	if !res.IsError || !strings.Contains(text, "401") || !strings.Contains(text, "JOBPILOT_TOKEN") {
		t.Fatalf("expected a 401 with the token hint, got %s", text)
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

// Found in early use: the API's messages end in a full stop, and the
// hints were appended after another, so a refusal read "write scope.. This".
// A CLI user also needs to know the script mints tokens, not only Settings.
func TestToolErrorHintsReadAsSentences(t *testing.T) {
	for _, c := range []struct {
		status        int
		message, want string
	}{
		{http.StatusForbidden, "This token does not have the write scope.", "agent-token.ps1"},
		{http.StatusUnauthorized, "Invalid or expired token.", "agent-token.ps1"},
		{http.StatusConflict, blockedMessage, "get_job"},
	} {
		err := toolError(&APIError{Status: c.status, Message: c.message})
		if text := err.Error(); strings.Contains(text, "..") || !strings.Contains(text, c.want) {
			t.Errorf("%d: %q should read as sentences and mention %s", c.status, text, c.want)
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != c.status {
			t.Errorf("%d: the API error must stay reachable for callers", c.status)
		}
	}
}

// A posting that says the role is filled but still loads is invisible to
// the link check, so the user can say so through the agent too. The tool
// passes the API's answer through: whether the job closed for everyone, or
// is hidden from this user until the link or a second person confirms it.
func TestReportJobUnavailable(t *testing.T) {
	cs, fake := session(t)
	res, text := call(t, cs, "report_job_unavailable", map[string]any{"job_id": "job-a"})
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[JobReport](t, res)
	if got.Closed || !got.HiddenForYou || got.Message == "" {
		t.Fatalf("the API's answer should come back as given: %s", text)
	}
	fake.mu.Lock()
	reports := append([]string(nil), fake.reports...)
	fake.mu.Unlock()
	if len(reports) != 1 || reports[0] != "job-a" {
		t.Fatalf("expected one report of job-a, got %v", reports)
	}

	if res, text = call(t, cs, "report_job_unavailable", map[string]any{"job_id": " "}); !res.IsError || !strings.Contains(text, "job_id") {
		t.Fatalf("a blank job_id should be refused before any request: %s", text)
	}
	if res, text = call(t, cs, "report_job_unavailable", map[string]any{"job_id": "nope"}); !res.IsError || !strings.Contains(text, "not found") {
		t.Fatalf("an unknown job should say so: %s", text)
	}
}
