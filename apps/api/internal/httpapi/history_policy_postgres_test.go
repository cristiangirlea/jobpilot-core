package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/discovery"
	"jobpilot/core/api/internal/models"
)

// The history rule holds for what the API answers, not only for the store:
// an imported job at an excluded company comes back rejected, without links.
func TestApplicationHistoryThroughAPIPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	stamp := fmt.Sprint(time.Now().UnixNano())
	u, err := st.CreateUser(ctx, "history-policy-api-"+stamp+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.DB.Exec(ctx, `DELETE FROM users WHERE id=$1`, u.ID) })
	const secret = "history-policy-api-secret"
	token, err := auth.Token(u.ID, u.Email, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{Store: st, Discovery: discovery.NewService(st), Secret: secret, TokenTTL: time.Hour}).Handler()
	employer := "Synthetic History " + stamp
	record := fmt.Sprintf(`{"external_key":"policy-api:%s","employer":%q,"title":"Backend Engineer","status":"applied","applied_at":"2026-01-10","source":"synthetic archive","evidence_kind":"application_receipt","evidence_ref":"archive:receipt-1"}`, stamp, employer+" Ltd")
	w := call(t, h, http.MethodPost, "/api/application-history/import", token, `{"record":`+record+`}`)
	var preview models.HistoryPreview
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.PreviewToken == "" {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	w = call(t, h, http.MethodPost, "/api/application-history/import", token, `{"record":`+record+`,"mode":"commit","preview_token":"`+preview.PreviewToken+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", w.Code, w.Body)
	}
	importJob := func(title string) models.JobWithMatch {
		t.Helper()
		body := fmt.Sprintf(`{"title":%q,"company":%q,"description":"Build services in Go.","apply_url":"https://example.test/jobs/%s"}`, title, employer, stamp)
		w := call(t, h, http.MethodPost, "/api/jobs", token, body)
		var got models.JobWithMatch
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Job.ID == "" {
			t.Fatalf("import %s: %d %s", title, w.Code, w.Body)
		}
		t.Cleanup(func() { _, _ = st.DB.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, got.Job.ID) })
		return got
	}
	// Warn only: the job is accepted, keeps its link and carries the evidence.
	warned := importJob("Platform Engineer " + stamp)
	if warned.Match.Rejected || warned.Job.ApplyURL == "" || warned.Job.ApplicationHistory == nil || warned.Job.ApplicationHistory.Blocked ||
		len(warned.Job.ApplicationHistory.Entries) != 1 || warned.Job.ApplicationHistory.Entries[0].EvidenceRef != "archive:receipt-1" ||
		!strings.Contains(strings.Join(warned.Match.Warnings, " "), "Application history: you applied to this company before.") {
		t.Fatalf("warn-only import: %+v", warned)
	}
	w = call(t, h, http.MethodPost, "/api/filters", token, `{"name":"History","active":true,"history_mode":"company"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("filter: %d %s", w.Code, w.Body)
	}
	excluded := importJob("Backend Engineer " + stamp)
	if !excluded.Match.Rejected || excluded.Job.ApplyURL != "" || excluded.Job.EmployerURL != "" ||
		excluded.Job.ApplicationHistory == nil || !excluded.Job.ApplicationHistory.Blocked ||
		!strings.Contains(strings.Join(excluded.Match.RejectionReasons, " "), "Application history: you already applied to this company") {
		t.Fatalf("excluded import: %+v", excluded)
	}
	w = call(t, h, http.MethodPost, "/api/applications", token, `{"job_id":"`+excluded.Job.ID+`"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("application for an excluded job: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/jobs?state=rejected", "/api/jobs/" + excluded.Job.ID} {
		w = call(t, h, http.MethodGet, path, token, "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"application_history"`) || !strings.Contains(w.Body.String(), "archive:receipt-1") ||
			strings.Contains(w.Body.String(), "example.test/jobs/"+stamp) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	w = call(t, h, http.MethodGet, "/api/jobs?state=accepted", token, "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), stamp) {
		t.Fatalf("an excluded company is still listed as accepted: %d %s", w.Code, w.Body)
	}
}
