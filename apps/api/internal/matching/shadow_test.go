package matching

import (
	"reflect"
	"testing"
	"time"

	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/models"
)

func TestShadow(t *testing.T) {
	job := models.JobInput{Title: "Backend Engineer", Company: "Example GmbH", Description: "Go or Rust required."}
	profile := models.Profile{DesiredTitles: []string{"Backend Engineer"}, Skills: []models.Skill{{Name: "Go"}}}
	blacklist := []models.BlacklistEntry{{CompanyName: "Example GmbH", NormalizedName: "example gmbh"}}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	rules := Evaluate(profile, blacklist, nil, job, now)
	answer := models.JobExtraction{
		ID: "example-answer", TextHash: extraction.Hash(job.Title, job.Description), Status: "ok",
		CreatedAt: now.In(time.FixedZone("test", 3600)),
		Extraction: &models.Extraction{
			Required: []models.ExtractedSkills{{Skills: []string{"Go", "Rust"}, Quote: job.Description}},
		},
	}
	want := &models.ShadowEvaluation{
		ExtractionID: answer.ID, TextHash: answer.TextHash, AnsweredAt: now,
		RulesScore: rules.Score, RulesRejected: rules.Rejected,
		Evaluation: EvaluateWith(profile, blacklist, nil, job, answer.Extraction, now),
	}
	if got := Shadow(profile, blacklist, nil, job, rules, answer, now); !reflect.DeepEqual(got, want) {
		t.Fatalf("Shadow() = %+v, want %+v", got, want)
	}
	for _, tc := range []struct {
		name   string
		change func(*models.JobExtraction)
	}{
		{"rejected", func(x *models.JobExtraction) { x.Status = "rejected" }},
		{"missing extraction", func(x *models.JobExtraction) { x.Extraction = nil }},
		{"stale hash", func(x *models.JobExtraction) { x.TextHash = extraction.Hash(job.Title, "Go required.") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := answer
			tc.change(&x)
			if got := Shadow(profile, blacklist, nil, job, rules, x, now); got != nil {
				t.Fatalf("Shadow() = %+v, want nil", got)
			}
		})
	}
}
