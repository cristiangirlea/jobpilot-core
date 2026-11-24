package matching

import (
	"reflect"
	"sync"
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
		RulesParts: &models.RulesParts{Skill: rules.Breakdown["skill_match"], Seniority: rules.Breakdown["seniority_match"], MatchedSkills: 1},
		Evaluation: EvaluateWith(profile, blacklist, nil, job, answer.Extraction, now),
	}
	if skill, seniority := want.RulesParts.Skill, want.RulesParts.Seniority; skill == 0 || seniority == 0 || skill == seniority {
		t.Fatalf("the fixture needs two different parts above zero, got skill %d and seniority %d", skill, seniority)
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

// A re-score evaluates jobs on several cores at once with the same profile,
// exclusions and filter, so scoring must only read them. The race detector
// in CI is what fails this test.
func TestScoringIsSafeFromSeveralGoroutines(t *testing.T) {
	profile := models.Profile{
		DesiredTitles: []string{"Backend Engineer"}, Seniorities: []string{"senior"}, Languages: []string{"English fluent"},
		Skills: []models.Skill{{Name: "Go"}, {Name: "Spring Boot"}, {Name: "stakeholder management"}},
	}
	blacklist := []models.BlacklistEntry{{CompanyName: "Example GmbH", NormalizedName: "example gmbh"}}
	filter := &models.FilterPreset{RequiredSkills: []string{"Go"}, ExcludedTechnologies: []string{"Kotlin"}, RejectUnlistedLanguages: true}
	job := models.JobInput{Title: "Senior Backend Engineer", Company: "Brambleglass Labs", Description: "Go or Rust required. Fluent German. Own the go-to-market plan with strong stakeholder management."}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	answer := models.JobExtraction{
		ID: "example-answer", TextHash: extraction.Hash(job.Title, job.Description), Status: "ok", CreatedAt: now,
		Extraction: &models.Extraction{Required: []models.ExtractedSkills{
			{Skills: []string{"Go", "Rust"}, Quote: "Go or Rust required."},
			{Skills: []string{"stakeholder management"}, Quote: "strong stakeholder management"},
		}},
	}
	wantRules := Evaluate(profile, blacklist, filter, job, now)
	wantShadow := Shadow(profile, blacklist, filter, job, wantRules, answer, now)
	if wantShadow == nil {
		t.Fatal("the fixture has no shadow")
	}
	results := make([]bool, 16)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			for range 20 {
				rules := Evaluate(profile, blacklist, filter, job, now)
				if !reflect.DeepEqual(rules, wantRules) || !reflect.DeepEqual(Shadow(profile, blacklist, filter, job, rules, answer, now), wantShadow) {
					return
				}
			}
			results[i] = true
		})
	}
	wg.Wait()
	for i, same := range results {
		if !same {
			t.Fatalf("goroutine %d scored the job differently", i)
		}
	}
}
