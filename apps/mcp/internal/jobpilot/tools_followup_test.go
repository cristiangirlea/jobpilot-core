package jobpilot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDraftFollowUp(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/applications/app-1/follow-up" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls > 2 {
			w.WriteHeader(409)
			_, _ = io.WriteString(w, `{"error":"At most two follow-up drafts are allowed per application."}`)
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "draft-1", "application_id": "app-1", "body": "A saved draft, never sent.", "created_at": "2026-10-01T00:00:00Z"})
	}))
	defer api.Close()
	client, err := NewClient(Options{BaseURL: api.URL, Token: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(client, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	host := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := host.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, text := call(t, cs, "draft_follow_up", map[string]any{"application_id": " "})
	if !res.IsError || calls != 0 {
		t.Fatal("blank id reached API", text)
	}
	for range 2 {
		res, text = call(t, cs, "draft_follow_up", map[string]any{"application_id": "app-1"})
		if res.IsError {
			t.Fatal(text)
		}
		var got map[string]any
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &got)
		if got["body"] != "A saved draft, never sent." {
			t.Fatal(got)
		}
	}
	res, text = call(t, cs, "draft_follow_up", map[string]any{"application_id": "app-1"})
	if !res.IsError || !strings.Contains(text, "two follow-up") {
		t.Fatal(text)
	}
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "draft_follow_up" && (tool.Annotations.ReadOnlyHint || tool.Annotations.IdempotentHint) {
			t.Fatal("drafting must be a non-idempotent write")
		}
	}
}

func TestQuietApplicationResolution(t *testing.T) {
	text := howToResolve(Notification{Kind: "quiet_application", SubjectID: "app-1"})
	for _, want := range []string{"draft_follow_up", "app-1", "update_application_status", "never sent"} {
		if !strings.Contains(text, want) {
			t.Fatal(text)
		}
	}
}
