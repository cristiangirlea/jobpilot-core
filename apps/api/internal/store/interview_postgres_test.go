package store

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/generator"
	"jobpilot/core/api/internal/models"
)

func TestInterviewPrepPostgres(t *testing.T) {
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
	if _, err := st.InterviewPrep(ctx, ana.ID, bob.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown application must be absent: %v", err)
	}
	for _, status := range []string{"draft", "ready", "applied", "rejected", "offer", "withdrawn", "interview"} {
		if _, err := pool.Exec(ctx, `UPDATE applications SET status=$2 WHERE id=$1`, app.ID, status); err != nil {
			t.Fatal(err)
		}
		pack, err := st.InterviewPrep(ctx, ana.ID, app.ID)
		if status == "interview" {
			if err != nil || pack.SentDocuments != nil {
				t.Fatalf("manual application must not pretend to have sent documents: %+v, %v", pack, err)
			}
		} else if !errors.Is(err, generator.ErrInterviewStatus) {
			t.Fatalf("%s: want status refusal, got %v", status, err)
		}
		if _, err := st.InterviewPrep(ctx, bob.ID, app.ID); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("other tenant must see no application: %v", err)
		}
	}
	cv, err := st.SaveCV(ctx, ana.ID, models.CVDocument{Kind: "base", Filename: "test.pdf", Format: "pdf", ExtractedText: "  CV sent\nGo services\n"}, []byte("%PDF test"))
	if err != nil {
		t.Fatal(err)
	}
	answers := []models.OutboxAnswer{{Field: "why", Label: "Why?", Source: "human", Answer: "  My exact answer.\n"}}
	entry, err := st.SaveOutboxEntry(ctx, ana.ID, models.OutboxEntry{JobID: app.JobID, ApplicationID: app.ID, CVDocumentID: cv.ID, Status: models.OutboxReady, CoverLetter: " Letter as sent\n", Answers: answers})
	if err != nil {
		t.Fatal(err)
	}
	pack, err := st.InterviewPrep(ctx, ana.ID, app.ID)
	if err != nil || pack.SentDocuments != nil {
		t.Fatalf("a draft is not sent: %+v, %v", pack, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_outbox SET status='sent',sent_at=now() WHERE id=$1`, entry.ID); err != nil {
		t.Fatal(err)
	}
	pack, err = st.InterviewPrep(ctx, ana.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pack.SentDocuments == nil || pack.SentDocuments.CVText == nil || *pack.SentDocuments.CVText != cv.ExtractedText || pack.SentDocuments.CoverLetter != entry.CoverLetter || !reflect.DeepEqual(pack.SentDocuments.Answers, answers) {
		t.Fatalf("want verbatim sent documents: %+v", pack.SentDocuments)
	}
	if !reflect.DeepEqual(pack.Posting.Match, job.Match) || pack.Posting.Job.Description != job.Job.Description {
		t.Fatal("posting and match must be preserved")
	}
	if _, err := pool.Exec(ctx, `UPDATE cv_documents SET extracted_text='later parse',updated_at=now() WHERE id=$1`, cv.ID); err != nil {
		t.Fatal(err)
	}
	pack, err = st.InterviewPrep(ctx, ana.ID, app.ID)
	if err != nil || pack.SentDocuments.CVText != nil {
		t.Fatalf("a later parse must not be represented as sent: %+v, %v", pack, err)
	}
	if err := st.DeleteCV(ctx, ana.ID, cv.ID); err != nil {
		t.Fatal(err)
	}
	pack, err = st.InterviewPrep(ctx, ana.ID, app.ID)
	if err != nil || pack.SentDocuments.CVText != nil || pack.SentDocuments.CoverLetter != entry.CoverLetter {
		t.Fatalf("deleted CV must leave other sent documents intact: %+v, %v", pack, err)
	}
	foreignCV, err := st.SaveCV(ctx, bob.ID, models.CVDocument{Kind: "base", Filename: "other.pdf", Format: "pdf", ExtractedText: "Other tenant private text"}, []byte("%PDF other"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_outbox SET cv_document_id=$2 WHERE id=$1`, entry.ID, foreignCV.ID); err != nil {
		t.Fatal(err)
	}
	pack, err = st.InterviewPrep(ctx, ana.ID, app.ID)
	if err != nil || pack.SentDocuments.CVText != nil {
		t.Fatal("cross-tenant CV reference leaked")
	}
	if _, err := pool.Exec(ctx, `UPDATE application_outbox SET application_id=NULL WHERE id=$1`, entry.ID); err != nil {
		t.Fatal(err)
	}
	pack, err = st.InterviewPrep(ctx, ana.ID, app.ID)
	if err != nil || pack.SentDocuments != nil {
		t.Fatal("unlinked outbox must not be used")
	}
}
