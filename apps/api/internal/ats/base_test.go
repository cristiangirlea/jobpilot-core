package ats

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The base override is what lets an end-to-end test serve a form itself. It
// has to take effect through Fetch, not only through the parser, or the API
// under test would still call the public service.
func TestGreenhouseBaseOverrideRoutesThroughFetch(t *testing.T) {
	var path, query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Staff Backend Engineer","company_name":"Fake Co","questions":[{"label":"First Name","required":true,"fields":[{"name":"first_name","type":"input_text","values":[]}]}]}`))
	}))
	defer server.Close()

	reader := NewReader().WithGreenhouseBase(server.URL + "/")
	form, err := reader.Fetch(context.Background(), "https://boards.greenhouse.io/fakeco/jobs/12345")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/boards/fakeco/jobs/12345" || query != "questions=true" {
		t.Fatalf("expected the board and job from the apply URL on the fake origin, got %s?%s", path, query)
	}
	if form.Provider != ProviderGreenhouse || form.Board != "fakeco" || len(form.Fields) != 1 {
		t.Fatalf("unexpected form: %+v", form)
	}
	if form.Fields[0].Label != "First Name" || !form.Fields[0].Required {
		t.Fatalf("the field should survive parsing: %+v", form.Fields[0])
	}
}

func TestEmptyBaseKeepsThePublicService(t *testing.T) {
	if got := NewReader().WithGreenhouseBase("").greenhouseOrigin(); got != greenhouseAPI {
		t.Fatalf("an empty base must mean the public service, got %q", got)
	}
	if got := NewReader().greenhouseOrigin(); got != greenhouseAPI {
		t.Fatalf("a reader with no override must use the public service, got %q", got)
	}
}

func TestValidateGreenhouseBase(t *testing.T) {
	for _, ok := range []string{"", "  ", "https://boards-api.greenhouse.io", "http://host.docker.internal:18999", "http://localhost:18999/"} {
		if err := ValidateGreenhouseBase(ok); err != nil {
			t.Errorf("%q should be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"localhost:18999", "boards-api.greenhouse.io", "ftp://example.test", "http://", "not a url"} {
		if err := ValidateGreenhouseBase(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

// ATS_ASHBY_URL and ATS_RECRUITEE_URL move the other two readers the same
// way, so an end-to-end test can serve their forms beside the Greenhouse one.
func TestAshbyAndRecruiteeBaseOverridesRouteThroughFetch(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/non-user-graphql":
			_, _ = w.Write([]byte(`{"data":{"jobPosting":{"title":"Platform Engineer","applicationForm":{"sections":[{"fieldEntries":[{"isRequired":true,"field":{"path":"_systemfield_name","title":"Name","type":"String"}}]}]},"surveyForms":[]}}}`))
		case "/api/offers/backend-engineer":
			_, _ = w.Write([]byte(`{"offer":{"id":1,"slug":"backend-engineer","title":"Backend Engineer","company_name":"Acme GmbH"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	reader := NewReader().WithAshbyBase(server.URL + "/").WithRecruiteeBase(" " + server.URL + " ")
	form, err := reader.Fetch(context.Background(), "https://jobs.ashbyhq.com/acme/6afff2cc")
	if err != nil || form.Provider != ProviderAshby || len(form.Fields) != 1 || form.Fields[0].Label != "Name" {
		t.Fatalf("the Ashby form should be read from the override: %+v, %v", form, err)
	}
	form, err = reader.Fetch(context.Background(), "https://acme.recruitee.com/o/backend-engineer")
	if err != nil || form.Provider != ProviderRecruitee || form.Board != "acme" || form.Title != "Backend Engineer" {
		t.Fatalf("the Recruitee offer should be read from the override: %+v, %v", form, err)
	}
	if _, err = reader.Fetch(context.Background(), "https://acme.recruitee.com/o/closed-role"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an offer the override does not have is gone, got %v", err)
	}
	want := []string{"POST /api/non-user-graphql", "GET /api/offers/backend-engineer", "GET /api/offers/closed-role"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("expected %v on the override, got %v", want, paths)
	}
}

func TestEmptyAshbyAndRecruiteeBasesKeepThePublicOrigins(t *testing.T) {
	reader := NewReader().WithAshbyBase("").WithRecruiteeBase("  ")
	if got := reader.ashbyOrigin(); got != ashbyAPI {
		t.Fatalf("an empty Ashby base must mean %s, got %q", ashbyAPI, got)
	}
	if got := reader.recruiteeAPI("acme.recruitee.com", "/api/offers/x"); got != "https://acme.recruitee.com/api/offers/x" {
		t.Fatalf("an empty Recruitee base must keep the careers site's own host, got %q", got)
	}
}

func TestValidateAshbyAndRecruiteeBase(t *testing.T) {
	for setting, validate := range map[string]func(string) error{"ATS_ASHBY_URL": ValidateAshbyBase, "ATS_RECRUITEE_URL": ValidateRecruiteeBase} {
		for _, ok := range []string{"", "  ", "https://jobs.ashbyhq.com", "http://host.docker.internal:18999", "http://localhost:18999/"} {
			if err := validate(ok); err != nil {
				t.Errorf("%s: %q should be accepted: %v", setting, ok, err)
			}
		}
		for _, bad := range []string{"localhost:18999", "jobs.ashbyhq.com", "ftp://example.test", "http://", "not a url"} {
			if err := validate(bad); err == nil || !strings.Contains(err.Error(), setting) {
				t.Errorf("%s: %q should be rejected naming the setting, got %v", setting, bad, err)
			}
		}
	}
}
