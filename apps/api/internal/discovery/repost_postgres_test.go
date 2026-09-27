package discovery

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// The startup cleanup is mostly SQL, which a unit test cannot reach. This runs
// it on a migrated database named by JOBPILOT_TEST_DATABASE_URL, and is
// skipped without one; CI's integration job provides it. Everything it
// creates is named uniquely, so it can share a database with other data.
func TestCloseStoredRepostsAgainstPostgres(t *testing.T) {
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated database to run this")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := store.New(pool)

	run := time.Now().Format("20060102150405.000000")
	user, err := st.CreateUser(ctx, "reposts-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	opening := models.JobInput{
		Title: "Backend Engineer", Company: "Repost Test " + run, Location: "Berlin",
		Description: "Build the platform's backend services in Go and own them end to end.",
	}
	// Three postings of one opening, as a board reposts it under new links.
	create := func(n, daysAgo int) models.JobInput {
		in := opening
		in.ApplyURL = fmt.Sprintf("https://example.test/%s/%d", run, n)
		posted := time.Now().AddDate(0, 0, -daysAgo)
		in.PostedAt = &posted
		if _, err := st.CreateDiscoveredJob(ctx, user.ID, "", "arbeitnow", "Arbeitnow", fmt.Sprint(n), Fingerprint(in), in); err != nil {
			t.Fatal(err)
		}
		return in
	}
	drafted, older, newest := create(1, 10), create(2, 5), create(3, 1)

	// Someone's draft hangs off the oldest copy, so it must stay open.
	draftedJob, err := st.CatalogJobByFingerprint(ctx, Fingerprint(drafted))
	if err != nil {
		t.Fatal(err)
	}
	evaluation := models.Evaluation{Breakdown: map[string]int{}, RejectionReasons: []string{}, Warnings: []string{}, Reasons: []string{}, MatchedSkills: []string{}, MissingSkills: []string{}, RedFlags: []string{}}
	if _, err = st.SaveJobMatch(ctx, user.ID, draftedJob.ID, evaluation, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateApplication(ctx, user.ID, draftedJob.ID); err != nil {
		t.Fatal(err)
	}

	for pass := 1; pass <= 2; pass++ {
		if _, err = CloseStoredReposts(ctx, st); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		for _, c := range []struct {
			name   string
			in     models.JobInput
			reason string
		}{
			{"the newest posting", newest, ""},
			{"an older copy", older, ReasonReposted},
			{"a copy with a draft", drafted, ""},
		} {
			job, err := st.CatalogJobByFingerprint(ctx, Fingerprint(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if job.ClosedReason != c.reason || (c.reason == "") != (job.ClosedAt == nil) {
				t.Errorf("pass %d: %s closed %v with %q, want %q", pass, c.name, job.ClosedAt != nil, job.ClosedReason, c.reason)
			}
		}
	}
}
