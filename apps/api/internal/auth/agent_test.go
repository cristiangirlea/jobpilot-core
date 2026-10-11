package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeVerifier stands in for the store so the middleware can be exercised
// without a database.
type fakeVerifier struct {
	principal Principal
	err       error
	seen      string
}

func (f *fakeVerifier) AgentTokenPrincipal(_ context.Context, hash string) (Principal, error) {
	f.seen = hash
	return f.principal, f.err
}

func ok(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }

func TestNewAgentTokenShape(t *testing.T) {
	raw, hash, prefix, err := NewAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, AgentTokenPrefix) {
		t.Fatalf("token should be recognisable by its prefix, got %q", raw)
	}
	if len(raw) < 40 {
		t.Fatalf("token looks too short to be unguessable: %d characters", len(raw))
	}
	if hash == raw || len(hash) != 64 {
		t.Fatalf("the stored value must be a sha256 hex digest, got %q", hash)
	}
	if HashAgentToken(raw) != hash {
		t.Fatal("hashing the token again must produce the stored value")
	}
	if len(prefix) != 8 || !strings.Contains(raw, prefix) {
		t.Fatalf("the display prefix must come from the token itself, got %q", prefix)
	}
	other, _, _, _ := NewAgentToken()
	if other == raw {
		t.Fatal("two tokens must not be equal")
	}
}

func request(t *testing.T, h http.Handler, header string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/profile", nil)
	if header != "" {
		r.Header.Set("Authorization", header)
	}
	h.ServeHTTP(w, r)
	return w
}

func TestMiddlewareAcceptsSessionTokensAndAgentTokens(t *testing.T) {
	const secret = "test-secret"
	session, err := Token("user-1", "a@b.test", secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &fakeVerifier{principal: Principal{UserID: "user-2", Scopes: []string{"read"}, TokenID: "tok-1"}}

	var got Principal
	h := Middleware(secret, verifier, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = Principal{UserID: UserID(r.Context()), Scopes: Scopes(r.Context())}
		if IsSession(r.Context()) {
			got.TokenID = "session"
		}
		ok(w, r)
	}))

	if w := request(t, h, "Bearer "+session); w.Code != 204 {
		t.Fatalf("a session token should be accepted, got %d", w.Code)
	}
	if got.UserID != "user-1" || got.TokenID != "session" {
		t.Fatalf("session principal wrong: %+v", got)
	}
	if !HasScopeIn(got.Scopes, "write") {
		t.Fatalf("a browser session carries full access, got %v", got.Scopes)
	}

	raw, hash, _, _ := NewAgentToken()
	if w := request(t, h, "Bearer "+raw); w.Code != 204 {
		t.Fatalf("a valid agent token should be accepted, got %d", w.Code)
	}
	if got.UserID != "user-2" || got.TokenID == "session" {
		t.Fatalf("agent principal wrong: %+v", got)
	}
	if verifier.seen != hash {
		t.Fatal("the middleware must look the token up by its hash, never by the token itself")
	}
	if HasScopeIn(got.Scopes, "write") {
		t.Fatalf("a read token must not carry write, got %v", got.Scopes)
	}
}

func TestMiddlewareRejectsBadTokens(t *testing.T) {
	const secret = "test-secret"
	verifier := &fakeVerifier{err: errors.New("revoked")}
	h := Middleware(secret, verifier, http.HandlerFunc(ok))

	for _, c := range []struct{ name, header string }{
		{"no header", ""},
		{"not bearer", "Basic abc"},
		{"garbage", "Bearer nonsense"},
		{"revoked agent token", "Bearer " + AgentTokenPrefix + "deadbeef"},
	} {
		if w := request(t, h, c.header); w.Code != 401 {
			t.Errorf("%s: expected 401, got %d", c.name, w.Code)
		}
	}

	// An agent token must never be verified as a session token, even if the
	// signature somehow matched.
	if verifier.seen == "" {
		t.Fatal("the prefixed token should have reached the verifier")
	}
}

func TestRequireScope(t *testing.T) {
	const secret = "test-secret"
	session, _ := Token("user-1", "a@b.test", secret, time.Hour)
	readOnly := &fakeVerifier{principal: Principal{UserID: "user-2", Scopes: []string{"read"}, TokenID: "tok-1"}}
	readWrite := &fakeVerifier{principal: Principal{UserID: "user-2", Scopes: []string{"read", "write"}, TokenID: "tok-2"}}
	raw, _, _, _ := NewAgentToken()

	protected := func(v TokenVerifier) http.Handler {
		return Middleware(secret, v, RequireScope("write", http.HandlerFunc(ok)))
	}

	if w := request(t, protected(readOnly), "Bearer "+raw); w.Code != 403 {
		t.Fatalf("a read token must be refused a write route, got %d", w.Code)
	} else if !strings.Contains(w.Body.String(), "write") {
		t.Fatalf("the refusal should name the missing scope: %s", w.Body.String())
	}
	if w := request(t, protected(readWrite), "Bearer "+raw); w.Code != 204 {
		t.Fatalf("a write token must be allowed, got %d", w.Code)
	}
	if w := request(t, protected(readOnly), "Bearer "+session); w.Code != 204 {
		t.Fatalf("a browser session must keep full access, got %d", w.Code)
	}
}

func TestRequireSession(t *testing.T) {
	const secret = "test-secret"
	session, _ := Token("user-1", "a@b.test", secret, time.Hour)
	verifier := &fakeVerifier{principal: Principal{UserID: "user-2", Scopes: []string{"read", "write"}, TokenID: "tok-1"}}
	raw, _, _, _ := NewAgentToken()

	h := Middleware(secret, verifier, RequireSession("Use the web app for this.", http.HandlerFunc(ok)))

	// Even a write-scoped agent token cannot manage tokens, so a leaked one
	// cannot mint siblings or hide itself.
	w := request(t, h, "Bearer "+raw)
	if w.Code != 403 {
		t.Fatalf("an agent token must be refused token management, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Use the web app for this.") {
		t.Fatalf("the refusal must say why, got %s", w.Body.String())
	}
	if w := request(t, h, "Bearer "+session); w.Code != 204 {
		t.Fatalf("a browser session must be allowed, got %d", w.Code)
	}
}

func TestMiddlewareWithoutVerifierKeepsSessionsWorking(t *testing.T) {
	const secret = "test-secret"
	session, _ := Token("user-1", "a@b.test", secret, time.Hour)
	h := Middleware(secret, nil, http.HandlerFunc(ok))
	if w := request(t, h, "Bearer "+session); w.Code != 204 {
		t.Fatalf("a nil verifier must still accept session tokens, got %d", w.Code)
	}
	raw, _, _, _ := NewAgentToken()
	if w := request(t, h, "Bearer "+raw); w.Code != 401 {
		t.Fatalf("without a verifier an agent token cannot be checked, got %d", w.Code)
	}
}
