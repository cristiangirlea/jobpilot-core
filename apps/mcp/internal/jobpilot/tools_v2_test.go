package jobpilot

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestImportJobValidatesBeforeAnyRequest(t *testing.T) {
	cs, fake := session(t)
	bad := []struct {
		name string
		args map[string]any
		want string
	}{
		{"no title", map[string]any{"company": "Fresh Systems", "description": "Build things."}, "required"},
		{"no description", map[string]any{"title": "Backend Engineer", "company": "Fresh Systems"}, "required"},
		{"bad apply url", map[string]any{"title": "T", "company": "C", "description": "D", "apply_url": "ftp://example.test"}, "http or https"},
		{"inverted salary", map[string]any{"title": "T", "company": "C", "description": "D", "salary_min": 100, "salary_max": 50}, "salary range"},
		{"bad posted_at", map[string]any{"title": "T", "company": "C", "description": "D", "posted_at": "yesterday"}, "RFC3339"},
	}
	for _, c := range bad {
		res, text := call(t, cs, "import_job", c.args)
		if !res.IsError || !strings.Contains(text, c.want) {
			t.Errorf("%s: expected an error mentioning %q, got %s", c.name, c.want, text)
		}
	}
	if len(fake.imports) != 0 {
		t.Fatalf("invalid input must never reach the API, got %d requests", len(fake.imports))
	}
}

func TestImportJobReturnsTheEvaluation(t *testing.T) {
	cs, fake := session(t)
	res, text := call(t, cs, "import_job", map[string]any{
		"title": "Senior Backend Engineer", "company": "Fresh Systems GmbH",
		"description": "Build reliable backend services.", "apply_url": "https://example.test/apply-a",
		"posted_at": "2026-09-10T00:00:00Z", "salary_min": 95000, "salary_max": 110000,
	})
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[ImportedJob](t, res)
	if got.Job.ID != "job-a" || got.Match.Score != 82 || got.Match.Rejected {
		t.Fatalf("unexpected import result: %s", text)
	}
	if strings.Contains(text, "Long description") {
		t.Fatal("the import result must not echo the job description back")
	}
	if len(fake.imports) != 1 {
		t.Fatalf("expected one request, got %d", len(fake.imports))
	}
	if fake.imports[0]["posted_at"] != "2026-09-10T00:00:00Z" {
		t.Fatalf("posted_at must reach the API as RFC3339, got %v", fake.imports[0]["posted_at"])
	}

	// A hard-rejected import still succeeds, carrying its reasons.
	res, text = call(t, cs, "import_job", map[string]any{
		"title": "Backend Engineer", "company": "CurrentCo LLC", "description": "Build things.",
	})
	if res.IsError {
		t.Fatal(text)
	}
	rejected := structured[ImportedJob](t, res)
	if !rejected.Match.Rejected || len(rejected.Match.RejectionReasons) != 1 {
		t.Fatalf("a rejected import must report why: %s", text)
	}
	if rejected.Job.ApplyURL != "" {
		t.Fatalf("a rejected import must not carry an apply URL, got %q", rejected.Job.ApplyURL)
	}
}

func TestGetFiltersExplainsTheActivePreset(t *testing.T) {
	cs, _ := session(t)
	res, text := call(t, cs, "get_filters", nil)
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[FilterList](t, res)
	if len(got.Filters) != 1 || !got.Filters[0].Active || got.Filters[0].Name != "Backend remote" {
		t.Fatalf("unexpected filters: %s", text)
	}
	if len(got.Filters[0].ExcludedTechnologies) != 1 || got.Filters[0].MaxAgeDays != 30 {
		t.Fatalf("filter rules must survive: %+v", got.Filters[0])
	}
}

func TestDiscoverySourceTools(t *testing.T) {
	cs, fake := session(t)

	res, text := call(t, cs, "list_discovery_sources", nil)
	if got := structured[SourceList](t, res); len(got.Sources) != 1 || got.Sources[0].Provider != "remotive" {
		t.Fatalf("unexpected sources: %s", text)
	}

	// Provider and identifier rules are checked before any network call.
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"provider": "linkedin", "name": "No"}, "supported provider"},
		{map[string]any{"provider": "remotive", "name": ""}, "supported provider"},
		{map[string]any{"provider": "remotive", "name": "Needs a query"}, "focused search query"},
		{map[string]any{"provider": "lever", "name": "Needs an id"}, "identifier"},
	} {
		res, text = call(t, cs, "validate_discovery_source", c.args)
		if !res.IsError || !strings.Contains(text, c.want) {
			t.Errorf("expected %q, got %s", c.want, text)
		}
	}

	res, text = call(t, cs, "validate_discovery_source", map[string]any{"provider": "remotive", "name": "Remote Go", "search_query": "go backend"})
	if res.IsError {
		t.Fatal(text)
	}
	if got := structured[SourceValidation](t, res); got.Count != 12 || got.SampleTitle == "" {
		t.Fatalf("unexpected validation: %s", text)
	}

	// The API's own failure text is surfaced verbatim.
	res, text = call(t, cs, "validate_discovery_source", map[string]any{"provider": "greenhouse", "name": "Bad board", "identifier": "nope"})
	if !res.IsError || !strings.Contains(text, "Source could not be read") {
		t.Fatalf("expected the provider error, got %s", text)
	}

	res, text = call(t, cs, "add_discovery_source", map[string]any{"provider": "remotive", "name": "Remote Go", "search_query": "go backend"})
	if res.IsError {
		t.Fatal(text)
	}
	if got := structured[DiscoverySource](t, res); got.ID != "s-new" {
		t.Fatalf("unexpected source: %s", text)
	}
	if len(fake.sources) != 1 || fake.sources[0]["active"] != true {
		t.Fatalf("active must default to true, sent %v", fake.sources)
	}

	// A duplicate is a 409 and must not carry the rejected-job hint.
	res, text = call(t, cs, "add_discovery_source", map[string]any{"provider": "remotive", "name": "Remote Go", "search_query": "go backend"})
	if !res.IsError || !strings.Contains(text, "already exists") {
		t.Fatalf("expected the duplicate error, got %s", text)
	}
	if strings.Contains(text, "Rejected jobs") {
		t.Fatalf("the rejected-job hint belongs only to a blocked-application 409: %s", text)
	}
}

