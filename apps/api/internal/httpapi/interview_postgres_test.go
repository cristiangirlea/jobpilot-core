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
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/generator"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

func TestInterviewPrepRoutePostgres(t *testing.T) {
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
	st := store.New(pool)
	run := time.Now().Format("20060102150405.000000")
	user, err := st.CreateUser(ctx, "prep-route-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser(ctx, "prep-route-other-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, user.ID, other.ID) })
	job, err := st.InsertJob(ctx, user.ID, models.JobInput{Title: "Engineer", Company: "Prep Route", Description: "Build Go services", ApplyURL: "https://example.test/prep-route-" + run}, models.Evaluation{}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, job.Job.ID) })
	app, err := st.CreateApplication(ctx, user.ID, job.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "interview-route-test"
	token, _, err := st.CreateAgentToken(ctx, user.ID, "prep", []string{auth.ScopeRead}, nil)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := auth.Token(other.ID, other.Email, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{Store: st, Secret: secret, UserAgentTokens: TokensRead}).Handler()
	get := func(bearer string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/applications/"+app.ID+"/interview-prep", nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		h.ServeHTTP(w, r)
		return w
	}
	if w := get(""); w.Code != 401 {
		t.Fatalf("want auth refusal: %d %s", w.Code, w.Body.String())
	}
	for _, status := range []string{"draft", "ready", "applied", "rejected", "offer", "withdrawn", "interview"} {
		if _, err := pool.Exec(ctx, `UPDATE applications SET status=$2 WHERE id=$1`, app.ID, status); err != nil {
			t.Fatal(err)
		}
		w := get(token)
		if status != "interview" {
			if w.Code != 409 || !strings.Contains(w.Body.String(), "interview status") {
				t.Fatalf("%s: %d %s", status, w.Code, w.Body.String())
			}
		} else {
			var pack generator.InterviewPack
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &pack) != nil || pack.ApplicationID != app.ID || pack.Posting.Job.Description != job.Job.Description {
				t.Fatalf("want pack with no AI provider: %d %s", w.Code, w.Body.String())
			}
		}
		if w := get(otherToken); w.Code != 404 {
			t.Fatalf("foreign application must be 404: %d %s", w.Code, w.Body.String())
		}
	}
	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM application_events WHERE application_id=$1`, app.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("reads changed application events: %d, %v", events, err)
	}
}
