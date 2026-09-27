package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"jobpilot/core/api/internal/store"
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

// The health endpoint is the one route a client can call before it has a
// token, so it is where the contract version has to live.
func TestHealthReportsTheContract(t *testing.T) {
	w := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
	if w.Code != 200 {
		t.Fatalf("health returned %d", w.Code)
	}
	var body struct {
		Status   string `json:"status"`
		Contract int    `json:"contract"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" || body.Contract != Contract || Contract < 1 {
		t.Fatalf("unexpected health body: %s", w.Body.String())
	}
}

// Found in early use: every match linked to an aggregator page, so
// no form could be read, and the blocker only said to apply by hand. It now
// says what makes forms readable: the employer's own board as a source.
func TestUnreadableLinkPointsToTheEmployerBoard(t *testing.T) {
	if reason := unsupportedReason(""); !strings.Contains(reason, "discovery source") || !strings.Contains(reason, "Greenhouse") {
		t.Fatalf("the blocker should say how to get readable forms: %s", reason)
	}
}

// Listing every job was the only way to list any, which is 14.4 MB on a real
// account. The optional parameters page and narrow the list on the server;
// without them the list is whole, as clients built before them expect.
func TestJobQueryReadsListParameters(t *testing.T) {
	for _, c := range []struct {
		url, fixed string
		want       store.JobQuery
		problem    string
	}{
		{url: "/api/jobs", want: store.JobQuery{}},
		{url: "/api/jobs?state=rejected&min_score=70&limit=20&offset=40&fields=summary&open=true",
			want: store.JobQuery{State: "rejected", MinScore: 70, Limit: 20, Offset: 40, Summary: true, OpenOnly: true}},
		{url: "/api/jobs?state=all", want: store.JobQuery{State: "all"}},
		{url: "/api/matches?limit=5", fixed: "accepted", want: store.JobQuery{State: "accepted", Limit: 5}},
		{url: "/api/matches?state=accepted", fixed: "accepted", want: store.JobQuery{State: "accepted"}},
		{url: "/api/matches?state=rejected", fixed: "accepted", problem: "state"},
		{url: "/api/jobs?state=maybe", problem: "state"},
		{url: "/api/jobs?min_score=101", problem: "min_score"},
		{url: "/api/jobs?limit=0", problem: "limit"},
		{url: "/api/jobs?limit=501", problem: "limit"},
		{url: "/api/jobs?offset=-1", problem: "offset"},
		{url: "/api/jobs?offset=10", problem: "offset"},
		{url: "/api/jobs?fields=everything", problem: "fields"},
		{url: "/api/jobs?open=yes", problem: "open"},
	} {
		got, problem := jobQuery(httptest.NewRequest("GET", c.url, nil), c.fixed)
		if c.problem != "" {
			if !strings.Contains(problem, c.problem) {
				t.Errorf("%s: want a problem naming %s, got %q", c.url, c.problem, problem)
			}
			continue
		}
		if problem != "" || got != c.want {
			t.Errorf("%s: got %+v (%q), want %+v", c.url, got, problem, c.want)
		}
	}
}
