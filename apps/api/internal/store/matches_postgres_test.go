package store

import (
	"context"
	"os"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

// A re-score writes every match of an account in one batch. When a later
// write in it fails, the earlier ones must not stay: a half-written re-score
// would leave matches scored against two different profiles.
func TestSaveJobMatchesWritesAllOrNoneAgainstPostgres(t *testing.T) {
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated database to run this")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := New(pool)

	run := time.Now().Format("20060102150405.000000")
	user, err := st.CreateUser(ctx, "matches-batch-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.CreateDiscoveredJob(ctx, user.ID, "", "", "", "", "matches-batch-"+run, models.JobInput{
		Title: "Backend Engineer", Company: "Batch Test " + run, Description: "Build services.",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := st.DB.Exec(ctx, `DELETE FROM users WHERE id=$1`, user.ID); err != nil {
			t.Error(err)
		}
		if _, err := st.DB.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, job.ID); err != nil {
			t.Error(err)
		}
	})
	if _, err := st.SaveJobMatch(ctx, user.ID, job.ID, models.Evaluation{Score: 40}, ""); err != nil {
		t.Fatal(err)
	}

	// The batch is sorted by job id, so the all-f id, which names no job, is
	// written after the fixture's and fails there.
	const missing = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if job.ID >= missing {
		t.Fatalf("the fixture's id %s does not sort before %s", job.ID, missing)
	}
	err = st.SaveJobMatches(ctx, user.ID, "", []JobEvaluation{
		{JobID: job.ID, Evaluation: models.Evaluation{Score: 90}},
		{JobID: missing, Evaluation: models.Evaluation{Score: 90}},
	})
	if err == nil {
		t.Fatal("a batch naming a job that does not exist was saved")
	}
	got, err := st.Job(ctx, user.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Match.Score != 40 {
		t.Fatalf("the match holds score %d after a failed batch, want the earlier 40", got.Match.Score)
	}
}
