package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"jobpilot/core/api/internal/config"
)

// Each configured origin has to reach the reader the server is given, or the
// end-to-end test's fake boards would be bypassed for the public services.
// Every provider gets its own origin, so a setting wired to the wrong provider fails.
func TestFormReaderUsesTheConfiguredOrigins(t *testing.T) {
	var mu sync.Mutex
	seen := map[string][]string{}
	origin := func(name string) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen[name] = append(seen[name], r.URL.Path)
			mu.Unlock()
			http.NotFound(w, r)
		}))
		t.Cleanup(server.Close)
		return server
	}
	greenhouse, ashby, recruitee := origin("greenhouse"), origin("ashby"), origin("recruitee")

	reader := formReader(config.Config{ATSGreenhouseURL: greenhouse.URL, ATSAshbyURL: ashby.URL, ATSRecruiteeURL: recruitee.URL})
	for _, tc := range []struct{ link, origin, path string }{
		{"https://boards.greenhouse.io/fakeco/jobs/12345", "greenhouse", "/v1/boards/fakeco/jobs/12345"},
		{"https://jobs.ashbyhq.com/fakeco/6afff2cc", "ashby", "/api/non-user-graphql"},
		{"https://fakeco.recruitee.com/o/backend-engineer", "recruitee", "/api/offers/backend-engineer"},
	} {
		mu.Lock()
		clear(seen)
		mu.Unlock()

		_, _ = reader.Fetch(context.Background(), tc.link)

		mu.Lock()
		for name, paths := range seen {
			if name != tc.origin {
				t.Errorf("%s reached the %s origin: %v", tc.link, name, paths)
			}
		}
		if !slices.Contains(seen[tc.origin], tc.path) {
			t.Errorf("%s did not request %s from the %s origin; saw %v", tc.link, tc.path, tc.origin, seen)
		}
		mu.Unlock()
	}
}