func TestToolCatalogV2(t *testing.T) {
	cs, _ := session(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"get_profile": true, "list_jobs": true, "get_job": true, "list_applications": true,
		"get_application": true, "get_dashboard": true, "list_discovery_runs": true,
		"get_filters": true, "list_discovery_sources": true, "validate_discovery_source": true,
		"generate_documents": false, "create_application": false, "update_application_status": false,
		"run_discovery": false, "import_job": false, "add_discovery_source": false,
	}
	seen := map[string]bool{}
	for _, tool := range res.Tools {
		readOnly, known := want[tool.Name]
		if !known {
			t.Errorf("unexpected tool %q", tool.Name)
			continue
		}
		seen[tool.Name] = true
		if tool.Title == "" {
			t.Errorf("%s: needs a Title for hosts that show one", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("%s: missing description", tool.Name)
		}
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly {
			t.Errorf("%s: readOnlyHint should be %v", tool.Name, readOnly)
		}
		if strings.Contains(tool.Name, "outbound") || strings.Contains(tool.Name, "delete") {
			t.Errorf("%s: attribution and deletes stay out of the tool set", tool.Name)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestPromptsCarryTheWorkflowRules(t *testing.T) {
	cs, _ := session(t)
	ctx := context.Background()
	list, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]*mcp.Prompt{}
	for _, p := range list.Prompts {
		names[p.Name] = p
	}
	if len(names) != 2 || names["prepare_application"] == nil || names["weekly_review"] == nil {
		t.Fatalf("expected both prompts, got %d", len(list.Prompts))
	}
	if args := names["prepare_application"].Arguments; len(args) != 1 || args[0].Name != "job_id" || !args[0].Required {
		t.Fatalf("prepare_application needs a required job_id argument, got %+v", args)
	}

	got, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "prepare_application", Arguments: map[string]string{"job_id": "job-a"}})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, msg := range got.Messages {
		if tc, ok := msg.Content.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	body := strings.ToLower(text.String())
	for _, want := range []string{"job-a", "never invent", "apply_url", "get_filters"} {
		if !strings.Contains(body, want) {
			t.Errorf("prepare_application should mention %q", want)
		}
	}

	if _, err = cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "prepare_application"}); err == nil {
		t.Fatal("a missing job_id must be an error")
	}
	if _, err = cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "weekly_review"}); err != nil {
		t.Fatalf("weekly_review takes no arguments: %v", err)
	}
}

// lockedBuffer collects log output without racing the server goroutine.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func TestToolCallsAreLoggedWithoutArguments(t *testing.T) {
	logs := &lockedBuffer{}
	cs, _ := sessionWithLogger(t, slog.New(slog.NewTextHandler(logs, nil)))
	if res, text := call(t, cs, "get_job", map[string]any{"job_id": "job-a"}); res.IsError {
		t.Fatal(text)
	}
	if res, _ := call(t, cs, "get_job", map[string]any{"job_id": "nope"}); !res.IsError {
		t.Fatal("expected the missing job to be a tool error")
	}
	out := logs.String()
	if !strings.Contains(out, "get_job") || !strings.Contains(out, "duration_ms") {
		t.Fatalf("each tool call should log its name and duration: %s", out)
	}
	if !strings.Contains(out, "is_error=true") {
		t.Fatalf("a failing call should be logged as an error: %s", out)
	}
	if strings.Contains(out, "job-a") || strings.Contains(out, "nope") {
		t.Fatalf("arguments must never be logged: %s", out)
	}
}

func TestRunDiscoveryReportsProgressOnlyWhenAsked(t *testing.T) {
	var mu sync.Mutex
	var notes []string
	client := mcp.NewClient(&mcp.Implementation{Name: "test-host", Version: "0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			notes = append(notes, req.Params.Message)
			mu.Unlock()
		},
	})
	cs := sessionWithClient(t, client)
	ctx := context.Background()

	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "run_discovery", Meta: mcp.Meta{"progressToken": "p1"},
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	withToken := len(notes)
	notes = nil
	mu.Unlock()
	if withToken == 0 {
		t.Fatal("a progress token should produce at least one notification")
	}

	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "run_discovery"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	withoutToken := len(notes)
	mu.Unlock()
	if withoutToken != 0 {
		t.Fatalf("no token means no notifications, got %d", withoutToken)
	}
}
