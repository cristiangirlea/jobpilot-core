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
	doc, err = st.SetCVApproval(ctx, owner.ID, doc.ID, true)
	if err != nil || !doc.Approved {
		t.Fatalf("keyword gaps must not block approval: %+v %v", doc, err)
	}
	doc, err = st.RefreshCVReview(ctx, owner.ID, doc)
	if err != nil || !doc.Approved || doc.ApprovedAt == nil {
		t.Fatalf("fresh findings reset identical-file approval: %+v %v", doc, err)
	}
	doc.Parsed["blockers"] = []any{"PDF reading order: company is missing."}
	doc, err = st.RefreshCVReview(ctx, owner.ID, doc)
	if err != nil || doc.Approved || doc.ApprovedAt != nil {
		t.Fatalf("fresh blocker kept approval: %+v %v", doc, err)
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
