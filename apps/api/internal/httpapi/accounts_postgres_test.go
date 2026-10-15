package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/config"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/ratelimit"
	"jobpilot/core/api/internal/store"
)

func accountsStore(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated database to run this")
	}
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return store.New(pool)
}

func call(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.20:5555"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func registerBody(email, invite string) string {
	b, _ := json.Marshal(map[string]string{"email": email, "password": "invited-password", "invite": invite})
	return string(b)
}

// Invite-only sign-up end to end: only an admin's browser session manages
// invites; a code creates one account, once; a refused sign-up (the email is
// taken) leaves the code usable; missing, unknown, used and expired codes all
// get the same refusal.
func TestInviteOnlySignUpAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	run := time.Now().Format("20060102150405.000000")
	adminEmail := "admin-" + run + "@example.test"
	admin, err := st.CreateUser(ctx, adminEmail, "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "member-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(ctx, strings.ToUpper(adminEmail), "x"); err != store.ErrEmailTaken {
		t.Fatalf("a second account with the same email should be ErrEmailTaken, got %v", err)
	}
	const secret = "invite-test-secret"
	s := &Server{Store: st, Secret: secret, TokenTTL: time.Hour, Registration: config.RegistrationInvite, AdminEmails: []string{adminEmail}}
	h := s.Handler()
	adminSession, _ := auth.Token(admin.ID, adminEmail, secret, time.Hour)
	memberSession, _ := auth.Token(member.ID, "member@example.test", secret, time.Hour)
	agent, _, err := st.CreateAgentToken(ctx, admin.ID, "invite-test", []string{auth.ScopeRead, auth.ScopeWrite}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if w := call(t, h, "POST", "/api/invites", memberSession, `{}`); w.Code != 403 || errorText(t, w) != invitesNeedAdmin {
		t.Fatalf("a member who is not an admin must not create invites: %d %s", w.Code, w.Body.String())
	}
	for _, method := range []string{"GET", "POST", "DELETE"} {
		path := "/api/invites"
		if method == "DELETE" {
			path += "/00000000-0000-0000-0000-000000000000"
		}
		if w := call(t, h, method, path, agent, `{}`); w.Code != 403 {
			t.Fatalf("an agent token must not %s invites, even an admin's: %d", method, w.Code)
		}
	}
	if w := call(t, h, "POST", "/api/invites", adminSession, `{"expires_in_days":91}`); w.Code != 422 {
		t.Fatalf("an expiry past the maximum should be refused: %d", w.Code)
	}
	create := func(note string) string {
		w := call(t, h, "POST", "/api/invites", adminSession, `{"note":"`+note+`"}`)
		var made struct {
			Code   string `json:"code"`
			Invite struct {
				ID         string    `json:"id"`
				Note       string    `json:"note"`
				CodePrefix string    `json:"code_prefix"`
				ExpiresAt  time.Time `json:"expires_at"`
			} `json:"invite"`
		}
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &made) != nil {
			t.Fatalf("the admin should create an invite: %d %s", w.Code, w.Body.String())
		}
		if !strings.HasPrefix(made.Code, store.InvitePrefix) || !strings.Contains(made.Code, made.Invite.CodePrefix) || made.Invite.Note != note {
			t.Fatalf("unexpected invite: %+v", made)
		}
		if days := time.Until(made.Invite.ExpiresAt).Hours() / 24; days < 13.9 || days > 14.1 {
			t.Fatalf("an invite should last 14 days by default, got %.1f", days)
		}
		return made.Code
	}
	code := create("for Ana Testova")

	refused := func(name, body string) {
		t.Helper()
		w := call(t, h, "POST", "/api/auth/register", "", body)
		if w.Code != 403 || errorText(t, w) != signUpNeedsInvite {
			t.Fatalf("%s: expected 403 %q, got %d %s", name, signUpNeedsInvite, w.Code, w.Body.String())
		}
	}
	newEmail := "invited-" + run + "@example.test"
	refused("no invite", registerBody(newEmail, ""))
	refused("unknown invite", registerBody(newEmail, store.InvitePrefix+"unknown"))

	// The email is taken: refused, and the invite is not spent.
	if w := call(t, h, "POST", "/api/auth/register", "", registerBody(adminEmail, code)); w.Code != 409 {
		t.Fatalf("an existing email should be refused with 409: %d %s", w.Code, w.Body.String())
	}
	w := call(t, h, "POST", "/api/auth/register", "", registerBody(newEmail, " "+code+" "))
	if w.Code != 201 {
		t.Fatalf("a valid invite should create the account: %d %s", w.Code, w.Body.String())
	}
	refused("used invite", registerBody("other-"+newEmail, code))
	if w := call(t, h, "POST", "/api/auth/login", "", `{"email":"`+strings.ToUpper(newEmail)+`","password":"invited-password"}`); w.Code != 200 {
		t.Fatalf("the invited account should sign in: %d %s", w.Code, w.Body.String())
	}

	// Listed for the admin, marked used; a used invite cannot be deleted.
	var listed []struct {
		ID     string     `json:"id"`
		UsedAt *time.Time `json:"used_at"`
	}
	w = call(t, h, "GET", "/api/invites", adminSession, ``)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0].UsedAt == nil {
		t.Fatalf("the admin should see one used invite: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "DELETE", "/api/invites/"+listed[0].ID, adminSession, ``); w.Code != 404 {
		t.Fatalf("a used invite stays as the record of who was let in: %d", w.Code)
	}

	// An unused invite can be withdrawn by its creator only, and then no
	// longer works.
	withdrawn := create("withdrawn")
	invites, _ := st.Invites(ctx, admin.ID)
	if err := st.DeleteInvite(ctx, member.ID, invites[0].ID); err == nil {
		t.Fatal("another account must not withdraw an admin's invite")
	}
	if w := call(t, h, "DELETE", "/api/invites/"+invites[0].ID, adminSession, ``); w.Code != 204 {
		t.Fatalf("an unused invite should be deletable: %d %s", w.Code, w.Body.String())
	}
	refused("withdrawn invite", registerBody("withdrawn-"+newEmail, withdrawn))

	// An expired invite is refused like any other.
	expired, _, err := st.CreateInvite(ctx, admin.ID, "expired", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	refused("expired invite", registerBody("expired-"+newEmail, expired))

	// Another admin's member cannot see this admin's invites: the list is
	// scoped to whoever created them.
	if mine, _ := st.Invites(ctx, member.ID); len(mine) != 0 {
		t.Fatalf("invites must be listed per creator, got %d", len(mine))
	}
}

// Two sign-ups racing for one code: exactly one account is created.
func TestInviteIsClaimedOnceUnderARaceAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	run := time.Now().Format("20060102150405.000000")
	admin, err := st.CreateUser(ctx, "race-admin-"+run+"@example.test", "x")
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := st.CreateInvite(ctx, admin.ID, "", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 4)
	for i := range 4 {
		go func() {
			_, err := st.CreateUserWithInvite(ctx, "race-"+string(rune('a'+i))+"-"+run+"@example.test", "x", code)
			results <- err
		}()
	}
	won := 0
	for range 4 {
		switch err := <-results; err {
		case nil:
			won++
		case store.ErrInviteInvalid:
		default:
			t.Fatal(err)
		}
	}
	if won != 1 {
		t.Fatalf("exactly one sign-up should claim the code, %d did", won)
	}
}

