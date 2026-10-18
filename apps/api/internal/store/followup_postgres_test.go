package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

type quietQueryTrace struct{ perRow atomic.Int32 }

func (q *quietQueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "SELECT notification_open(a.user_id") {
		q.perRow.Add(1)
	}
	return ctx
}
func (*quietQueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestQuietReviewRegressionsPostgres(t *testing.T) {
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated throwaway database")
	}
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	trace := &quietQueryTrace{}
	config.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := New(pool)
	user, err := st.CreateUser(ctx, fmt.Sprintf("quiet-review-%d@example.test", time.Now().UnixNano()), "fake")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user.ID)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	app := func() models.Application {
		t.Helper()
		j, e := st.InsertJob(ctx, user.ID, models.JobInput{Title: "Engineer", Company: "Review Test", Description: "Build", ApplyURL: fmt.Sprintf("https://example.test/%d", time.Now().UnixNano())}, models.Evaluation{Score: 80}, "")
		if e != nil {
			t.Fatal(e)
		}
		a, e := st.CreateApplication(ctx, user.ID, j.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e := st.UpdateApplication(ctx, user.ID, a.ID, "applied", nil); e != nil {
			t.Fatal(e)
		}
		return a
	}
	t.Run("dismiss before due", func(t *testing.T) {
		a := app()
		defer exec(`DELETE FROM applications WHERE id=$1`, a.ID)
		defer exec(`DELETE FROM notifications WHERE user_id=$1`, user.ID)
		if e := st.DismissQuietApplication(ctx, user.ID, a.ID); e != nil {
			t.Fatal(e)
		}
		exec(`UPDATE applications SET status_changed_at=now()-interval '11 days' WHERE id=$1`, a.ID)
		f, e := st.Notifications(ctx, user.ID, "open", "", 200)
		if e != nil || f.Open != 0 {
			t.Fatalf("early dismissal reopened: %+v %v", f, e)
		}
	})
	t.Run("65 days at default threshold", func(t *testing.T) {
		a := app()
		defer exec(`DELETE FROM applications WHERE id=$1`, a.ID)
		defer exec(`DELETE FROM notifications WHERE user_id=$1`, user.ID)
		exec(`UPDATE applications SET status_changed_at=now()-interval '65 days' WHERE id=$1`, a.ID)
		f, e := st.Notifications(ctx, user.ID, "open", "", 200)
		if e != nil || f.Open != 1 || len(f.Items) != 1 || f.Items[0].SubjectID != a.ID {
			t.Fatalf("default-threshold reminder missing: %+v %v", f, e)
		}
	})
	t.Run("more than 60 days overdue", func(t *testing.T) {
		a := app()
		defer exec(`DELETE FROM applications WHERE id=$1`, a.ID)
		defer exec(`DELETE FROM notifications WHERE user_id=$1`, user.ID)
		exec(`UPDATE applications SET status_changed_at=now()-interval '71 days' WHERE id=$1`, a.ID)
		f, e := st.Notifications(ctx, user.ID, "open", "", 200)
		if e != nil || f.Open != 0 {
			t.Fatalf("ancient application surfaced: %+v %v", f, e)
		}
	})
	t.Run("90 day threshold", func(t *testing.T) {
		a := app()
		defer exec(`DELETE FROM applications WHERE id=$1`, a.ID)
		defer exec(`DELETE FROM notifications WHERE user_id=$1`, user.ID)
		if _, e := st.SaveAccountSettings(ctx, user.ID, 90); e != nil {
			t.Fatal(e)
		}
		defer func() {
			if _, e := st.SaveAccountSettings(ctx, user.ID, 10); e != nil {
				t.Fatal(e)
			}
		}()
		exec(`UPDATE applications SET status_changed_at=now()-interval '91 days' WHERE id=$1`, a.ID)
		f, e := st.Notifications(ctx, user.ID, "open", "", 200)
		if e != nil || f.Open != 1 || len(f.Items) != 1 || f.Items[0].SubjectID != a.ID {
			t.Fatalf("90-day reminder missing: %+v %v", f, e)
		}
	})
	t.Run("uppercase dismissal", func(t *testing.T) {
		a := app()
		defer exec(`DELETE FROM applications WHERE id=$1`, a.ID)
		defer exec(`DELETE FROM notifications WHERE user_id=$1`, user.ID)
		exec(`UPDATE applications SET status_changed_at=now()-interval '11 days' WHERE id=$1`, a.ID)
		f, e := st.Notifications(ctx, user.ID, "open", "", 200)
		if e != nil || f.Open != 1 {
			t.Fatalf("initial reminder: %+v %v", f, e)
		}
		if e := st.DismissQuietApplication(ctx, user.ID, strings.ToUpper(a.ID)); e != nil {
			t.Fatal(e)
		}
		f, e = st.Notifications(ctx, user.ID, "all", "", 200)
		if e != nil || f.Open != 0 || len(f.Items) != 1 || f.Items[0].SubjectID != a.ID || f.Items[0].ResolvedAt == nil || f.Items[0].Detail["href"] != "/applications/"+a.ID {
			t.Fatalf("uppercase dismissal left an open or stray reminder: %+v %v", f, e)
		}
		f, e = st.Notifications(ctx, user.ID, "open", "", 200)
		if e != nil || f.Open != 0 || len(f.Items) != 0 {
			t.Fatalf("dismissed reminder reopened: %+v %v", f, e)
		}
	})
	t.Run("rejected match", func(t *testing.T) {
		a := app()
		defer exec(`DELETE FROM applications WHERE id=$1`, a.ID)
		defer exec(`DELETE FROM notifications WHERE user_id=$1`, user.ID)
		exec(`UPDATE applications SET status_changed_at=now()-interval '11 days' WHERE id=$1`, a.ID)
		exec(`UPDATE job_matches SET rejected=true WHERE user_id=$1 AND job_id=$2`, user.ID, a.JobID)
		trace.perRow.Store(0)
		f, e := st.Notifications(ctx, user.ID, "all", "", 200)
		if e != nil || f.Open != 0 || len(f.Items) != 0 {
			t.Fatalf("blocked application surfaced: %+v %v", f, e)
		}
		if n := trace.perRow.Load(); n != 0 {
			t.Errorf("blocked application ran %d per-row statements", n)
		}
	})
	t.Run("open reminder becomes rejected", func(t *testing.T) {
		a := app()
		defer exec(`DELETE FROM applications WHERE id=$1`, a.ID)
		defer exec(`DELETE FROM notifications WHERE user_id=$1`, user.ID)
		exec(`UPDATE applications SET status_changed_at=now()-interval '11 days' WHERE id=$1`, a.ID)
		f, e := st.Notifications(ctx, user.ID, "open", "", 200)
		if e != nil || f.Open != 1 || len(f.Items) != 1 || f.Items[0].SubjectID != a.ID {
			t.Fatalf("initial reminder: %+v %v", f, e)
		}
		reminderID := f.Items[0].ID
		exec(`UPDATE job_matches SET rejected=true WHERE user_id=$1 AND job_id=$2`, user.ID, a.JobID)
		f, e = st.Notifications(ctx, user.ID, "open", "", 200)
		if e != nil || f.Open != 0 || len(f.Items) != 0 {
			t.Fatalf("rejected application's reminder stayed open: %+v %v", f, e)
		}
		f, e = st.Notifications(ctx, user.ID, "all", "", 200)
		if e != nil || f.Open != 0 || len(f.Items) != 1 || f.Items[0].ID != reminderID || f.Items[0].ResolvedAt == nil {
			t.Fatalf("resolved reminder history missing: %+v %v", f, e)
		}
	})
	t.Run("steady state", func(t *testing.T) {
		// Remove notifications of applications deleted by the other subtests.
		exec(`DELETE FROM notifications WHERE user_id=$1`, user.ID)
		a, b := app(), app()
		exec(`UPDATE applications SET status_changed_at=now()-interval '11 days' WHERE id IN ($1,$2)`, a.ID, b.ID)
		if _, e := st.Notifications(ctx, user.ID, "open", "", 200); e != nil {
			t.Fatal(e)
		}
		if e := st.DismissQuietApplication(ctx, user.ID, b.ID); e != nil {
			t.Fatal(e)
		}
		trace.perRow.Store(0)
		f, e := st.Notifications(ctx, user.ID, "all", "", 200)
		if e != nil || len(f.Items) != 2 || f.Open != 1 {
			t.Fatalf("steady feed: %+v %v", f, e)
		}
		if n := trace.perRow.Load(); n != 0 {
			t.Errorf("steady read ran %d per-row statements", n)
		}
		// An already handled application must not block feed navigation.
		tx, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		if _, e = tx.Exec(ctx, `SELECT id FROM applications WHERE id IN ($1,$2) FOR UPDATE`, a.ID, b.ID); e != nil {
			t.Fatal(e)
		}
		short, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if _, e = st.Notifications(short, user.ID, "open", "", 1); e != nil {
			t.Errorf("steady feed waited for application locks: %v", e)
		}
	})
}

