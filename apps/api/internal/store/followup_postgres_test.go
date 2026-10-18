package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

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
	settings, e := st.DiscoverySettings(ctx, bob)
	if e != nil {
		t.Fatal(e)
	}
	settings.QuietApplicationDays = 20
	if _, e = st.SaveDiscoverySettings(ctx, bob, settings); e != nil {
		t.Fatal(e)
	}
	settings.QuietApplicationDays = 0
	if saved, e := st.SaveDiscoverySettings(ctx, bob, settings); e != nil || saved.QuietApplicationDays != 20 {
		t.Fatal("omitting setting changed it", saved, e)
	}
	if f := feed(bob); f.Open != 0 {
		t.Fatal("user setting ignored", f)
	}
	if e = st.DismissQuietApplication(ctx, bob, a.ID); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("cross-user dismiss", e)
	}
	if e = st.DismissQuietApplication(ctx, ana, a.ID); e != nil {
		t.Fatal(e)
	}
	if f := feed(ana); f.Open != 0 {
		t.Fatal("dismissal reopened", f)
	}
	if _, e = st.UpdateApplication(ctx, ana, a.ID, "interview", nil); e != nil {
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
	if _, e = st.UpdateApplication(ctx, ana, a.ID, "offer", nil); e != nil {
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
	_, err = pool.Exec(ctx, `INSERT INTO application_outbox(user_id,job_id,application_id,status,cv_document_id,cover_letter,answers,sent_at,updated_at) VALUES($1,$2,$3,'sent',$4,'Sent outbox letter','[{"label":"Experience","answer":"Sent outbox answer"}]',now(),now())`, u.ID, a.JobID, a.ID, cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	sent, err = st.FollowUpMaterials(ctx, u.ID, a.ID)
	if err != nil || sent.CV != "Sent CV fact" || sent.CoverLetter != "Sent outbox letter" || sent.Answers["Experience"] != "Sent outbox answer" {
		t.Fatal("sent outbox missing", sent, err)
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
