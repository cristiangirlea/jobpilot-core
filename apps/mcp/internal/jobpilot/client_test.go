package jobpilot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientRejectsOversizedResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[` + strings.Repeat(`{"job":{"id":"x"}},`, 50) + `{"job":{"id":"y"}}]`))
	}))
	defer srv.Close()
	c, err := NewClient(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	c.maxBytes = 64
	if _, err = c.Jobs(context.Background(), false); err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected a size error, got %v", err)
	}
}

func TestClientTrimsNonJSONErrorBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("<html>" + strings.Repeat("x", 2000) + "</html>"))
	}))
	defer srv.Close()
	c, err := NewClient(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Profile(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 502 {
		t.Fatalf("expected a 502 APIError, got %v", err)
	}
	if len([]rune(apiErr.Message)) != maxErrorMessage+3 || !strings.HasPrefix(apiErr.Message, "<html>") {
		t.Fatalf("message not trimmed: %d chars", len(apiErr.Message))
	}
}

func TestNewClientNormalizesBaseURL(t *testing.T) {
	for _, in := range []string{"http://localhost:8080", "http://localhost:8080/", "http://localhost:8080/api", "http://localhost:8080/api/"} {
		c, err := NewClient(Options{BaseURL: in, Token: "tok"})
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got := c.BaseURL(); got != "http://localhost:8080" {
			t.Fatalf("%s: normalized to %q", in, got)
		}
	}
}

func TestNewClientRequiresCredentials(t *testing.T) {
	if _, err := NewClient(Options{BaseURL: "http://localhost:8080"}); err == nil {
		t.Fatal("expected an error without a token or email/password")
	}
	if _, err := NewClient(Options{BaseURL: "http://localhost:8080", Email: "a@b.test"}); err == nil {
		t.Fatal("expected an error with an email but no password")
	}
	if _, err := NewClient(Options{Token: "tok"}); err == nil {
		t.Fatal("expected an error without a base URL")
	}
}

func TestClientSendsBearerToken(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Octavian","desired_titles":["Backend Engineer"]}`))
	}))
	defer srv.Close()
	c, err := NewClient(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Profile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if seen != "Bearer tok" || p.Name != "Octavian" {
		t.Fatalf("header %q, profile %+v", seen, p)
	}
}

func TestClientLogsInLazilyAndRetriesOnce(t *testing.T) {
	var logins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["email"] != "me@example.test" || body["password"] != "secret-pass" {
				w.WriteHeader(401)
				_, _ = w.Write([]byte(`{"error":"Invalid email or password."}`))
				return
			}
			logins.Add(1)
			_, _ = w.Write([]byte(`{"token":"fresh","user":{"id":"u1","email":"me@example.test"}}`))
		case r.URL.Path == "/api/profile":
			if r.Header.Get("Authorization") != "Bearer fresh" {
				w.WriteHeader(401)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			_, _ = w.Write([]byte(`{"name":"Me"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	// No token at all: the first call logs in, then succeeds.
	c, err := NewClient(Options{BaseURL: srv.URL, Email: "me@example.test", Password: "secret-pass"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Profile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if logins.Load() != 1 {
		t.Fatalf("expected one login, got %d", logins.Load())
	}

	// A stale token: one 401 triggers exactly one re-login and a retry.
	c, err = NewClient(Options{BaseURL: srv.URL, Token: "stale", Email: "me@example.test", Password: "secret-pass"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Profile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if logins.Load() != 2 {
		t.Fatalf("expected a second login, got %d", logins.Load())
	}

	// A stale token without credentials surfaces the 401 instead of looping.
	c, err = NewClient(Options{BaseURL: srv.URL, Token: "stale"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Profile(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Fatalf("expected a 401 APIError, got %v", err)
	}
	if logins.Load() != 2 {
		t.Fatalf("login must not be attempted without credentials, got %d", logins.Load())
	}
}

func TestClientMapsAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"application actions are blocked for this rejected job"}`))
	}))
	defer srv.Close()
	c, err := NewClient(Options{BaseURL: srv.URL, Token: "tok", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Generate(context.Background(), "job-1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T %v", err, err)
	}
	if apiErr.Status != 409 || apiErr.Message != "application actions are blocked for this rejected job" {
		t.Fatalf("unexpected error %+v", apiErr)
	}
	if err.Error() != "jobpilot api: 409: application actions are blocked for this rejected job" {
		t.Fatalf("unexpected message %q", err.Error())
	}
}

// An unconfigured client never touches the network: every request fails with
// the configuration problem, which names the fix.
func TestUnconfiguredClientExplainsInsteadOfCalling(t *testing.T) {
	c := Unconfigured(errors.New("jobpilot: a token, or an email and password, is required"))
	if c.Configured() {
		t.Fatal("an unconfigured client must say so")
	}
	_, err := c.Profile(context.Background())
	var cfg *ConfigError
	if !errors.As(err, &cfg) {
		t.Fatalf("expected a ConfigError, got %T %v", err, err)
	}
	for _, want := range []string{"not configured", "a token, or an email and password", "agent-token.ps1", "restart"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
	if _, err = c.CheckContract(context.Background()); !errors.As(err, &cfg) {
		t.Fatalf("the contract check must fail the same way, got %v", err)
	}
}
