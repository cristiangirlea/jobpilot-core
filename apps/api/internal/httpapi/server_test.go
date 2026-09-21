package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// A status-only PATCH must leave notes untouched. The field is a pointer so
// the handler can tell "no notes were sent" from "clear the notes", which a
// plain string cannot express.
func TestApplicationUpdateDistinguishesOmittedNotesFromEmpty(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		ok       bool
		status   string
		hasNotes bool
		notes    string
	}{
		{name: "status only", body: `{"status":"applied"}`, ok: true, status: "applied"},
		{name: "status and notes", body: `{"status":"interview","notes":"Phone screen booked"}`, ok: true, status: "interview", hasNotes: true, notes: "Phone screen booked"},
		{name: "notes cleared explicitly", body: `{"status":"applied","notes":""}`, ok: true, status: "applied", hasNotes: true},
		{name: "notes only", body: `{"notes":"just a note"}`, ok: true, hasNotes: true, notes: "just a note"},
		{name: "unknown field", body: `{"status":"applied","extra":1}`, ok: false},
		{name: "empty object still decodes", body: `{}`, ok: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("PATCH", "/api/applications/app-1", strings.NewReader(c.body))
			var x applicationUpdate
			if got := decode(w, r, &x); got != c.ok {
				t.Fatalf("decode returned %v, want %v (%s)", got, c.ok, w.Body.String())
			}
			if !c.ok {
				if w.Code != 400 {
					t.Fatalf("a rejected body must be a 400, got %d", w.Code)
				}
				return
			}
			if x.Status != c.status {
				t.Fatalf("status %q, want %q", x.Status, c.status)
			}
			if (x.Notes != nil) != c.hasNotes {
				t.Fatalf("notes present = %v, want %v", x.Notes != nil, c.hasNotes)
			}
			if c.hasNotes && *x.Notes != c.notes {
				t.Fatalf("notes %q, want %q", *x.Notes, c.notes)
			}
		})
	}
}
