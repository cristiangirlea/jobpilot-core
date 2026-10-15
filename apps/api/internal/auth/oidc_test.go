package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth/oidctest"
)

const testClient = "client-123"

func endpoints(p *oidctest.Provider) OIDCEndpoints {
	return OIDCEndpoints{Issuer: p.URL, AuthURL: p.URL + "/auth", TokenURL: p.URL + "/token", JWKSURL: p.URL + "/jwks"}
}

func newTestOIDC(t *testing.T, p *oidctest.Provider) *OIDC {
	t.Helper()
	o, err := NewOIDC(context.Background(), OIDCConfig{Provider: "google", Endpoints: endpoints(p), ClientID: testClient, ClientSecret: "s", RedirectURL: "https://api.example.test/api/auth/google/callback", Secret: "test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// start runs Start and returns the flow cookie and the parameters the browser
// was sent to the provider with.
func start(t *testing.T, o *OIDC, p *oidctest.Provider) (*http.Cookie, url.Values) {
	t.Helper()
	rec := httptest.NewRecorder()
	o.Start(rec, httptest.NewRequest("GET", "/api/auth/google/start", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("start answered %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || !strings.HasPrefix(loc.String(), p.URL+"/auth") {
		t.Fatalf("start redirected to %q", rec.Header().Get("Location"))
	}
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("client_id") != testClient || q.Get("state") == "" || q.Get("nonce") == "" {
		t.Fatalf("authorization request lacks PKCE, state or nonce: %v", q)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != flowCookie || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("flow cookie is not an HttpOnly, Secure, SameSite=Lax cookie: %+v", cookies)
	}
	return cookies[0], q
}

func callback(o *OIDC, cookie *http.Cookie, query string) (Identity, *httptest.ResponseRecorder, error) {
	r := httptest.NewRequest("GET", "/api/auth/google/callback?"+query, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	id, _, err := o.Callback(rec, r)
	return id, rec, err
}

func goodClaims(p *oidctest.Provider, nonce string) map[string]any {
	return map[string]any{"iss": p.URL, "aud": testClient, "sub": "google-sub-1", "email": "Ada@Example.test", "email_verified": true, "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
}

func TestOIDCSignInReturnsTheVerifiedIdentity(t *testing.T) {
	p := oidctest.New(t)
	o := newTestOIDC(t, p)
	cookie, q := start(t, o, p)
	p.Expect(q.Get("code_challenge"), goodClaims(p, q.Get("nonce")))
	id, rec, err := callback(o, cookie, "code="+oidctest.Code+"&state="+url.QueryEscape(q.Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	if id != (Identity{Provider: "google", Subject: "google-sub-1", Email: "ada@example.test"}) {
		t.Fatalf("identity = %+v", id)
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Fatalf("the flow cookie was not cleared: %+v", c)
	}
}

func TestOIDCCallbackRefusesBadStateAndCookies(t *testing.T) {
	p := oidctest.New(t)
	o := newTestOIDC(t, p)
	cookie, q := start(t, o, p)
	p.Expect(q.Get("code_challenge"), goodClaims(p, q.Get("nonce")))
	state := url.QueryEscape(q.Get("state"))
	tampered := *cookie
	tampered.Value = "x" + cookie.Value
	other := newTestOIDC(t, p)
	other.key = []byte("another secret")
	for name, tc := range map[string]struct {
		o      *OIDC
		cookie *http.Cookie
		query  string
	}{
		"no cookie":           {o, nil, "code=" + oidctest.Code + "&state=" + state},
		"wrong state":         {o, cookie, "code=" + oidctest.Code + "&state=other"},
		"missing state":       {o, cookie, "code=" + oidctest.Code},
		"tampered cookie":     {o, &tampered, "code=" + oidctest.Code + "&state=" + state},
		"cookie from another": {other, cookie, "code=" + oidctest.Code + "&state=" + state},
		"provider error":      {o, cookie, "error=access_denied&state=" + state},
		"missing code":        {o, cookie, "state=" + state},
	} {
		t.Run(name, func(t *testing.T) {
			before := p.Exchanges()
			if _, _, err := callback(tc.o, tc.cookie, tc.query); !errors.Is(err, ErrSignIn) {
				t.Fatalf("err = %v, want ErrSignIn", err)
			}
			if p.Exchanges() != before {
				t.Fatal("the code was exchanged before the state was checked")
			}
		})
	}
}

func TestOIDCCallbackRefusesAnExpiredFlow(t *testing.T) {
	p := oidctest.New(t)
	o := newTestOIDC(t, p)
	cookie, q := start(t, o, p)
	p.Expect(q.Get("code_challenge"), goodClaims(p, q.Get("nonce")))
	o.now = func() time.Time { return time.Now().Add(flowTTL + time.Minute) }
	if _, _, err := callback(o, cookie, "code="+oidctest.Code+"&state="+url.QueryEscape(q.Get("state"))); !errors.Is(err, ErrSignIn) {
		t.Fatalf("err = %v, want ErrSignIn", err)
	}
}

func TestOIDCCallbackRefusesAWrongPKCEVerifier(t *testing.T) {
	p := oidctest.New(t)
	o := newTestOIDC(t, p)
	cookie, q := start(t, o, p)
	p.Expect("a challenge from another sign-in", goodClaims(p, q.Get("nonce")))
	if _, _, err := callback(o, cookie, "code="+oidctest.Code+"&state="+url.QueryEscape(q.Get("state"))); !errors.Is(err, ErrSignIn) {
		t.Fatalf("err = %v, want ErrSignIn", err)
	}
}

func TestOIDCIDTokenChecks(t *testing.T) {
	p := oidctest.New(t)
	o := newTestOIDC(t, p)
	cookie, q := start(t, o, p)
	state := url.QueryEscape(q.Get("state"))
	for name, change := range map[string]func(map[string]any){
		"wrong issuer":          func(c map[string]any) { c["iss"] = "https://evil.example.test" },
		"wrong audience":        func(c map[string]any) { c["aud"] = "someone-else" },
		"expired":               func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
		"wrong nonce":           func(c map[string]any) { c["nonce"] = "replayed" },
		"no nonce":              func(c map[string]any) { delete(c, "nonce") },
		"email not verified":    func(c map[string]any) { c["email_verified"] = false },
		"email_verified absent": func(c map[string]any) { delete(c, "email_verified") },
		"no email":              func(c map[string]any) { delete(c, "email") },
		"no subject":            func(c map[string]any) { delete(c, "sub") },
	} {
		t.Run(name, func(t *testing.T) {
			claims := goodClaims(p, q.Get("nonce"))
			change(claims)
			p.Expect(q.Get("code_challenge"), claims)
			if id, _, err := callback(o, cookie, "code="+oidctest.Code+"&state="+state); !errors.Is(err, ErrSignIn) {
				t.Fatalf("accepted %+v (err %v)", id, err)
			}
		})
	}
	t.Run("signed by another key", func(t *testing.T) {
		real := p.Key
		p.Key, _ = rsa.GenerateKey(rand.Reader, 2048)
		forged := p.Sign(goodClaims(p, q.Get("nonce")))
		p.Key = real
		if _, err := o.verify(context.Background(), forged, q.Get("nonce")); !errors.Is(err, ErrSignIn) {
			t.Fatalf("a token signed by another key was accepted: %v", err)
		}
	})
	t.Run("no ID token in the response", func(t *testing.T) {
		p.Expect(q.Get("code_challenge"), goodClaims(p, q.Get("nonce")))
		p.OmitIDToken(true)
		defer p.OmitIDToken(false)
		if _, _, err := callback(o, cookie, "code="+oidctest.Code+"&state="+state); !errors.Is(err, ErrSignIn) {
			t.Fatalf("err = %v, want ErrSignIn", err)
		}
	})
	t.Run("string email_verified", func(t *testing.T) {
		claims := goodClaims(p, q.Get("nonce"))
		claims["email_verified"] = "true"
		p.Expect(q.Get("code_challenge"), claims)
		if _, _, err := callback(o, cookie, "code="+oidctest.Code+"&state="+state); err != nil {
			t.Fatal(err)
		}
	})
}

func TestFlowCookieIsNotASessionToken(t *testing.T) {
	p := oidctest.New(t)
	o := newTestOIDC(t, p)
	cookie, _ := start(t, o, p)
	if _, err := Parse(cookie.Value, "test-secret"); err == nil {
		t.Fatal("a flow cookie parsed as a session token")
	}
}

func TestNewOIDCNeedsEveryValue(t *testing.T) {
	if _, err := NewOIDC(context.Background(), OIDCConfig{Endpoints: Google, ClientID: "id", RedirectURL: "https://x.test/cb", Secret: "s"}); err == nil {
		t.Fatal("a configuration without a client secret was accepted")
	}
}

// A flow started with ?link=1 says so at the callback, and only then.
func TestOIDCLinkFlowIsCarriedInTheCookie(t *testing.T) {
	p := oidctest.New(t)
	o := newTestOIDC(t, p)
	for _, link := range []bool{false, true} {
		rec := httptest.NewRecorder()
		path := "/api/auth/google/start"
		if link {
			path += "?link=1"
		}
		o.Start(rec, httptest.NewRequest("GET", path, nil))
		loc, _ := url.Parse(rec.Header().Get("Location"))
		q := loc.Query()
		p.Expect(q.Get("code_challenge"), goodClaims(p, q.Get("nonce")))
		r := httptest.NewRequest("GET", "/api/auth/google/callback?code="+oidctest.Code+"&state="+url.QueryEscape(q.Get("state")), nil)
		r.AddCookie(rec.Result().Cookies()[0])
		_, got, err := o.Callback(httptest.NewRecorder(), r)
		if err != nil || got != link {
			t.Fatalf("started with link=%v, the callback says %v (%v)", link, got, err)
		}
	}
}

func TestHandoffCodesNeedTheirBindingWorkOnceAndExpire(t *testing.T) {
	h := NewHandoff()
	code, binding := h.Issue(Grant{Token: "tok"})
	if _, ok := h.Redeem(code, ""); ok {
		t.Fatal("a code was redeemed without its binding")
	}
	if _, ok := h.Redeem(code, "another browser's binding"); ok {
		t.Fatal("a code was redeemed with another binding")
	}
	if g, ok := h.Redeem(code, binding); !ok || g.Token != "tok" {
		t.Fatalf("redeem with the binding = %+v %v; a wrong binding must not use the code up", g, ok)
	}
	if _, ok := h.Redeem(code, binding); ok {
		t.Fatal("a code was redeemed twice")
	}
	if _, ok := h.Redeem("never-issued", binding); ok {
		t.Fatal("an unknown code was redeemed")
	}
	late, lateBinding := h.Issue(Grant{Token: "tok"})
	h.now = func() time.Time { return time.Now().Add(2 * handoffTTL) }
	if _, ok := h.Redeem(late, lateBinding); ok {
		t.Fatal("an expired code was redeemed")
	}
}
