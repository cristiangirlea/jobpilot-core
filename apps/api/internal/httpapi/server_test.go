package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/ats"
	"jobpilot/core/api/internal/models"
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

// Health says whether this server sends approved applications, so a client
// can tell the user whether an approval waits or goes.
func TestHealthReportsWhetherApprovalsAreSent(t *testing.T) {
	for _, server := range []*Server{{}, {Sender: &kicks{}}} {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
		var body struct {
			Sending bool `json:"sending"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Sending != (server.Sender != nil) {
			t.Fatalf("sending should be %v, got %s", server.Sender != nil, w.Body.String())
		}
	}
}

// kicks counts the approvals that asked the sender for a sweep.
type kicks struct{ n int }

func (k *kicks) Kick() { k.n++ }

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
		{url: "/api/jobs?q=%20Node.js%20", want: store.JobQuery{Search: "Node.js"}},
		{url: "/api/jobs?q=", want: store.JobQuery{}},
		{url: "/api/jobs?q=" + strings.Repeat("x", 61), problem: "q"},
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

// Found in early use: a company's own careers page was refused as
// "an aggregator link", which it is not. The refusal says which links work
// without guessing what the pasted one was.
func TestDetectBoardDoesNotCallEveryLinkAnAggregator(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{}).detectBoard(rec, httptest.NewRequest("POST", "/api/discovery/detect", strings.NewReader(`{"url":"https://larchmont.example/careers"}`)))
	if rec.Code != 422 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); strings.Contains(body, "an aggregator link") || !strings.Contains(body, "careers page") {
		t.Fatalf("the refusal should not call a careers page an aggregator: %s", body)
	}
}

// A careers-site link to a Greenhouse job becomes a Greenhouse source once
// Greenhouse names the board; when it cannot, the refusal is the usual one.
func TestDetectBoardResolvesAGreenhouseCareersSiteLink(t *testing.T) {
	var found atomic.Bool
	found.Store(true)
	greenhouse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !found.Load() || r.URL.Path != "/embed/job_app" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "https://job-boards.greenhouse.io/embed/job_app?for=acme&token="+r.URL.Query().Get("token"), http.StatusMovedPermanently)
	}))
	defer greenhouse.Close()
	server := &Server{ATS: ats.NewReader().WithGreenhouseLegacyBase(greenhouse.URL)}
	detect := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		server.detectBoard(rec, httptest.NewRequest("POST", "/api/discovery/detect", strings.NewReader(`{"url":"https://careers.example.com/?gh_jid=123"}`)))
		return rec
	}

	rec := detect()
	var got struct {
		Provider, Identifier string
		JobID                string `json:"job_id"`
		Supported            bool
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Provider != "greenhouse" || got.Identifier != "acme" || got.JobID != "123" || !got.Supported {
		t.Fatalf("detected %+v", got)
	}

	found.Store(false)
	if rec = detect(); rec.Code != 422 || !strings.Contains(rec.Body.String(), "not a job board") {
		t.Fatalf("an unknown job should get the usual refusal, got %d: %s", rec.Code, rec.Body)
	}
}

// The web job list pages on the server, so it has to read how many jobs match
// from X-Total-Count, and a browser only lets a page read a cross-origin
// response header the API exposes.
func TestCorsExposesTheTotalCount(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{FrontendURL: "http://localhost:3000"}).cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})).ServeHTTP(rec, httptest.NewRequest("GET", "/api/jobs", nil))
	if !strings.Contains(rec.Header().Get("Access-Control-Expose-Headers"), "X-Total-Count") {
		t.Fatalf("X-Total-Count should be exposed to the web app, got %q", rec.Header().Get("Access-Control-Expose-Headers"))
	}
}

// A refused approval or cancellation is the user's to fix (409), a missing
// entry a 404, and anything else a server fault (500), never passed off as a
// refusal.
func TestOutboxResponsesTellRefusalsFromFaults(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{store.OutboxRefusal("This application has already been sent."), 409},
		{pgx.ErrNoRows, 404},
		{errors.New("connection reset"), 500},
		{nil, 200},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		respondOutbox(w, models.OutboxEntry{ID: "entry-1"}, c.err)
		if w.Code != c.code {
			t.Errorf("%v: got %d, want %d", c.err, w.Code, c.code)
		}
	}
}
