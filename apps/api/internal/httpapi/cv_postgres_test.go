package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/cv"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

func TestCVImportUnknownYearsAgainstPostgres(t *testing.T) {
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
	st := store.New(pool)
	run := time.Now().Format("20060102150405.000000")
	u, err := st.CreateUser(ctx, "cv-years-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := st.Profile(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile.YearsExperience = 8
	if err = st.SaveProfile(ctx, u.ID, profile); err != nil {
		t.Fatal(err)
	}
	const secret = "cv-years-test-secret"
	token, err := auth.Token(u.ID, u.Email, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{Store: st, Secret: secret}).Handler()
	for _, tc := range []struct {
		summary string
		want    float64
	}{
		{"I build reliable services for product teams.", 8},
		{"I have 0 years of experience.", 8}, // Import retains its non-empty-only policy.
		{"I have 13 years overall experience.", 13},
	} {
		parsed := cv.Parse("JANE EXAMPLE\nPlatform Engineer\njane@example.test\nSUMMARY\n" + tc.summary)
		raw, err := json.Marshal(parsed)
		if err != nil {
			t.Fatal(err)
		}
		var facts map[string]any
		if err = json.Unmarshal(raw, &facts); err != nil {
			t.Fatal(err)
		}
		doc, err := st.SaveCV(ctx, u.ID, models.CVDocument{Kind: "base", Filename: "synthetic.txt", Format: "text", Parsed: facts}, []byte(tc.summary))
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/cv/"+doc.ID+"/import", strings.NewReader(`{"fields":["years_experience"]}`))
		r.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("import = %d %s", w.Code, w.Body.String())
		}
		got, err := st.Profile(ctx, u.ID)
		if err != nil || got.YearsExperience != tc.want {
			t.Fatalf("profile years = %v, want %v: %v", got.YearsExperience, tc.want, err)
		}
		stored, err := st.CV(ctx, u.ID, doc.ID)
		if err != nil || stored.Approved {
			t.Fatalf("import must leave approval unchanged: %v", err)
		}
	}
}

// Import returns only after its profile/child transaction commits, then refreshes
// hard exclusions using that committed state.
func TestCVImportReevaluatesCommittedEmployersPostgres(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated throwaway database")
	}
	ctx := context.Background()
	pool, e := database.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	st := store.New(pool)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	u, e := st.CreateUser(ctx, "cv-matching-"+time.Now().Format("150405.000000000")+"@example.test", "fake")
	must(e)
	doc, e := st.SaveCV(ctx, u.ID, models.CVDocument{Kind: "base", Filename: "synthetic.txt", Format: "text", Parsed: map[string]any{"experiences": []models.WorkExperience{{Company: "Imported Employer", Title: "Engineer"}}}}, []byte("synthetic"))
	must(e)
	job, e := st.CreateDiscoveredJob(ctx, u.ID, "", "", "", "", time.Now().Format("150405.000000000"), models.JobInput{Title: "Engineer", Company: "Imported Employer", Description: "Build Go services", ApplyURL: "https://example.test/import"})
	must(e)
	_, e = pool.Exec(ctx, `INSERT INTO job_matches(user_id,job_id,rejected,score) VALUES($1,$2,false,70)`, u.ID, job.ID)
	must(e)
	const secret = "synthetic-import-only"
	token, e := auth.Token(u.ID, u.Email, secret, time.Hour)
	must(e)
	sender := &kicks{}
	h := (&Server{Store: st, Secret: secret, Sender: sender}).Handler()
	r := httptest.NewRequest(http.MethodPost, "/api/cv/"+doc.ID+"/import", strings.NewReader(`{"fields":["phone"],"import_experiences":true}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("import status %d: %s", w.Code, w.Body.String())
	}
	matched, e := st.Job(ctx, u.ID, job.ID)
	must(e)
	if !matched.Match.Rejected || matched.Job.ApplyURL != "" {
		t.Fatal("committed employer import did not refresh hard exclusion")
	}
	stored, e := st.CV(ctx, u.ID, doc.ID)
	must(e)
	if stored.Approved || sender.n != 0 {
		t.Fatal("import approved CV or kicked sender")
	}
}
