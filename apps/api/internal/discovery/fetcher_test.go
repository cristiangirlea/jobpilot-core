package discovery

import (
	"context"
	"jobpilot/core/api/internal/models"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemotiveFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search") != "go engineer" {
			t.Error("missing search")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jobs":[{"id":7,"url":"https://apply.test/7","title":"Go Engineer","company_name":"Example","category":"Software","job_type":"full_time","publication_date":"2026-06-18T10:00:00Z","candidate_required_location":"Europe","description":"<p>Build &amp; ship.</p>"}]}`))
	}))
	defer srv.Close()
	f := NewFetcher()
	f.RemotiveBase = srv.URL
	jobs, err := f.Fetch(context.Background(), models.DiscoverySource{Provider: "remotive", SearchQuery: "go engineer"})
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if jobs[0].Input.Description != "Build & ship." || jobs[0].Input.RemoteType != "remote" {
		t.Fatal(jobs[0])
	}
}
func TestGreenhouseRequiresToken(t *testing.T) {
	f := NewFetcher()
	if _, err := f.Fetch(context.Background(), models.DiscoverySource{Provider: "greenhouse"}); err == nil {
		t.Fatal("expected token validation")
	}
}

// ATS_GREENHOUSE_URL names the Greenhouse origin for the form reader, and
// board listings live on the same origin, so one setting moves both to a
// test double. Unset, the public API is used.
func TestGreenhouseOriginAlsoMovesBoardListings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/boards/fakeco/jobs" || r.URL.Query().Get("content") != "true" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jobs":[{"id":1,"title":"Backend Engineer","absolute_url":"https://job-boards.greenhouse.io/fakeco/jobs/1","content":"Go.","location":{"name":"Remote"}}]}`))
	}))
	defer srv.Close()
	f := NewFetcher()
	public := f.GreenhouseBase
	f.UseGreenhouseOrigin("")
	if f.GreenhouseBase != public {
		t.Fatalf("an empty origin should keep %s, got %s", public, f.GreenhouseBase)
	}
	f.UseGreenhouseOrigin(srv.URL + "/")
	jobs, err := f.Fetch(context.Background(), models.DiscoverySource{Provider: "greenhouse", Identifier: "fakeco", Name: "Fake Co"})
	if err != nil || len(jobs) != 1 || jobs[0].Input.Company != "Fake Co" {
		t.Fatal(jobs, err)
	}
}

func TestAshbyFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jobs":[{"id":"ash-1","title":"Platform Engineer","team":"Infrastructure","employmentType":"FullTime","location":"Europe","isRemote":true,"publishedAt":"2026-06-18T10:00:00Z","descriptionHtml":"<p>Build with Go.</p>","applyUrl":"https://apply.test/ash-1"}]}`))
	}))
	defer srv.Close()
	f := NewFetcher()
	f.AshbyBase = srv.URL
	jobs, err := f.Fetch(context.Background(), models.DiscoverySource{Provider: "ashby", Identifier: "example", Name: "Example"})
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if jobs[0].Input.Company != "Example" || jobs[0].Input.RemoteType != "remote" || jobs[0].Input.Description != "Build with Go." {
		t.Fatal(jobs[0])
	}
}

func TestSmartRecruitersFetchesDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/demo/postings" {
			w.Write([]byte(`{"content":[{"id":"sr-1","name":"Go Engineer","releasedDate":"2026-06-18T10:00:00Z","location":{"city":"Bucharest","country":"Romania","remote":true},"typeOfEmployment":{"label":"Full-time"}}]}`))
			return
		}
		w.Write([]byte(`{"applyUrl":"https://apply.test/sr-1","jobAd":{"sections":{"jobDescription":{"text":"<p>Build APIs.</p>"},"qualifications":{"text":"Go</p>"}}}}`))
	}))
	defer srv.Close()
	f := NewFetcher()
	f.SmartRecruitersBase = srv.URL
	jobs, err := f.Fetch(context.Background(), models.DiscoverySource{Provider: "smartrecruiters", Identifier: "demo", Name: "Demo"})
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if jobs[0].Input.RemoteType != "remote" || jobs[0].Input.Location != "Bucharest, Romania" {
		t.Fatal(jobs[0])
	}
}

func TestArbeitnowFiltersQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"slug":"one","company_name":"One","title":"Go Engineer","description":"<p>PostgreSQL</p>","remote":true,"url":"https://apply.test/one","tags":["Go"],"job_types":["Full-time"],"location":"Europe","created_at":1781776800},{"slug":"two","company_name":"Two","title":"Designer","description":"Design","url":"https://apply.test/two"}]}`))
	}))
	defer srv.Close()
	f := NewFetcher()
	f.ArbeitnowBase = srv.URL
	jobs, err := f.Fetch(context.Background(), models.DiscoverySource{Provider: "arbeitnow", SearchQuery: "rust OR go", ExcludedSearchQuery: "WordPress"})
	if err != nil || len(jobs) != 1 || jobs[0].ExternalID != "one" {
		t.Fatal(jobs, err)
	}
}

func TestArbeitnowExcludesAnyNegativeTerm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"slug":"one","company_name":"One","title":"Go Engineer","description":"Go and PostgreSQL","url":"https://apply.test/one"}]}`))
	}))
	defer srv.Close()
	f := NewFetcher()
	f.ArbeitnowBase = srv.URL
	jobs, err := f.Fetch(context.Background(), models.DiscoverySource{Provider: "arbeitnow", SearchQuery: "Go", ExcludedSearchQuery: "PHP OR PostgreSQL"})
	if err != nil || len(jobs) != 0 {
		t.Fatal(jobs, err)
	}
}

func TestSplitOrTerms(t *testing.T) {
	terms := splitOrTerms("Go, Rust OR Backend Engineer | go")
	if len(terms) != 3 || terms[0] != "go" || terms[1] != "rust" || terms[2] != "backend engineer" {
		t.Fatalf("unexpected OR terms: %v", terms)
	}
}
