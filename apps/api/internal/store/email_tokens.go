package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Purposes of an emailed link.
const (
	PurposeVerify = "verify"
	PurposeReset  = "reset"
)

// ErrLinkInvalid covers an unknown, used and expired link alike.
var ErrLinkInvalid = errors.New("this link is invalid or has expired")

func hashEmailToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

// lockUser holds the user's row until tx ends. Issuing and using links both
// take it first, so two requests for one user run one after the other and
// always lock in the same order (user, then tokens).
func lockUser(ctx context.Context, tx pgx.Tx, userID string) error {
	_, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id=$1 FOR UPDATE`, userID)
	return err
}

// CreateEmailToken mints a single-use link token for the user and returns it;
// only its digest is stored. A new token for a purpose retires the user's
// earlier ones for it, so only the latest email works, also when two are
// requested at once.
func (s *Store) CreateEmailToken(ctx context.Context, userID, purpose string, ttl time.Duration) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := lockUser(ctx, tx, userID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE email_tokens SET used_at=now() WHERE user_id=$1 AND purpose=$2 AND used_at IS NULL`, userID, purpose); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO email_tokens(user_id,purpose,token_hash,expires_at) VALUES($1,$2,$3,$4)`, userID, purpose, hashEmailToken(token), time.Now().Add(ttl)); err != nil {
		return "", err
	}
	return token, tx.Commit(ctx)
}

// useEmailToken claims a token in tx and returns its user, after locking the
// user (see lockUser). The claim is a conditional update, so two requests
// with one link cannot both succeed.
func useEmailToken(ctx context.Context, tx pgx.Tx, token, purpose string) (string, error) {
	var userID string
	err := tx.QueryRow(ctx, `SELECT user_id FROM email_tokens WHERE token_hash=$1 AND purpose=$2`, hashEmailToken(token), purpose).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrLinkInvalid
	}
	if err != nil {
		return "", err
	}
	if err := lockUser(ctx, tx, userID); err != nil {
		return "", err
	}
	tag, err := tx.Exec(ctx, `UPDATE email_tokens SET used_at=now() WHERE token_hash=$1 AND purpose=$2 AND used_at IS NULL AND expires_at>now()`, hashEmailToken(token), purpose)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrLinkInvalid
	}
	return userID, nil
}

// VerifyEmail marks the address of the token's user as verified.
func (s *Store) VerifyEmail(ctx context.Context, token string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	userID, err := useEmailToken(ctx, tx, token, PurposeVerify)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET email_verified_at=coalesce(email_verified_at, now()) WHERE id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ResetPassword sets a new password hash for the token's user. Opening the
// link proves the address too, so it counts as verified; every other
// outstanding reset link for the user stops working.
func (s *Store) ResetPassword(ctx context.Context, token, hash string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	userID, err := useEmailToken(ctx, tx, token, PurposeReset)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2, email_verified_at=coalesce(email_verified_at, now()) WHERE id=$1`, userID, hash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE email_tokens SET used_at=now() WHERE user_id=$1 AND purpose=$2 AND used_at IS NULL`, userID, PurposeReset); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
