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
	"jobpilot/core/api/internal/models"
)

// InvitePrefix marks an invite code, so one pasted into the wrong field is
// recognisable, and is never confused with an agent token.
const InvitePrefix = "jpi_"

// MaxOpenInvites caps how many unused, unexpired invites one admin may hold.
const MaxOpenInvites = 50

var (
	ErrTooManyInvites = errors.New("too many open invites; delete one first")
	// ErrInviteInvalid covers an unknown, used and expired code alike, so a
	// caller cannot tell which codes once existed.
	ErrInviteInvalid = errors.New("invite is not valid")
	ErrEmailTaken    = errors.New("an account with this email already exists")
)

const inviteColumns = `id,note,code_prefix,created_at,expires_at,used_at`

func hashInvite(code string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return hex.EncodeToString(sum[:])
}

func scanInvite(row scanner) (models.Invite, error) {
	var x models.Invite
	err := row.Scan(&x.ID, &x.Note, &x.CodePrefix, &x.CreatedAt, &x.ExpiresAt, &x.UsedAt)
	return x, err
}

// CreateInvite mints a single-use code and returns it with the stored record.
// The code is readable only now: like an agent token, only its digest is kept.
func (s *Store) CreateInvite(ctx context.Context, admin, note string, expires time.Time) (string, models.Invite, error) {
	var open int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM invites WHERE created_by=$1 AND used_at IS NULL AND expires_at>now()`, admin).Scan(&open); err != nil {
		return "", models.Invite{}, err
	}
	if open >= MaxOpenInvites {
		return "", models.Invite{}, ErrTooManyInvites
	}
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", models.Invite{}, err
	}
	body := base64.RawURLEncoding.EncodeToString(buf)
	code := InvitePrefix + body
	x, err := scanInvite(s.DB.QueryRow(ctx,
		`INSERT INTO invites(created_by,note,code_hash,code_prefix,expires_at) VALUES($1,$2,$3,$4,$5) RETURNING `+inviteColumns,
		admin, note, hashInvite(code), body[:6], expires))
	if err != nil {
		return "", models.Invite{}, err
	}
	return code, x, nil
}

// Invites lists the invites one admin created, newest first.
func (s *Store) Invites(ctx context.Context, admin string) ([]models.Invite, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+inviteColumns+` FROM invites WHERE created_by=$1 ORDER BY created_at DESC`, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Invite{}
	for rows.Next() {
		x, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// DeleteInvite withdraws an unused invite. A used one stays, as the record of
// who was let in; deleting it is refused with pgx.ErrNoRows like a missing one.
func (s *Store) DeleteInvite(ctx context.Context, admin, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM invites WHERE id=$1 AND created_by=$2 AND used_at IS NULL`, id, admin)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// InviteUsable reports whether a code could be claimed now. Sign-up asks
// before hashing the password, so a guessed code costs no bcrypt work; the
// claim itself happens in CreateUserWithInvite.
func (s *Store) InviteUsable(ctx context.Context, code string) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM invites WHERE code_hash=$1 AND used_at IS NULL AND expires_at>now())`, hashInvite(code)).Scan(&ok)
	return ok, err
}

// CreateUserWithInvite claims the invite and creates the account in one
// transaction. Two sign-ups racing for the same code cannot both win, because
// the claim is a conditional update; and a sign-up that fails (the email is
// taken) rolls the claim back, so the code can still be used.
func (s *Store) CreateUserWithInvite(ctx context.Context, email, hash, code string) (models.User, error) {
	var u models.User
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return u, err
	}
	defer tx.Rollback(ctx)
	var inviteID string
	err = tx.QueryRow(ctx, `UPDATE invites SET used_at=now() WHERE code_hash=$1 AND used_at IS NULL AND expires_at>now() RETURNING id`, hashInvite(code)).Scan(&inviteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrInviteInvalid
	}
	if err != nil {
		return u, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO users(email,password_hash) VALUES(lower($1),$2) RETURNING `+userColumns, email, hash).Scan(&u.ID, &u.Email, &u.CreatedAt, &u.EmailVerified)
	if isUniqueViolation(err) {
		return models.User{}, ErrEmailTaken
	}
	if err != nil {
		return models.User{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE invites SET used_by=$1 WHERE id=$2`, u.ID, inviteID); err != nil {
		return models.User{}, err
	}
	return u, tx.Commit(ctx)
}
