package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

// RecordOutbound applies at models.Job.ApplyTarget: the partner link, else the
// employer's page, else the apply URL. The query still only reaches a job the
// user matches and has not rejected. Runs on a migrated database named by
// JOBPILOT_TEST_DATABASE_URL, and is skipped without one.
func TestRecordOutboundPostgres(t *testing.T) {
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
	st := New(pool)

	run := time.Now().Format("20060102150405.000000")
	user, err := st.CreateUser(ctx, "outbound-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser(ctx, "outbound-other-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	add := func(name, apply, employer, partner string, rejected bool) string {
		in := models.JobInput{Title: name, Company: "Outbound Test " + run, ApplyURL: apply}
		job, err := st.CreateDiscoveredJob(ctx, user.ID, "", "", "", "", "outbound-"+run+"-"+name, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.Exec(ctx, `UPDATE jobs SET employer_url=$2, partner_apply_url=$3, referral_partner=$4 WHERE id=$1`, job.ID, employer, partner, "partner-"+name); err != nil {
			t.Fatal(err)
		}
		evaluation := models.Evaluation{Rejected: rejected, Breakdown: map[string]int{}, RejectionReasons: []string{}, Warnings: []string{}, Reasons: []string{}, MatchedSkills: []string{}, MissingSkills: []string{}, RedFlags: []string{}}
		if _, err = st.SaveJobMatch(ctx, user.ID, job.ID, evaluation, ""); err != nil {
			t.Fatal(err)
		}
		return job.ID
	}
	const apply, employer, partner = "https://aggregator.example/j/1", "https://boards.example/acme/1", "https://partner.example/p/1"

	for _, c := range []struct {
		name, apply, employer, partner, want string
	}{
		{"all", apply, employer, partner, partner},
		{"employer", apply, employer, "", employer},
		{"apply", apply, "", "", apply},
	} {
		id := add(c.name, c.apply, c.employer, c.partner, false)
		destination, referral, _, err := st.RecordOutbound(ctx, user.ID, id)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if destination != c.want || referral != "partner-"+c.name {
			t.Errorf("%s: got %q via %q, want %q via %q", c.name, destination, referral, c.want, "partner-"+c.name)
		}
		var recorded string
		if err = st.DB.QueryRow(ctx, `SELECT destination_url FROM outbound_clicks WHERE user_id=$1 AND job_id=$2`, user.ID, id).Scan(&recorded); err != nil || recorded != c.want {
			t.Errorf("%s: recorded %q (%v), want %q", c.name, recorded, err, c.want)
		}
	}

	if _, _, _, err = st.RecordOutbound(ctx, user.ID, add("none", "", "", "", false)); err == nil {
		t.Error("a job with no link should be refused")
	}
	rejected := add("rejected", apply, employer, partner, true)
	if _, _, _, err = st.RecordOutbound(ctx, user.ID, rejected); !errors.Is(err, ErrApplicationBlocked) {
		t.Errorf("a rejected job should be blocked, got %v", err)
	}
	if _, _, _, err = st.RecordOutbound(ctx, other.ID, add("unmatched", apply, employer, partner, false)); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("another user's match should not be reachable, got %v", err)
	}
	var clicks int
	if err = st.DB.QueryRow(ctx, `SELECT count(*) FROM outbound_clicks WHERE user_id IN ($1,$2)`, user.ID, other.ID).Scan(&clicks); err != nil || clicks != 3 {
		t.Errorf("only the three allowed clicks should be recorded, got %d (%v)", clicks, err)
	}
}
