package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type claims struct {
	UserID  string `json:"sub"`
	Email   string `json:"email"`
	Expires int64  `json:"exp"`
}
type contextKey string

const userKey contextKey = "user-id"

func Hash(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(b), err
}
func Verify(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// dummyHash is compared against when a login names no account, at the same
// cost as a real hash. Built once, on first use; PrepareLogin builds it at
// startup so the first unknown email is not the slow one.
var dummyHash = sync.OnceValue(func() string {
	h, err := Hash("jobpilot: no account has this password")
	if err != nil {
		panic(err)
	}
	return h
})

func PrepareLogin() { dummyHash() }

// CheckLogin verifies a password for an account that may not exist. Without
// an account it still runs one bcrypt comparison, against dummyHash, so the
// answer takes as long as a wrong password and timing does not tell whether
// an email is registered. An account with no password (created through
// Google, empty hash) is answered the same way: bcrypt would refuse the
// empty hash at once, and that speed would tell.
func CheckLogin(hash string, found bool, password string) bool {
	if !found || hash == "" {
		Verify(dummyHash(), password)
		return false
	}
	return Verify(hash, password)
}
func Token(userID, email, secret string, ttl time.Duration) (string, error) {
	c := claims{userID, email, time.Now().Add(ttl).Unix()}
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func Parse(token, secret string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", errors.New("invalid token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("invalid token")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", errors.New("invalid signature")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	var c claims
	if json.Unmarshal(b, &c) != nil || c.UserID == "" || time.Now().Unix() > c.Expires {
		return "", errors.New("expired token")
	}
	return c.UserID, nil
}

// Middleware authenticates a request as either a browser session or an agent
// token, and puts the resulting principal in the context. A nil agents
// verifier keeps session-only behaviour, which is what the tests and any
// caller without a store use.
func Middleware(secret string, agents TokenVerifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		raw := strings.TrimPrefix(header, "Bearer ")
		principal, err := resolve(r.Context(), raw, secret, agents)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, principal)
		ctx = context.WithValue(ctx, userKey, principal.UserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func resolve(ctx context.Context, raw, secret string, agents TokenVerifier) (Principal, error) {
	if strings.HasPrefix(raw, AgentTokenPrefix) {
		if agents == nil {
			return Principal{}, errors.New("agent tokens are not configured")
		}
		// Looked up by digest: the token itself is never stored, so a
		// database copy cannot be replayed against the API.
		return agents.AgentTokenPrincipal(ctx, HashAgentToken(raw))
	}
	id, err := Parse(raw, secret)
	if err != nil {
		return Principal{}, err
	}
	// A browser session is the user themselves, so it holds every scope and
	// no token id, which is what RequireSession keys on.
	return Principal{UserID: id, Scopes: []string{ScopeRead, ScopeWrite}}, nil
}

func UserID(ctx context.Context) string { id, _ := ctx.Value(userKey).(string); return id }
