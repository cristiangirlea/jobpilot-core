package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"jobpilot/core/api/internal/auth"
	mailer "jobpilot/core/api/internal/mail"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

const (
	verifyLinkTTL = 24 * time.Hour
	resetLinkTTL  = time.Hour

	needsVerifiedEmail = "Verify your email address first: open the link we sent you, or send a new one from Settings."
	linkInvalid        = "This link is invalid, already used or expired. Ask for a new one."
	resetRequested     = "If an account uses that email, a link to reset its password is on its way."
)

// Email work (looking an address up, minting its link, sending) runs on
// mailWorkers goroutines fed by a queue of mailQueue jobs, so a slow relay
// or a burst of requests cannot pile up connections or goroutines. A job that
// finds the queue full is dropped and logged; the request it came from has
// already been answered the same way either way.
const (
	mailWorkers = 4
	mailQueue   = 64
)

type mailJob func(ctx context.Context)

func (s *Server) startMail() {
	s.mailJobs = make(chan mailJob, mailQueue)
	for range mailWorkers {
		go func() {
			for job := range s.mailJobs {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				job(ctx)
				cancel()
			}
		}()
	}
}

func (s *Server) enqueue(job mailJob) {
	select {
	case s.mailJobs <- job:
	default:
		slog.Error("email queue is full; dropped one")
	}
}

func (s *Server) mail() mailer.Sender {
	if s.Mailer == nil {
		return mailer.Disabled{}
	}
	return s.Mailer
}

// verificationRequired is true when emails can actually be sent: without a
// sender nobody could verify, so nothing is held back.
func (s *Server) verificationRequired() bool { return s.mail().Enabled() }

// deliver queues m, so a slow relay does not hold the request up.
func (s *Server) deliver(m mailer.Message) {
	s.enqueue(func(ctx context.Context) { s.send(ctx, m) })
}

// send is the last step of every email job. Failures are logged with the
// recipient and subject only, never the body with its link.
func (s *Server) send(ctx context.Context, m mailer.Message) {
	if err := s.mail().Send(ctx, m); err != nil {
		slog.Error("sending email failed", "to", m.To, "subject", m.Subject, "error", err)
	}
}

// link is the web app's page for a token. The token goes in the fragment,
// which browsers never send to a server or in a Referer header.
func (s *Server) link(page, token string) string {
	return strings.TrimRight(s.FrontendURL, "/") + page + "#token=" + token
}

// sendVerification emails u a link to verify their address, when email is
// on. The token is created before deliver, so a failure to store it is the
// caller's to report.
func (s *Server) sendVerification(ctx context.Context, u models.User) error {
	if !s.verificationRequired() || u.EmailVerified {
		return nil
	}
	token, err := s.Store.CreateEmailToken(ctx, u.ID, store.PurposeVerify, verifyLinkTTL)
	if err != nil {
		return err
	}
	s.deliver(mailer.Message{
		To:      u.Email,
		Subject: "Verify your email for JobPilot",
		Body: "Open this link to verify the email address of your JobPilot account:\n\n" +
			s.link("/verify-email", token) + "\n\n" +
			"It works once and expires in 24 hours. If you did not create an account, ignore this email.\n",
	})
	return nil
}

// requireVerified refuses accounts whose address is not verified yet, while
// email is on. It guards what costs the server work or outlives a session:
// discovery runs, everything that generates documents or answers (generate,
// compose, a tailored CV, resolving a job's answers), agent tokens and
// linking a Google identity.
func (s *Server) requireVerified(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.verificationRequired() {
			u, err := s.Store.User(r.Context(), auth.UserID(r.Context()))
			if err != nil {
				internal(w, err)
				return
			}
			if !u.EmailVerified {
				problem(w, 403, needsVerifiedEmail)
				return
			}
		}
		next(w, r)
	}
}

// account is the signed-in user's own record, with whether this server asks
// for a verified address.
func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.User(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		internal(w, err)
		return
	}
	write(w, 200, map[string]any{"email": u.Email, "email_verified": u.EmailVerified, "verification_required": s.verificationRequired()})
}

// resendVerification sends a new link; the earlier ones stop working.
func (s *Server) resendVerification(w http.ResponseWriter, r *http.Request) {
	if !s.verificationRequired() {
		problem(w, 409, "This server does not send email, so there is nothing to verify.")
		return
	}
	if limited(w, s.limits.resendUser, auth.UserID(r.Context())) {
		return
	}
	u, err := s.Store.User(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		internal(w, err)
		return
	}
	if u.EmailVerified {
		problem(w, 409, "Your email address is already verified.")
		return
	}
	if err := s.sendVerification(r.Context(), u); err != nil {
		internal(w, err)
		return
	}
	write(w, 202, map[string]string{"status": "sent"})
}

type linkToken struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	if limited(w, s.limits.linkIP, s.clientIP(r)) {
		return
	}
	var x linkToken
	if !decode(w, r, &x) {
		return
	}
	err := s.Store.VerifyEmail(r.Context(), x.Token)
	if errors.Is(err, store.ErrLinkInvalid) {
		problem(w, 400, linkInvalid)
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	write(w, 200, map[string]string{"status": "verified"})
}

// requestPasswordReset answers the same way, at once, whether or not the
// email has an account: the lookup and the email happen afterwards, in the
// background, so neither the answer nor its timing tells.
func (s *Server) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	if limited(w, s.limits.resetIP, s.clientIP(r)) {
		return
	}
	var x struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &x) {
		return
	}
	email := strings.TrimSpace(strings.ToLower(x.Email))
	if _, err := mail.ParseAddress(email); err != nil {
		problem(w, 422, "Enter a valid email.")
		return
	}
	if limited(w, s.limits.resetEmail, emailKey(email)) {
		return
	}
	if s.mail().Enabled() {
		s.enqueue(func(ctx context.Context) { s.sendReset(ctx, email) })
	}
	write(w, 202, map[string]string{"status": resetRequested})
}

// sendReset runs as a mail job: the lookup happens here, after the request
// was answered.
func (s *Server) sendReset(ctx context.Context, email string) {
	u, _, err := s.Store.Credentials(ctx, email)
	if err != nil {
		return // no account, or the lookup failed; either way the answer was already sent
	}
	token, err := s.Store.CreateEmailToken(ctx, u.ID, store.PurposeReset, resetLinkTTL)
	if err != nil {
		slog.Error("creating a password reset link failed", "error", err)
		return
	}
	s.send(ctx, mailer.Message{
		To:      u.Email,
		Subject: "Reset your JobPilot password",
		Body: "Someone asked to reset the password of the JobPilot account for this address. Open this link to choose a new one:\n\n" +
			s.link("/reset-password", token) + "\n\n" +
			"It works once and expires in one hour. If you did not ask, ignore this email: your password stays as it is.\n",
	})
}

func (s *Server) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	if limited(w, s.limits.linkIP, s.clientIP(r)) {
		return
	}
	var x linkToken
	if !decode(w, r, &x) {
		return
	}
	if len(x.Password) < 8 {
		problem(w, 422, "Choose a password of at least 8 characters.")
		return
	}
	hash, err := auth.Hash(x.Password)
	if err != nil {
		problem(w, 422, "Choose a password of at most 72 bytes.")
		return
	}
	err = s.Store.ResetPassword(r.Context(), x.Token, hash)
	if errors.Is(err, store.ErrLinkInvalid) {
		problem(w, 400, linkInvalid)
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	write(w, 200, map[string]string{"status": "Your password is changed. Sign in with the new one."})
}
