package store

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

func TestInterviewDocumentsPostgres(t *testing.T) {
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
	run := time.Now().Format("20060102150405.000000")
	ana, err := st.CreateUser(ctx, "prep-ana-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(ctx, "prep-bob-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, ana.ID, bob.ID) })
	job, err := st.InsertJob(ctx, ana.ID, models.JobInput{Title: "Engineer", Company: "Prep Test", Description: "Build Go services; Kafka desired.", ApplyURL: "https://example.test/prep-" + run}, models.Evaluation{MatchedSkills: []string{"Go"}, MissingSkills: []string{"Kafka"}, Warnings: []string{"No salary"}, RejectionReasons: []string{}, Reasons: []string{}, RedFlags: []string{}, Breakdown: map[string]int{}}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, job.Job.ID) })
	app, err := st.CreateApplication(ctx, ana.ID, job.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if docs, err := st.InterviewDocuments(ctx, ana.ID, bob.ID, job.Job.ID); err != nil || docs != nil {
		t.Fatalf("unknown application must have no sent documents: %+v, %v", docs, err)
	}
	if docs, err := st.InterviewDocuments(ctx, ana.ID, app.ID, app.JobID); err != nil || docs != nil {
		t.Fatalf("manual application must have no sent documents: %+v, %v", docs, err)
	}
	cv, err := st.SaveCV(ctx, ana.ID, models.CVDocument{Kind: "base", Filename: "test.pdf", Format: "pdf", Fingerprint: "prep-" + run, ExtractedText: "  CV sent\nGo services\n"}, []byte("%PDF test"))
	if err != nil {
		t.Fatal(err)
	}
	answers := []models.OutboxAnswer{
		{Field: "why", Label: "Why?", Source: "human", Answer: "  My exact answer.\n"},
		{Field: "resume", Source: "cv"},
		{Field: "letter", Source: "cover_letter"},
	}
	entry, err := st.SaveOutboxEntry(ctx, ana.ID, models.OutboxEntry{JobID: app.JobID, ApplicationID: app.ID, CVDocumentID: cv.ID, Status: models.OutboxReady, CoverLetter: " Letter as sent\n", Answers: answers})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := st.InterviewDocuments(ctx, ana.ID, app.ID, app.JobID)
	if err != nil || docs != nil {
		t.Fatalf("a draft is not sent: %+v, %v", docs, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_outbox SET status='sent',sent_at=now() WHERE id=$1`, entry.ID); err != nil {
		t.Fatal(err)
	}
	docs, err = st.InterviewDocuments(ctx, ana.ID, app.ID, app.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if docs == nil || docs.CVText == nil || *docs.CVText != cv.ExtractedText || docs.CoverLetter != entry.CoverLetter || !reflect.DeepEqual(docs.Answers, answers) {
		t.Fatalf("want verbatim stored documents: %+v", docs)
	}
	if docs, err := st.InterviewDocuments(ctx, bob.ID, app.ID, app.JobID); err != nil || docs != nil {
		t.Fatalf("other tenant must see no documents: %+v, %v", docs, err)
	}
	if docs, err := st.InterviewDocuments(ctx, ana.ID, app.ID, bob.ID); err != nil || docs != nil {
		t.Fatalf("wrong job must see no documents: %+v, %v", docs, err)
	}
	changed := cv
	changed.ExtractedText = "later parse"
	if saved, err := st.SaveCV(ctx, ana.ID, changed, []byte("%PDF test")); err != nil || saved.ID != cv.ID {
		t.Fatalf("a re-upload must update the existing CV: %+v, %v", saved, err)
	}
	docs, err = st.InterviewDocuments(ctx, ana.ID, app.ID, app.JobID)
	if err != nil || docs == nil || docs.CVText != nil {
		t.Fatalf("a later parse must not be represented as sent: %+v, %v", docs, err)
	}
	if err := st.DeleteCV(ctx, ana.ID, cv.ID); err != nil {
		t.Fatal(err)
	}
	docs, err = st.InterviewDocuments(ctx, ana.ID, app.ID, app.JobID)
	if err != nil || docs == nil || docs.CVText != nil || docs.CoverLetter != entry.CoverLetter {
		t.Fatalf("deleted CV must leave other sent documents intact: %+v, %v", docs, err)
	}
	foreignCV, err := st.SaveCV(ctx, bob.ID, models.CVDocument{Kind: "base", Filename: "other.pdf", Format: "pdf", ExtractedText: "Other tenant private text"}, []byte("%PDF other"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_outbox SET cv_document_id=$2 WHERE id=$1`, entry.ID, foreignCV.ID); err != nil {
		t.Fatal(err)
	}
	docs, err = st.InterviewDocuments(ctx, ana.ID, app.ID, app.JobID)
	if err != nil || docs == nil || docs.CVText != nil {
		t.Fatal("cross-tenant CV reference leaked")
	}
	if _, err := pool.Exec(ctx, `UPDATE application_outbox SET application_id=NULL WHERE id=$1`, entry.ID); err != nil {
		t.Fatal(err)
	}
	docs, err = st.InterviewDocuments(ctx, ana.ID, app.ID, app.JobID)
	if err != nil || docs != nil {
		t.Fatal("unlinked outbox must not be used")
	}
}
