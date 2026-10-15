package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/config"
	"jobpilot/core/api/internal/ratelimit"
)

func postFrom(t *testing.T, h http.Handler, path, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.10:5555"
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func errorText(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("not a JSON error: %q", w.Body.String())
	}
	return body.Error
}

// With sign-up closed, registering is refused before anything is read from
// the database or hashed, invite or not.
func TestClosedRegistrationRefusesEverySignUp(t *testing.T) {
	h := (&Server{Registration: config.RegistrationClosed}).Handler()
	for _, body := range []string{
		`{"email":"new@example.test","password":"long enough"}`,
		`{"email":"new@example.test","password":"long enough","invite":"jpi_anything"}`,
	} {
		w := postFrom(t, h, "/api/auth/register", body, nil)
		if w.Code != 403 || errorText(t, w) != signUpClosed {
			t.Fatalf("expected 403 %q, got %d %s", signUpClosed, w.Code, w.Body.String())
		}
	}
}

func TestRegistrationModeIsPublic(t *testing.T) {
	for set, want := range map[string]string{"": "invite", "open": "open", "closed": "closed"} {
		w := httptest.NewRecorder()
		(&Server{Registration: set}).Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/auth/registration", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"`+want+`"`) {
			t.Errorf("Registration=%q: got %d %s", set, w.Code, w.Body.String())
		}
	}
}

// A spent limit answers 429 with Retry-After and a message saying when,
// before the request reaches the database or bcrypt (the server here has no
// store, so reaching it would panic).
func TestSpentLimitsAnswer429WithRetryAfter(t *testing.T) {
	spent := ratelimit.New(0, 90*time.Second)
	open := ratelimit.New(100, time.Hour)
	for _, c := range []struct {
		name, path string
		limits     *authLimits
	}{
		{"login by address", "/api/auth/login", &authLimits{loginIP: spent, loginEmail: open, registerIP: open, registerEmail: open}},
		{"login by email", "/api/auth/login", &authLimits{loginIP: open, loginEmail: spent, registerIP: open, registerEmail: open}},
		{"sign-up by address", "/api/auth/register", &authLimits{loginIP: open, loginEmail: open, registerIP: spent, registerEmail: open}},
		{"sign-up by email", "/api/auth/register", &authLimits{loginIP: open, loginEmail: open, registerIP: open, registerEmail: spent}},
	} {
		s := &Server{limits: c.limits}
		w := postFrom(t, s.Handler(), c.path, `{"email":"someone@example.test","password":"long enough"}`, nil)
		if w.Code != 429 || w.Header().Get("Retry-After") != "90" || errorText(t, w) != "Too many attempts. Try again in 2 minutes." {
			t.Errorf("%s: got %d, Retry-After %q, %s", c.name, w.Code, w.Header().Get("Retry-After"), w.Body.String())
		}
	}
}

// The limits count the configured proxy header's address only when one is
// configured, so a client cannot pick its own address by sending the header.
func TestClientAddressComesFromTheConfiguredHeaderOnly(t *testing.T) {
	req := func(remote, header string) *http.Request {
		r := httptest.NewRequest("POST", "/api/auth/login", nil)
		r.RemoteAddr = remote
		if header != "" {
			r.Header.Set("CF-Connecting-IP", header)
		}
		return r
	}
	plain := &Server{}
	behindTunnel := &Server{ClientIPHeader: "CF-Connecting-IP"}
	for _, c := range []struct {
		s    *Server
		r    *http.Request
		want string
	}{
		{plain, req("192.0.2.10:5555", "203.0.113.7"), "192.0.2.10"},
		{behindTunnel, req("10.0.0.2:5555", "203.0.113.7"), "203.0.113.7"},
		{behindTunnel, req("10.0.0.2:5555", "not an address"), "10.0.0.2"},
		{behindTunnel, req("10.0.0.2:5555", "2001:db8:1:2:3:4:5:6"), "2001:db8:1:2::/64"},
		{plain, req("[2001:db8:1:2::9]:5555", ""), "2001:db8:1:2::/64"},
	} {
		if got := c.s.clientIP(c.r); got != c.want {
			t.Errorf("remote %s, header %q: got %q, want %q", c.r.RemoteAddr, c.r.Header.Get("CF-Connecting-IP"), got, c.want)
		}
	}
}

// Sign-up counts per address across emails and per email across addresses,
// with the real limits. Closed mode refuses after the limits, so no database
// is needed to watch them count.
func TestSignUpLimitsCountPerAddressAndPerEmail(t *testing.T) {
	body := func(email string) string { return `{"email":"` + email + `","password":"long enough"}` }
	from := func(ip string) map[string]string { return map[string]string{"CF-Connecting-IP": ip} }

	h := (&Server{Registration: config.RegistrationClosed, ClientIPHeader: "CF-Connecting-IP"}).Handler()
	for i := range 10 {
		if w := postFrom(t, h, "/api/auth/register", body(string(rune('a'+i))+"@example.test"), from("203.0.113.1")); w.Code != 403 {
			t.Fatalf("attempt %d from one address should reach the gate: %d", i+1, w.Code)
		}
	}
	if w := postFrom(t, h, "/api/auth/register", body("k@example.test"), from("203.0.113.1")); w.Code != 429 {
		t.Fatalf("the eleventh sign-up from one address in an hour should be limited: %d", w.Code)
	}
	if w := postFrom(t, h, "/api/auth/register", body("k@example.test"), from("203.0.113.2")); w.Code != 403 {
		t.Fatalf("another address has its own count: %d", w.Code)
	}

	h = (&Server{Registration: config.RegistrationClosed, ClientIPHeader: "CF-Connecting-IP"}).Handler()
	for i := range 5 {
		if w := postFrom(t, h, "/api/auth/register", body("Same@Example.test"), from("203.0.113."+string(rune('1'+i)))); w.Code != 403 {
			t.Fatalf("attempt %d for one email should reach the gate: %d", i+1, w.Code)
		}
	}
	if w := postFrom(t, h, "/api/auth/register", body("same@example.test"), from("203.0.113.9")); w.Code != 429 {
		t.Fatalf("the sixth sign-up for one email in an hour should be limited, whatever its case: %d", w.Code)
	}
}

// However long the email a caller sends, the limiter keeps a fixed-size key.
func TestEmailRateLimitKeysAreFixedSize(t *testing.T) {
	short, long := emailKey("a@example.test"), emailKey(strings.Repeat("a", 1<<20)+"@example.test")
	if len(short) != len(long) || len(long) > 80 || short == long {
		t.Fatalf("keys should be distinct digests of one size: %d and %d bytes", len(short), len(long))
	}
}
