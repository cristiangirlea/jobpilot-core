package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/store"
)

// maxTokenDays bounds the expiry a caller can ask for. Tokens may also have
// no expiry at all, because revocation exists and an agent that stops working
// on an unremembered date is worse than one the user retires deliberately.
const maxTokenDays = 365

type tokenCreate struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays int      `json:"expires_in_days"`
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	x, err := s.Store.AgentTokens(r.Context(), auth.UserID(r.Context()))
	respond(w, x, err)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var x tokenCreate
	if !decode(w, r, &x) {
		return
	}
	x.Name = strings.TrimSpace(x.Name)
	if x.Name == "" {
		problem(w, 422, "Token name is required.")
		return
	}
	scopes, ok := normalizeScopes(x.Scopes)
	if !ok {
		problem(w, 422, "Scopes must be read and/or write.")
		return
	}
	if x.ExpiresInDays < 0 || x.ExpiresInDays > maxTokenDays {
		problem(w, 422, "Expiry must be between 0 and 365 days.")
		return
	}
	policy, err := s.tokenPolicy(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		internal(w, err)
		return
	}
	switch {
	case policy == TokensOff:
		problem(w, 403, tokensOff)
		return
	case policy == TokensRead && auth.HasScopeIn(scopes, auth.ScopeWrite):
		problem(w, 403, tokensReadOnly)
		return
	}
	var expires *time.Time
	if x.ExpiresInDays > 0 {
		at := time.Now().AddDate(0, 0, x.ExpiresInDays)
		expires = &at
	}
	raw, token, err := s.Store.CreateAgentToken(r.Context(), auth.UserID(r.Context()), x.Name, scopes, expires)
	if errors.Is(err, store.ErrTooManyAgentTokens) {
		problem(w, 422, "Too many agent tokens; revoke one first.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	// The only time the token itself is readable. It is not stored, so it
	// cannot be shown again.
	write(w, 201, map[string]any{"token": raw, "agent_token": token})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	err := s.Store.RevokeAgentToken(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Token not found.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	w.WriteHeader(204)
}

// normalizeScopes accepts read, write, or both, in any order, and defaults to
// read alone so a token created without thought is the harmless kind.
func normalizeScopes(in []string) ([]string, bool) {
	if len(in) == 0 {
		return []string{auth.ScopeRead}, true
	}
	var read, writeScope bool
	for _, s := range in {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case auth.ScopeRead:
			read = true
		case auth.ScopeWrite:
			writeScope = true
		default:
			return nil, false
		}
	}
	out := []string{}
	if read || writeScope {
		// Write implies read: a token that can change things can see them.
		out = append(out, auth.ScopeRead)
	}
	if writeScope {
		out = append(out, auth.ScopeWrite)
	}
	return out, len(out) > 0
}
