package httpapi

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/config"
	mailer "jobpilot/core/api/internal/mail"
	"jobpilot/core/api/internal/ratelimit"
	"jobpilot/core/api/internal/store"
)

// fakeMail stands in for the SMTP relay and hands each message to the test.
type fakeMail struct{ sent chan mailer.Message }

func newFakeMail() *fakeMail { return &fakeMail{sent: make(chan mailer.Message, 10)} }

func (f *fakeMail) Send(_ context.Context, m mailer.Message) error { f.sent <- m; return nil }
func (f *fakeMail) Enabled() bool                                  { return true }

func (f *fakeMail) next(t *testing.T, to string) (mailer.Message, string) {
	t.Helper()
	select {
	case m := <-f.sent:
		if m.To != to {
			t.Fatalf("mail went to %q, expected %q", m.To, to)
		}
		token := regexp.MustCompile(`#token=([A-Za-z0-9_-]+)`).FindStringSubmatch(m.Body)
		if token == nil {
			t.Fatalf("no link in the mail: %q", m.Body)
		}
		return m, token[1]
	case <-time.After(5 * time.Second):
		t.Fatalf("no mail reached %s", to)
	}
	return mailer.Message{}, ""
}

func (f *fakeMail) none(t *testing.T) {
	t.Helper()
	select {
	case m := <-f.sent:
		t.Fatalf("no mail was expected, got one to %s", m.To)
	case <-time.After(500 * time.Millisecond):
	}
}

