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
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// Saving and approving an answer can choose where it is reused. The job is
// named by id or slug and looked up through the caller's own matches, and the
// answer bank's refusals reach the client in its words. This runs on a
// migrated database named by JOBPILOT_TEST_DATABASE_URL, and is skipped
// without one; CI's integration job provides it.
func TestAnswerScopeRoutesAgainstPostgres(t *testing.T) {
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

	run := time.Now().Format("20060102150405.000000")
	const secret = "answer-scope-test-secret"
	user := func(name string) (string, string) {
		email := "answer-scope-" + name + "-" + run + "@example.test"
		u, err := st.CreateUser(ctx, email, "not a real hash")
		if err != nil {
			t.Fatal(err)
		}
		token, err := auth.Token(u.ID, email, secret, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID, token
	}
	anaID, ana := user("ana")
	bobID, bob := user("bob")
	// A job the user has a match for, as discovery leaves it: that is what
	// makes it theirs to name.
	job := func(owner, name string) models.Job {
		j, err := st.InsertJob(ctx, owner, models.JobInput{
			Title: "Backend Engineer", Company: "Answer Scope Test " + run, Description: "Build services.", ApplyURL: "https://example.test/answer-scope-" + name + "-" + run,
		}, models.Evaluation{Score: 80}, "")
		if err != nil {
			t.Fatal(err)
		}
		return j.Job
	}
	anasJob, bobsJob := job(anaID, "ana"), job(bobID, "bob")
	if anasJob.Slug == "" {
		t.Fatal("Ana's job needs a slug for this test")
	}

	h := (&Server{Store: st, Secret: secret, FrontendURL: "http://localhost:3000"}).Handler()
	call := func(method, path, token, body string) (*httptest.ResponseRecorder, models.AnswerEntry) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(w, r)
		var entry models.AnswerEntry
		_ = json.Unmarshal(w.Body.Bytes(), &entry)
		return w, entry
	}

	w, saved := call("POST", "/api/answers", ana, `{"question_text":"Which of the following best describes you?","answer":"An engineer","scope":"job","job_id":"`+anasJob.Slug+`"}`)
	if w.Code != http.StatusCreated || saved.Scope != models.AnswerScopeJob || saved.JobID != anasJob.ID {
		t.Fatalf("saving for one job named by its slug, got %d %s", w.Code, w.Body.String())
	}
	if w, _ = call("POST", "/api/answers", ana, `{"question_text":"Q","answer":"A","scope":"everywhere"}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "scope must be") {
		t.Fatalf("an unknown scope is a 400, got %d %s", w.Code, w.Body.String())
	}
	if w, _ = call("POST", "/api/answers", ana, `{"question_text":"Q","answer":"A","scope":"job"}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "needs the job") {
		t.Fatalf("a job-only answer without a job is a 400, got %d %s", w.Code, w.Body.String())
	}
	if w, _ = call("POST", "/api/answers", ana, `{"question_text":"Q","answer":"A","scope":"job","job_id":"`+bobsJob.ID+`"}`); w.Code != http.StatusNotFound {
		t.Fatalf("Ana must not name Bob's job, got %d %s", w.Code, w.Body.String())
	}
	if w, _ = call("POST", "/api/answers", ana, `{"question_text":"Q","answer":"A","scope":"job","job_id":"not-a-job"}`); w.Code != http.StatusNotFound {
		t.Fatalf("an unknown job is a 404, got %d %s", w.Code, w.Body.String())
	}

	w, global := call("POST", "/api/answers", ana, `{"question_text":"Which of the following best describes you?","answer":"A person","scope":"global","approved":false}`)
	if w.Code != http.StatusCreated || global.Scope != models.AnswerScopeGlobal || global.Approved {
		t.Fatalf("saving for every job, unapproved, got %d %s", w.Code, w.Body.String())
	}
	if w, _ = call("PATCH", "/api/answers/"+global.ID, ana, `{"scope":"job","job_id":"`+anasJob.ID+`"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "already have an answer") {
		t.Fatalf("moving into a scope already holding an answer is a 409, got %d %s", w.Code, w.Body.String())
	}
	if w, _ = call("PATCH", "/api/answers/"+global.ID, ana, `{"scope":"job"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("narrowing an answer with no job to one job is a 400, got %d %s", w.Code, w.Body.String())
	}
	if w, _ = call("PATCH", "/api/answers/"+global.ID, bob, `{"approved":true,"scope":"global"}`); w.Code != http.StatusNotFound {
		t.Fatalf("Bob must not approve Ana's answer, got %d %s", w.Code, w.Body.String())
	}
	w, approved := call("PATCH", "/api/answers/"+global.ID, ana, `{"approved":true,"scope":"role_family"}`)
	if w.Code != http.StatusOK || !approved.Approved || approved.Scope != models.AnswerScopeRoleFamily {
		t.Fatalf("approving for the role family, got %d %s", w.Code, w.Body.String())
	}
}
