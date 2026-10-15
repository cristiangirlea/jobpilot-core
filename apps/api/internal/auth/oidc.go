package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Google's OpenID Connect endpoints, from
// https://accounts.google.com/.well-known/openid-configuration. They are set
// here rather than discovered so that startup makes no network call.
var Google = OIDCEndpoints{
	Issuer:   "https://accounts.google.com",
	AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL: "https://oauth2.googleapis.com/token",
	JWKSURL:  "https://www.googleapis.com/oauth2/v3/certs",
}

type OIDCEndpoints struct{ Issuer, AuthURL, TokenURL, JWKSURL string }

// OIDCConfig configures one OpenID Connect provider. Secret signs the cookie
// that carries a sign-in's state, nonce and PKCE verifier between the start
// and the callback.
type OIDCConfig struct {
	Provider     string
	Endpoints    OIDCEndpoints
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Secret       string
}

// Identity is what a provider vouches for: who the person is to it (Subject)
// and an email address it has verified.
type Identity struct {
	Provider string
	Subject  string
	Email    string
}

// OIDC runs the authorization-code flow with PKCE, state and nonce, and
// verifies the ID token (signature, issuer, audience, expiry) before it trusts
// any claim in it.
type OIDC struct {
	provider string
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	key      []byte
	secure   bool
	now      func() time.Time
}

const (
	flowCookie = "jobpilot_oidc"
	flowTTL    = 10 * time.Minute
	// bindCookie ties a handoff code to the browser that finished the flow.
	bindCookie = "jobpilot_oidc_bind"
)

var ErrSignIn = errors.New("the sign-in could not be completed")

func NewOIDC(ctx context.Context, cfg OIDCConfig) (*OIDC, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" || cfg.Secret == "" {
		return nil, errors.New("a client id, client secret, redirect URL and secret are all required")
	}
	provider := (&oidc.ProviderConfig{
		IssuerURL:  cfg.Endpoints.Issuer,
		AuthURL:    cfg.Endpoints.AuthURL,
		TokenURL:   cfg.Endpoints.TokenURL,
		JWKSURL:    cfg.Endpoints.JWKSURL,
		Algorithms: []string{oidc.RS256},
	}).NewProvider(ctx)
	// A key of its own, so a flow cookie can never pass for a session token
	// signed with the same secret.
	mac := hmac.New(sha256.New, []byte(cfg.Secret))
	mac.Write([]byte("oidc-flow"))
	return &OIDC{
		provider: cfg.Provider,
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		key:      mac.Sum(nil),
		secure:   strings.HasPrefix(cfg.RedirectURL, "https://"),
		now:      time.Now,
	}, nil
}

type flow struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Expires  int64  `json:"exp"`
	Link     bool   `json:"link,omitempty"`
}

// Start sets the flow cookie and sends the browser to the provider. With
// ?link=1 the flow links the identity to the signed-in account instead of
// signing in.
func (o *OIDC) Start(w http.ResponseWriter, r *http.Request) {
	f := flow{State: random(), Nonce: random(), Verifier: oauth2.GenerateVerifier(), Expires: o.now().Add(flowTTL).Unix(), Link: r.URL.Query().Get("link") == "1"}
	http.SetCookie(w, o.cookie(flowCookie, o.seal(f), int(flowTTL/time.Second)))
	url := o.oauth.AuthCodeURL(f.State, oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier), oauth2.SetAuthURLParam("prompt", "select_account"))
	http.Redirect(w, r, url, http.StatusFound)
}

// Callback finishes the flow the cookie belongs to and returns the verified
// identity, and whether the flow was started to link it. The cookie is cleared
// whatever the outcome, so a state is used once.
func (o *OIDC) Callback(w http.ResponseWriter, r *http.Request) (Identity, bool, error) {
	http.SetCookie(w, o.cookie(flowCookie, "", -1))
	c, err := r.Cookie(flowCookie)
	if err != nil {
		return Identity{}, false, fmt.Errorf("%w: no sign-in in progress", ErrSignIn)
	}
	f, ok := o.open(c.Value)
	if !ok || o.now().Unix() > f.Expires {
		return Identity{}, false, fmt.Errorf("%w: the sign-in expired or was altered", ErrSignIn)
	}
	id, err := o.finish(r, f)
	return id, f.Link, err
}

