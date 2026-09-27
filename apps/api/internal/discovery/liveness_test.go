package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClosedByTellsDefiniteAnswersFromNoise(t *testing.T) {
	mustParse := func(s string) *url.URL { u, _ := url.Parse(s); return u }
	cases := []struct {
		name   string
		status int
		final  string
		closed bool
	}{
		{"410 is the aggregator saying the posting was removed", 410, "https://www.arbeitnow.com/jobs/x-1", true},
		{"404 means the posting is gone", 404, "https://jobs.lever.co/acme/123", true},
		{"a live page", 200, "https://www.arbeitnow.com/jobs/x-1", false},
		{"Greenhouse sends a closed job back to its board with error=true", 200, "https://job-boards.greenhouse.io/acme?error=true", true},
		{"error=true means nothing on another host", 200, "https://example.com/jobs?error=true", false},
		{"a server error is not an answer", 503, "https://example.com/jobs/1", false},
		{"bot protection is not an answer", 403, "https://example.com/jobs/1", false},
		{"rate limiting is not an answer", 429, "https://example.com/jobs/1", false},
	}
	for _, c := range cases {
		closed, reason := closedBy(c.status, mustParse(c.final))
		if closed != c.closed {
			t.Errorf("%s: closed=%v, want %v", c.name, closed, c.closed)
		}
		if closed && reason == "" {
			t.Errorf("%s: a closed posting needs a reason to show the user", c.name)
		}
	}
}

func TestCheckPostingReadsTheStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gone":
			w.WriteHeader(http.StatusGone)
		case "/moved":
			http.Redirect(w, r, "/gone", http.StatusFound)
		default:
			w.Write([]byte("<h1>Senior Go Engineer</h1>"))
		}
	}))
	defer server.Close()
	checker := NewLinkChecker(true)
	for path, want := range map[string]bool{"/gone": true, "/moved": true, "/open": false} {
		closed, _, err := checker.Check(context.Background(), server.URL+path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if closed != want {
			t.Errorf("%s: closed=%v, want %v", path, closed, want)
		}
	}
}

// On a public deployment the checker fetches links users supplied, so it must
// not be usable to reach the server's own network.
func TestCheckPostingRefusesPrivateAddresses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer server.Close()
	closed, _, err := NewLinkChecker(false).Check(context.Background(), server.URL+"/anything")
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("a loopback link should be refused, got closed=%v err=%v", closed, err)
	}
	if closed {
		t.Fatal("a refused check must never close a job")
	}
}