func signUp(t *testing.T, s *Server, email, password string) string {
	t.Helper()
	w := call(t, s.Handler(), "POST", "/api/auth/register", "", `{"email":"`+email+`","password":"`+password+`"}`)
	var body struct {
		Token string `json:"token"`
		User  struct {
			EmailVerified bool `json:"email_verified"`
		} `json:"user"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatalf("sign-up failed: %d %s", w.Code, w.Body.String())
	}
	if body.User.EmailVerified {
		t.Fatal("a new account starts unverified")
	}
	return body.Token
}

// While the server sends email, a new account gets a link, is held back from
// the routes that cost work or outlive a session until it opens it, and a
// link works once; a new link retires the earlier one.
func TestEmailVerificationAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	run := time.Now().Format("20060102150405.000000")
	sent := newFakeMail()
	s := &Server{Store: st, Secret: "verify-test-secret", TokenTTL: time.Hour, Registration: config.RegistrationOpen, FrontendURL: "https://jobs.example.test/", Mailer: sent}
	h := s.Handler()
	email := "verify-" + run + "@example.test"
	session := signUp(t, s, email, "verify-password")
	m, first := sent.next(t, email)
	if !strings.Contains(m.Body, "https://jobs.example.test/verify-email#token=") {
		t.Fatalf("the link should point at the web app's page: %q", m.Body)
	}

	w := call(t, h, "GET", "/api/account", session, ``)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"email_verified":false`) || !strings.Contains(w.Body.String(), `"verification_required":true`) {
		t.Fatalf("the account should say it needs verifying: %d %s", w.Code, w.Body.String())
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/tokens", `{"name":"held back","scopes":["read"]}`},
		{"POST", "/api/discovery/run", `{}`},
		{"POST", "/api/discovery/recommended", `{}`},
		{"POST", "/api/jobs/00000000-0000-0000-0000-000000000000/generate", `{}`},
		{"POST", "/api/jobs/00000000-0000-0000-0000-000000000000/compose", `{}`},
		{"POST", "/api/jobs/00000000-0000-0000-0000-000000000000/cv", `{}`},
		{"POST", "/api/jobs/00000000-0000-0000-0000-000000000000/answers", `{"questions":[{"text":"Cover letter"}]}`},
		// Someone who registered another person's address must not attach a
		// Google login that would survive the owner's password reset.
		{"POST", "/api/auth/google/link", `{"code":"any"}`},
	} {
		if w := call(t, h, c.method, c.path, session, c.body); w.Code != 403 || errorText(t, w) != needsVerifiedEmail {
			t.Errorf("%s %s should wait for a verified address: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}

	// A resend retires the first link.
	if w := call(t, h, "POST", "/api/account/verification", session, ``); w.Code != 202 {
		t.Fatalf("resending should be accepted: %d %s", w.Code, w.Body.String())
	}
	_, second := sent.next(t, email)
	if w := call(t, h, "POST", "/api/auth/password-reset/confirm", "", `{"token":"`+second+`","password":"taken-over"}`); w.Code != 400 {
		t.Fatalf("a verification link must not reset a password: %d", w.Code)
	}
	if w := call(t, h, "POST", "/api/auth/verify-email", "", `{"token":"`+first+`"}`); w.Code != 400 || errorText(t, w) != linkInvalid {
		t.Fatalf("an older link must stop working once a new one is sent: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/auth/verify-email", "", `{"token":"`+second+`"}`); w.Code != 200 {
		t.Fatalf("the latest link should verify: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/auth/verify-email", "", `{"token":"`+second+`"}`); w.Code != 400 {
		t.Fatalf("a link works once: %d", w.Code)
	}
	if w := call(t, h, "POST", "/api/tokens", session, `{"name":"after verifying","scopes":["read"]}`); w.Code != 201 {
		t.Fatalf("a verified account creates agent tokens: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/account/verification", session, ``); w.Code != 409 {
		t.Fatalf("a verified account has nothing to resend: %d", w.Code)
	}
	if u, _ := st.User(context.Background(), mustUserID(t, st, email)); !u.EmailVerified {
		t.Fatal("the account should now be verified")
	}

	// An expired link is refused like a used one.
	other := "verify-expired-" + run + "@example.test"
	signUp(t, s, other, "verify-password")
	sent.next(t, other)
	expired, err := st.CreateEmailToken(context.Background(), mustUserID(t, st, other), store.PurposeVerify, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(t, h, "POST", "/api/auth/verify-email", "", `{"token":"`+expired+`"}`); w.Code != 400 {
		t.Fatalf("an expired link should be refused: %d", w.Code)
	}
}

func mustUserID(t *testing.T, st *store.Store, email string) string {
	t.Helper()
	u, _, err := st.Credentials(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// Without a mail sender nothing is sent and nobody is held back, which is
// how CI and local runs work.
func TestNoMailMeansNoVerificationAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	run := time.Now().Format("20060102150405.000000")
	s := &Server{Store: st, Secret: "nomail-test-secret", TokenTTL: time.Hour, Registration: config.RegistrationOpen}
	h := s.Handler()
	session := signUp(t, s, "nomail-"+run+"@example.test", "nomail-password")
	if w := call(t, h, "GET", "/api/account", session, ``); !strings.Contains(w.Body.String(), `"verification_required":false`) {
		t.Fatalf("verification should not be required without mail: %s", w.Body.String())
	}
	if w := call(t, h, "POST", "/api/tokens", session, `{"name":"no mail","scopes":["read"]}`); w.Code != 201 {
		t.Fatalf("an unverified account is not held back without mail: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/account/verification", session, ``); w.Code != 409 {
		t.Fatalf("there is nothing to resend without mail: %d", w.Code)
	}
	if w := call(t, h, "POST", "/api/auth/password-reset", "", `{"email":"nomail-`+run+`@example.test"}`); w.Code != 202 {
		t.Fatalf("a reset request is answered the same without mail: %d", w.Code)
	}
}

// A reset request answers alike for a known and an unknown email and mails
// only the known one; the link sets a new password once, within the hour,
// and retires the old password and any other reset link.
func TestPasswordResetAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	run := time.Now().Format("20060102150405.000000")
	sent := newFakeMail()
	s := &Server{Store: st, Secret: "reset-test-secret", TokenTTL: time.Hour, Registration: config.RegistrationOpen, FrontendURL: "https://jobs.example.test", Mailer: sent}
	h := s.Handler()
	email := "reset-" + run + "@example.test"
	signUp(t, s, email, "old-password")
	sent.next(t, email) // the verification mail

	known := call(t, h, "POST", "/api/auth/password-reset", "", `{"email":"`+strings.ToUpper(email)+`"}`)
	unknown := call(t, h, "POST", "/api/auth/password-reset", "", `{"email":"nobody-`+run+`@example.test"}`)
	if known.Code != 202 || unknown.Code != 202 || known.Body.String() != unknown.Body.String() {
		t.Fatalf("known and unknown emails must be answered alike: %d %s / %d %s", known.Code, known.Body.String(), unknown.Code, unknown.Body.String())
	}
	m, first := sent.next(t, email)
	if !strings.Contains(m.Body, "https://jobs.example.test/reset-password#token=") {
		t.Fatalf("the link should point at the reset page: %q", m.Body)
	}
	sent.none(t) // nothing for the unknown email

	call(t, h, "POST", "/api/auth/password-reset", "", `{"email":"`+email+`"}`)
	_, second := sent.next(t, email)
	if w := call(t, h, "POST", "/api/auth/password-reset/confirm", "", `{"token":"`+first+`","password":"new-password"}`); w.Code != 400 {
		t.Fatalf("an earlier reset link stops working once a new one is sent: %d", w.Code)
	}
	if w := call(t, h, "POST", "/api/auth/password-reset/confirm", "", `{"token":"`+second+`","password":"short"}`); w.Code != 422 {
		t.Fatalf("a short password should be refused: %d", w.Code)
	}
	if w := call(t, h, "POST", "/api/auth/password-reset/confirm", "", `{"token":"`+second+`","password":"new-password"}`); w.Code != 200 {
		t.Fatalf("the link should set the new password: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/auth/password-reset/confirm", "", `{"token":"`+second+`","password":"newer-password"}`); w.Code != 400 {
		t.Fatalf("a reset link works once: %d", w.Code)
	}
	if w := call(t, h, "POST", "/api/auth/login", "", `{"email":"`+email+`","password":"old-password"}`); w.Code != 401 {
		t.Fatalf("the old password must stop working: %d", w.Code)
	}
	w := call(t, h, "POST", "/api/auth/login", "", `{"email":"`+email+`","password":"new-password"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"email_verified":true`) {
		t.Fatalf("the new password signs in, and opening the link verified the address: %d %s", w.Code, w.Body.String())
	}

	expired, err := st.CreateEmailToken(context.Background(), mustUserID(t, st, email), store.PurposeReset, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(t, h, "POST", "/api/auth/password-reset/confirm", "", `{"token":"`+expired+`","password":"newer-password"}`); w.Code != 400 {
		t.Fatalf("an expired reset link should be refused: %d", w.Code)
	}
	if w := call(t, h, "POST", "/api/auth/password-reset/confirm", "", `{"token":"`+expired+`x","password":"newer-password"}`); w.Code != 400 {
		t.Fatalf("an unknown reset link should be refused: %d", w.Code)
	}

	// The per-email limit on requests: three an hour.
	s.limits.resetIP = ratelimit.New(1000, time.Hour)
	call(t, h, "POST", "/api/auth/password-reset", "", `{"email":"`+email+`"}`)
	if w := call(t, h, "POST", "/api/auth/password-reset", "", `{"email":"`+email+`"}`); w.Code != 429 {
		t.Fatalf("the fourth request for one email in an hour should be limited: %d", w.Code)
	}
}
