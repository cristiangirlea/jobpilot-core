package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
)

// AgentTokenPrefix marks a long-lived token issued for an agent or script,
// as opposed to the short-lived token a browser login returns. The prefix
// lets the middleware tell them apart without a database lookup, and makes a
// leaked token recognisable in a log or a config file.
const AgentTokenPrefix = "jpat_"

// Scope names. read is every GET plus the two lookups that change nothing;
// write is everything that can alter the account.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

// Principal is who is making a request. TokenID is empty for a browser
// session, which is how token management tells the two apart.
type Principal struct {
	UserID  string
	Scopes  []string
	TokenID string
}

// TokenVerifier resolves an agent token's hash to its principal, refusing
// revoked and expired tokens. The store implements it; the interface lives
// here so this package does not depend on the store.
type TokenVerifier interface {
	AgentTokenPrincipal(ctx context.Context, hash string) (Principal, error)
}

const principalKey contextKey = "principal"

// NewAgentToken mints a token, returning the value to show the user once, the
// digest to store, and a short prefix for displaying it later. Only the
// digest is ever persisted, so a stolen database cannot be used to call the
// API. A digest rather than bcrypt because this runs on every request.
func NewAgentToken() (raw, hash, prefix string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	body := base64.RawURLEncoding.EncodeToString(buf)
	raw = AgentTokenPrefix + body
	return raw, HashAgentToken(raw), body[:8], nil
}

func HashAgentToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func HasScopeIn(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// Scopes returns what the caller is allowed to do.
func Scopes(ctx context.Context) []string {
	p, _ := ctx.Value(principalKey).(Principal)
	return p.Scopes
}

// IsSession reports whether the caller is a browser login rather than an
// agent token.
func IsSession(ctx context.Context) bool {
	p, ok := ctx.Value(principalKey).(Principal)
	return ok && p.TokenID == ""
}

// TokenID identifies the agent token in use, or "" for a browser session.
func TokenID(ctx context.Context) string {
	p, _ := ctx.Value(principalKey).(Principal)
	return p.TokenID
}

// RequireScope refuses a caller whose token lacks the named scope. A browser
// session always holds every scope, so this only ever stops an agent token.
func RequireScope(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !HasScopeIn(Scopes(r.Context()), scope) {
			deny(w, "This token does not have the "+scope+" scope.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireSession refuses agent tokens outright. Token management uses it so
// that a leaked token cannot mint siblings or revoke itself to cover tracks.
func RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsSession(r.Context()) {
			deny(w, "Use the web app to manage agent tokens.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func deny(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":` + quote(message) + `}`))
}

// quote is a minimal JSON string encoder for the fixed messages above, which
// never contain characters needing an escape.
func quote(s string) string { return `"` + s + `"` }
