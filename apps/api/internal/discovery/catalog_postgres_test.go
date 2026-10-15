package discovery

import (
	"context"
	"os"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// Jobs one account's discovery stores are scored for every other account
// against that account's own profile and exclusions, without fetching
// anything: when the run that added them finishes, and on demand. A job is
// scored once per account, a closed job not at all, and the summary counts
// only open jobs that fit and that the account has not reported gone.
func TestCatalogScoringAgainstPostgres(t *testing.T) {
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
	svc := NewService(st)

	run := time.Now().Format("20060102150405.000000")
	admin, err := st.CreateUser(ctx, "catalog-admin-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	bea, err := st.CreateUser(ctx, "catalog-bea-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	job := func(company, n string) models.Job {
		t.Helper()
		j, err := st.CreateDiscoveredJob(ctx, admin.ID, "", "", "", "", "catalog-"+run+"-"+n, models.JobInput{
			Title: "Backend Engineer", Company: company, Location: "Remote",
			Description: "Build backend services in Go.", ApplyURL: "https://example.test/catalog/" + run + "/" + n,
		})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	matchOf := func(u, jobID string) (models.JobWithMatch, bool) {
		t.Helper()
		jobs, _, err := st.Jobs(ctx, u, store.JobQuery{})
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range jobs {
			if x.Job.ID == jobID {
				return x, true
			}
		}
		return models.JobWithMatch{}, false
	}

	// The run that stored a job scores it for the others when it finishes.
	open := job("Catalog Open "+run, "open")
	started, err := st.StartDiscoveryRun(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	started.JobsAdded = 1
	if _, err := svc.finish(ctx, admin.ID, started, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := matchOf(bea.ID, open.ID); !ok {
		t.Fatal("a job another account's run stored was not scored for Bea")
	}

	// Scoring on demand picks up what arrived since, once, and not a closed job.
	if _, err := st.AddBlacklist(ctx, bea.ID, models.BlacklistEntry{CompanyName: "Catalog Excluded " + run}); err != nil {
		t.Fatal(err)
	}
	excluded := job("Catalog Excluded "+run, "excluded")
	closed := job("Catalog Closed "+run, "closed")
	if err := st.CloseJob(ctx, closed.ID, "closed for the test"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ScoreCatalog(ctx, bea.ID); err != nil {
		t.Fatal(err)
	}
	if m, ok := matchOf(bea.ID, excluded.ID); !ok || !m.Match.Rejected {
		t.Fatalf("a job at a company Bea excludes was not scored as rejected for her: %+v %v", m.Match, ok)
	}
	if _, ok := matchOf(bea.ID, closed.ID); ok {
		t.Fatal("a closed job was scored")
	}
	if again, err := svc.ScoreCatalog(ctx, bea.ID); err != nil || again != 0 {
		t.Fatalf("a second pass scored %d jobs again (%v)", again, err)
	}
	unscored, err := st.UnscoredJobs(ctx, bea.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range unscored {
		if j.ID == open.ID || j.ID == excluded.ID {
			t.Fatalf("job %s is still listed as unscored", j.ID)
		}
	}

	summary, err := st.CatalogSummary(ctx, bea.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The database is shared with other tests, so counts are compared, not
	// matched exactly: the open job counts, and reporting it gone removes it.
	if summary.LastJobAt == nil || summary.JobsLast24h < 3 || summary.JobsLast7d < summary.JobsLast24h || summary.OpenMatches < 1 {
		t.Fatalf("summary = %+v; want the three new jobs counted and the open one among Bea's matches", summary)
	}
	if _, err := st.ReportJob(ctx, bea.ID, open.ID); err != nil {
		t.Fatal(err)
	}
	if after, err := st.CatalogSummary(ctx, bea.ID); err != nil || after.OpenMatches != summary.OpenMatches-1 {
		t.Fatalf("a job Bea reported gone still counts: %+v, before %+v (%v)", after, summary, err)
	}
}

// Scheduled discovery runs only for accounts MayDiscover allows (#128): a
// member whose schedule falls due is claimed and skipped, so nothing is
// fetched on its behalf.
func TestSchedulerSkipsAccountsThatMayNotDiscoverAgainstPostgres(t *testing.T) {
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
	member, err := st.CreateUser(ctx, "scheduler-member-"+time.Now().Format("20060102150405.000000")+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveDiscoverySettings(ctx, member.ID, models.DiscoverySettings{Enabled: true, IntervalMinutes: 360, MinScore: 50, MaxJobsPerRun: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE discovery_settings SET next_run_at=now()-interval '1 minute' WHERE user_id=$1`, member.ID); err != nil {
		t.Fatal(err)
	}
	svc := NewService(st)
	asked := map[string]bool{}
	// Every account due in the shared database is refused, so this run
	// fetches nothing for anyone.
	svc.MayDiscover = func(_ context.Context, id string) bool { asked[id] = true; return false }
	for i := 0; i < 20 && !asked[member.ID]; i++ {
		svc.runDue(ctx)
	}
	if !asked[member.ID] {
		t.Fatal("the member's due schedule was never claimed")
	}
	runs, err := st.DiscoveryRuns(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("a member's schedule ran %d times", len(runs))
	}
}
