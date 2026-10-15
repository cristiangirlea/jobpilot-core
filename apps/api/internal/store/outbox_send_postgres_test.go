package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

// Sending takes only approved entries, checks the approval again at that
// moment, moves each through sending to sent or failed, and never lets an entry
// that may have reached the employer go out a second time. Runs on a migrated
// database named by JOBPILOT_TEST_DATABASE_URL, and is skipped without one.
func TestOutboxSendingAgainstPostgres(t *testing.T) {
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
	ana, err := st.CreateUser(ctx, "send-ana-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	cv, err := st.SaveCV(ctx, ana.ID, models.CVDocument{Kind: "base", Filename: "ana.pdf", Format: "pdf", Approved: true, Fingerprint: "send-" + run}, []byte("%PDF-1.4 ana"))
	if err != nil {
		t.Fatal(err)
	}
	// approved queues an approved entry for a new job Ana matches, with its
	// application, as compose and the web app's approval would.
	approved := func(name string, rejected bool) models.OutboxEntry {
		t.Helper()
		job, err := st.CreateDiscoveredJob(ctx, ana.ID, "", "", "", "", "send-"+name+"-"+run, models.JobInput{
			Title: "Backend Engineer", Company: "Send Test " + run, Description: "Build services.", ApplyURL: "https://example.test/" + name + "-" + run,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO job_matches(user_id,job_id,rejected,score) VALUES($1,$2,false,80)`, ana.ID, job.ID); err != nil {
			t.Fatal(err)
		}
		application, err := st.CreateApplication(ctx, ana.ID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := st.SaveOutboxEntry(ctx, ana.ID, models.OutboxEntry{
			JobID: job.ID, ApplicationID: application.ID, Status: models.OutboxReady, Channel: models.ChannelATSForm,
			ATSProvider: "greenhouse", CVDocumentID: cv.ID, Form: map[string]any{"board": "fakeco", "job_id": name},
		})
		if err != nil {
			t.Fatal(err)
		}
		if entry, err = st.ApproveOutboxEntry(ctx, ana.ID, entry.ID); err != nil {
			t.Fatal(err)
		}
		if rejected {
			if _, err := pool.Exec(ctx, `UPDATE job_matches SET rejected=true WHERE user_id=$1 AND job_id=$2`, ana.ID, job.ID); err != nil {
				t.Fatal(err)
			}
		}
		return entry
	}
	claim := func(delay time.Duration) (OutboxClaim, bool) {
		t.Helper()
		got, ok, err := st.ClaimOutboxEntry(ctx, delay, ana.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got, ok
	}
	stored := func(id string) models.OutboxEntry {
		t.Helper()
		entry, err := st.OutboxEntry(ctx, ana.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	refusedWhileSending := func(id string) {
		t.Helper()
		var refusal OutboxRefusal
		if _, err := st.ApproveOutboxEntry(ctx, ana.ID, id); !errors.As(err, &refusal) || !strings.Contains(err.Error(), "being sent") {
			t.Fatalf("approving an entry being sent should be refused, got %v", err)
		}
		if _, err := st.CancelOutboxEntry(ctx, ana.ID, id); !errors.As(err, &refusal) || !strings.Contains(err.Error(), "being sent") {
			t.Fatalf("cancelling an entry being sent should be refused, got %v", err)
		}
		entry := stored(id)
		if _, err := st.SaveOutboxEntry(ctx, ana.ID, models.OutboxEntry{JobID: entry.JobID, Status: models.OutboxReady}); !errors.Is(err, ErrOutboxSending) {
			t.Fatalf("composing an entry being sent should be refused, got %v", err)
		}
	}

	t.Run("an approval waits out the delay, then is sent once", func(t *testing.T) {
		entry := approved("sent", false)
		if _, ok := claim(time.Hour); ok {
			t.Fatal("an entry approved just now must wait out the delay")
		}
		got, ok := claim(0)
		if !ok || got.Entry.ID != entry.ID || got.Refused != "" || got.Entry.Status != models.OutboxSending || got.UserID != ana.ID {
			t.Fatalf("expected the entry taken for sending, got %+v (ok %v)", got, ok)
		}
		if _, ok := claim(0); ok {
			t.Fatal("an entry being sent must not be taken twice")
		}
		refusedWhileSending(entry.ID)
		if err := st.RecordSubmitAttempt(ctx, ana.ID, entry.ID); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishOutboxSend(ctx, ana.ID, entry.ID, true, "ignored"); err != nil {
			t.Fatal(err)
		}
		sent := stored(entry.ID)
		if sent.Status != models.OutboxSent || sent.SentAt == nil || sent.Error != "" {
			t.Fatalf("expected sent with a time and no error, got %+v", sent)
		}
		application, err := st.Application(ctx, ana.ID, entry.ApplicationID)
		if err != nil || application.Status != "applied" {
			t.Fatalf("a sent application should be marked applied, got %+v (%v)", application, err)
		}
		var events int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM application_events WHERE application_id=$1 AND to_status='applied'`, entry.ApplicationID).Scan(&events); err != nil || events != 1 {
			t.Fatalf("marking it applied should be recorded once, got %d (%v)", events, err)
		}
		if err := st.FinishOutboxSend(ctx, ana.ID, entry.ID, false, "again"); err == nil {
			t.Fatal("a sent entry must not be finished again")
		}
		if _, err := st.SaveOutboxEntry(ctx, ana.ID, models.OutboxEntry{JobID: entry.JobID, Status: models.OutboxReady}); !errors.Is(err, ErrOutboxSent) {
			t.Fatalf("a sent entry must never be composed again, got %v", err)
		}
	})

	t.Run("a hand-back is failed with its reason, and can be prepared again", func(t *testing.T) {
		entry := approved("handback", false)
		if got, ok := claim(0); !ok || got.Entry.ID != entry.ID {
			t.Fatalf("expected the entry taken, got %+v", got)
		}
		reason := "The employer's form is protected by a CAPTCHA."
		if err := st.FinishOutboxSend(ctx, ana.ID, entry.ID, false, reason); err != nil {
			t.Fatal(err)
		}
		failed := stored(entry.ID)
		if failed.Status != models.OutboxFailed || failed.Error != reason || failed.SentAt != nil {
			t.Fatalf("expected failed with the reason, got %+v", failed)
		}
		if application, _ := st.Application(ctx, ana.ID, entry.ApplicationID); application.Status != "draft" {
			t.Fatalf("an unsent application stays where it was, got %q", application.Status)
		}
		// Nothing was posted, so preparing and approving it again sends it.
		again, err := st.SaveOutboxEntry(ctx, ana.ID, models.OutboxEntry{JobID: entry.JobID, ApplicationID: entry.ApplicationID, Status: models.OutboxReady,
			Channel: models.ChannelATSForm, ATSProvider: "greenhouse", CVDocumentID: cv.ID})
		if err != nil || again.Error != "" {
			t.Fatalf("a handed-back entry should prepare again with its reason cleared, got %+v (%v)", again, err)
		}
		if _, err := st.ApproveOutboxEntry(ctx, ana.ID, entry.ID); err != nil {
			t.Fatal(err)
		}
		if got, ok := claim(0); !ok || got.Refused != "" {
			t.Fatalf("a handed-back entry approved again should be sendable, got %+v", got)
		}
		if err := st.FinishOutboxSend(ctx, ana.ID, entry.ID, false, reason); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an entry that may have arrived is never sent again", func(t *testing.T) {
		entry := approved("posted", false)
		if _, ok := claim(0); !ok {
			t.Fatal("expected the entry taken")
		}
		if err := st.RecordSubmitAttempt(ctx, ana.ID, entry.ID); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishOutboxSend(ctx, ana.ID, entry.ID, false, "The employer's form failed (502)."); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SaveOutboxEntry(ctx, ana.ID, models.OutboxEntry{JobID: entry.JobID, Status: models.OutboxReady, Channel: models.ChannelATSForm, CVDocumentID: cv.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.ApproveOutboxEntry(ctx, ana.ID, entry.ID); err != nil {
			t.Fatal(err)
		}
		got, ok := claim(0)
		if !ok || got.Refused != SendAttemptedBefore || got.Entry.Status != models.OutboxFailed {
			t.Fatalf("a posted entry approved again must be refused, got %+v", got)
		}
		if failed := stored(entry.ID); failed.Error != SendAttemptedBefore {
			t.Fatalf("the refusal should be the entry's error, got %q", failed.Error)
		}
	})

	t.Run("the approval is checked again when sending", func(t *testing.T) {
		excluded := approved("excluded", true)
		got, ok := claim(0)
		if !ok || got.Entry.ID != excluded.ID || got.Refused != ErrApplicationBlocked.Error() {
			t.Fatalf("a job the user's rules now exclude must not be sent, got %+v", got)
		}
		closed := approved("closed", false)
		if err := st.CloseJob(ctx, closed.JobID, "the employer removed it"); err != nil {
			t.Fatal(err)
		}
		got, ok = claim(0)
		if !ok || got.Entry.ID != closed.ID || !strings.Contains(got.Refused, "closed") {
			t.Fatalf("a closed posting must not be sent, got %+v", got)
		}
		if _, ok := claim(0); ok {
			t.Fatal("refused entries are failed, not left to be taken again")
		}
	})

	t.Run("a stopped send is written off, and says whether it may have arrived", func(t *testing.T) {
		before := approved("stopped-before", false)
		claim(0)
		during := approved("stopped-during", false)
		claim(0)
		if err := st.RecordSubmitAttempt(ctx, ana.ID, during.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE application_outbox SET updated_at=now()-interval '1 hour' WHERE id IN ($1,$2)`, before.ID, during.ID); err != nil {
			t.Fatal(err)
		}
		if n, err := st.FailStaleSends(ctx, 10*time.Minute); err != nil || n < 2 {
			t.Fatalf("expected both written off, got %d (%v)", n, err)
		}
		if got := stored(before.ID); got.Status != models.OutboxFailed || got.Error != SendInterrupted {
			t.Fatalf("one stopped before posting is interrupted, got %+v", got)
		}
		if got := stored(during.ID); got.Status != models.OutboxFailed || got.Error != SendOutcomeUnknown {
			t.Fatalf("one stopped after posting may have arrived, got %+v", got)
		}
		if err := st.RecordSubmitAttempt(ctx, ana.ID, before.ID); err == nil {
			t.Fatal("an entry written off must never be posted after all")
		}
	})
}
