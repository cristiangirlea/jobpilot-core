package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

// The notification feed lists what waits for each user and nobody else's:
// unapproved answers and CVs, and outbox entries to review or approve. An
// item that stops waiting is resolved, not deleted; one that starts waiting
// again opens a new row, whether or not anyone read the feed in between;
// kinds opened by other code are resolved by that code; pages follow on.
// This runs on a migrated database named by JOBPILOT_TEST_DATABASE_URL, and
// is skipped without one; CI's integration job provides it.
func TestNotificationsAgainstPostgres(t *testing.T) {
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
	ana, err := st.CreateUser(ctx, "notify-ana-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(ctx, "notify-bob-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	draft := func(userID, question string) models.AnswerEntry {
		entry, err := st.SaveAnswer(ctx, userID, models.AnswerEntry{
			QuestionKey: "motivation", QuestionText: question, QuestionNormalized: strings.ToLower(question) + " " + run,
			Answer: "I like building reliable services.", RoleFamily: "backend", Source: "ai",
		})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	feed := func(userID, state string) models.NotificationFeed {
		t.Helper()
		f, err := st.Notifications(ctx, userID, state, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	find := func(f models.NotificationFeed, kind, subject string) []models.Notification {
		var out []models.Notification
		for _, n := range f.Items {
			if n.Kind == kind && n.SubjectID == subject {
				out = append(out, n)
			}
		}
		return out
	}
	open := func(f models.NotificationFeed, kind, subject string) models.Notification {
		t.Helper()
		for _, n := range find(f, kind, subject) {
			if n.ResolvedAt == nil {
				return n
			}
		}
		t.Fatalf("expected an open %s notification for %s, got %+v", kind, subject, f.Items)
		return models.Notification{}
	}

	answer := draft(ana.ID, "Why do you want to work here?")
	cv, err := st.SaveCV(ctx, ana.ID, models.CVDocument{Kind: "tailored", Label: "Backend CV", Filename: "notify.pdf", Format: "pdf", Fingerprint: "notify-" + run}, []byte("%PDF-1.4 test"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.CreateDiscoveredJob(ctx, ana.ID, "", "", "", "", "notify-"+run, models.JobInput{
		Title: "Backend Engineer", Company: "Notify Test " + run, Description: "Build services.", ApplyURL: "https://example.test/notify-" + run,
	})
	if err != nil {
		t.Fatal(err)
	}
	compose := func(status string, blockers ...string) models.OutboxEntry {
		entry, err := st.SaveOutboxEntry(ctx, ana.ID, models.OutboxEntry{
			JobID: job.ID, Status: status, Channel: models.ChannelATSForm, CVDocumentID: cv.ID, Blockers: blockers,
		})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	entry := compose(models.OutboxNeedsReview, cvBlocker("tailored"))
	bobAnswer := draft(bob.ID, "Where are you based?")

	f := feed(ana.ID, "")
	if f.Open != 3 || len(f.Items) != 3 {
		t.Fatalf("Ana should have three open items, got open=%d %+v", f.Open, f.Items)
	}
	a := open(f, NotificationAnswer, answer.ID)
	if a.Detail["answer"] != answer.Answer || a.Detail["href"] != "/answers" {
		t.Fatalf("an answer item must carry the exact wording and where to approve it: %+v", a)
	}
	if c := open(f, NotificationCV, cv.ID); !strings.Contains(c.Title, "tailored CV") || !strings.Contains(c.Title, "Backend CV") {
		t.Fatalf("a CV item should name the CV: %q", c.Title)
	}
	o := open(f, NotificationOutbox, entry.ID)
	if !strings.HasPrefix(o.Title, "Review the application to Backend Engineer") || o.Detail["status"] != models.OutboxNeedsReview {
		t.Fatalf("an outbox entry with a blocker is to review: %+v", o)
	}
	if blockers, _ := o.Detail["blockers"].([]any); len(blockers) != 1 || blockers[0] != cvBlocker("tailored") {
		t.Fatalf("the blockers travel in their exact words: %+v", o.Detail)
	}

	// Per-user isolation: Bob sees his own draft and nothing of Ana's.
	bf := feed(bob.ID, "all")
	if bf.Open != 1 || len(bf.Items) != 1 || bf.Items[0].SubjectID != bobAnswer.ID {
		t.Fatalf("Bob should see only his own answer, got open=%d %+v", bf.Open, bf.Items)
	}

	// Reading again changes nothing.
	if again := feed(ana.ID, ""); open(again, NotificationAnswer, answer.ID).ID != a.ID || again.Open != 3 {
		t.Fatalf("a second read must keep the same rows: %+v", again.Items)
	}

	// Approving the answer resolves it; the history keeps it.
	if _, err := st.UpdateAnswer(ctx, ana.ID, answer.ID, "", true, "", ""); err != nil {
		t.Fatal(err)
	}
	f = feed(ana.ID, "")
	if f.Open != 2 || len(find(f, NotificationAnswer, answer.ID)) != 0 {
		t.Fatalf("an approved answer must leave the open list, got open=%d %+v", f.Open, f.Items)
	}
	all := feed(ana.ID, "all")
	if got := find(all, NotificationAnswer, answer.ID); len(got) != 1 || got[0].ResolvedAt == nil || got[0].ID != a.ID {
		t.Fatalf("the resolved answer should stay in the history: %+v", got)
	}
	// Withdrawing the approval makes it wait again, as a new row.
	if _, err := st.UpdateAnswer(ctx, ana.ID, answer.ID, "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if again := open(feed(ana.ID, ""), NotificationAnswer, answer.ID); again.ID == a.ID {
		t.Fatal("an item that waits again must open a new row, not reopen the resolved one")
	}

	// Approving the CV clears its blocker, and the entry moves to ready under
	// the same row with a title asking for approval.
	if _, err := st.SetCVApproval(ctx, ana.ID, cv.ID, true); err != nil {
		t.Fatal(err)
	}
	f = feed(ana.ID, "")
	if len(find(f, NotificationCV, cv.ID)) != 0 {
		t.Fatalf("an approved CV must leave the open list: %+v", f.Items)
	}
	ready := open(f, NotificationOutbox, entry.ID)
	if ready.ID != o.ID || !strings.HasPrefix(ready.Title, "Approve the application") || ready.Detail["status"] != models.OutboxReady {
		t.Fatalf("a ready entry keeps its row and asks for approval: %+v", ready)
	}
	if _, err := st.CancelOutboxEntry(ctx, ana.ID, entry.ID); err != nil {
		t.Fatal(err)
	}
	if left := find(feed(ana.ID, ""), NotificationOutbox, entry.ID); len(left) != 0 {
		t.Fatalf("a cancelled entry waits for nothing: %+v", left)
	}

	// Transitions are recorded as they happen, not when the feed is read:
	// an answer drafted, approved, withdrawn and approved again with no read
	// in between leaves two resolved rows, each with its own times.
	quick := draft(ana.ID, "What is your notice period?")
	for _, approved := range []bool{true, false, true} {
		if _, err := st.UpdateAnswer(ctx, ana.ID, quick.ID, "", approved, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	history := find(feed(ana.ID, "all"), NotificationAnswer, quick.ID)
	if len(history) != 2 || history[0].ResolvedAt == nil || history[1].ResolvedAt == nil || history[0].ID == history[1].ID {
		t.Fatalf("each wait should leave its own resolved row: %+v", history)
	}
	for _, n := range history {
		if n.ResolvedAt.Before(n.CreatedAt) {
			t.Fatalf("a row cannot resolve before it opened: %+v", n)
		}
	}

	// Kinds opened by their own code are resolved by that code, per user.
	if err := OpenNotification(ctx, pool, ana.ID, "proposal", "p-"+run, "Review a proposed profile change", map[string]any{"href": "/profile"}); err != nil {
		t.Fatal(err)
	}
	if err := OpenNotification(ctx, pool, ana.ID, "proposal", "p-"+run, "Review two proposed profile changes", nil); err != nil {
		t.Fatal(err)
	}
	f = feed(ana.ID, "")
	if p := open(f, "proposal", "p-"+run); p.Title != "Review two proposed profile changes" || len(find(f, "proposal", "p-"+run)) != 1 {
		t.Fatalf("opening an open item refreshes it in place: %+v", find(f, "proposal", "p-"+run))
	}
	if len(find(feed(bob.ID, "all"), "proposal", "p-"+run)) != 0 {
		t.Fatal("Bob must not see Ana's proposal")
	}
	if err := ResolveNotification(ctx, pool, bob.ID, "proposal", "p-"+run); err != nil {
		t.Fatal(err)
	}
	open(feed(ana.ID, ""), "proposal", "p-"+run) // Bob cannot resolve it either
	if err := ResolveNotification(ctx, pool, ana.ID, "proposal", "p-"+run); err != nil {
		t.Fatal(err)
	}
	if left := find(feed(ana.ID, ""), "proposal", "p-"+run); len(left) != 0 {
		t.Fatalf("a resolved proposal leaves the open list: %+v", left)
	}

	// Pages follow on by position: the open list oldest first, the history
	// newest first, neither repeating nor skipping a row.
	// They open in one transaction, as several items changed by one write
	// do, so they share created_at and only the id breaks the tie.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := OpenNotification(ctx, tx, ana.ID, "proposal", fmt.Sprintf("page-%d-%s", i, run), "Review a proposed profile change", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"", "all"} {
		whole := feed(ana.ID, state)
		var paged []string
		after := ""
		for {
			page, err := st.Notifications(ctx, ana.ID, state, after, 2)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range page.Items {
				paged = append(paged, n.ID)
			}
			if page.Next == "" {
				break
			}
			after = page.Next
		}
		if len(paged) != len(whole.Items) {
			t.Fatalf("state %q: paging returned %d rows, the whole list has %d", state, len(paged), len(whole.Items))
		}
		for i, n := range whole.Items {
			if paged[i] != n.ID {
				t.Fatalf("state %q: row %d differs between paging and the whole list", state, i)
			}
		}
	}
	if page, err := st.Notifications(ctx, bob.ID, "", whole0(t, st, ctx, ana.ID), 50); err != nil || len(page.Items) != 0 {
		t.Fatalf("a cursor from another user's list must return nothing: %+v %v", page, err)
	}
	for i := 0; i < 3; i++ {
		if err := ResolveNotification(ctx, pool, ana.ID, "proposal", fmt.Sprintf("page-%d-%s", i, run)); err != nil {
			t.Fatal(err)
		}
	}

	// Deleting a waiting thing resolves its item too.
	if err := st.DeleteAnswer(ctx, ana.ID, answer.ID); err != nil {
		t.Fatal(err)
	}
	if f = feed(ana.ID, ""); f.Open != 0 || len(f.Items) != 0 {
		t.Fatalf("nothing should wait for Ana now, got open=%d %+v", f.Open, f.Items)
	}
}

// whole0 is the id of the first open item in the user's list.
func whole0(t *testing.T, st *Store, ctx context.Context, userID string) string {
	t.Helper()
	f, err := st.Notifications(ctx, userID, "", "", 1)
	if err != nil || len(f.Items) == 0 {
		t.Fatalf("expected an open item: %+v %v", f, err)
	}
	return f.Items[0].ID
}
