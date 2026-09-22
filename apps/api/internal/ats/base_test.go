package ats

import (
	"context"
	"net/http"
	"net/http/httptest"
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
