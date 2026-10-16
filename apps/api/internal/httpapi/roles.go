package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/llm"
)

// Roles (#128). With ADMIN_EMAILS set, the accounts it names are admins and
// every other account is a member: a member works with its own data only,
// while discovery, the configured AI provider and the server's own settings
// belong to the admins. Each account's data is private either way; roles
// fence off what is shared or spends the server owner's resources. Without
// ADMIN_EMAILS there are no roles and every account has full access, which
// keeps a single-user install as it was.
const (
	// needsAdmin answers a member on discovery and the AI status.
	needsAdmin     = "Only an admin of this server can use this. Your matches come from the jobs the admin's discovery finds: use Find jobs for my profile on the jobs page."
	tokensOff      = "Agent tokens are not available for this account. Ask the server's admin."
	tokensReadOnly = "This account's agent tokens can only read."
)

// Agent token policies for members (USER_AGENT_TOKENS).
const (
	TokensOff   = "off"
	TokensRead  = "read"
	TokensWrite = "write"
)

func (s *Server) rolesOn() bool { return len(s.AdminEmails) > 0 }

// admin reports whether the account is an admin; with roles off every
// account is.
func (s *Server) admin(ctx context.Context, userID string) (bool, error) {
	if !s.rolesOn() {
		return true, nil
	}
	return s.namedAdmin(ctx, userID)
}

// namedAdmin reports whether ADMIN_EMAILS names the account's address. While
// the server sends email the address must also be verified, so registering
// an admin's address before its owner does gains nothing.
func (s *Server) namedAdmin(ctx context.Context, userID string) (bool, error) {
	u, err := s.Store.User(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if s.verificationRequired() && !u.EmailVerified {
		return false, nil
	}
	return slices.Contains(s.AdminEmails, strings.ToLower(u.Email)), nil
}

// IsAdmin is admin for callers that cannot handle an error, such as the
// discovery scheduler: an error counts as not an admin.
func (s *Server) IsAdmin(ctx context.Context, userID string) bool {
	ok, err := s.admin(ctx, userID)
	if err != nil {
		slog.Error("check whether an account is an admin", "error", err)
	}
	return ok
}

// adminOnly lets through only admins, whether signed in or through one of
// their agent tokens, so the owner's MCP server keeps running discovery.
func (s *Server) adminOnly(message string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, err := s.admin(r.Context(), auth.UserID(r.Context()))
		if err != nil {
			internal(w, err)
			return
		}
		if !ok {
			problem(w, 403, message)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// llmFor is the AI client a request may use: the configured one for admins,
// none for members, who get the deterministic drafts every AI path already
// has. An error also means none.
func (s *Server) llmFor(ctx context.Context) *llm.Client {
	return s.llmForUser(ctx, auth.UserID(ctx))
}

func (s *Server) llmForUser(ctx context.Context, userID string) *llm.Client {
	if ok, err := s.admin(ctx, userID); err != nil || !ok {
		return nil
	}
	return s.LLM
}

// tokenPolicy is the agent token policy for the account: write for admins,
// USER_AGENT_TOKENS (off unless set) for members.
func (s *Server) tokenPolicy(ctx context.Context, userID string) (string, error) {
	ok, err := s.admin(ctx, userID)
	if err != nil {
		return TokensOff, err
	}
	if ok {
		return TokensWrite, nil
	}
	switch s.UserAgentTokens {
	case TokensRead, TokensWrite:
		return s.UserAgentTokens, nil
	}
	return TokensOff, nil
}

// agentPolicy applies the policy to requests made with an agent token, at
// use as well as at creation, so turning USER_AGENT_TOKENS off or down to
// read takes effect for tokens a member already holds. Browser sessions pass.
func (s *Server) agentPolicy(writes bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.IsSession(r.Context()) {
			next.ServeHTTP(w, r)
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
		case policy == TokensRead && writes:
			problem(w, 403, tokensReadOnly)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// me tells the web app what the caller may do, so it can hide what an admin
// alone can use. The API enforces each of these on its own.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), auth.UserID(r.Context())
	ok, err := s.admin(ctx, uid)
	if err != nil {
		internal(w, err)
		return
	}
	policy, err := s.tokenPolicy(ctx, uid)
	if err != nil {
		internal(w, err)
		return
	}
	u, err := s.Store.User(ctx, uid)
	if err != nil {
		internal(w, err)
		return
	}
	write(w, 200, map[string]any{"admin": ok, "roles": s.rolesOn(), "agent_tokens": policy,
		"email": u.Email, "email_verified": u.EmailVerified, "verification_required": s.verificationRequired()})
}
