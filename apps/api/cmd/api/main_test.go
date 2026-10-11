package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"jobpilot/core/api/internal/config"
)

// Each configured origin has to reach the reader the server is given, or the
// end-to-end test's fake boards would be bypassed for the public services.
func TestFormReaderUsesTheConfiguredOrigins(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = true
		mu.Unlock()
		http.NotFound(w, r)
	}))
	defer server.Close()

	reader := formReader(config.Config{ATSGreenhouseURL: server.URL, ATSAshbyURL: server.URL, ATSRecruiteeURL: server.URL})
	for _, link := range []string{
		"https://boards.greenhouse.io/fakeco/jobs/12345",
		"https://jobs.ashbyhq.com/fakeco/6afff2cc",
		"https://fakeco.recruitee.com/o/backend-engineer",
	} {
		_, _ = reader.Fetch(context.Background(), link)
	}
	for _, path := range []string{"/v1/boards/fakeco/jobs/12345", "/api/non-user-graphql", "/api/offers/backend-engineer"} {
		if !seen[path] {
			t.Errorf("%s was not requested from the configured origin; saw %v", path, seen)
		}
	}
}
