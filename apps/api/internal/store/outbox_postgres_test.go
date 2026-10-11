package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

// Approving an outbox entry is the user's consent to send it, so approval
// re-checks, at that moment, everything the consent covers: the entry's status,
// its blockers, its CV, its required drafted answers and the posting. This runs
// on a migrated database named by JOBPILOT_TEST_DATABASE_URL, and is skipped
// without one; CI's integration job provides it.
func TestOutboxApprovalAgainstPostgres(t *testing.T) {
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
	ana, err := st.CreateUser(ctx, "approval-ana-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(ctx, "approval-bob-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	newJob := func(name string) models.Job {
		in := models.JobInput{Title: "Backend Engineer", Company: "Approval Test " + run, Description: "Build services.", ApplyURL: "https://example.test/" + name + "-" + run}
		job, err := st.CreateDiscoveredJob(ctx, ana.ID, "", "", "", "", "approval-"+name+"-"+run, in)
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	newCV := func(name string, approved bool) models.CVDocument {
		doc, err := st.SaveCV(ctx, ana.ID, models.CVDocument{
			Kind: "tailored", Filename: name + ".pdf", Format: "pdf", Approved: approved, Fingerprint: "approval-" + name + "-" + run,
		}, []byte("%PDF-1.4 test"))
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	newDraft := func(question string, approved bool) models.AnswerEntry {
		entry, err := st.SaveAnswer(ctx, ana.ID, models.AnswerEntry{
			QuestionKey: "motivation", QuestionText: question, QuestionNormalized: strings.ToLower(question) + " " + run,
			Answer: "I like building reliable services.", RoleFamily: "backend", Source: "ai", Approved: approved,
		})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}

	job := newJob("main")
	cv := newCV("approved", true)
	// compose queues (or recomposes) the entry for job with these answers and
	// blockers, as the compose handler would.
	compose := func(j models.Job, cvID string, answers []models.OutboxAnswer, blockers ...string) models.OutboxEntry {
		status := models.OutboxReady
		if len(blockers) > 0 {
			status = models.OutboxNeedsReview
		}
		entry, err := st.SaveOutboxEntry(ctx, ana.ID, models.OutboxEntry{
			JobID: j.ID, Status: status, Channel: models.ChannelATSForm, CVDocumentID: cvID,
			Answers: answers, Blockers: blockers,
		})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	approves := func(name string, entry models.OutboxEntry) models.OutboxEntry {
		t.Helper()
		approved, err := st.ApproveOutboxEntry(ctx, ana.ID, entry.ID)
		if err != nil {
			t.Fatalf("%s: approval refused: %v", name, err)
		}
		if approved.Status != models.OutboxApproved || approved.ApprovedAt == nil || approved.Job == nil {
			t.Fatalf("%s: expected an approved entry with its job, got %+v", name, approved)
		}
		return approved
	}
	refuses := func(name string, entry models.OutboxEntry, wantIn string) {
		t.Helper()
		_, err := st.ApproveOutboxEntry(ctx, ana.ID, entry.ID)
		var refusal OutboxRefusal
		if !errors.As(err, &refusal) || !strings.Contains(refusal.Error(), wantIn) {
			t.Fatalf("%s: expected a refusal containing %q, got %v", name, wantIn, err)
		}
		stored, err := st.OutboxEntry(ctx, ana.ID, entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status == models.OutboxApproved {
			t.Fatalf("%s: a refused approval must leave the entry unapproved", name)
		}
	}
	required := func(label, source, answer, entryID string) models.OutboxAnswer {
		return models.OutboxAnswer{Field: label, Label: label, Type: "textarea", Required: true, Answer: answer, Source: source, NeedsReview: true, EntryID: entryID}
	}

	// A complete entry approves, and approving it again changes nothing.
	first := approves("a ready entry", compose(job, cv.ID, nil))
	again := approves("an approved entry", first)
	if !again.ApprovedAt.Equal(*first.ApprovedAt) {
		t.Fatalf("approving twice must keep the first approval, got %v then %v", first.ApprovedAt, again.ApprovedAt)
	}

	// Blockers still stop it.
	refuses("a blocked entry", compose(job, cv.ID, nil, "Your profile has no email address."), "Your profile has no email address.")

	// A cancelled entry has to be prepared again.
	cancelled, err := st.CancelOutboxEntry(ctx, ana.ID, compose(job, cv.ID, nil).ID)
	if err != nil || cancelled.Status != models.OutboxCancelled {
		t.Fatalf("cancel: %+v, %v", cancelled, err)
	}
	refuses("a cancelled entry", cancelled, "Prepare it again")

	// The CV as it is now, not as it was at compose time.
	unapproved := newCV("unapproved", false)
	refuses("an entry whose CV is not approved", compose(job, unapproved.ID, nil), CVBlocker)
	gone := newCV("deleted", true)
	withGone := compose(job, gone.ID, nil)
	if err := st.DeleteCV(ctx, ana.ID, gone.ID); err != nil {
		t.Fatal(err)
	}
	refuses("an entry whose CV was deleted", withGone, "CV was deleted")

	// Required drafts someone has to have read.
	draft := newDraft("Why do you want this role?", false)
	withDraft := compose(job, cv.ID, []models.OutboxAnswer{required("Why this role?", "ai", draft.Answer, draft.ID)})
	refuses("a required AI draft not approved", withDraft, "not approved")
	if _, err := st.UpdateAnswer(ctx, ana.ID, draft.ID, "", true); err != nil {
		t.Fatal(err)
	}
	approves("a required AI draft approved since compose, as worded", withDraft)

	edited := newDraft("What would you build first?", true)
	withEdited := compose(job, cv.ID, []models.OutboxAnswer{required("Build first?", "ai", edited.Answer, edited.ID)})
	if _, err := st.UpdateAnswer(ctx, ana.ID, edited.ID, "Something else entirely.", true); err != nil {
		t.Fatal(err)
	}
	refuses("a required AI draft approved with other wording", withEdited, "You changed your answer")
	refuses("a required AI draft the bank never stored", compose(job, cv.ID, []models.OutboxAnswer{required("Unsaved?", "ai", "Draft.", "")}), "not approved")
	refuses("a required template answer", compose(job, cv.ID, []models.OutboxAnswer{required("Cover note?", "template", "Template wording.", "")}), "template wording")

	optional := newDraft("Anything else?", false)
	optionalDraft := required("Anything else?", "ai", optional.Answer, optional.ID)
	optionalDraft.Required = false
	confirm := required("First name", "profile", "Ana", "")
	approves("an optional AI draft and a profile answer to confirm", compose(job, cv.ID, []models.OutboxAnswer{optionalDraft, confirm}))

	// Only the owner reaches an entry, and a missing one is a missing row.
	if _, err := st.ApproveOutboxEntry(ctx, bob.ID, first.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("another user's entry must read as missing, got %v", err)
	}
	if _, err := st.ApproveOutboxEntry(ctx, ana.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("a missing entry must read as missing, got %v", err)
	}

	// A sent entry is history: neither approved again nor cancelled.
	sentJob := newJob("sent")
	sent := compose(sentJob, cv.ID, nil)
	if _, err := pool.Exec(ctx, `UPDATE application_outbox SET status='sent', sent_at=now() WHERE id=$1`, sent.ID); err != nil {
		t.Fatal(err)
	}
	refuses("a sent entry", sent, "already been sent")
	var refusal OutboxRefusal
	if _, err := st.CancelOutboxEntry(ctx, ana.ID, sent.ID); !errors.As(err, &refusal) {
		t.Fatalf("cancelling a sent entry must be refused, got %v", err)
	}

	// The posting closed after the entry was composed.
	closing := compose(job, cv.ID, nil)
	if err := st.RecordLinkCheck(ctx, job.ID, true, "the employer removed the posting", ""); err != nil {
		t.Fatal(err)
	}
	refuses("an entry for a closed posting", closing, "This posting has closed: the employer removed the posting")
}
