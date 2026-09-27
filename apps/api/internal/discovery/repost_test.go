package discovery

import (
	"testing"
	"time"

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

// Jobs stored twice before reposts were recognised stay in the catalog. Of
// each opening's copies, the newest posting is the live one; the older copies
// are the ones to close. A copy with nothing to match stays open.
func TestOlderCopiesPointAtTheNewestPosting(t *testing.T) {
	day := func(month time.Month, d int) *time.Time {
		at := time.Date(2026, month, d, 0, 0, 0, 0, time.UTC)
		return &at
	}
	opening := models.JobInput{Title: "Remote Python Backend Engineer", Location: "Berlin", Description: "Build the scheduling platform in Python and own services end to end."}
	copyOf := func(id string, posted *time.Time) models.Job {
		job := models.Job{ID: id, JobInput: opening}
		job.PostedAt = posted
		return job
	}
	other := models.Job{ID: "frontend", JobInput: models.JobInput{Title: "Frontend Engineer", Location: "Berlin", Description: opening.Description}}
	undated := copyOf("undated", nil)
	undated.CreatedAt = time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

	copies := olderCopies([]models.Job{copyOf("aug27", day(time.August, 27)), other, copyOf("sep14", day(time.September, 14)), undated, copyOf("sep09", day(time.September, 9))})
	want := map[string]string{"aug27": "sep14", "sep09": "sep14", "undated": "sep14"}
	if len(copies) != len(want) {
		t.Fatalf("got %v, want %v", copies, want)
	}
	for copyID, survivor := range want {
		if copies[copyID] != survivor {
			t.Errorf("%s should be closed in favour of %s, got %q", copyID, survivor, copies[copyID])
		}
	}
}
