package boards

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// stallingGreenhouse answers no lookup until the request is abandoned, and
// says when one has arrived.
func stallingGreenhouse(t *testing.T) (*ats.Reader, <-chan struct{}) {
	t.Helper()
	arrived := make(chan struct{}, 1)
	reader, _ := fakeGreenhouse(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case arrived <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})
	return reader, arrived
}

// prompt bounds how long an abandoned lookup may take to return: well above
// the deadlines used here, well below the reader's own 20s timeout.
const prompt = 3 * time.Second

func TestResolveGivesUpAtItsOwnDeadline(t *testing.T) {
	saved := resolveTimeout
	resolveTimeout = 100 * time.Millisecond
	t.Cleanup(func() { resolveTimeout = saved })

	reader, _ := stallingGreenhouse(t)
	start := time.Now()
	if got, ok := Resolve(context.Background(), reader, "https://careers.example.com/?gh_jid=123"); ok || got != (Detected{}) {
		t.Fatalf("Resolve = %+v, %v; want nothing", got, ok)
	}
	if elapsed := time.Since(start); elapsed > prompt {
		t.Fatalf("Resolve took %v past a %v deadline", elapsed, resolveTimeout)
	}
}

func TestResolveStopsWhenTheCallerDoes(t *testing.T) {
	t.Run("cancelled during the lookup", func(t *testing.T) {
		reader, arrived := stallingGreenhouse(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			<-arrived
			cancel()
		}()
		start := time.Now()
		if _, ok := Resolve(ctx, reader, "https://careers.example.com/?gh_jid=123"); ok {
			t.Fatal("a cancelled lookup should leave the link undetected")
		}
		if elapsed := time.Since(start); elapsed > prompt {
			t.Fatalf("Resolve took %v after its caller cancelled", elapsed)
		}
	})
	t.Run("earlier deadline", func(t *testing.T) {
		reader, _ := stallingGreenhouse(t)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		if _, ok := Resolve(ctx, reader, "https://careers.example.com/?gh_jid=123"); ok {
			t.Fatal("a lookup past its caller's deadline should leave the link undetected")
		}
		if elapsed := time.Since(start); elapsed > prompt {
			t.Fatalf("Resolve took %v past its caller's 100ms deadline", elapsed)
		}
	})
}

// countingServer stands in for a host Resolve must never contact.
func countingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// Only Greenhouse is asked: neither the pasted link's host nor the host a
// redirect names is fetched, whether or not the redirect names a board.
func TestResolveFetchesNothingButGreenhouse(t *testing.T) {
	careers, careersHits := countingServer(t)
	elsewhere, elsewhereHits := countingServer(t)
	link := careers.URL + "/jobs?gh_jid=123"

	reader, hits := fakeGreenhouse(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/embed/job_app?for=acme&token=123", http.StatusFound)
	})
	if got, ok := Resolve(context.Background(), reader, link); ok || got != (Detected{}) {
		t.Fatalf("a redirect to another host names no board: %+v, %v", got, ok)
	}
	reader, boardHits := fakeGreenhouse(t, redirectToBoard)
	if got, ok := Resolve(context.Background(), reader, link); !ok || got.Identifier != "acme" || got.JobID != "123" {
		t.Fatalf("Resolve = %+v, %v; want acme/123", got, ok)
	}
	if hits.Load() != 1 || boardHits.Load() != 1 {
		t.Fatalf("want one lookup each at Greenhouse, got %d and %d", hits.Load(), boardHits.Load())
	}
	if careersHits.Load() != 0 || elsewhereHits.Load() != 0 {
		t.Fatalf("the link's host got %d requests and the redirect's host %d; want none", careersHits.Load(), elsewhereHits.Load())
	}
}

// Greenhouse is asked for exactly the digits the link carries, or not at all.
func TestResolveHostileJobIDs(t *testing.T) {
	cases := []struct {
		name, query string
		token       string // "" means no request
	}{
		{"plain", "gh_jid=123", "123"},
		{"percent-encoded digits", "gh_jid=%31%32%33", "123"},
		{"encoded ampersand", "gh_jid=123%26token%3D9", ""},
		{"encoded slash", "gh_jid=123%2F..%2F9", ""},
		{"encoded hash", "gh_jid=123%239", ""},
		{"encoded space", "gh_jid=123%20", ""},
		{"encoded newline", "gh_jid=123%0D%0AX:1", ""},
		{"semicolon", "gh_jid=123;9", ""},
		{"empty", "gh_jid=", ""},
		{"no value", "gh_jid", ""},
		{"letters", "gh_jid=12a", ""},
		{"minus", "gh_jid=-123", ""},
		{"plus", "gh_jid=%2B123", ""},
		{"fullwidth digits", "gh_jid=%EF%BC%91%EF%BC%92", ""},
		{"19 digits", "gh_jid=9223372036854775807", "9223372036854775807"},
		{"20 digits", "gh_jid=12345678901234567890", ""},
		{"a megabyte of digits", "gh_jid=" + strings.Repeat("7", 1<<20), ""},
		// The first gh_jid wins, in either order, and a bad first one is not
		// rescued by a good second one.
		{"duplicate, 111 first", "gh_jid=111&gh_jid=222", "111"},
		{"duplicate, 222 first", "gh_jid=222&gh_jid=111", "222"},
		{"duplicate, bad first", "gh_jid=abc&gh_jid=111", ""},
		{"fragment ignored", "gh_jid=123#gh_jid=9", "123"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var asked atomic.Value
			reader, hits := fakeGreenhouse(t, func(w http.ResponseWriter, r *http.Request) {
				asked.Store(r.URL.RawQuery)
				redirectToBoard(w, r)
			})
			got, ok := Resolve(context.Background(), reader, "https://careers.example.com/?"+c.query)
			if c.token == "" {
				if ok || hits.Load() != 0 {
					t.Fatalf("Resolve = %+v, %v with %d requests; want nothing and no request", got, ok, hits.Load())
				}
				return
			}
			if hits.Load() != 1 || asked.Load() != "token="+c.token {
				t.Fatalf("Greenhouse was asked %d times for %v; want once for token=%s", hits.Load(), asked.Load(), c.token)
			}
			if !ok || got.Identifier != "acme" || got.JobID != c.token {
				t.Fatalf("Resolve = %+v, %v; want acme/%s", got, ok, c.token)
			}
		})
	}
}

// A redirect without a usable Location, or one naming an empty board, leaves
// the link undetected.
func TestResolveFallsBackOnAnUnusableLocation(t *testing.T) {
	answers := map[string]func(w http.ResponseWriter, r *http.Request){
		"missing Location": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusFound) },
		"malformed Location": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://%zz/embed/job_app?for=acme&token=123")
			w.WriteHeader(http.StatusFound)
		},
		"relative Location": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/embed/job_app?for=acme&token=123")
			w.WriteHeader(http.StatusFound)
		},
		"empty board": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://job-boards.greenhouse.io/embed/job_app?for=&token=123", http.StatusFound)
		},
		"no board parameter": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://job-boards.greenhouse.io/embed/job_app?token=123", http.StatusFound)
		},
	}
	for name, answer := range answers {
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
