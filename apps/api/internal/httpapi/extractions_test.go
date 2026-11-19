package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
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

func TestExtractionSummaryUsesSignedInAccount(t *testing.T) {
	const userID = "example-account"
	const secret = "extraction-summary-test-secret"
	token, err := auth.Token(userID, "admin@example.test", secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ setting, want string }{{"", "off"}, {"shadow", "shadow"}} {
		t.Run(tc.want, func(t *testing.T) {
			s := &Server{Secret: secret, ExtractionModel: "example-model", ExtractionScoring: tc.setting}
			calls := 0
			s.extractionCounts = func(_ context.Context, user, model, prompt string) (models.ExtractionSummary, error) {
				calls++
				if user != userID || model != s.ExtractionModel || prompt != extraction.PromptVersion {
					t.Fatalf("summary requested for %q, %q, %q", user, model, prompt)
				}
				return models.ExtractionSummary{Model: model, PromptVersion: prompt, MinScore: 75}, nil
			}
			w := call(t, s.Handler(), "GET", "/api/extractions/summary", token, "")
			var out models.ExtractionSummary
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Scoring != tc.want ||
				out.Model != s.ExtractionModel || out.PromptVersion != extraction.PromptVersion || out.MinScore != 75 || calls != 1 {
				t.Fatalf("response = %d %s, seam called %d times", w.Code, w.Body, calls)
			}
		})
	}
}
