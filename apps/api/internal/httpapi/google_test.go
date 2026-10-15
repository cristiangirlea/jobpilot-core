package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/auth/oidctest"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/store"
)

const (
	googleClient   = "jobpilot-test-client"
	googleSecret   = "google-test-secret"
	googleFrontend = "http://web.example.test"
)

func googleServer(t *testing.T, p *oidctest.Provider, st *store.Store, signup bool) http.Handler {
	t.Helper()
	o, err := auth.NewOIDC(context.Background(), auth.OIDCConfig{
		Provider:  "google",
		Endpoints: auth.OIDCEndpoints{Issuer: p.URL, AuthURL: p.URL + "/auth", TokenURL: p.URL + "/token", JWKSURL: p.URL + "/jwks"},
		ClientID:  googleClient, ClientSecret: "s", RedirectURL: "http://api.example.test/api/auth/google/callback", Secret: googleSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	return (&Server{Store: st, Google: o, GoogleSignup: signup, Secret: googleSecret, TokenTTL: time.Hour, FrontendURL: googleFrontend}).Handler()
}

func get(h http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// With Google unset, which is how CI and a default install run, the login
// page is told there is no Google button and the Google routes answer 404.
func TestGoogleSignInIsOffWhenUnconfigured(t *testing.T) {
	h := (&Server{FrontendURL: googleFrontend}).Handler()
	rec := get(h, "/api/auth/providers")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"google":false}` {
		t.Fatalf("providers = %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/auth/google/start", "/api/auth/google/callback?code=x&state=y"} {
		if rec := get(h, path); rec.Code != 404 {
			t.Errorf("%s answered %d with Google off", path, rec.Code)
		}
	}
}

func TestGoogleCallbackWithAForgedStateGoesBackToLogin(t *testing.T) {
	p := oidctest.New(t)
	h := googleServer(t, p, nil, false)
	if rec := get(h, "/api/auth/providers"); strings.TrimSpace(rec.Body.String()) != `{"google":true}` {
		t.Fatalf("providers = %s", rec.Body)
	}
	start := get(h, "/api/auth/google/start")
	rec := get(h, "/api/auth/google/callback?code="+oidctest.Code+"&state=forged", start.Result().Cookies()...)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != googleFrontend+"/login?error=google" {
		t.Fatalf("callback = %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if p.Exchanges() != 0 {
		t.Fatal("the code was exchanged for a forged state")
	}
}

func TestGoogleExchangeRefusesAnUnknownCode(t *testing.T) {
	h := (&Server{FrontendURL: googleFrontend}).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/google/exchange", strings.NewReader(`{"code":"made-up"}`)))
	if rec.Code != 401 {
		t.Fatalf("exchange = %d %s", rec.Code, rec.Body)
	}
}

// Only the two routes that read the binding cookie answer credentialed
// cross-origin requests.
func TestOnlyHandoffRoutesAllowCredentials(t *testing.T) {
	h := (&Server{FrontendURL: googleFrontend}).Handler()
	for path, want := range map[string]string{"/api/auth/google/exchange": "true", "/api/auth/google/link": "true", "/api/auth/login": "", "/api/profile": ""} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("OPTIONS", path, nil))
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != want {
			t.Errorf("%s: Allow-Credentials = %q, want %q", path, got, want)
		}
	}
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && c.MaxAge > 0 {
			return c
		}
	}
	return nil
}

func post(h http.Handler, path, body, bearer string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, c := range cookies {
		if c != nil {
			r.AddCookie(c)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// The whole flow on a migrated database, through the fake provider:
//   - an account registered with a password is not joined by a Google
//     sign-in with its email (someone else may have registered it);
//   - its owner links Google from the signed-in account, and then signs in
//     with Google;
//   - a handoff code works only in the browser that finished the flow, so a
//     code sent to someone else cannot sign them into the sender's account;
//   - the code works once and the token reads that user's own profile;
//   - someone with no account is sent back to login while sign-up is off.
func TestGoogleSignInAgainstPostgres(t *testing.T) {
	dbURL := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated database to run this")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := store.New(pool)
	run := time.Now().Format("20060102150405.000000")
	email := "google-" + run + "@example.test"
	user, err := st.CreateUser(ctx, email, "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	p := oidctest.New(t)
	h := googleServer(t, p, st, false)

	// flow runs the browser through the provider and back, and returns the
	// callback's response.
	flow := func(startPath, subject, email string) *httptest.ResponseRecorder {
		t.Helper()
		start := get(h, startPath)
		loc, _ := url.Parse(start.Header().Get("Location"))
		q := loc.Query()
		p.Expect(q.Get("code_challenge"), map[string]any{"iss": p.URL, "aud": googleClient, "sub": subject, "email": email, "email_verified": true, "nonce": q.Get("nonce"), "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()})
		return get(h, "/api/auth/google/callback?code="+oidctest.Code+"&state="+url.QueryEscape(q.Get("state")), start.Result().Cookies()...)
	}
	codeOf := func(rec *httptest.ResponseRecorder, wantPrefix string) string {
		t.Helper()
		loc, _ := url.Parse(rec.Header().Get("Location"))
		if rec.Code != http.StatusFound || !strings.HasPrefix(loc.String(), wantPrefix) || loc.Query().Get("code") == "" {
			t.Fatalf("callback = %d to %q, want %s...", rec.Code, loc, wantPrefix)
		}
		if strings.Contains(loc.String(), "token") || strings.Contains(loc.String(), "@") {
			t.Fatalf("the redirect carries a token or an email: %q", loc)
		}
		return loc.Query().Get("code")
	}

	if rec := flow("/api/auth/google/start", "sub-"+run, email); rec.Header().Get("Location") != googleFrontend+"/login?error=google_link" {
		t.Fatalf("a Google sign-in joined an existing account by email: %q", rec.Header().Get("Location"))
	}

	session, _ := auth.Token(user.ID, user.Email, googleSecret, time.Hour)
	link := flow("/api/auth/google/start?link=1", "sub-"+run, email)
	linkCode := codeOf(link, googleFrontend+"/login/google?")
	if !strings.Contains(link.Header().Get("Location"), "link=1") {
		t.Fatalf("a link flow came back as a sign-in: %q", link.Header().Get("Location"))
	}
	if rec := post(h, "/api/auth/google/link", `{"code":"`+linkCode+`"}`, session); rec.Code != 401 {
		t.Fatalf("a link code worked without its binding cookie: %d", rec.Code)
	}
	if rec := post(h, "/api/auth/google/link", `{"code":"`+linkCode+`"}`, "", cookieNamed(link, "jobpilot_oidc_bind")); rec.Code != 401 {
		t.Fatalf("linking without a session = %d", rec.Code)
	}
	if rec := post(h, "/api/auth/google/link", `{"code":"`+linkCode+`"}`, session, cookieNamed(link, "jobpilot_oidc_bind")); rec.Code != 200 {
		t.Fatalf("linking from the signed-in account = %d %s", rec.Code, rec.Body)
	}

	signIn := flow("/api/auth/google/start", "sub-"+run, email)
	code := codeOf(signIn, googleFrontend+"/login/google?code=")
	if rec := post(h, "/api/auth/google/exchange", `{"code":"`+code+`"}`, ""); rec.Code != 401 {
		t.Fatalf("a code was exchanged in a browser without its binding (login CSRF): %d", rec.Code)
	}
	first := post(h, "/api/auth/google/exchange", `{"code":"`+code+`"}`, "", cookieNamed(signIn, "jobpilot_oidc_bind"))
	var body struct{ Token string }
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &body) != nil || body.Token == "" {
		t.Fatalf("exchange in the right browser = %d %s", first.Code, first.Body)
	}
	if id, err := auth.Parse(body.Token, googleSecret); err != nil || id != user.ID {
		t.Fatalf("the token is for %q (%v), want %s", id, err, user.ID)
	}
	if again := post(h, "/api/auth/google/exchange", `{"code":"`+code+`"}`, "", cookieNamed(signIn, "jobpilot_oidc_bind")); again.Code != 401 {
		t.Fatalf("a code was exchanged twice: %d", again.Code)
	}
	r := httptest.NewRequest("GET", "/api/profile", nil)
	r.Header.Set("Authorization", "Bearer "+body.Token)
	profile := httptest.NewRecorder()
	h.ServeHTTP(profile, r)
	if profile.Code != 200 || !strings.Contains(profile.Body.String(), email) {
		t.Fatalf("profile with the Google session = %d %s", profile.Code, profile.Body)
	}

	stranger := flow("/api/auth/google/start", "sub-stranger-"+run, "stranger-"+run+"@example.test")
	if stranger.Header().Get("Location") != googleFrontend+"/login?error=google_no_account" {
		t.Fatalf("someone without an account was sent to %q", stranger.Header().Get("Location"))
	}
}
