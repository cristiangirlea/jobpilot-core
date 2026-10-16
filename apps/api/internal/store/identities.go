package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/models"
)

var (
	// ErrNoAccount means a provider vouched for someone who has no account
	// here, and sign-up through the provider is not allowed.
	ErrNoAccount = errors.New("no account for this identity")
	// ErrLinkRequired means an account with the identity's email exists but
	// the identity is not linked to it. It is never linked by email alone:
	// sign-up does not verify addresses, so the account may have been
	// registered by someone else in the email owner's name.
	ErrLinkRequired = errors.New("sign in with the password and link the identity first")
	// ErrIdentityTaken means the identity is already linked to another account.
	ErrIdentityTaken = errors.New("this identity belongs to another account")
)

// SignInWithIdentity finds the user a provider identity is linked to. An
// identity not linked yet creates an account only when allowSignup is true and
// no account has its email. An account created here has an empty password
// hash, which no password matches, so it signs in only through the provider.
func (s *Store) SignInWithIdentity(ctx context.Context, provider, subject, email string, allowSignup bool) (models.User, error) {
	var u models.User
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return u, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT u.id,u.email,u.created_at,u.email_verified_at IS NOT NULL FROM user_identities i JOIN users u ON u.id=i.user_id WHERE i.provider=$1 AND i.subject=$2`, provider, subject).Scan(&u.ID, &u.Email, &u.CreatedAt, &u.EmailVerified)
	if err == nil {
		return u, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return u, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email=lower($1))`, email).Scan(&exists); err != nil {
		return models.User{}, err
	}
	if exists {
		return models.User{}, ErrLinkRequired
	}
	if !allowSignup {
		return models.User{}, ErrNoAccount
	}
	// The provider vouched for the address (the ID token's email_verified),
	// so the account starts verified.
	if err = tx.QueryRow(ctx, `INSERT INTO users(email,password_hash,email_verified_at) VALUES(lower($1),'',now()) RETURNING `+userColumns, email).Scan(&u.ID, &u.Email, &u.CreatedAt, &u.EmailVerified); err != nil {
		return models.User{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_identities(user_id,provider,subject,email) VALUES($1,$2,$3,lower($4))`, u.ID, provider, subject, email); err != nil {
		return models.User{}, err
	}
	return u, tx.Commit(ctx)
}

// LinkIdentity links a provider identity to a signed-in user, whatever its
// email. Linking it again to the same user changes nothing.
func (s *Store) LinkIdentity(ctx context.Context, userID, provider, subject, email string) error {
	var owner string
	err := s.DB.QueryRow(ctx, `INSERT INTO user_identities(user_id,provider,subject,email) VALUES($1,$2,$3,lower($4))
		ON CONFLICT (provider,subject) DO UPDATE SET provider=EXCLUDED.provider RETURNING user_id`, userID, provider, subject, email).Scan(&owner)
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrIdentityTaken
	}
	return nil
}
