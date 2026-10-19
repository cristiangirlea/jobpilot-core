package httpapi

import (
	"context"
	"errors"
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

type cvLookupTrace struct {
	afterLookup func()
	beforeSave  func()
	failLookup  bool
}
type cvLookupKey struct{}

func (tr *cvLookupTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	lookup := strings.Contains(data.SQL, "FROM cv_documents WHERE user_id=$1 AND fingerprint=$2")
	if lookup && tr.failLookup {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	if strings.Contains(data.SQL, "INSERT INTO cv_documents") && tr.beforeSave != nil {
		f := tr.beforeSave
		tr.beforeSave = nil
		f()
	}
	return context.WithValue(ctx, cvLookupKey{}, lookup)
}
func (tr *cvLookupTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if lookup, _ := ctx.Value(cvLookupKey{}).(bool); lookup && tr.afterLookup != nil {
		f := tr.afterLookup
		tr.afterLookup = nil
		f()
	}
}

func TestDecideCVPreservesApprovalPostgres(t *testing.T) {
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
	for _, scenario := range []string{"lookup error", "approval before save"} {
		t.Run(scenario, func(t *testing.T) {
			run := fmt.Sprint(time.Now().UnixNano())
			owner, err := st.CreateUser(ctx, "cv-race-"+run+"@example.test", "not a real hash")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM users WHERE id=$1", owner.ID) })
			profile := models.Profile{Name: "Ana Example", Email: "ana@example.test", Phone: "+40 712 345 678", Summary: "Build reliable services."}
			job := models.JobWithMatch{Job: models.Job{JobInput: models.JobInput{Title: "Backend Engineer", Company: "Example", Description: "Rust"}}}
			first, err := (&Server{Store: st}).decideCV(ctx, owner.ID, job, profile)
			if err != nil {
				t.Fatal(err)
			}
			if first.Decision != decisionGenerated || first.Approved || len(first.CV.Parsed["blockers"].([]any)) != 0 {
				t.Fatal("fixture needs an unapproved CV with no approval blockers")
			}
			// Force identical-file reuse rather than the earlier similar-role path.
			if _, err := pool.Exec(ctx, `UPDATE cv_documents SET role_family='other',label='Original job' WHERE id=$1`, first.CV.ID); err != nil {
				t.Fatal(err)
			}
			var entries []models.OutboxEntry
			for _, status := range []string{models.OutboxApproved, models.OutboxSent} {
				posting, err := st.CreateDiscoveredJob(ctx, owner.ID, "", "", "", "", run+status, models.JobInput{Title: "Engineer", Company: "Example", ApplyURL: "https://example.test/" + run + status})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM jobs WHERE id=$1", posting.ID) })
				entry, err := st.SaveOutboxEntry(ctx, owner.ID, models.OutboxEntry{JobID: posting.ID, Status: models.OutboxReady, Channel: models.ChannelATSForm, CVDocumentID: first.CV.ID})
				if err != nil {
					t.Fatal(err)
				}
				entry.Status = status
				entries = append(entries, entry)
			}
			var before models.CVDocument
			approve := func() {
				before, err = st.SetCVApproval(ctx, owner.ID, first.CV.ID, true)
				if err != nil {
					t.Fatal(err)
				}
				for i, entry := range entries {
					if _, err := pool.Exec(ctx, `UPDATE application_outbox SET status=$1,approved_at=now(),sent_at=CASE WHEN $1='sent' THEN now() END WHERE id=$2`, entry.Status, entry.ID); err != nil {
						t.Fatal(err)
					}
					entries[i], err = st.OutboxEntry(ctx, owner.ID, entry.ID)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			trace := &cvLookupTrace{}
			if scenario == "lookup error" {
				approve()
				trace.failLookup = true
			} else {
				trace.beforeSave = approve
			}
			config, err := pgxpool.ParseConfig(url)
			if err != nil {
				t.Fatal(err)
			}
			config.ConnConfig.Tracer = trace
			traced, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer traced.Close()
			got, decideErr := (&Server{Store: store.New(traced)}).decideCV(ctx, owner.ID, job, profile)
			if scenario == "lookup error" {
				if !errors.Is(decideErr, context.Canceled) {
					t.Errorf("lookup error was ignored: %v", decideErr)
				}
			} else if trace.beforeSave != nil || decideErr != nil || got.Decision != decisionReused || !got.Approved || !reflect.DeepEqual(got.CV, before) {
				t.Errorf("approval in the save window was not reused: decision=%s approved=%v error=%v", got.Decision, got.Approved, decideErr)
			}
			after, err := st.CV(ctx, owner.ID, first.CV.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Errorf("identical-file reuse changed the approved CV: approved=%v label=%q", after.Approved, after.Label)
			}
			for _, entry := range entries {
				after, err := st.OutboxEntry(ctx, owner.ID, entry.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(after, entry) {
					t.Errorf("reuse moved outbox entry or changed its review: status=%s, want %s", after.Status, entry.Status)
				}
			}
		})
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
		first.CV.Parsed["blockers"] = append(first.CV.Parsed["blockers"].([]any), "PDF text layer: old blocker.")
		first.CV.Parsed["warnings"] = append(first.CV.Parsed["warnings"].([]any), "PDF text layer: old blocker.")
		if _, err := pool.Exec(ctx, `UPDATE cv_documents SET approved=false,approved_at=NULL,parsed=$2 WHERE id=$1`, first.CV.ID, first.CV.Parsed); err != nil {
			t.Fatal(err)
		}
		jobB := job
		jobB.Job.Company, jobB.Job.Description = "Second Example", "Kubernetes"
		got, err := s.decideCV(ctx, owner.ID, jobB, profile)
		if err != nil {
			t.Fatal(err)
		}
		if got.Decision != decisionGenerated || got.Approved || got.CV.Approved || got.CV.Parsed["blockers"] == nil {
			t.Fatalf("unapproved reuse: %+v", got)
		}
		if got.CV.ID != first.CV.ID || got.CV.Fingerprint != first.CV.Fingerprint {
			t.Fatal("fixture must reuse identical bytes")
		}
		if !reflect.DeepEqual(got.CV.Parsed["tailored"], tailoredMap(*got.Preview)) {
			t.Errorf("job B lost its tailored review: %+v", got.CV.Parsed["tailored"])
		}
		keywords := got.CV.Parsed["text_layer"].(map[string]any)["keywords"]
		want := []any{map[string]any{"technology": "kubernetes", "status": "gap", "present": false}}
		if !reflect.DeepEqual(keywords, want) {
			t.Errorf("job B keywords = %+v, want %+v", keywords, want)
		}
		warnings := fmt.Sprint(got.CV.Parsed["warnings"])
		if !strings.Contains(warnings, "Kubernetes") || strings.Contains(warnings, "Rust") || strings.Contains(warnings, "old blocker") {
			t.Errorf("job B kept stale warnings: %s", warnings)
		}
		if blockers := got.CV.Parsed["blockers"].([]any); len(blockers) != 1 || strings.Contains(fmt.Sprint(blockers), "old blocker") {
			t.Errorf("job B kept stale blockers: %v", blockers)
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
