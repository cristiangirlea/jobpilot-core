package sender

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// A sweep sends what is due through the employer's form and records each
// outcome: sent with the approved CV, handed back from a form behind a
// CAPTCHA, and handed back from a provider the sender cannot fill. Runs on a
// migrated database named by JOBPILOT_TEST_DATABASE_URL, and is skipped
// without one.
func TestSweepSendsApprovedEntriesAgainstPostgres(t *testing.T) {
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
	ana, err := st.CreateUser(ctx, "sweep-ana-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	cv, err := st.SaveCV(ctx, ana.ID, models.CVDocument{Kind: "base", Filename: "ana-cv.pdf", Format: "pdf", ContentType: "application/pdf", Approved: true, Fingerprint: "sweep-" + run}, []byte("%PDF-1.4 ana"))
	if err != nil {
		t.Fatal(err)
	}

	e := newEmployer(t)
	guarded := newEmployer(t)
	guarded.page = strings.Replace(formPage, "</form>", `<script src="https://js.hcaptcha.com/1/api.js"></script></form>`, 1)

	approve := func(name, provider string) models.OutboxEntry {
		t.Helper()
		job, err := st.CreateDiscoveredJob(ctx, ana.ID, "", "", "", "", "sweep-"+name+"-"+run, models.JobInput{
			Title: "Backend Engineer", Company: "Sweep Test " + run, Description: "Build services.", ApplyURL: "https://example.test/" + name + "-" + run,
		})
		if err != nil {
			t.Fatal(err)
		}
		entry, err := st.SaveOutboxEntry(ctx, ana.ID, models.OutboxEntry{
			JobID: job.ID, Status: models.OutboxReady, Channel: models.ChannelATSForm, ATSProvider: provider,
			CVDocumentID: cv.ID, CoverLetter: "Dear team,\nI would like to apply.",
			Form: map[string]any{"board": "fakeco", "job_id": "1"}, Answers: answers(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ApproveOutboxEntry(ctx, ana.ID, entry.ID); err != nil {
			t.Fatal(err)
		}
		return entry
	}
	sweep := func(employer *employer) {
		service := New(st, NewGreenhouse(employer.server.URL), 0)
		service.onlyUser = ana.ID
		service.Sweep(ctx)
	}
	stored := func(id string) models.OutboxEntry {
		entry, err := st.OutboxEntry(ctx, ana.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}

	sent := approve("sent", "greenhouse")
	sweep(e)
	if got := stored(sent.ID); got.Status != models.OutboxSent || got.SentAt == nil {
		t.Fatalf("expected sent, got %s: %s", got.Status, got.Error)
	}
	if e.posted() != 1 || e.files["resume"] != "ana-cv.pdf:%PDF-1.4 ana" || e.parts["first_name"] != "Ana" {
		t.Fatalf("expected one post carrying the answers and the stored CV, got %d posts, %v, %v", e.posted(), e.parts, e.files)
	}
	sweep(e)
	if e.posted() != 1 {
		t.Fatal("a sent entry must never be posted again")
	}

	captcha := approve("captcha", "greenhouse")
	other := approve("ashby", "ashby")
	sweep(guarded)
	if got := stored(captcha.ID); got.Status != models.OutboxFailed || !strings.Contains(got.Error, "CAPTCHA") {
		t.Fatalf("a form behind a CAPTCHA must come back unsent, got %s: %s", got.Status, got.Error)
	}
	if got := stored(other.ID); got.Status != models.OutboxFailed || !strings.Contains(got.Error, "cannot send ashby forms") {
		t.Fatalf("a provider the sender cannot fill must come back unsent, got %s: %s", got.Status, got.Error)
	}
	if guarded.posted() != 0 {
		t.Fatal("nothing may be posted to a form behind a CAPTCHA")
	}
}
