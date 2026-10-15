// Package oidctest is a minimal OpenID Connect provider for tests: a token
// endpoint that checks the authorization code and the PKCE verifier and
// returns an ID token the test shapes, and a JWKS endpoint with the key that
// signs it. Nothing calls Google.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Code is the only authorization code the token endpoint accepts.
const Code = "good-code"

type Provider struct {
	URL string
	// Key signs ID tokens; the JWKS endpoint always serves the key the
	// provider started with, so replacing it forges a signature.
	Key *rsa.PrivateKey

	mu        sync.Mutex
	challenge string
	claims    map[string]any
	exchanges int
}

func New(t *testing.T) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{Key: key}
	m := http.NewServeMux()
	m.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.exchanges++
		_ = r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("code") != Code || base64.RawURLEncoding.EncodeToString(sum[:]) != p.challenge {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": p.Sign(p.claims)})
	})
	m.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	p.URL = srv.URL
	return p
}

// Expect sets the PKCE challenge the browser was sent with and the claims of
// the next ID token.
func (p *Provider) Expect(challenge string, claims map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.challenge, p.claims = challenge, claims
}

// Exchanges counts the calls to the token endpoint.
func (p *Provider) Exchanges() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exchanges
}

// Sign returns an RS256 JWT with the claims, signed with Key.
func (p *Provider) Sign(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	body, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, p.Key, crypto.SHA256, sum[:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}
