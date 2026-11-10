package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"jobpilot/core/api/internal/auth"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProfileRepairRoutesPostgres(t *testing.T) {
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
	u, e := st.CreateUser(ctx, fmt.Sprintf("repair-api-%d@example.test", time.Now().UnixNano()), "fake")
	must(e)
	read, _, e := st.CreateAgentToken(ctx, u.ID, "read", []string{auth.ScopeRead}, nil)
	must(e)
	write, _, e := st.CreateAgentToken(ctx, u.ID, "write", []string{auth.ScopeRead, auth.ScopeWrite}, nil)
	must(e)
	const secret = "repair-test-only"
	browser, e := auth.Token(u.ID, u.Email, secret, time.Hour)
	must(e)
	doc, e := st.SaveCV(ctx, u.ID, models.CVDocument{Kind: "base", Filename: "synthetic.txt", Format: "text", Parsed: map[string]any{"summary": "Synthetic source summary", "skills": []models.Skill{{Name: "Go"}}, "experiences": []models.WorkExperience{{Company: "Synthetic Employer", Title: "Engineer"}}}}, []byte("Synthetic bytes"))
	must(e)
	job, e := st.CreateDiscoveredJob(ctx, u.ID, "", "", "", "", fmt.Sprint(time.Now().UnixNano()), models.JobInput{Title: "Engineer", Company: "Synthetic Employer", Description: "Build Go services", ApplyURL: "https://example.test/repair"})
	must(e)
	_, e = pool.Exec(ctx, `INSERT INTO job_matches(user_id,job_id,rejected,score) VALUES($1,$2,false,70)`, u.ID, job.ID)
	must(e)
	sender := &kicks{}
	h := (&Server{Store: st, Secret: secret, Sender: sender}).Handler()
	req := func(method, path, token, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(w, r)
		return w
	}
	const path = "/api/profile/repairs"
	body := fmt.Sprintf(`{"source_cv_id":%q}`, doc.ID)
	for _, token := range []string{"", read} {
		w := req("POST", path, token, body)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("proposal scope", w.Code)
		}
	}
	for _, bad := range []string{`{"source_cv_id":""}`, strings.TrimSuffix(body, "}") + `,"summary":"Agent fact"}`, body + body} {
		if w := req("POST", path, write, bad); w.Code != 400 {
			t.Fatal("strict body", w.Code)
		}
	}
	w := req("POST", path, write, body)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var proposal store.ProfileRepair
	must(json.Unmarshal(w.Body.Bytes(), &proposal))
	if proposal.ProposedBy != store.RepairByAgent {
		t.Fatal("a proposal made with an agent token must say so:", proposal.ProposedBy)
	}
	for _, decision := range []string{"apply", "reject", "restore"} {
		for _, token := range []string{read, write} {
			w = req("POST", path+"/"+proposal.ID+"/decision", token, fmt.Sprintf(`{"decision":%q,"expected_fingerprint":%q}`, decision, proposal.BeforeFingerprint))
			if w.Code != 403 || (token == write && !strings.Contains(w.Body.String(), "Review profile repairs in the web app")) {
				t.Fatal("agent decision allowed", decision, w.Code, w.Body.String())
			}
		}
	}
	w = req("GET", path+"/"+proposal.ID, read, "")
	if w.Code != 200 {
		t.Fatal("read detail", w.Code)
	}
	w = req("GET", path, read, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), proposal.ID) {
		t.Fatal("read summaries", w.Code)
	}
	// An id that is not a UUID, or nobody's, is a 404, never a database error.
	for _, id := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		if w = req("GET", path+"/"+id, read, ""); w.Code != 404 {
			t.Fatal("unknown repair read", id, w.Code)
		}
		if w = req("POST", path+"/"+id+"/decision", browser, `{"decision":"reject","expected_fingerprint":"x"}`); w.Code != 404 {
			t.Fatal("unknown repair decision", id, w.Code)
		}
	}
	if w = req("POST", path, write, `{"source_cv_id":"00000000-0000-0000-0000-000000000000"}`); w.Code != 404 {
		t.Fatal("unknown source CV", w.Code)
	}
	if w = req("POST", path+"/"+proposal.ID+"/decision", browser, `{"decision":"delete","expected_fingerprint":"x"}`); w.Code != 400 {
		t.Fatal("unknown decision", w.Code)
	}
	current, e := st.Profile(ctx, u.ID)
	must(e)
	if len(current.Skills) != 0 || current.Summary != "" {
		t.Fatal("a proposal or a refused decision changed the profile")
	}
	w = req("POST", path+"/"+proposal.ID+"/decision", browser, fmt.Sprintf(`{"decision":"apply","expected_fingerprint":%q}`, proposal.BeforeFingerprint))
	if w.Code != 200 {
		t.Fatal("browser apply", w.Code, w.Body.String())
	}
	must(json.Unmarshal(w.Body.Bytes(), &proposal))
	matched, e := st.Job(ctx, u.ID, job.ID)
	must(e)
	if !matched.Match.Rejected || matched.Job.ApplyURL != "" {
		t.Fatal("repair did not re-evaluate employer exclusion")
	}
	w = req("POST", path+"/"+proposal.ID+"/decision", browser, `{"decision":"restore","expected_fingerprint":"stale"}`)
	if w.Code != 409 {
		t.Fatal("stale restore", w.Code)
	}
	w = req("POST", path+"/"+proposal.ID+"/decision", browser, fmt.Sprintf(`{"decision":"restore","expected_fingerprint":%q}`, proposal.AppliedFingerprint))
	if w.Code != 200 {
		t.Fatal("browser restore", w.Code, w.Body.String())
	}
	cv, e := st.CV(ctx, u.ID, doc.ID)
	must(e)
	if cv.Approved || sender.n != 0 {
		t.Fatal("repair approved CV or kicked sender")
	}

	// An employer that leaves the role list is still rejected after the apply.
	_, e = st.AddExperience(ctx, u.ID, models.WorkExperience{Company: "Leaving Employer", Title: "Engineer"})
	must(e)
	leaving, e := st.CreateDiscoveredJob(ctx, u.ID, "", "", "", "", fmt.Sprint(time.Now().UnixNano()), models.JobInput{Title: "Engineer", Company: "Leaving Employer", Description: "Build Go services", ApplyURL: "https://example.test/leaving"})
	must(e)
	_, e = pool.Exec(ctx, `INSERT INTO job_matches(user_id,job_id,rejected,score) VALUES($1,$2,false,70)`, u.ID, leaving.ID)
	must(e)
	w = req("POST", path, browser, body)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	must(json.Unmarshal(w.Body.Bytes(), &proposal))
	if proposal.ProposedBy != store.RepairByBrowser {
		t.Fatal("a proposal made in the browser must say so:", proposal.ProposedBy)
	}
	w = req("GET", path, read, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"proposed_by":"agent"`) || !strings.Contains(w.Body.String(), `"proposed_by":"browser"`) {
		t.Fatal("summaries must say who proposed", w.Body.String())
	}
	w = req("POST", path+"/"+proposal.ID+"/decision", browser, fmt.Sprintf(`{"decision":"apply","expected_fingerprint":%q}`, proposal.BeforeFingerprint))
	if w.Code != 200 {
		t.Fatal("second apply", w.Code, w.Body.String())
	}
	current, e = st.Profile(ctx, u.ID)
	must(e)
	if len(current.Experiences) != 1 || current.Experiences[0].Company != "Synthetic Employer" {
		t.Fatalf("roles after apply: %+v", current.Experiences)
	}
	matched, e = st.Job(ctx, u.ID, leaving.ID)
	must(e)
	if !matched.Match.Rejected || matched.Job.ApplyURL != "" {
		t.Fatal("an employer that left the role list came back into the matches")
	}
}
