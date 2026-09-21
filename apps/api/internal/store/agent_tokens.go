package store

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/models"
)

// MaxAgentTokens caps how many live tokens one user may hold, so a runaway
// script cannot fill the table.
const MaxAgentTokens = 20

var ErrTooManyAgentTokens = errors.New("too many agent tokens; revoke one first")

const agentTokenColumns = `id,name,token_prefix,scopes,created_at,last_used_at,expires_at,revoked_at`

// AgentTokenPrincipal implements auth.TokenVerifier. It resolves a token by
// its digest and refuses anything revoked or expired, so revocation takes
// effect on the very next request.
func (s *Store) AgentTokenPrincipal(ctx context.Context, hash string) (auth.Principal, error) {
	var p auth.Principal
	err := s.DB.QueryRow(ctx, `SELECT id,user_id,scopes FROM agent_tokens WHERE token_hash=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now())`, hash).
		Scan(&p.TokenID, &p.UserID, &p.Scopes)
	if err != nil {
		return auth.Principal{}, err
	}
	// Recorded at most once a minute: this runs on every request, and the
	// exact second a token was last used is not worth a write each time.
	// A failure here must not fail the request, but it is logged, because a
	// blank last-used column is what the user reads before revoking.
	if _, err := s.DB.Exec(ctx, `UPDATE agent_tokens SET last_used_at=now() WHERE id=$1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, p.TokenID); err != nil {
		slog.Warn("could not record agent token use", "token_id", p.TokenID, "error", err)
	}
	return p, nil
}

func (s *Store) AgentTokens(ctx context.Context, u string) ([]models.AgentToken, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+agentTokenColumns+` FROM agent_tokens WHERE user_id=$1 ORDER BY created_at DESC`, u)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.AgentToken{}
	for rows.Next() {
		x, err := scanAgentToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func scanAgentToken(row scanner) (models.AgentToken, error) {
	var x models.AgentToken
	err := row.Scan(&x.ID, &x.Name, &x.TokenPrefix, &x.Scopes, &x.CreatedAt, &x.LastUsedAt, &x.ExpiresAt, &x.RevokedAt)
	return x, err
}

// CreateAgentToken mints a token and returns the raw value alongside the
// stored record. The raw value is the only time it can be read.
func (s *Store) CreateAgentToken(ctx context.Context, u, name string, scopes []string, expires *time.Time) (string, models.AgentToken, error) {
	var live int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM agent_tokens WHERE user_id=$1 AND revoked_at IS NULL`, u).Scan(&live); err != nil {
		return "", models.AgentToken{}, err
	}
	if live >= MaxAgentTokens {
		return "", models.AgentToken{}, ErrTooManyAgentTokens
	}
	raw, hash, prefix, err := auth.NewAgentToken()
	if err != nil {
		return "", models.AgentToken{}, err
	}
	x, err := scanAgentToken(s.DB.QueryRow(ctx,
		`INSERT INTO agent_tokens(user_id,name,token_hash,token_prefix,scopes,expires_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+agentTokenColumns,
		u, name, hash, prefix, scopes, expires))
	if err != nil {
		return "", models.AgentToken{}, err
	}
	return raw, x, nil
}

// RevokeAgentToken is a soft delete: the row stays so the user can see that a
// token existed and when it was last used.
func (s *Store) RevokeAgentToken(ctx context.Context, u, id string) error {
	tag, err := s.DB.Exec(ctx, `UPDATE agent_tokens SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, u)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
