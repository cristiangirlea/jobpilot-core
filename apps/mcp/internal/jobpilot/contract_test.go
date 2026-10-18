package jobpilot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientReadsTheContractFromHealth(t *testing.T) {
	for _, c := range []struct {
		body     string
		contract int
		predates bool
	}{
		{`{"status":"ok","contract":1}`, 1, false},
		{`{"status":"ok","contract":7}`, 7, false},
		{`{"status":"ok"}`, 0, true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				t.Errorf("health must be called without a token, got %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(c.body))
		}))
		client, err := NewClient(Options{BaseURL: srv.URL, Token: "tok"})
		if err != nil {
			t.Fatal(err)
		}
		if client.PredatesContract() {
			t.Error("before any check the API must count as compatible")
		}
		h, err := client.CheckContract(context.Background())
		if err != nil || h.Contract != c.contract || client.PredatesContract() != c.predates {
			t.Errorf("%s: got contract %d predates=%v err=%v", c.body, h.Contract, client.PredatesContract(), err)
		}
		srv.Close()
	}
}

// An API whose version cannot be read does not support a contract, and the
// next call asks again, so an API that recovers is recognised.
func TestSupportsChecksUntilTheAPIAnswers(t *testing.T) {
	var up atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","contract":6}`))
	}))
	defer srv.Close()
	client, err := NewClient(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if client.supports(context.Background(), scopesContract) {
		t.Fatal("an API that cannot be checked must not count as supporting a contract")
	}
	up.Store(true)
	if !client.supports(context.Background(), scopesContract) {
		t.Fatal("once the API answers, a current one supports the contract")
	}
}

// Against an API that replaces notes on every update, a status-only call
// would erase them, so it is refused with instructions rather than sent.
func TestStatusOnlyUpdateIsRefusedAgainstAnOldAPI(t *testing.T) {
	cs, fake := sessionAgainstOldAPI(t)
	res, text := call(t, cs, "update_application_status", map[string]any{"application_id": "app-1", "status": "applied"})
	if !res.IsError || !strings.Contains(text, "notes") || !strings.Contains(text, "get_application") {
		t.Fatalf("expected a refusal that explains what to pass: %s", text)
	}
	if len(fake.patches) != 0 {
		t.Fatal("the refusal must happen before any request")
	}
	res, text = call(t, cs, "update_application_status", map[string]any{"application_id": "app-1", "status": "applied", "notes": "kept by hand"})
	if res.IsError {
		t.Fatalf("explicit notes must still work on an old API: %s", text)
	}
	if len(fake.patches) != 1 || fake.patches[0]["notes"] != "kept by hand" {
		t.Fatalf("explicit notes must be sent, got %v", fake.patches)
	}
}

func TestFilterGatesContract(t *testing.T) {
	for _, version := range []int{0, 6, 11, 12} {
		for _, field := range []string{"reject_eligibility_requirements", "reject_unlisted_languages"} {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%s/%t", version, field, enabled), func(t *testing.T) {
					cs, fake := sessionUnchecked(t, false)
					fake.apiContract = version
					fake.noContract = version == 0
					res, message := call(t, cs, "set_filters", map[string]any{"name": "Gates", field: enabled})
					if res.IsError != (version < 12) {
						t.Fatal(message)
					}
					paths, _ := fake.savedFilters()
					if version < 12 && (len(paths) > 0 || !strings.Contains(message, "update the API") || !strings.Contains(message, field)) {
						t.Fatal(message, paths)
					}
				})
			}
		}
	}
}
