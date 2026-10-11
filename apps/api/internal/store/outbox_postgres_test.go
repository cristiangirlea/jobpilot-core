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
	// Even on a one-connection pool: the transaction ends before the job is
	// read through the pool.
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	single, err := database.Open(ctx, url+sep+"pool_max_conns=1")
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	quick, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if again, err := New(single).ApproveOutboxEntry(quick, ana.ID, first.ID); err != nil || again.Status != models.OutboxApproved || again.Job == nil {
		t.Fatalf("approving again on a one-connection pool: %+v, %v", again, err)
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

	// Approving the CV clears its blocker, but not a required draft beside it.
	pendingCV := newCV("pending", false)
	pendingDraft := newDraft("Why us?", false)
	both := compose(job, pendingCV.ID, []models.OutboxAnswer{required("Why us?", "ai", pendingDraft.Answer, pendingDraft.ID)}, CVBlocker)
	if _, err := st.SetCVApproval(ctx, ana.ID, pendingCV.ID, true); err != nil {
		t.Fatal(err)
	}
	if cleared, err := st.OutboxEntry(ctx, ana.ID, both.ID); err != nil || len(cleared.Blockers) != 0 || cleared.Status != models.OutboxReady {
		t.Fatalf("approving the CV should clear its blocker, got %+v, %v", cleared, err)
	}
	refuses("a cleared CV blocker beside a required draft still pending", both, "not approved")

	// The bank entry is compared as stored: wording up to surrounding space,
	// and only the user's own entry.
	spaced := newDraft("When can you start?", true)
	approves("a draft differing only in surrounding space", compose(job, cv.ID, []models.OutboxAnswer{required("Start?", "ai", spaced.Answer+"\n", spaced.ID)}))
	bobs, err := st.SaveAnswer(ctx, bob.ID, models.AnswerEntry{
		QuestionKey: "motivation", QuestionText: "Why this team?", QuestionNormalized: "why this team " + run,
		Answer: "Because of the team.", RoleFamily: "backend", Source: "ai", Approved: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	refuses("a draft pointing at another user's answer", compose(job, cv.ID, []models.OutboxAnswer{required("Team?", "ai", bobs.Answer, bobs.ID)}), "not approved")

	// needs_review with its blockers cleared waits for approval; failed does not.
	reviewed := compose(job, cv.ID, nil)
	if _, err := pool.Exec(ctx, `UPDATE application_outbox SET status='needs_review', blockers='{}' WHERE id=$1`, reviewed.ID); err != nil {
		t.Fatal(err)
	}
	approves("needs_review with no blockers left", reviewed)
	failed := compose(job, cv.ID, nil)
	if _, err := pool.Exec(ctx, `UPDATE application_outbox SET status='failed' WHERE id=$1`, failed.ID); err != nil {
		t.Fatal(err)
	}
	refuses("a failed entry", failed, "Prepare it again")

	// A deleted bank entry is no approval either.
	deleted := newDraft("What is your notice period?", true)
	withDeleted := compose(job, cv.ID, []models.OutboxAnswer{required("Notice?", "ai", deleted.Answer, deleted.ID)})
	if err := st.DeleteAnswer(ctx, ana.ID, deleted.ID); err != nil {
		t.Fatal(err)
	}
	refuses("a draft whose bank entry was deleted", withDeleted, "not approved")

	// Re-uploading the entry's CV clears its approval, and the entries carrying
	// it follow, as when its approval is withdrawn: an approved one goes back to
	// review, a ready one is blocked.
	reuploaded := newCV("reuploaded", true)
	followed := approves("an entry whose CV will be uploaded again", compose(job, reuploaded.ID, nil))
	if _, err := st.SaveCV(ctx, ana.ID, models.CVDocument{Kind: "tailored", Filename: "reuploaded.pdf", Format: "pdf", Fingerprint: "approval-reuploaded-" + run}, []byte("%PDF-1.4 test")); err != nil {
		t.Fatal(err)
	}
	if back, err := st.OutboxEntry(ctx, ana.ID, followed.ID); err != nil || back.Status != models.OutboxNeedsReview || back.ApprovedAt != nil || len(back.Blockers) != 1 || back.Blockers[0] != CVBlocker {
		t.Fatalf("a re-uploaded CV must send its approved entry back to review, got %+v, %v", back, err)
	}

	// Concurrency. bounded caps every wait below, so a regression fails the test
	// instead of hanging it.
	bounded, cancelBounded := context.WithTimeout(ctx, 30*time.Second)
	defer cancelBounded()
	backend := func(tx pgx.Tx) int {
		var pid int
		if err := tx.QueryRow(bounded, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		return pid
	}
	// waiter returns the backend waiting for a lock that backend holder holds,
	// once there is one. A run that finishes instead of waiting fails the test.
	waiter := func(name string, holder int, finished <-chan error) int {
		t.Helper()
		for i := 0; i < 250; i++ {
			select {
			case err := <-finished:
				t.Fatalf("%s must wait for the transaction in flight, but it finished: %v", name, err)
			default:
			}
			var pid int
			err := pool.QueryRow(bounded, `SELECT pid FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)) LIMIT 1`, holder).Scan(&pid)
			if err == nil {
				return pid
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s never waited for the transaction in flight", name)
		return 0
	}
	approval := func(entry models.OutboxEntry) <-chan error {
		finished := make(chan error, 1)
		go func() {
			_, err := st.ApproveOutboxEntry(bounded, ana.ID, entry.ID)
			finished <- err
		}()
		return finished
	}
	outcome := func(finished <-chan error) error {
		select {
		case err := <-finished:
			return err
		case <-bounded.Done():
			t.Fatal("timed out waiting for the approval")
			return nil
		}
	}

	// An approval reads each answer under a share lock, so it waits for a
	// revocation in flight and then sees it, rather than approving on the
	// committed value the revocation is about to replace.
	revoked := newDraft("Where are you based?", true)
	inFlight := compose(job, cv.ID, []models.OutboxAnswer{required("Based?", "ai", revoked.Answer, revoked.ID)})
	revoking, err := pool.Begin(bounded)
	if err != nil {
		t.Fatal(err)
	}
	defer revoking.Rollback(ctx)
	if _, err := revoking.Exec(bounded, `UPDATE answer_bank SET approved=false WHERE id=$1`, revoked.ID); err != nil {
		t.Fatal(err)
	}
	waitingApproval := approval(inFlight)
	waiter("the approval", backend(revoking), waitingApproval)
	if err := revoking.Commit(bounded); err != nil {
		t.Fatal(err)
	}
	var waited OutboxRefusal
	if err := outcome(waitingApproval); !errors.As(err, &waited) || !strings.Contains(waited.Error(), "not approved") {
		t.Fatalf("after the revocation the approval must be refused, got %v", err)
	}

	// The CV is read without a lock. A re-upload during the approval clears it
	// only together with its entries, whose lock the approval holds: so once the
	// approval commits, the entry is sent back to review.
	raced := newCV("raced", true)
	racedDraft := newDraft("What do you value in a team?", false)
	racing := compose(job, raced.ID, []models.OutboxAnswer{required("Value?", "ai", racedDraft.Answer, racedDraft.ID)})
	approving, err := pool.Begin(bounded)
	if err != nil {
		t.Fatal(err)
	}
	defer approving.Rollback(ctx)
	if _, err := approving.Exec(bounded, `UPDATE answer_bank SET approved=true WHERE id=$1`, racedDraft.ID); err != nil {
		t.Fatal(err)
	}
	racingApproval := approval(racing)
	approver := waiter("the approval", backend(approving), racingApproval)
	upload := make(chan error, 1)
	go func() {
		_, err := st.SaveCV(bounded, ana.ID, models.CVDocument{Kind: "tailored", Filename: "raced.pdf", Format: "pdf", Fingerprint: "approval-raced-" + run}, []byte("%PDF-1.4 test"))
		upload <- err
	}()
	waiter("the re-upload", approver, upload)
	if err := approving.Commit(bounded); err != nil {
		t.Fatal(err)
	}
	if err := outcome(racingApproval); err != nil {
		t.Fatalf("the approval read an approved CV and answer, so it goes through: %v", err)
	}
	if err := outcome(upload); err != nil {
		t.Fatal(err)
	}
	if back, err := st.OutboxEntry(ctx, ana.ID, racing.ID); err != nil || back.Status != models.OutboxNeedsReview || back.ApprovedAt != nil {
		t.Fatalf("a CV re-uploaded during the approval must leave the entry back in review, got %+v, %v", back, err)
	}

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
