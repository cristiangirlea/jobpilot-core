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

func TestAccountQuietSettingsPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	member, e := st.CreateUser(ctx, fmt.Sprintf("quiet-settings-%d@example.test", time.Now().UnixNano()), "fake")
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Exec(ctx, `DELETE FROM users WHERE id=$1`, member.ID)
	token, e := auth.Token(member.ID, member.Email, "settings-test", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	h := (&Server{Store: st, Secret: "settings-test", AdminEmails: []string{"admin@example.test"}, UserAgentTokens: TokensWrite}).Handler()
	path := "/api/account/settings"
	check := func(method, body string, code, days int) {
		t.Helper()
		w := call(t, h, method, path, token, body)
		if w.Code != code {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body)
		}
		if days > 0 {
			var got struct {
				Days int `json:"quiet_application_days"`
			}
			if e := json.Unmarshal(w.Body.Bytes(), &got); e != nil || got.Days != days {
				t.Fatalf("days: %s %v", w.Body, e)
			}
		}
	}
	check("GET", "", 200, 10)
	check("PUT", `{"quiet_application_days":20}`, 200, 20)
	check("PUT", `{}`, 200, 20)
	for _, body := range []string{`{"quiet_application_days":0}`, `{"quiet_application_days":-1}`, `{"quiet_application_days":366}`, `{"quiet_application_days":null}`} {
		check("PUT", body, 422, 0)
	}
	check("GET", "", 200, 20)
	read, _, e := st.CreateAgentToken(ctx, member.ID, "read", []string{auth.ScopeRead}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if w := call(t, h, "PUT", path, read, `{"quiet_application_days":5}`); w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(t, h, "GET", path, read, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	write, _, e := st.CreateAgentToken(ctx, member.ID, "write", []string{auth.ScopeRead, auth.ScopeWrite}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if w := call(t, h, "PUT", path, write, `{"quiet_application_days":30}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	// Discovery updates retain the account preference and do not expose it.
	d, e := st.DiscoverySettings(ctx, member.ID)
	if e != nil {
		t.Fatal(e)
	}
	d.Enabled, d.IntervalMinutes, d.MinScore = true, 720, 85
	if _, e = st.SaveDiscoverySettings(ctx, member.ID, d); e != nil {
		t.Fatal(e)
	}
	check("GET", "", 200, 30)
	check("PUT", `{"quiet_application_days":1}`, 200, 1)
	check("PUT", `{"quiet_application_days":365}`, 200, 365)
	preserved, e := st.DiscoverySettings(ctx, member.ID)
	if e != nil || !preserved.Enabled || preserved.IntervalMinutes != 720 || preserved.MinScore != 85 || preserved.NextRunAt == nil {
		t.Fatal("account preference changed discovery schedule", preserved, e)
	}
	other, e := st.CreateUser(ctx, fmt.Sprintf("quiet-settings-other-%d@example.test", time.Now().UnixNano()), "fake")
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Exec(ctx, `DELETE FROM users WHERE id=$1`, other.ID)
	otherToken, e := auth.Token(other.ID, other.Email, "settings-test", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	w := call(t, h, "GET", path, otherToken, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"quiet_application_days":10`) {
		t.Fatal(w.Code, w.Body)
	}
}

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
