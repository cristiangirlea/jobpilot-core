package boards

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"jobpilot/core/api/internal/ats"
)

func TestExtractBoardRoot(t *testing.T) {
	cases := []struct {
		name       string
		rawURL     string
		provider   string
		identifier string
	}{
		{"greenhouse", "https://job-boards.greenhouse.io/grafanalabs", "greenhouse", "grafanalabs"},
		{"lever", "https://jobs.lever.co/gitlab", "lever", "gitlab"},
		{"ashby", "https://jobs.ashbyhq.com/linear", "ashby", "linear"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Extract(tc.rawURL)
			if !ok {
				t.Fatalf("Extract(%q) returned no match", tc.rawURL)
			}
			if got.Provider != tc.provider || got.Identifier != tc.identifier || !got.Supported {
				t.Fatalf("Extract(%q) = %+v, want provider %q identifier %q and supported", tc.rawURL, got, tc.provider, tc.identifier)
			}
		})
	}
}

func TestExtractPostingURL(t *testing.T) {
	got, ok := Extract("https://boards.greenhouse.io/example/jobs/123")
	if !ok {
		t.Fatal("expected posting URL to be detected")
	}
	if got.Provider != "greenhouse" || got.Identifier != "example" || got.JobID != "123" || !got.Supported {
		t.Fatalf("unexpected detection: %+v", got)
	}
}

func TestExtractUnsupportedURL(t *testing.T) {
	if _, ok := Extract("https://example.com/careers"); ok {
		t.Fatal("generic careers URL should not be detected")
	}
}

// fakeGreenhouse answers the legacy board host's job lookup as answer says and
// counts the requests, so a test can tell when no lookup was made.
func fakeGreenhouse(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) (*ats.Reader, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/embed/job_app" {
			http.NotFound(w, r)
			return
		}
		answer(w, r)
	}))
	t.Cleanup(server.Close)
	return ats.NewReader().WithGreenhouseLegacyBase(server.URL), &hits
}

func redirectToBoard(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "https://job-boards.greenhouse.io/embed/job_app?for=acme&token="+r.URL.Query().Get("token"), http.StatusMovedPermanently)
}

func TestResolveAsksGreenhouseForACareersSiteLink(t *testing.T) {
	reader, hits := fakeGreenhouse(t, redirectToBoard)
	got, ok := Resolve(context.Background(), reader, "https://careers.example.com/?gh_jid=4967927101&utm_source=x")
	want := Detected{Provider: ats.ProviderGreenhouse, Identifier: "acme", JobID: "4967927101", Supported: true}
	if !ok || got != want {
		t.Fatalf("Resolve = %+v, %v; want %+v", got, ok, want)
	}
	if hits.Load() != 1 {
		t.Fatalf("want one lookup at Greenhouse, got %d", hits.Load())
	}
}

// A failed lookup answers as detection did before: no board, and no new error.
func TestResolveFallsBackWhenGreenhouseDoesNotNameABoard(t *testing.T) {
	cases := map[string]func(w http.ResponseWriter, r *http.Request){
		"job unknown":  func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"server error": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", http.StatusInternalServerError) },
		"no redirect":  func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) },
		"other board host": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://careers.example.com/jobs/123", http.StatusFound)
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			reader, hits := fakeGreenhouse(t, answer)
			if got, ok := Resolve(context.Background(), reader, "https://careers.example.com/?gh_jid=123"); ok || got != (Detected{}) {
				t.Fatalf("Resolve = %+v, %v; want nothing", got, ok)
			}
			if hits.Load() != 1 {
				t.Fatalf("want one lookup, got %d", hits.Load())
			}
		})
	}
}

func TestResolveMakesNoRequestWhenNoneIsNeeded(t *testing.T) {
	reader, hits := fakeGreenhouse(t, redirectToBoard)
	if _, ok := Resolve(context.Background(), reader, "https://example.com/careers"); ok {
		t.Fatal("a careers page with no gh_jid should not be detected")
	}
	got, ok := Resolve(context.Background(), reader, "https://job-boards.greenhouse.io/grafanalabs/jobs/9")
	if !ok || got.Identifier != "grafanalabs" || got.JobID != "9" {
		t.Fatalf("a link naming its board should be read from the link: %+v, %v", got, ok)
	}
	if hits.Load() != 0 {
		t.Fatalf("neither link needs a lookup, got %d requests", hits.Load())
	}
	if got, ok := Resolve(context.Background(), nil, "https://jobs.lever.co/gitlab"); !ok || got.Provider != ats.ProviderLever {
		t.Fatalf("without a reader Resolve is Extract: %+v, %v", got, ok)
	}
	if _, ok := Resolve(context.Background(), nil, "https://careers.example.com/?gh_jid=123"); ok {
		t.Fatal("without a reader nothing can be looked up")
	}
}

func TestResolveGivesUpWithTheContext(t *testing.T) {
	reader, _ := fakeGreenhouse(t, redirectToBoard)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := Resolve(ctx, reader, "https://careers.example.com/?gh_jid=123"); ok {
		t.Fatal("a cancelled request should leave the link undetected")
	}
}