// Login counts per address across emails, before reaching the database.
func TestLoginLimitCountsPerAddressAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	h := (&Server{Store: st, Secret: "ip-test-secret", TokenTTL: time.Hour}).Handler()
	for i := range 30 {
		w := call(t, h, "POST", "/api/auth/login", "", `{"email":"nobody-`+string(rune('a'+i%26))+`@example.test","password":"wrong-password"}`)
		if w.Code != 401 {
			t.Fatalf("attempt %d from one address should be answered: %d", i+1, w.Code)
		}
	}
	if w := call(t, h, "POST", "/api/auth/login", "", `{"email":"other@example.test","password":"wrong-password"}`); w.Code != 429 {
		t.Fatalf("the 31st login from one address in 15 minutes should be limited: %d", w.Code)
	}
}

// Open sign-up needs no invite; an unknown email and a wrong password get
// the same 401; the per-email limit counts failures and a success resets it.
func TestOpenSignUpAndLoginLimitsAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	run := time.Now().Format("20060102150405.000000")
	s := &Server{Store: st, Secret: "open-test-secret", TokenTTL: time.Hour, Registration: config.RegistrationOpen}
	h := s.Handler()
	// This test is about the per-email limit; the per-address one is
	// covered without a database.
	s.limits.loginIP = ratelimit.New(1000, time.Hour)
	email := "open-" + run + "@example.test"
	if w := call(t, h, "POST", "/api/auth/register", "", registerBody(email, "")); w.Code != 201 {
		t.Fatalf("open sign-up should not need an invite: %d %s", w.Code, w.Body.String())
	}

	wrong := call(t, h, "POST", "/api/auth/login", "", `{"email":"`+email+`","password":"wrong-password"}`)
	unknown := call(t, h, "POST", "/api/auth/login", "", `{"email":"nobody-`+run+`@example.test","password":"wrong-password"}`)
	if wrong.Code != 401 || unknown.Code != 401 || wrong.Body.String() != unknown.Body.String() {
		t.Fatalf("an unknown email and a wrong password must answer alike: %d %s / %d %s", wrong.Code, wrong.Body.String(), unknown.Code, unknown.Body.String())
	}

	// Nine more failures spend the per-email limit of ten.
	for range 9 {
		call(t, h, "POST", "/api/auth/login", "", `{"email":"`+email+`","password":"wrong-password"}`)
	}
	if w := call(t, h, "POST", "/api/auth/login", "", `{"email":"`+email+`","password":"invited-password"}`); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("the eleventh attempt for one email should be limited, even with the right password: %d", w.Code)
	}
	// A fresh window: a success resets the email's count.
	s.limits.loginEmail.Reset(emailKey(email))
	for range 9 {
		call(t, h, "POST", "/api/auth/login", "", `{"email":"`+email+`","password":"wrong-password"}`)
	}
	if w := call(t, h, "POST", "/api/auth/login", "", `{"email":"`+email+`","password":"invited-password"}`); w.Code != 200 {
		t.Fatalf("the tenth attempt with the right password should pass: %d", w.Code)
	}
	for range 9 {
		call(t, h, "POST", "/api/auth/login", "", `{"email":"`+email+`","password":"wrong-password"}`)
	}
	if w := call(t, h, "POST", "/api/auth/login", "", `{"email":"`+email+`","password":"invited-password"}`); w.Code != 200 {
		t.Fatalf("after a success the email's earlier failures no longer count: %d", w.Code)
	}
}