func (o *OIDC) finish(r *http.Request, f flow) (Identity, error) {
	q := r.URL.Query()
	if q.Get("error") != "" {
		return Identity{}, fmt.Errorf("%w: the provider refused (%s)", ErrSignIn, q.Get("error"))
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(f.State)) != 1 {
		return Identity{}, fmt.Errorf("%w: state mismatch", ErrSignIn)
	}
	code := q.Get("code")
	if code == "" {
		return Identity{}, fmt.Errorf("%w: no authorization code", ErrSignIn)
	}
	tok, err := o.oauth.Exchange(r.Context(), code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("%w: code exchange: %v", ErrSignIn, err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return Identity{}, fmt.Errorf("%w: no ID token", ErrSignIn)
	}
	return o.verify(r.Context(), raw, f.Nonce)
}

func (o *OIDC) verify(ctx context.Context, raw, nonce string) (Identity, error) {
	idt, err := o.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: ID token: %v", ErrSignIn, err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(nonce)) != 1 {
		return Identity{}, fmt.Errorf("%w: nonce mismatch", ErrSignIn)
	}
	var claims struct {
		Email    string `json:"email"`
		Verified any    `json:"email_verified"`
	}
	if err := idt.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("%w: claims: %v", ErrSignIn, err)
	}
	// Providers send a boolean; some older ones send the string "true".
	verified := claims.Verified == true || claims.Verified == "true"
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if !verified || email == "" || idt.Subject == "" {
		return Identity{}, fmt.Errorf("%w: the provider has not verified the email address", ErrSignIn)
	}
	return Identity{Provider: o.provider, Subject: idt.Subject, Email: email}, nil
}

func (o *OIDC) cookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/api/auth/", MaxAge: maxAge, HttpOnly: true, Secure: o.secure, SameSite: http.SameSiteLaxMode}
}

// Bind sets the cookie that a handoff code needs alongside it. It goes to the
// browser that finished the flow, so a code sent to anyone else is useless to
// them: without this, an attacker could sign a victim's browser into the
// attacker's account by sending them a code (login CSRF).
func (o *OIDC) Bind(w http.ResponseWriter, binding string) {
	http.SetCookie(w, o.cookie(bindCookie, binding, int(handoffTTL/time.Second)))
}

// Binding reads that cookie, and clears it.
func Binding(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(bindCookie)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: bindCookie, Path: "/api/auth/", MaxAge: -1})
	return c.Value
}

func (o *OIDC) seal(f flow) string {
	b, _ := json.Marshal(f)
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + base64.RawURLEncoding.EncodeToString(o.sign(payload))
}

func (o *OIDC) open(value string) (flow, bool) {
	payload, sig, ok := strings.Cut(value, ".")
	if !ok {
		return flow{}, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, o.sign(payload)) {
		return flow{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	var f flow
	if err != nil || json.Unmarshal(b, &f) != nil || f.State == "" || f.Nonce == "" || f.Verifier == "" {
		return flow{}, false
	}
	return f, true
}

func (o *OIDC) sign(payload string) []byte {
	mac := hmac.New(sha256.New, o.key)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error since Go 1.24
	return base64.RawURLEncoding.EncodeToString(b)
}

// Grant is what a finished flow hands to the web app: a session token for a
// sign-in, or the verified identity for linking to the signed-in account.
type Grant struct {
	Token    string
	Identity Identity
}

const handoffTTL = time.Minute

// Handoff carries a grant from the provider's callback to the web app
// without putting a token in a URL: the callback redirects with a code that
// works once, for a minute, and only together with the binding cookie set in
// the same response. Codes live in memory, like the scheduler lock, so this
// assumes one API instance (deploy/base runs one replica).
type Handoff struct {
	mu    sync.Mutex
	now   func() time.Time
	codes map[string]handoff
}

type handoff struct {
	grant   Grant
	binding string
	expires time.Time
}

func NewHandoff() *Handoff {
	return &Handoff{now: time.Now, codes: map[string]handoff{}}
}

// Issue returns the code for the URL and the binding for the cookie.
func (h *Handoff) Issue(g Grant) (code, binding string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	for c, x := range h.codes {
		if now.After(x.expires) {
			delete(h.codes, c)
		}
	}
	code, binding = random(), random()
	h.codes[code] = handoff{grant: g, binding: binding, expires: now.Add(handoffTTL)}
	return code, binding
}

// Redeem returns the grant for a code once, and only with its binding; a
// wrong binding leaves the code for its own browser.
func (h *Handoff) Redeem(code, binding string) (Grant, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	x, ok := h.codes[code]
	if !ok || subtle.ConstantTimeCompare([]byte(binding), []byte(x.binding)) != 1 {
		return Grant{}, false
	}
	delete(h.codes, code)
	if h.now().After(x.expires) {
		return Grant{}, false
	}
	return x.grant, true
}
