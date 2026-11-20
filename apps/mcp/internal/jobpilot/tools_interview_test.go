package jobpilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPrepareInterview(t *testing.T) {
	requests := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer prep-token" {
			t.Error("must be an authenticated GET")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/applications/app/interview-prep":
			w.Write([]byte(`{"application_id":"app","posting":{"job":{"description":"Go services"}},"sent_documents":{"cv_text":" CV\n","cover_letter":" Letter\n","answers":[{"answer":" Exact\n"}]},"star_prompts":[],"gap_prompts":[],"questions":["Go?"],"notices":[]}`))
		case "/api/applications/ready/interview-prep":
			w.WriteHeader(409)
			w.Write([]byte(`{"error":"Interview preparation requires an application in interview status."}`))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"error":"Record not found."}`))
		}
	}))
	defer api.Close()
	c, err := NewClient(Options{BaseURL: api.URL, Token: "prep-token"})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(c, "test", nil)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range list.Tools {
		if tool.Name == "prepare_interview" {
			found = true
			if !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || !strings.Contains(tool.Description, "real examples") || !strings.Contains(tool.Description, "Use only facts from the profile, CV and job") || !strings.Contains(tool.Description, "do not fill in situation, task, action or result for the user") {
				t.Fatalf("missing read-only annotation or factuality guidance: %+v", tool)
			}
		}
	}
	if !found {
		t.Fatal("prepare_interview is not registered")
	}
	for _, tc := range []struct {
		id, want string
		bad      bool
	}{{"app", " Exact", false}, {"ready", "interview status", true}, {"other", "Record not found", true}, {" ", "application_id", true}, {"app/other", "application_id", true}} {
		res, text := call(t, cs, "prepare_interview", map[string]any{"application_id": tc.id})
		if res.IsError != tc.bad || !strings.Contains(text, tc.want) {
			t.Fatalf("%q: %s", tc.id, text)
		}
		if !tc.bad {
			out := structured[map[string]any](t, res)
			posting := out["posting"].(map[string]any)
			if got := posting["employer_text"]; got != employerTextNotice {
				t.Errorf("interview posting employer text notice = %v, want %q", got, employerTextNotice)
			}
			docs := out["sent_documents"].(map[string]any)
			if docs["cv_text"] != " CV\n" || docs["cover_letter"] != " Letter\n" {
				t.Fatal("documents changed in transit")
			}
		}
	}
	if requests != 3 {
		t.Fatalf("want only three reads, got %d", requests)
	}
}

func TestPrepareInterviewUnavailableRoute(t *testing.T) {
	for _, tc := range []struct {
		name, id, want string
		available      bool
	}{
		{"older API", "app", "JobPilot API does not offer interview preparation yet", false},
		{"missing application", "missing", "Record not found", true},
		{"foreign application", "foreign", "Record not found", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"status":"ok","contract":10}`))
			})
			if tc.available {
				mux.HandleFunc("GET /api/applications/{id}/interview-prep", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					w.Write([]byte(`{"error":"Record not found."}`))
				})
			}
			api := httptest.NewServer(mux)
			defer api.Close()
			c, err := NewClient(Options{BaseURL: api.URL, Token: "prep-token"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if health, err := c.CheckContract(ctx); err != nil || health.Contract != 10 {
				t.Fatalf("expected API contract 10: %+v, %v", health, err)
			}
			s := NewServer(c, "test", nil)
			st, ct := mcp.NewInMemoryTransports()
			ss, err := s.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			res, text := call(t, cs, "prepare_interview", map[string]any{"application_id": tc.id})
			if !res.IsError || !strings.Contains(text, tc.want) {
				t.Fatalf("want %q, got %s", tc.want, text)
			}
		})
	}
}
