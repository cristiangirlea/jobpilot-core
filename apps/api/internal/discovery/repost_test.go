package discovery

import (
	"testing"

	"jobpilot/core/api/internal/models"
)

// Found in early use: Arbeitnow reposts an opening under a new link,
// the fingerprint includes the link, so Larchmont's Python role was stored
// three times and prepared twice. On that catalog, 249 of 272 repeats of a
// company, title and location had descriptions at least 80% alike; the rest
// were different openings that share a title and must stay separate.
func TestRepostOfMatchesTheSameOpeningOnly(t *testing.T) {
	description := "We build a scheduling platform in Python. You will design APIs, own services end to end, and work with product on data models for shift rosters."
	stored := []models.Job{
		{ID: "other-title", JobInput: models.JobInput{Title: "Frontend Engineer", Location: "Berlin", Description: description}},
		{ID: "larchmont", JobInput: models.JobInput{Title: "Remote Python Backend Engineer", Location: "Berlin", Description: description}},
	}

	repost := models.JobInput{Title: "Remote  python backend engineer", Location: "berlin", Description: description + " Apply now."}
	if job, ok := repostOf(repost, stored); !ok || job.ID != "larchmont" {
		t.Fatalf("a reposted opening should match the stored one, got %q, %v", job.ID, ok)
	}

	elsewhere := repost
	elsewhere.Location = "Munich"
	if _, ok := repostOf(elsewhere, stored); ok {
		t.Fatal("the same title in another location is another opening")
	}

	// Deliveroo's two Senior Software Engineer openings: same title and
	// place, different teams, different text.
	different := repost
	different.Description = "Join the logistics team to scale rider dispatch in Go and Kafka, with on-call for the delivery network."
	if _, ok := repostOf(different, stored); ok {
		t.Fatal("a different description is a different opening, even with the same title")
	}
}

func TestDescriptionSimilarityIgnoresCaseAndPunctuation(t *testing.T) {
	if got := descriptionSimilarity("Build APIs, in Go.", "build apis in go"); got != 1 {
		t.Fatalf("same words should be fully similar, got %v", got)
	}
	if got := descriptionSimilarity("", ""); got != 0 {
		t.Fatalf("two empty descriptions say nothing about being the same opening, got %v", got)
	}
}
