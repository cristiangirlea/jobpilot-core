package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/models"
)

// The pending list asks the store for the configured model, this prompt version and the caller's limit.
func TestPendingExtractionsAsksForTheConfiguredModel(t *testing.T) {
	type asked struct {
		model, prompt string
		limit         int
	}
	var calls []asked
	s := &Server{ExtractionModel: "example-model"}
	s.pendingJobs = func(_ context.Context, model, prompt string, limit int) ([]models.PendingExtraction, error) {
		calls = append(calls, asked{model, prompt, limit})
		return []models.PendingExtraction{{ID: "i", Title: "t", Description: "d", TextHash: "h"}}, nil
	}
	get := func(query string) string {
		t.Helper()
		w := httptest.NewRecorder()
		s.pendingExtractions(w, httptest.NewRequest("GET", "/api/extractions/pending"+query, nil))
		if w.Code != 200 {
			t.Fatalf("GET %s = %d", query, w.Code)
		}
		return w.Body.String()
	}
	body := get("?limit=7")
	get("")
	want := []asked{{"example-model", extraction.PromptVersion, 7}, {"example-model", extraction.PromptVersion, 50}}
	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("store asked for %+v, want %+v", calls, want)
	}
	if want := `{"model":"example-model","prompt_version":"` + extraction.PromptVersion + `","jobs":[{"id":"i","title":"t","description":"d","text_hash":"h"}]}` + "\n"; body != want {
		t.Fatalf("body = %s, want %s", body, want)
	}

	s.ExtractionModel = ""
	if body := get(""); len(calls) != 2 || body != `{"model":"","prompt_version":"`+extraction.PromptVersion+`","jobs":[]}`+"\n" {
		t.Fatalf("with extraction off the store was asked %d times and the body was %s", len(calls), body)
	}
}
