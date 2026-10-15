package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/discovery"
	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

// A catalog pass that read an account's exclusions before the account
// excluded a company may save an accepted match for that company. The
// re-evaluation the exclusion triggers waits for the pass to finish, then
// rejects it: a hard rejection always wins (#128).
func TestExclusionWinsOverAConcurrentCatalogPassAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	run := time.Now().Format("20060102150405.000000")
	member, err := st.CreateUser(ctx, "lock-member-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	company := "Lock Excluded " + run
	job, err := st.CreateDiscoveredJob(ctx, member.ID, "", "", "", "", "lock-"+run, models.JobInput{
		Title: "Backend Engineer", Company: company, Description: "Build services.", ApplyURL: "https://example.test/lock/" + run,
	})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "lock-test-secret"
	svc := discovery.NewService(st)
	h := (&Server{Store: st, Discovery: svc, Secret: secret, TokenTTL: time.Hour}).Handler()
	session, _ := auth.Token(member.ID, member.Email, secret, time.Hour)

	// The pass holds the account's lock, as ScoreCatalog does.
	unlock := svc.LockMatching(member.ID)
	done := make(chan int)
	go func() {
		done <- call(t, h, "POST", "/api/blacklist", session, `{"company_name":"`+company+`"}`).Code
	}()
	select {
	case code := <-done:
		t.Fatalf("the exclusion's re-evaluation did not wait for the pass (answered %d)", code)
	case <-time.After(300 * time.Millisecond):
	}
	// The pass saves what it evaluated with the old exclusions.
	if _, err := st.SaveJobMatch(ctx, member.ID, job.ID, models.Evaluation{Score: 90}, ""); err != nil {
		t.Fatal(err)
	}
	unlock()
	if code := <-done; code != 201 {
		t.Fatalf("adding the exclusion = %d", code)
	}
	got, err := st.Job(ctx, member.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Match.Rejected || got.Job.ApplyURL != "" {
		t.Fatalf("the stale accepted match survived the exclusion: rejected=%v apply_url=%q", got.Match.Rejected, got.Job.ApplyURL)
	}
}

// With an AI provider configured, a member's answers are drafted without it
// and an admin's with it, through the real handler (#128).
func TestMembersDraftWithoutTheProviderAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"I enjoy designing systems that stay simple to run."}`))
	}))
	defer provider.Close()
	ai, err := llm.Build(llm.Options{Provider: "ollama", Model: "fake", OllamaURL: provider.URL, Timeout: 10 * time.Second, CallsPerHour: 100}, nil)
	if err != nil || !ai.Enabled() {
		t.Fatalf("fake provider: %v", err)
	}
	run := time.Now().Format("20060102150405.000000")
	adminEmail := "ai-admin-" + run + "@example.test"
	admin, err := st.CreateUser(ctx, adminEmail, "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "ai-member-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	const secret = "ai-test-secret"
	h := (&Server{Store: st, Secret: secret, TokenTTL: time.Hour, AdminEmails: []string{adminEmail}, LLM: ai}).Handler()
	ask := func(u models.User, n string) {
		t.Helper()
		job, err := st.CreateDiscoveredJob(ctx, u.ID, "", "", "", "", "ai-"+run+"-"+n, models.JobInput{
			Title: "Backend Engineer", Company: "AI Test " + run + " " + n, Description: "Build services.", ApplyURL: "https://example.test/ai/" + run + "/" + n,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.SaveJobMatch(ctx, u.ID, job.ID, models.Evaluation{Score: 90}, ""); err != nil {
			t.Fatal(err)
		}
		session, _ := auth.Token(u.ID, u.Email, secret, time.Hour)
		w := call(t, h, "POST", "/api/jobs/"+job.ID+"/answers", session, `{"questions":[{"text":"Describe the most interesting system you have designed, and why it mattered."}]}`)
		if w.Code != 200 {
			t.Fatalf("resolving answers = %d %s", w.Code, w.Body)
		}
		if u.ID == member.ID && (strings.Contains(w.Body.String(), `"ai_enabled":true`) || strings.Contains(w.Body.String(), "ollama")) {
			t.Fatalf("a member's answers report the server's AI provider: %s", w.Body)
		}
	}
	ask(member, "member")
	if n := calls.Load(); n != 0 {
		t.Fatalf("a member's answers called the AI provider %d times", n)
	}
	ask(admin, "admin")
	if calls.Load() == 0 {
		t.Fatal("an admin's answers never called the AI provider, so this test proves nothing")
	}
}
