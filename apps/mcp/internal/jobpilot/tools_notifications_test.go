package jobpilot

import (
	"net/http"
	"strings"
	"testing"
)

// notificationRoutes adds the notification feed to the fake API: three open
// items of the kinds the API derives, one of a kind this server does not
// know, and, for state=all, one resolved item (the API orders state=all
// newest first; the fixture keeps one order for both so the assertions stay
// simple).
func (f *fakeAPI) notificationRoutes(m *http.ServeMux, send func(http.ResponseWriter, int, string)) {
	m.HandleFunc("GET /api/notifications", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("after") == "n-4" {
			send(w, 200, `{"open":5,"items":[{"id":"n-5","kind":"cv","subject_id":"cv-3","title":"Approve the CV: base","detail":{},"created_at":"2026-10-05T00:00:00Z","updated_at":"2026-10-05T00:00:00Z"}]}`)
			return
		}
		if q.Get("limit") != "20" || (q.Get("state") != "open" && q.Get("state") != "all") {
			send(w, 400, `{"error":"unexpected query `+r.URL.RawQuery+`"}`)
			return
		}
		items := []string{
			`{"id":"n-1","kind":"answer","subject_id":"a-2","title":"Approve the answer to: Why this role?","detail":{"question":"Why this role?","answer":"Draft text.","href":"/answers"},"created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-01T00:00:00Z"}`,
			`{"id":"n-2","kind":"cv","subject_id":"cv-2","title":"Approve the tailored CV: Backend","detail":{"cv_kind":"tailored","href":"/cv"},"created_at":"2026-10-02T00:00:00Z","updated_at":"2026-10-02T00:00:00Z"}`,
			`{"id":"n-3","kind":"outbox","subject_id":"ob-1","title":"Review the application to Senior Backend Engineer at Fresh Systems GmbH","detail":{"status":"needs_review","blockers":["Approve the tailored CV"],"href":"/outbox"},"created_at":"2026-10-03T00:00:00Z","updated_at":"2026-10-03T00:00:00Z"}`,
			`{"id":"n-4","kind":"proposal","subject_id":"p-1","title":"Review a proposed profile change","detail":{"href":"/profile"},"created_at":"2026-10-04T00:00:00Z","updated_at":"2026-10-04T00:00:00Z"}`,
		}
		if q.Get("state") == "all" {
			items = append(items, `{"id":"n-0","kind":"answer","subject_id":"a-1","title":"Approve the answer to: Notice period?","detail":{},"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-02T00:00:00Z","resolved_at":"2026-09-02T00:00:00Z"}`)
		}
		send(w, 200, `{"open":5,"next":"n-4","items":[`+strings.Join(items, ",")+`]}`)
	})
}

func TestListPendingDecisions(t *testing.T) {
	cs, _ := session(t)

	res, text := call(t, cs, "list_pending_decisions", nil)
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[PendingDecisions](t, res)
	if got.Open != 5 || len(got.Items) != 4 || got.Next != "n-4" {
		t.Fatalf("expected the first page of four with next n-4, got %+v", got)
	}
	want := map[string][]string{
		"n-1": {"approve_answer", "a-2", "word for word", "only when the user approves"},
		"n-2": {"get_cv", "cv-2", "approve_cv only when"},
		"n-3": {"get_outbox_entry", "ob-1", "exact words", "only in the web app"},
		// A kind this server does not know yet is still shown, and left to
		// the web page the API names.
		"n-4": {"/profile", "web app"},
	}
	for _, item := range got.Items {
		for _, phrase := range want[item.ID] {
			if !strings.Contains(item.HowToResolve, phrase) {
				t.Errorf("%s (%s): how_to_resolve should mention %q, got %q", item.ID, item.Kind, phrase, item.HowToResolve)
			}
		}
		if item.Kind == "outbox" && strings.Contains(item.HowToResolve, "approve_outbox") {
			t.Fatal("no tool approves an outbox entry")
		}
	}
	if got.Items[0].Detail["answer"] != "Draft text." {
		t.Fatalf("the detail must pass through unchanged: %+v", got.Items[0].Detail)
	}

	// after fetches the following page, and the last page has no next.
	res, text = call(t, cs, "list_pending_decisions", map[string]any{"after": "n-4"})
	if res.IsError {
		t.Fatal(text)
	}
	if page := structured[PendingDecisions](t, res); len(page.Items) != 1 || page.Items[0].ID != "n-5" || page.Next != "" {
		t.Fatalf("after should fetch the following page: %+v", page)
	}

	res, text = call(t, cs, "list_pending_decisions", map[string]any{"include_resolved": true})
	if res.IsError {
		t.Fatal(text)
	}
	got = structured[PendingDecisions](t, res)
	if len(got.Items) != 5 || got.Items[4].ResolvedAt == nil || !strings.HasPrefix(got.Items[4].HowToResolve, "Nothing") {
		t.Fatalf("include_resolved should add the resolved item, marked as needing nothing: %+v", got.Items)
	}
}
