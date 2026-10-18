package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

type followUpAI struct{ calls atomic.Int32 }

func (p *followUpAI) Name() string  { return "fake" }
func (p *followUpAI) Model() string { return "test" }
func (p *followUpAI) Complete(context.Context, llm.Request) (string, error) {
	p.calls.Add(1)
	return "Invented claim about Kubernetes.", nil
}

func TestFollowUpRoutesAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	user := func(name string) (models.User, string) {
		u, e := st.CreateUser(ctx, fmt.Sprintf("followup-route-%s-%d@example.test", name, time.Now().UnixNano()), "fake")
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _, _ = st.DB.Exec(ctx, `DELETE FROM users WHERE id=$1`, u.ID) })
		token, e := auth.Token(u.ID, u.Email, "followup-test", time.Hour)
		if e != nil {
			t.Fatal(e)
		}
		return u, token
	}
	admin, adminToken := user("admin")
	member, memberToken := user("member")
	app := func(u string) models.Application {
		j, e := st.InsertJob(ctx, u, models.JobInput{Title: "Engineer", Company: "Followup Routes", Description: "Build services", ApplyURL: fmt.Sprintf("https://example.test/%d", time.Now().UnixNano())}, models.Evaluation{Score: 80}, "")
		if e != nil {
			t.Fatal(e)
		}
		a, e := st.CreateApplication(ctx, u, j.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if e = st.SaveDocuments(ctx, u, a.JobID, models.Documents{CoverLetter: "I build Go services."}); e != nil {
			t.Fatal(e)
		}
		return a
	}
	a, m := app(admin.ID), app(member.ID)
	provider := &followUpAI{}
	h := (&Server{Store: st, Secret: "followup-test", AdminEmails: []string{admin.Email}, UserAgentTokens: TokensWrite, LLM: llm.New(provider, nil, 0, 0)}).Handler()
	path := func(id string) string { return "/api/applications/" + id + "/follow-up" }
	if w := call(t, h, "POST", path(a.ID), adminToken, `{}`); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	for _, pair := range []struct{ u, id string }{{admin.ID, a.ID}, {member.ID, m.ID}} {
		if _, e := st.UpdateApplication(ctx, pair.u, pair.id, "applied", nil); e != nil {
			t.Fatal(e)
		}
	}
	if w := call(t, h, "POST", path(a.ID), memberToken, `{}`); w.Code != 404 {
		t.Fatal("foreign application", w.Code, w.Body)
	}
	read, _, e := st.CreateAgentToken(ctx, member.ID, "read", []string{auth.ScopeRead}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if w := call(t, h, "POST", path(m.ID), read, `{}`); w.Code != 403 {
		t.Fatal("read token wrote", w.Code, w.Body)
	}
	for range 2 {
		w := call(t, h, "POST", path(m.ID), memberToken, `{}`)
		if w.Code != 201 || strings.Contains(w.Body.String(), "Kubernetes") {
			t.Fatal(w.Code, w.Body)
		}
	}
	if provider.calls.Load() != 0 {
		t.Fatal("member reached AI")
	}
	if w := call(t, h, "POST", path(m.ID), memberToken, `{}`); w.Code != 409 || !strings.Contains(w.Body.String(), "two follow-up") {
		t.Fatal(w.Code, w.Body)
	}
	w := call(t, h, "GET", "/api/applications/"+m.ID, memberToken, "")
	var detail struct {
		FollowUps []store.FollowUpDraft `json:"follow_ups"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &detail); e != nil || w.Code != 200 || len(detail.FollowUps) != 2 {
		t.Fatal(w.Code, w.Body, e)
	}
	if w := call(t, h, "POST", path(a.ID), adminToken, `{}`); w.Code != 201 || strings.Contains(w.Body.String(), "Kubernetes") {
		t.Fatal(w.Code, w.Body)
	}
	if provider.calls.Load() != 1 {
		t.Fatal("admin provider not used")
	}
	if _, e = st.SaveJobMatch(ctx, admin.ID, a.JobID, models.Evaluation{Rejected: true, RejectionReasons: []string{"Excluded company"}}, ""); e != nil {
		t.Fatal(e)
	}
	if w := call(t, h, "POST", path(a.ID), adminToken, `{}`); w.Code != 409 || !strings.Contains(w.Body.String(), store.ErrApplicationBlocked.Error()) {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(t, h, "POST", "/api/applications/"+m.ID+"/dismiss-follow-up", memberToken, `{}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(t, h, "POST", "/api/applications/"+a.ID+"/dismiss-follow-up", memberToken, `{}`); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
}
