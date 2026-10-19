package store

import (
	"context"
	"os"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

func TestSentCVVersionPostgres(t *testing.T) {
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
	u, err := st.CreateUser(ctx, "sent-cv-"+run+"@example.test", "fake")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, u.ID) })

	for _, edited := range []bool{false, true} {
		name := "unchanged"
		if edited {
			name = "edited before post"
		}
		t.Run(name, func(t *testing.T) {
			cv, err := st.SaveCV(ctx, u.ID, models.CVDocument{Kind: "base", Filename: "fake.pdf", Format: "pdf", ExtractedText: "Original CV"}, []byte("original bytes"))
			if err != nil {
				t.Fatal(err)
			}
			read, data, err := st.CVFile(ctx, u.ID, cv.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !read.UpdatedAt.Equal(cv.UpdatedAt) || string(data) != "original bytes" {
				t.Errorf("CVFile must return the version of its bytes: got %v, %q; want %v", read.UpdatedAt, data, cv.UpdatedAt)
			}
			job, err := st.CreateDiscoveredJob(ctx, u.ID, "", "", "", "", "sent-cv-"+run+name, models.JobInput{Title: "Engineer", Company: "Fake", ApplyURL: "https://example.test/" + run + name})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, job.ID) })
			if _, err := pool.Exec(ctx, `INSERT INTO job_matches(user_id,job_id,rejected,score) VALUES($1,$2,false,80)`, u.ID, job.ID); err != nil {
				t.Fatal(err)
			}
			app, err := st.CreateApplication(ctx, u.ID, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			entry, err := st.SaveOutboxEntry(ctx, u.ID, models.OutboxEntry{JobID: job.ID, ApplicationID: app.ID, CVDocumentID: cv.ID, Status: models.OutboxReady, Answers: []models.OutboxAnswer{{Field: "resume", Source: "cv"}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE application_outbox SET status='sending' WHERE id=$1`, entry.ID); err != nil {
				t.Fatal(err)
			}
			if edited {
				var updated time.Time
				if err := pool.QueryRow(ctx, `UPDATE cv_documents SET extracted_text='Unsent edit',data=$2,updated_at=now() WHERE id=$1 RETURNING updated_at`, cv.ID, []byte("edited bytes")).Scan(&updated); err != nil {
					t.Fatal(err)
				}
				if !updated.After(cv.UpdatedAt) {
					t.Fatal("the edit must be later than the read")
				}
			}
			if err := st.RecordSubmitAttempt(ctx, u.ID, entry.ID, &cv.UpdatedAt); err != nil {
				t.Fatal(err)
			}
			var recorded *time.Time
			if err := pool.QueryRow(ctx, `SELECT sent_cv_updated_at FROM application_outbox WHERE id=$1`, entry.ID).Scan(&recorded); err != nil {
				t.Fatal(err)
			}
			if recorded == nil || !recorded.Equal(cv.UpdatedAt) {
				t.Errorf("recorded version: got %v, want %v", recorded, cv.UpdatedAt)
			}
			if err := st.FinishOutboxSend(ctx, u.ID, entry.ID, true, ""); err != nil {
				t.Fatal(err)
			}
			want := "Original CV"
			if edited {
				want = ""
			}
			docs, err := st.InterviewDocuments(ctx, u.ID, app.ID, job.ID)
			if err != nil || docs == nil {
				t.Fatalf("interview documents: %v, %v", docs, err)
			}
			got := ""
			if docs.CVText != nil {
				got = *docs.CVText
			}
			if got != want {
				t.Errorf("interview CV: got %q, want %q", got, want)
			}
			materials, err := st.FollowUpMaterials(ctx, u.ID, app.ID)
			if err != nil || materials.CV != want {
				t.Errorf("follow-up CV: got %q (%v), want %q", materials.CV, err, want)
			}
		})
	}
}