func TestQuietApplicationsAgainstPostgres(t *testing.T) {
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated throwaway database")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := New(pool)
	user := func(name string) string {
		u, e := st.CreateUser(ctx, fmt.Sprintf("quiet-%s-%d@example.test", name, time.Now().UnixNano()), "fake")
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, u.ID) })
		return u.ID
	}
	ana, bob := user("ana"), user("bob")
	app := func(u string) models.Application {
		j, e := st.InsertJob(ctx, u, models.JobInput{Title: "Engineer", Company: "Quiet Test", Description: "Build services", ApplyURL: fmt.Sprintf("https://example.test/%d", time.Now().UnixNano())}, models.Evaluation{Score: 80}, "")
		if e != nil {
			t.Fatal(e)
		}
		a, e := st.CreateApplication(ctx, u, j.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		_, e = st.UpdateApplication(ctx, u, a.ID, "applied", nil)
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	a, b := app(ana), app(bob)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`UPDATE applications SET status_changed_at=now()-interval '11 days' WHERE id IN ($1,$2)`, a.ID, b.ID)
	feed := func(u string) models.NotificationFeed {
		t.Helper()
		f, e := st.Notifications(ctx, u, "open", "", 200)
		if e != nil {
			t.Fatal(e)
		}
		return f
	}
	f := feed(ana)
	if f.Open != 1 || len(f.Items) != 1 || f.Items[0].SubjectID != a.ID || f.Items[0].Kind != "quiet_application" {
		t.Fatalf("quiet application: %+v", f)
	}
	if again := feed(ana); again.Open != 1 || again.Items[0].ID != f.Items[0].ID {
		t.Fatal("duplicate quiet reminder", again)
	}
	notes := "A private note, not a status change"
	if _, e := st.UpdateApplication(ctx, ana, a.ID, "applied", &notes); e != nil {
		t.Fatal(e)
	}
	if unchanged := feed(ana); unchanged.Open != 1 || unchanged.Items[0].ID != f.Items[0].ID {
		t.Fatal("notes or same status reset quiet period", unchanged)
	}
	if _, e := st.SaveAccountSettings(ctx, bob, 20); e != nil {
		t.Fatal(e)
	}
	if f := feed(bob); f.Open != 0 {
		t.Fatal("user setting ignored", f)
	}
	if e := st.DismissQuietApplication(ctx, bob, a.ID); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("cross-user dismiss", e)
	}
	if e := st.DismissQuietApplication(ctx, ana, a.ID); e != nil {
		t.Fatal(e)
	}
	if f := feed(ana); f.Open != 0 {
		t.Fatal("dismissal reopened", f)
	}
	if _, e := st.UpdateApplication(ctx, ana, a.ID, "interview", nil); e != nil {
		t.Fatal(e)
	}
	if f := feed(ana); f.Open != 0 {
		t.Fatal("fresh status is quiet", f)
	}
	exec(`UPDATE applications SET status_changed_at=now()-interval '11 days' WHERE id=$1`, a.ID)
	// The old dismissal predates this simulated status period.
	exec(`UPDATE notifications SET created_at=now()-interval '12 days' WHERE user_id=$1`, ana)
	if f := feed(ana); f.Open != 1 {
		t.Fatal("interview not surfaced", f)
	}
	if _, e := st.UpdateApplication(ctx, ana, a.ID, "offer", nil); e != nil {
		t.Fatal(e)
	}
	if f := feed(ana); f.Open != 0 {
		t.Fatal("status change did not resolve", f)
	}
	exec(`UPDATE applications SET status_changed_at=now()-interval '100 days' WHERE id=$1`, a.ID)
	if f := feed(ana); f.Open != 0 {
		t.Fatal("terminal outcome became quiet", f)
	}
}

