package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/discovery"
	"jobpilot/core/api/internal/models"
)

// A re-score that runs out of time leaves the change saved and every match as
// it was, and says so; the next save scores every job again.
func TestAFailedRescoreIsReportedAndTheNextOneScoresEveryJobAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	run := time.Now().Format("20060102150405.000000")
	user, err := st.CreateUser(ctx, "rescore-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	const jobs = 24
	ids := make([]string, jobs)
	t.Cleanup(func() {
		if _, err := st.DB.Exec(ctx, `DELETE FROM users WHERE id=$1`, user.ID); err != nil {
			t.Error(err)
		}
		if _, err := st.DB.Exec(ctx, `DELETE FROM jobs WHERE id = ANY($1)`, ids); err != nil {
			t.Error(err)
		}
	})
	for i := range ids {
		job, err := st.CreateDiscoveredJob(ctx, user.ID, "", "", "", "", fmt.Sprintf("rescore-%s-%d", run, i), models.JobInput{
			Title: "Backend Engineer", Company: fmt.Sprintf("Quillfern Dynamics %s %d", run, i), Description: "Build services in Kotlin.",
			ApplyURL: fmt.Sprintf("https://example.test/rescore/%s/%d", run, i),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.SaveJobMatch(ctx, user.ID, job.ID, models.Evaluation{Score: 1}, ""); err != nil {
			t.Fatal(err)
		}
		ids[i] = job.ID
	}
	const secret = "rescore-test-secret"
	session, _ := auth.Token(user.ID, user.Email, secret, time.Hour)
	server := func(timeout time.Duration) *Server {
		return &Server{Store: st, Discovery: discovery.NewService(st), Secret: secret, TokenTTL: time.Hour, rescoreTimeout: timeout}
	}

	w := call(t, server(time.Nanosecond).Handler(), "POST", "/api/skills", session, `{"name":"Kotlin"}`)
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	// The words are the user's only sign that a retry is due, so they are pinned here.
	const told = "The change was saved, but your matches may not be re-scored. Save your profile again to re-score them."
	if w.Code != 500 || body["error"] != told || body["code"] != "not_rescored" {
		t.Fatalf("a save whose re-score ran out of time answered %d %v, want 500 %q with code not_rescored", w.Code, body, told)
	}
	skills, err := st.Skills(ctx, user.ID)
	if err != nil || len(skills) != 1 || skills[0].Name != "Kotlin" {
		t.Fatalf("the skill was not saved: %v, %v", skills, err)
	}
	for _, id := range ids {
		got, err := st.Job(ctx, user.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Match.Score != 1 || len(got.Match.MatchedSkills) != 0 {
			t.Fatalf("a match changed although the re-score failed: %+v", got.Match)
		}
	}

	if w := call(t, server(0).Handler(), "POST", "/api/skills", session, `{"name":"Rust"}`); w.Code != 201 {
		t.Fatalf("the next save answered %d: %s", w.Code, w.Body.String())
	}
	for _, id := range ids {
		got, err := st.Job(ctx, user.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Match.Score == 1 || !reflect.DeepEqual(got.Match.MatchedSkills, []string{"Kotlin"}) {
			t.Fatalf("job %s was not scored again: score %d, matched %v", id, got.Match.Score, got.Match.MatchedSkills)
		}
	}
}
