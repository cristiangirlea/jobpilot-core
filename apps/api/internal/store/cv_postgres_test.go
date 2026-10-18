package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

func TestCVTextLayerReviewAgainstPostgres(t *testing.T) {
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
	run := fmt.Sprint(time.Now().UnixNano())
	owner, err := st.CreateUser(ctx, "cv-review-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser(ctx, "cv-other-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id=$1 OR id=$2", owner.ID, other.ID)
	})
	parsed := map[string]any{
		"tailored":   map[string]any{"name": "Ana Example"},
		"blockers":   []any{"PDF text layer: phone is missing or split across lines."},
		"warnings":   []any{"PDF text layer: phone is missing or split across lines.", "Keyword gap: Rust is not supported by stored facts."},
		"text_layer": map[string]any{"blockers": []any{"PDF text layer: phone is missing or split across lines."}, "keywords": []any{map[string]any{"technology": "rust", "status": "gap", "present": false}}},
	}
	doc, err := st.SaveCV(ctx, owner.ID, models.CVDocument{Kind: "tailored", Filename: "synthetic.pdf", Format: "pdf", Parsed: parsed, ExtractedText: "Ana Example", Fingerprint: "cv-review-" + run, Approved: true}, []byte("synthetic PDF"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Approved {
		t.Fatal("a blocked CV was saved approved")
	}
	stored, err := st.CV(ctx, owner.ID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.Parsed, parsed) || stored.ExtractedText != "Ana Example" {
		t.Fatalf("review did not round trip: %+v", stored)
	}
	if _, err := st.CV(ctx, other.ID, doc.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("other user read CV: %v", err)
	}
	if _, err := st.SetCVApproval(ctx, owner.ID, doc.ID, true); !errors.Is(err, ErrCVReviewBlocked) || !strings.Contains(err.Error(), "phone") {
		t.Fatalf("approval must name the blocker: %v", err)
	}
	if _, err := st.SetCVApproval(ctx, other.ID, doc.ID, true); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("other user approved CV: %v", err)
	}
	if _, err := st.SetCVApproval(ctx, owner.ID, doc.ID, false); err != nil {
		t.Fatalf("withdrawal refused: %v", err)
	}
	t.Run("reupload preserves tailored findings", func(t *testing.T) {
		uploaded, err := st.SaveCV(ctx, owner.ID, models.CVDocument{Kind: "base", Filename: "upload.pdf", Format: "pdf", Fingerprint: doc.Fingerprint, Parsed: map[string]any{"warnings": []any{"base parse"}}}, []byte("synthetic PDF"))
		if err != nil {
			t.Fatal(err)
		}
		if uploaded.Kind != "tailored" || !reflect.DeepEqual(uploaded.Parsed, parsed) {
			t.Errorf("upload erased tailored findings: %+v", uploaded)
		}
		if _, err := st.SetCVApproval(ctx, owner.ID, doc.ID, true); !errors.Is(err, ErrCVReviewBlocked) {
			t.Errorf("upload bypassed blockers: %v", err)
		}
	})
	t.Run("refresh adds blockers to a legacy null parse", func(t *testing.T) {
		legacy, err := st.SaveCV(ctx, owner.ID, models.CVDocument{Kind: "tailored", Filename: "legacy.pdf", Format: "pdf", Fingerprint: "legacy-" + run}, []byte("synthetic legacy PDF"))
		if err != nil {
			t.Fatal(err)
		}
		legacy.Parsed = parsed
		legacy, err = st.RefreshCVReview(ctx, owner.ID, legacy)
		if err != nil {
			t.Fatal(err)
		}
		if len(cvReviewBlockers(legacy)) == 0 || legacy.Parsed["text_layer"] == nil {
			t.Fatalf("legacy findings lost: %+v", legacy.Parsed)
		}
	})
	original := map[string]any{"tailored": map[string]any{"name": "Ana Example", "alignment": 0.25}, "warnings": []any{"original warning"}, "text_layer": map[string]any{"keywords": []any{map[string]any{"technology": "go", "status": "covered", "present": true}}}}
	if _, err := pool.Exec(ctx, `UPDATE cv_documents SET parsed=$1, approved=false, approved_at=NULL, label='Original job', role_family='backend', technologies=ARRAY['go'] WHERE id=$2`, original, doc.ID); err != nil {
		t.Fatal(err)
	}
	delete(parsed, "blockers")
	parsed["text_layer"].(map[string]any)["blockers"] = []any{}
	doc.Parsed = parsed
	if _, err := st.RefreshCVReview(ctx, other.ID, doc); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("other user refreshed CV: %v", err)
	}
	doc, err = st.RefreshCVReview(ctx, owner.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Parsed["tailored"], original["tailored"]) || !reflect.DeepEqual(doc.Parsed["warnings"], original["warnings"]) || !reflect.DeepEqual(doc.Parsed["text_layer"].(map[string]any)["keywords"], original["text_layer"].(map[string]any)["keywords"]) || doc.Label != "Original job" || doc.RoleFamily != "backend" || !reflect.DeepEqual(doc.Technologies, []string{"go"}) {
		t.Errorf("refresh rewrote job-specific review: %+v", doc)
	}
	doc, err = st.SetCVApproval(ctx, owner.ID, doc.ID, true)
	if err != nil || !doc.Approved {
		t.Fatalf("keyword gaps must not block approval: %+v %v", doc, err)
	}
	doc, err = st.RefreshCVReview(ctx, owner.ID, doc)
	if err != nil || !doc.Approved || doc.ApprovedAt == nil {
		t.Fatalf("fresh findings reset identical-file approval: %+v %v", doc, err)
	}
	before := doc
	doc.Parsed = map[string]any{"blockers": []any{"PDF reading order: company is missing."}, "tailored": map[string]any{"name": "Another job"}}
	doc, err = st.RefreshCVReview(ctx, owner.ID, doc)
	if err != nil || !reflect.DeepEqual(doc, before) {
		t.Fatalf("refresh changed approved CV: %+v %v", doc, err)
	}
}

