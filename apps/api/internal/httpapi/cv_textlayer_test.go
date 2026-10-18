package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

type cvLookupTrace struct{ afterLookup func() }
type cvLookupKey struct{}

func (tr *cvLookupTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, cvLookupKey{}, strings.Contains(data.SQL, "FROM cv_documents WHERE user_id=$1 AND fingerprint=$2"))
}
func (tr *cvLookupTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if lookup, _ := ctx.Value(cvLookupKey{}).(bool); lookup && tr.afterLookup != nil {
		f := tr.afterLookup
		tr.afterLookup = nil
		f()
	}
}

func TestDecideCVTextLayerReusePostgres(t *testing.T) {
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
	st := store.New(pool)
	owner, err := st.CreateUser(ctx, fmt.Sprintf("cv-decision-%d@example.test", time.Now().UnixNano()), "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM users WHERE id=$1", owner.ID) })
	profile := models.Profile{Name: "Ana Example", Email: "ana@example.test", Phone: strings.Repeat("+40 712 345 678 ", 10), Summary: "Build reliable services."}
	job := models.JobWithMatch{Job: models.Job{JobInput: models.JobInput{Title: "Backend Engineer", Company: "Example", Description: "Rust"}}}
	s := &Server{Store: st}
	first, err := s.decideCV(ctx, owner.ID, job, profile)
	if err != nil {
		t.Fatal(err)
	}
	if first.Decision != decisionGenerated || len(first.CV.Parsed["blockers"].([]any)) == 0 {
		t.Fatalf("fixture needs an unapproved blocked CV: %+v", first)
	}
	if _, err := pool.Exec(ctx, `UPDATE cv_documents SET approved=true,approved_at=now(),parsed='{"tailored":{"name":"Original review"}}',role_family='other',label='Original job' WHERE id=$1`, first.CV.ID); err != nil {
		t.Fatal(err)
	}
	before, err := st.CV(ctx, owner.ID, first.CV.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("approved identical bytes keep approval and original review", func(t *testing.T) {
		got, err := s.decideCV(ctx, owner.ID, job, profile)
		if err != nil {
			t.Fatal(err)
		}
		if got.Decision != decisionReused || !got.Approved || !reflect.DeepEqual(got.CV, before) {
			t.Fatalf("legacy approval changed: %+v", got)
		}
	})
	t.Run("approved similar role keeps approval and original review", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE cv_documents SET role_family='backend',technologies=ARRAY['rust'] WHERE id=$1`, first.CV.ID); err != nil {
			t.Fatal(err)
		}
		before, err := st.CV(ctx, owner.ID, first.CV.ID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.decideCV(ctx, owner.ID, job, profile)
		if err != nil {
			t.Fatal(err)
		}
		after, err := st.CV(ctx, owner.ID, first.CV.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Decision != decisionReused || !got.Approved || got.CV.ID != before.ID || !reflect.DeepEqual(after, before) {
			t.Fatalf("similar-role reuse changed approval or review: %+v", got)
		}
	})
	t.Run("unapproved identical bytes stay generated", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE cv_documents SET approved=false,approved_at=NULL WHERE id=$1`, first.CV.ID); err != nil {
			t.Fatal(err)
		}
		got, err := s.decideCV(ctx, owner.ID, job, profile)
		if err != nil {
			t.Fatal(err)
		}
		if got.Decision != decisionGenerated || got.Approved || got.CV.Approved || got.CV.Parsed["blockers"] == nil {
			t.Fatalf("unapproved reuse: %+v", got)
		}
	})
	t.Run("deleted after lookup generates again", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE cv_documents SET approved=true,approved_at=now(),role_family='other' WHERE id=$1`, first.CV.ID); err != nil {
			t.Fatal(err)
		}
		config, err := pgxpool.ParseConfig(url)
		if err != nil {
			t.Fatal(err)
		}
		trace := &cvLookupTrace{afterLookup: func() {
			if err := st.DeleteCV(ctx, owner.ID, first.CV.ID); err != nil {
				t.Error(err)
			}
		}}
		config.ConnConfig.Tracer = trace
		traced, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		defer traced.Close()
		s := &Server{Store: store.New(traced)}
		got, err := s.decideCV(ctx, owner.ID, job, profile)
		if err != nil {
			t.Fatalf("deletion became an internal error: %v", err)
		}
		if trace.afterLookup != nil || got.Decision != decisionGenerated || got.CV.ID == first.CV.ID {
			t.Fatalf("deleted row was reused: %+v", got)
		}
	})
}

func TestCVReviewBlockerResponse(t *testing.T) {
	w := httptest.NewRecorder()
	respond(w, nil, fmt.Errorf("%w: PDF text layer: phone is missing", store.ErrCVReviewBlocked))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "phone") {
		t.Fatalf("review blocker response: %d %s", w.Code, w.Body.String())
	}
}
