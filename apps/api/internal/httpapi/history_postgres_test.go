package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func TestHistoryAuthorizationPostgres(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated throwaway database")
	}
	ctx := context.Background()
	pool, e := database.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	st := store.New(pool)
	run := fmt.Sprint(time.Now().UnixNano())
	ana, e := st.CreateUser(ctx, "history-api-ana-"+run+"@example.test", "fake")
	if e != nil {
		t.Fatal(e)
	}
	bob, e := st.CreateUser(ctx, "history-api-bob-"+run+"@example.test", "fake")
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, ana.ID, bob.ID)
	read, _, e := st.CreateAgentToken(ctx, ana.ID, "read", []string{auth.ScopeRead}, nil)
	if e != nil {
		t.Fatal(e)
	}
	rw, _, e := st.CreateAgentToken(ctx, ana.ID, "write", []string{auth.ScopeRead, auth.ScopeWrite}, nil)
	if e != nil {
		t.Fatal(e)
	}
	const secret = "history-test-only"
	bobSession, e := auth.Token(bob.ID, bob.Email, secret, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	sender := &kicks{}
	h := (&Server{Store: st, Secret: secret, Sender: sender}).Handler()
	request := func(method, path, token string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		h.ServeHTTP(w, r)
		return w
	}
	in := models.HistoryImport{Mode: "preview", Record: models.ExternalApplicationInput{ExternalKey: "history-api:1", Employer: "Synthetic Closed Ltd", Status: "rejected", AppliedAt: "2023-02-01", OutcomeAt: "2023-03-01", Source: "synthetic", EvidenceKind: "rejection", EvidenceRef: "archive:1"}}
	for _, token := range []string{"", read} {
		w := request("POST", "/api/application-history/import", token, in)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("write scope", w.Code, w.Body.String())
		}
	}
	w := request("POST", "/api/application-history/import", rw, in)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p models.HistoryPreview
	if e = json.Unmarshal(w.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	in.Mode = "commit"
	in.PreviewToken = p.PreviewToken
	w = request("POST", "/api/application-history/import", rw, in)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	in.Record.EvidenceRef = "changed"
	w = request("POST", "/api/application-history/import", rw, in)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "reconciliation") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/api/application-history", read, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "2023-02-01") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/api/application-history?after=not-a-cursor", read, nil)
	if w.Code != 400 {
		t.Fatal("unknown cursor", w.Code, w.Body.String())
	}
	w = request("GET", "/api/application-history", bobSession, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "archive:1") {
		t.Fatal("tenant leak", w.Code, w.Body.String())
	}
	if sender.n != 0 {
		t.Fatal("history kicked sender", sender.n)
	}
}
