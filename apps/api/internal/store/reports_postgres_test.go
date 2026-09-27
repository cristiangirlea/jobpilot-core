package store

import (
	"context"
	"os"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

// A report that a posting is gone hides it from the reporter at once. It
// closes for everyone only on a second person's report, since the catalog is
// shared and one mistaken report must not hide a live job from others. Taking
// a report back reopens a job that closed on reports alone. This runs on a
// migrated database named by JOBPILOT_TEST_DATABASE_URL, and is skipped
// without one; CI's integration job provides it.
func TestJobReportsAgainstPostgres(t *testing.T) {
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
	ana, err := st.CreateUser(ctx, "reports-ana-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(ctx, "reports-bob-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	in := models.JobInput{Title: "Backend Engineer", Company: "Report Test " + run, Description: "Build services.", ApplyURL: "https://example.test/" + run}
	job, err := st.CreateDiscoveredJob(ctx, ana.ID, "", "", "", "", "report-"+run, in)
	if err != nil {
		t.Fatal(err)
	}
	evaluation := models.Evaluation{Score: 80, Breakdown: map[string]int{}, RejectionReasons: []string{}, Warnings: []string{}, Reasons: []string{}, MatchedSkills: []string{}, MissingSkills: []string{}, RedFlags: []string{}}
	for _, u := range []string{ana.ID, bob.ID} {
		if _, err = st.SaveJobMatch(ctx, u, job.ID, evaluation, ""); err != nil {
			t.Fatal(err)
		}
	}
	visible := func(u string) bool {
		jobs, _, err := st.Jobs(ctx, u, JobQuery{OpenOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range jobs {
			if x.Job.ID == job.ID {
				return true
			}
		}
		return false
	}
	closedReason := func() string {
		stored, err := st.CatalogJobByFingerprint(ctx, "report-"+run)
		if err != nil {
			t.Fatal(err)
		}
		if stored.ClosedAt == nil {
			return ""
		}
		return stored.ClosedReason
	}
	const reported = "two people reported it is no longer available"

	if n, err := st.ReportJob(ctx, ana.ID, job.ID); err != nil || n != 1 {
		t.Fatalf("first report: %d, %v", n, err)
	}
	if n, _ := st.ReportJob(ctx, ana.ID, job.ID); n != 1 {
		t.Fatalf("reporting twice is still one person, got %d", n)
	}
	if visible(ana.ID) || !visible(bob.ID) {
		t.Fatalf("one report hides the job from its reporter only: ana %v, bob %v", visible(ana.ID), visible(bob.ID))
	}
	if yes, _ := st.HasReported(ctx, ana.ID, job.ID); !yes {
		t.Fatal("ana's report should be remembered for her job page")
	}
	if dashboard, _ := st.Dashboard(ctx, ana.ID); len(dashboard["top_jobs"].([]models.JobWithMatch)) != 0 {
		t.Fatalf("a reported job should leave the reporter's recommendations, got %v", dashboard["top_jobs"])
	}

	if n, _ := st.ReportJob(ctx, bob.ID, job.ID); n != ReportsToClose {
		t.Fatalf("second person's report: got %d", n)
	}
	if err = st.CloseJob(ctx, job.ID, reported); err != nil {
		t.Fatal(err)
	}
	if closedReason() != reported {
		t.Fatalf("the job should be closed for everyone, got %q", closedReason())
	}

	// Ana takes hers back: one report is not enough to keep it closed, but
	// Bob's still hides it from Bob.
	if err = st.WithdrawReport(ctx, ana.ID, job.ID, reported); err != nil {
		t.Fatal(err)
	}
	if closedReason() != "" || !visible(ana.ID) || visible(bob.ID) {
		t.Fatalf("withdrawing should reopen it and show it to ana only: reason %q, ana %v, bob %v", closedReason(), visible(ana.ID), visible(bob.ID))
	}

	// A job the link check closed stays closed whatever reports do.
	if err = st.RecordLinkCheck(ctx, job.ID, true, "its link now returns 404 Not Found", ""); err != nil {
		t.Fatal(err)
	}
	if err = st.WithdrawReport(ctx, bob.ID, job.ID, reported); err != nil {
		t.Fatal(err)
	}
	if closedReason() != "its link now returns 404 Not Found" {
		t.Fatalf("a link-check closure must survive a withdrawn report, got %q", closedReason())
	}

	links, err := st.JobLinks(ctx, job.ID)
	if err != nil || links.ApplyURL != in.ApplyURL {
		t.Fatalf("the check needs the job's own link, got %+v, %v", links, err)
	}
}
