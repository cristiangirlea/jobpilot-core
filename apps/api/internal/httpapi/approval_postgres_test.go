package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// Approving an application is the user's consent to send it, so the route
// refuses agent tokens whatever their scopes, while the same user's browser
// session approves. Cancelling stays open to a write-scoped token. This runs
// on a migrated database named by JOBPILOT_TEST_DATABASE_URL, and is skipped
// without one; CI's integration job provides it.
func TestApprovingNeedsABrowserSessionAgainstPostgres(t *testing.T) {
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated database to run this")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := store.New(pool)

	run := time.Now().Format("20060102150405.000000")
	email := "consent-" + run + "@example.test"
	user, err := st.CreateUser(ctx, email, "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.CreateDiscoveredJob(ctx, user.ID, "", "", "", "", "consent-"+run, models.JobInput{
		Title: "Backend Engineer", Company: "Consent Test " + run, Description: "Build services.", ApplyURL: "https://example.test/consent-" + run,
	})
	if err != nil {
		t.Fatal(err)
	}
	cv, err := st.SaveCV(ctx, user.ID, models.CVDocument{Kind: "tailored", Filename: "cv.pdf", Format: "pdf", Approved: true, Fingerprint: "consent-" + run}, []byte("%PDF-1.4 test"))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := st.SaveOutboxEntry(ctx, user.ID, models.OutboxEntry{JobID: job.ID, Status: models.OutboxReady, Channel: models.ChannelATSForm, CVDocumentID: cv.ID})
	if err != nil {
		t.Fatal(err)
	}

	const secret = "consent-test-secret"
	agent, _, err := st.CreateAgentToken(ctx, user.ID, "consent-test", []string{auth.ScopeRead, auth.ScopeWrite}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := auth.Token(user.ID, email, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{Store: st, Secret: secret, FrontendURL: "http://localhost:3000"}).Handler()
	post := func(path, token string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(w, r)
		return w
	}
	status := func(body *httptest.ResponseRecorder) string {
		var x struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(body.Body.Bytes(), &x)
		return x.Status
	}

	w := post("/api/outbox/"+entry.ID+"/approve", agent)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "consent to send") {
		t.Fatalf("a write-scoped agent token must not approve, got %d %s", w.Code, w.Body.String())
	}
	if stored, err := st.OutboxEntry(ctx, user.ID, entry.ID); err != nil || stored.Status != models.OutboxReady {
		t.Fatalf("a refused approval must leave the entry ready, got %+v, %v", stored, err)
	}

	if w = post("/api/outbox/"+entry.ID+"/approve", session); w.Code != http.StatusOK || status(w) != models.OutboxApproved {
		t.Fatalf("the user's session must approve, got %d %s", w.Code, w.Body.String())
	}

	if w = post("/api/outbox/"+entry.ID+"/cancel", agent); w.Code != http.StatusOK || status(w) != models.OutboxCancelled {
		t.Fatalf("a write-scoped agent token may still cancel, got %d %s", w.Code, w.Body.String())
	}
	if w = post("/api/outbox/"+entry.ID+"/approve", session); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Prepare it again") {
		t.Fatalf("a cancelled entry's approval is a 409 saying what to do, got %d %s", w.Code, w.Body.String())
	}
}