func TestCVReviewPreservesApprovedOutboxPostgres(t *testing.T) {
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
	run := fmt.Sprint(time.Now().UnixNano())
	owner, err := st.CreateUser(ctx, "cv-outbox-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM users WHERE id=$1", owner.ID) })
	doc, err := st.SaveCV(ctx, owner.ID, models.CVDocument{Kind: "tailored", Filename: "synthetic.pdf", Format: "pdf", Fingerprint: run, Approved: true}, []byte("synthetic PDF"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []models.OutboxEntry
	for _, status := range []string{models.OutboxApproved, models.OutboxSent} {
		job, err := st.CreateDiscoveredJob(ctx, owner.ID, "", "", "", "", run+status, models.JobInput{Title: "Engineer", Company: "Example", ApplyURL: "https://example.test/" + run + status})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM jobs WHERE id=$1", job.ID) })
		entry, err := st.SaveOutboxEntry(ctx, owner.ID, models.OutboxEntry{JobID: job.ID, Status: models.OutboxReady, Channel: models.ChannelATSForm, CVDocumentID: doc.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE application_outbox SET status=$1,approved_at=now(),sent_at=CASE WHEN $1='sent' THEN now() END WHERE id=$2`, status, entry.ID); err != nil {
			t.Fatal(err)
		}
		entry, err = st.OutboxEntry(ctx, owner.ID, entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	doc.Parsed = map[string]any{"blockers": []any{"PDF reading order: company is missing."}}
	if _, err := st.RefreshCVReview(ctx, owner.ID, doc); err != nil {
		t.Fatal(err)
	}
	for _, before := range entries {
		after, err := st.OutboxEntry(ctx, owner.ID, before.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(after, before) {
			t.Errorf("refresh moved outbox entry or changed its blockers/notices: before=%+v after=%+v", before, after)
		}
	}
	if err := st.DeleteCV(ctx, owner.ID, doc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RefreshCVReview(ctx, owner.ID, doc); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("deleted row: %v", err)
	}
}

func TestCVReviewBlockers(t *testing.T) {
	for _, blockers := range []any{[]string{"phone"}, []any{"phone"}} {
		doc := models.CVDocument{Kind: "tailored", Parsed: map[string]any{"blockers": blockers}}
		if got := cvReviewBlockers(doc); !reflect.DeepEqual(got, []string{"phone"}) {
			t.Fatalf("blockers lost: %v", got)
		}
	}
	if got := cvReviewBlockers(models.CVDocument{}); len(got) != 0 {
		t.Fatalf("legacy CV blocked: %v", got)
	}
}