func TestFollowUpDraftsAgainstPostgres(t *testing.T) {
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated throwaway database")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := New(pool)
	u, err := st.CreateUser(ctx, fmt.Sprintf("followup-%d@example.test", time.Now().UnixNano()), "fake")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, u.ID)
	j, err := st.InsertJob(ctx, u.ID, models.JobInput{Title: "Engineer", Company: "Followup Test", Description: "Build", ApplyURL: fmt.Sprintf("https://example.test/%d", time.Now().UnixNano())}, models.Evaluation{Score: 80}, "")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateApplication(ctx, u.ID, j.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SaveDocuments(ctx, u.ID, a.JobID, models.Documents{CoverLetter: "Sent letter", Answers: map[string]string{"Q": "Sent answer"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.UpdateApplication(ctx, u.ID, a.ID, "applied", nil); err != nil {
		t.Fatal(err)
	}
	sent, err := st.FollowUpMaterials(ctx, u.ID, a.ID)
	if err != nil || sent.CoverLetter != "" || len(sent.Answers) != 0 {
		t.Fatal("prepared documents are not proof of what was sent", sent, err)
	}
	if err = st.SaveDocuments(ctx, u.ID, a.JobID, models.Documents{CoverLetter: "Unsent rewrite"}); err != nil {
		t.Fatal(err)
	}
	sent, err = st.FollowUpMaterials(ctx, u.ID, a.ID)
	if err != nil || sent.CoverLetter != "" {
		t.Fatal("unsent rewrite leaked", sent, err)
	}
	cv, err := st.SaveCV(ctx, u.ID, models.CVDocument{Kind: "base", Filename: "fake.pdf", Format: "pdf", ExtractedText: "Sent CV fact", Fingerprint: fmt.Sprintf("quiet-%d", time.Now().UnixNano())}, []byte("fake"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO application_outbox(user_id,job_id,application_id,status,cv_document_id,cover_letter,answers,sent_at,updated_at) VALUES($1,$2,$3,'sent',$4,'Sent outbox letter','[{"label":"Experience","answer":"Sent outbox answer","source":"bank_similar"}]',now(),now())`, u.ID, a.JobID, a.ID, cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	sent, err = st.FollowUpMaterials(ctx, u.ID, a.ID)
	if err != nil || sent.CV != "Sent CV fact" || sent.CoverLetter != "Sent outbox letter" || sent.Answers["Experience"] != "Sent outbox answer" {
		t.Fatal("sent outbox missing", sent, err)
	}
	if sent.AnswerSources["Experience"] != "bank_similar" || sent.JobTitle != "Engineer" || sent.Company != "Followup Test" {
		t.Fatal("sent answer provenance or job context lost", sent)
	}
	_, err = pool.Exec(ctx, `UPDATE cv_documents SET extracted_text='Unsent CV rewrite',updated_at=now() WHERE id=$1`, cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	sent, err = st.FollowUpMaterials(ctx, u.ID, a.ID)
	if err != nil || sent.CV != "" {
		t.Fatal("changed CV leaked", sent, err)
	}
	if _, err = st.FollowUpMaterials(ctx, "00000000-0000-0000-0000-000000000000", a.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err = st.SaveFollowUp(ctx, "00000000-0000-0000-0000-000000000000", a.ID, "foreign"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := st.SaveFollowUp(ctx, u.ID, a.ID, "Draft only"); results <- e }()
	}
	wg.Wait()
	close(results)
	successes, limited := 0, 0
	for e := range results {
		if e == nil {
			successes++
		} else if errors.Is(e, ErrFollowUpLimit) {
			limited++
		} else {
			t.Fatal(e)
		}
	}
	if successes != 2 || limited != 2 {
		t.Fatal(successes, limited)
	}
	drafts, err := st.FollowUps(ctx, u.ID, a.ID)
	if err != nil || len(drafts) != 2 {
		t.Fatal(drafts, err)
	}
	foreign, err := st.FollowUps(ctx, "00000000-0000-0000-0000-000000000000", a.ID)
	if err != nil || len(foreign) != 0 {
		t.Fatal(foreign, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO application_follow_ups(application_id,body) VALUES($1,'bypass')`, a.ID)
	if err == nil {
		t.Fatal("SQL allowed a third draft")
	}
}
