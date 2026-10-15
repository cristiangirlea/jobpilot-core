package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/llm"
)

// Roles (#128) on a migrated database: with ADMIN_EMAILS set, a member gets
// 403 from discovery and the AI status, works with its own data, gets no AI
// client, and holds agent tokens only as USER_AGENT_TOKENS allows, checked at
// use as well as at creation. An admin's agent token still reaches
// discovery, for the owner's MCP server. Without ADMIN_EMAILS every account
// has full access, as before.
func TestRolesAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	run := time.Now().Format("20060102150405.000000")
	adminEmail := "roles-admin-" + run + "@example.test"
	admin, err := st.CreateUser(ctx, adminEmail, "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "roles-member-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	const secret = "roles-test-secret"
	configured := &llm.Client{}
	s := &Server{Store: st, Secret: secret, TokenTTL: time.Hour, AdminEmails: []string{adminEmail}, LLM: configured}
	h := s.Handler()
	adminSession, _ := auth.Token(admin.ID, adminEmail, secret, time.Hour)
	memberSession, _ := auth.Token(member.ID, member.Email, secret, time.Hour)

	me := func(token string) map[string]any {
		t.Helper()
		w := call(t, h, "GET", "/api/me", token, "")
		var x map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &x) != nil {
			t.Fatalf("me = %d %s", w.Code, w.Body)
		}
		return x
	}
	if x := me(adminSession); x["admin"] != true || x["roles"] != true || x["agent_tokens"] != "write" {
		t.Fatalf("admin's me = %v", x)
	}
	if x := me(memberSession); x["admin"] != false || x["agent_tokens"] != "off" {
		t.Fatalf("member's me = %v", x)
	}

	for _, route := range []struct{ method, path, body string }{
		{"GET", "/api/discovery/sources", ""},
		{"GET", "/api/discovery/settings", ""},
		{"GET", "/api/discovery/runs", ""},
		{"POST", "/api/discovery/run", ""},
		{"POST", "/api/discovery/detect", `{"url":"https://job-boards.greenhouse.io/example"}`},
		{"GET", "/api/ai/status", ""},
	} {
		if w := call(t, h, route.method, route.path, memberSession, route.body); w.Code != 403 || errorText(t, w) != needsAdmin {
			t.Errorf("member %s %s = %d %s, want 403", route.method, route.path, w.Code, w.Body)
		}
	}
	if w := call(t, h, "GET", "/api/discovery/sources", adminSession, ""); w.Code != 200 {
		t.Fatalf("admin's discovery sources = %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "GET", "/api/profile", memberSession, ""); w.Code != 200 {
		t.Fatalf("member's own profile = %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "GET", "/api/catalog/summary", memberSession, ""); w.Code != 200 {
		t.Fatalf("member's catalog summary = %d %s", w.Code, w.Body)
	}

	// The AI provider is for admins; a member gets none, so every draft
	// takes the deterministic path.
	if got := s.llmForUser(ctx, admin.ID); got != configured {
		t.Fatal("an admin did not get the configured AI client")
	}
	if got := s.llmForUser(ctx, member.ID); got != nil {
		t.Fatal("a member got the configured AI client")
	}

	token := func(session, scopes string) (int, string) {
		t.Helper()
		w := call(t, h, "POST", "/api/tokens", session, `{"name":"roles-test","scopes":`+scopes+`}`)
		var x struct{ Token string }
		_ = json.Unmarshal(w.Body.Bytes(), &x)
		return w.Code, x.Token
	}
	if code, _ := token(memberSession, `["read"]`); code != 403 {
		t.Fatalf("a member made an agent token with USER_AGENT_TOKENS off: %d", code)
	}
	code, adminToken := token(adminSession, `["read","write"]`)
	if code != 201 || adminToken == "" {
		t.Fatalf("admin's agent token = %d", code)
	}
	if w := call(t, h, "GET", "/api/discovery/sources", adminToken, ""); w.Code != 200 {
		t.Fatalf("an admin's agent token was refused discovery: %d %s", w.Code, w.Body)
	}

	s.UserAgentTokens = TokensRead
	if code, _ := token(memberSession, `["read","write"]`); code != 403 {
		t.Fatalf("a member made a write token with USER_AGENT_TOKENS=read: %d", code)
	}
	if code, readToken := token(memberSession, `["read"]`); code != 201 || readToken == "" {
		t.Fatalf("a member's read token with USER_AGENT_TOKENS=read = %d", code)
	}
	s.UserAgentTokens = TokensWrite
	code, writeToken := token(memberSession, `["read","write"]`)
	if code != 201 {
		t.Fatalf("a member's write token with USER_AGENT_TOKENS=write = %d", code)
	}
	if w := call(t, h, "GET", "/api/profile", writeToken, ""); w.Code != 200 {
		t.Fatalf("a member's token reading its profile = %d", w.Code)
	}
	if w := call(t, h, "GET", "/api/discovery/sources", writeToken, ""); w.Code != 403 {
		t.Fatalf("a member's token reached discovery: %d", w.Code)
	}
	// Turning the policy down applies to tokens a member already holds.
	s.UserAgentTokens = TokensRead
	if w := call(t, h, "PUT", "/api/profile", writeToken, `{}`); w.Code != 403 || errorText(t, w) != tokensReadOnly {
		t.Fatalf("a write token wrote after USER_AGENT_TOKENS went to read: %d %s", w.Code, w.Body)
	}
	s.UserAgentTokens = ""
	if w := call(t, h, "GET", "/api/profile", writeToken, ""); w.Code != 403 || errorText(t, w) != tokensOff {
		t.Fatalf("a token worked after USER_AGENT_TOKENS went off: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "GET", "/api/profile", adminToken, ""); w.Code != 200 {
		t.Fatalf("the policy for members stopped an admin's token: %d", w.Code)
	}

	// Without ADMIN_EMAILS there are no roles: the member reaches discovery.
	open := (&Server{Store: st, Secret: secret, TokenTTL: time.Hour}).Handler()
	if w := call(t, open, "GET", "/api/discovery/sources", memberSession, ""); w.Code != 200 {
		t.Fatalf("without ADMIN_EMAILS a member was refused discovery: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(needsAdmin, "Find jobs for my profile") {
		t.Fatal("the refusal no longer points members at Find jobs for my profile")
	}
}
