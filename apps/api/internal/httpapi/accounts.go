package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/config"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/ratelimit"
	"jobpilot/core/api/internal/store"
)

// Refusals a sign-up can meet. The invite one is the same for a missing,
// unknown, used or expired code.
const (
	signUpClosed       = "Sign-up is closed on this server."
	signUpNeedsInvite  = "Sign-up needs a valid invite. Ask the person who runs this server for one."
	invitesNeedAdmin   = "Only an admin of this server can manage invites."
	defaultInviteDays  = 14
	maxInviteDays      = 90
	maxInviteNoteChars = 200
)

var (
	errSignUpClosed  = errors.New(signUpClosed)
	errInviteInvalid = errors.New(signUpNeedsInvite)
)

// authLimits are the rate limits on login and sign-up, per client address
// and per email. Counted in memory, which is right for one API instance (see
// package ratelimit).
type authLimits struct {
	loginIP, loginEmail, registerIP, registerEmail *ratelimit.Limiter
}

func newAuthLimits() *authLimits {
	return &authLimits{
		loginIP:       ratelimit.New(30, 15*time.Minute),
		loginEmail:    ratelimit.New(10, 15*time.Minute),
		registerIP:    ratelimit.New(10, time.Hour),
		registerEmail: ratelimit.New(5, time.Hour),
	}
}

// emailKey is the rate-limit key for an email: a fixed-size digest, so the
// limiter holds 64 bytes per key however long a string a caller sends.
func emailKey(email string) string {
	sum := sha256.Sum256([]byte(email))
	return "email:" + hex.EncodeToString(sum[:])
}

// limited answers 429 when key has used up l, saying when to retry, and
// reports whether it did.
func limited(w http.ResponseWriter, l *ratelimit.Limiter, key string) bool {
	ok, wait := l.Allow(key)
	if ok {
		return false
	}
	seconds := int(math.Ceil(wait.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	minutes := (seconds + 59) / 60
	unit := "minutes"
	if minutes == 1 {
		unit = "minute"
	}
	problem(w, 429, fmt.Sprintf("Too many attempts. Try again in %d %s.", minutes, unit))
	return true
}

// clientIP is the address rate limits count against: the configured proxy
// header when one is set and holds an address, else the connection's. IPv6
// addresses count by their /64, since one host usually holds the whole block.
func (s *Server) clientIP(r *http.Request) string {
	raw := ""
	if s.ClientIPHeader != "" {
		raw = strings.TrimSpace(r.Header.Get(s.ClientIPHeader))
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip = net.ParseIP(host)
	}
	if ip == nil {
		return r.RemoteAddr
	}
	if ip.To4() == nil {
		return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	return ip.String()
}

// registration reports how sign-up works here, so the web app knows whether
// to ask for an invite.
func (s *Server) registration(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]string{"mode": s.registrationMode()})
}

func (s *Server) registrationMode() string {
	if s.Registration == "" {
		return config.RegistrationInvite
	}
	return s.Registration
}

// checkSignUp is the one gate every way of creating an account goes through
// before any expensive work: the registration mode, then the invite. Sign-in
// providers that can create accounts call it too.
func (s *Server) checkSignUp(ctx context.Context, invite string) error {
	switch s.registrationMode() {
	case config.RegistrationOpen:
		return nil
	case config.RegistrationInvite:
		if strings.TrimSpace(invite) == "" {
			return errInviteInvalid
		}
		ok, err := s.Store.InviteUsable(ctx, invite)
		if err != nil {
			return err
		}
		if !ok {
			return errInviteInvalid
		}
		return nil
	default:
		return errSignUpClosed
	}
}

// createAccount creates the user under the registration mode, claiming the
// invite in the same transaction when one is needed. Call checkSignUp first.
func (s *Server) createAccount(ctx context.Context, email, hash, invite string) (models.User, error) {
	switch s.registrationMode() {
	case config.RegistrationOpen:
		return s.Store.CreateUser(ctx, email, hash)
	case config.RegistrationInvite:
		u, err := s.Store.CreateUserWithInvite(ctx, email, hash, invite)
		if errors.Is(err, store.ErrInviteInvalid) {
			err = errInviteInvalid
		}
		return u, err
	default:
		return models.User{}, errSignUpClosed
	}
}

// signUpRefused answers a refusal from checkSignUp or createAccount, and
// reports whether err was one.
func signUpRefused(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errSignUpClosed), errors.Is(err, errInviteInvalid):
		problem(w, 403, err.Error())
	case errors.Is(err, store.ErrEmailTaken):
		problem(w, 409, "An account with this email already exists.")
	default:
		return false
	}
	return true
}

// requireAdmin lets through only the accounts ADMIN_EMAILS names.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email, err := s.Store.UserEmail(r.Context(), auth.UserID(r.Context()))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			internal(w, err)
			return
		}
		if err != nil || !slices.Contains(s.AdminEmails, strings.ToLower(email)) {
			problem(w, 403, invitesNeedAdmin)
			return
		}
		next(w, r)
	}
}

type inviteCreate struct {
	Note          string `json:"note"`
	ExpiresInDays int    `json:"expires_in_days"`
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request) {
	x, err := s.Store.Invites(r.Context(), auth.UserID(r.Context()))
	respond(w, x, err)
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	var x inviteCreate
	if !decode(w, r, &x) {
		return
	}
	x.Note = strings.TrimSpace(x.Note)
	if len([]rune(x.Note)) > maxInviteNoteChars {
		problem(w, 422, fmt.Sprintf("Keep the note under %d characters.", maxInviteNoteChars))
		return
	}
	if x.ExpiresInDays == 0 {
		x.ExpiresInDays = defaultInviteDays
	}
	if x.ExpiresInDays < 1 || x.ExpiresInDays > maxInviteDays {
		problem(w, 422, fmt.Sprintf("Expiry must be between 1 and %d days.", maxInviteDays))
		return
	}
	code, invite, err := s.Store.CreateInvite(r.Context(), auth.UserID(r.Context()), x.Note, time.Now().AddDate(0, 0, x.ExpiresInDays))
	if errors.Is(err, store.ErrTooManyInvites) {
		problem(w, 422, "Too many open invites; delete one first.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	// The only time the code is readable: only its digest is stored.
	write(w, 201, map[string]any{"code": code, "invite": invite})
}

func (s *Server) deleteInvite(w http.ResponseWriter, r *http.Request) {
	err := s.Store.DeleteInvite(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Invite not found, or already used.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	w.WriteHeader(204)
}
