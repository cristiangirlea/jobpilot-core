package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/models"
)

func TestExtractionRoutesAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	run := time.Now().Format("20060102150405.000000000")
	const secret = "extraction-test-secret"
	const pendingPath = "/api/extractions/pending"
	const savePath = "/api/extractions"
	const marker = "EXTRACTION_PRIVATE_MARKER"
	const title = "Backend Engineer"
	const description = "Go required.\n" + marker

	account := func(role string) (models.User, string) {
		t.Helper()
		u, err := st.CreateUser(ctx, "extraction-"+role+"-"+run+"@example.test", "not a real hash")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := st.DB.Exec(ctx, `DELETE FROM users WHERE id=$1`, u.ID); err != nil {
				t.Error(err)
			}
		})
		token, err := auth.Token(u.ID, u.Email, secret, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return u, token
	}
	admin, adminSession := account("admin")
	member, memberSession := account("member")
	insert := func(discovered bool) string {
		t.Helper()
		var id string
		// Closed, so no other package's test scores or lists these jobs; the listing itself is tested in the store.
		err := st.DB.QueryRow(ctx, `INSERT INTO jobs (user_id, title, company, company_normalized, description, apply_url, closed_at, discovered_at)
			VALUES (CASE WHEN NOT $3 THEN $4::uuid END, $1, 'Example GmbH', 'example gmbh', $2,
				'https://example.test/apply', now(), CASE WHEN $3 THEN now() END)
			RETURNING id`, title, description, discovered, member.ID).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := st.DB.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, id); err != nil {
				t.Error(err)
			}
		})
		return id
	}
	discovered, pasted := insert(true), insert(false)
	model := "http-extraction-" + discovered
	hash := extraction.Hash(title, description)
	s := &Server{Store: st, Secret: secret, TokenTTL: time.Hour, AdminEmails: []string{admin.Email}, ExtractionModel: model}
	h := s.Handler()

	post := func(t *testing.T, token, body string, code int) *httptest.ResponseRecorder {
		t.Helper()
		w := call(t, h, "POST", savePath, token, body)
		if strings.Contains(w.Body.String(), marker) {
			t.Fatal("POST response exposed posting or answer text")
		}
		if w.Code != code {
			t.Fatalf("POST status = %d, want %d (%s)", w.Code, code, w.Body)
		}
		return w
	}
	get := func(path, token string, code int) *httptest.ResponseRecorder {
		t.Helper()
		w := call(t, h, "GET", path, token, "")
		if w.Code != code {
			t.Fatalf("GET %s status = %d, want %d", path, w.Code, code)
		}
		return w
	}
	body := func(id, hash, answer string) string {
		raw, _ := json.Marshal(map[string]string{"job_id": id, "text_hash": hash, "answer": answer})
		return string(raw)
	}

	get(pendingPath, "", 401)
	post(t, "", "", 401)
	get(pendingPath, memberSession, 403)
	post(t, memberSession, "", 403)
	w := call(t, h, "POST", "/api/tokens", adminSession, `{"name":"extraction-reader","scopes":["read"]}`)
	var token struct{ Token string }
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &token) != nil || token.Token == "" {
		t.Fatalf("creating a read-only agent token returned %d", w.Code)
	}
	get(pendingPath, token.Token, 200)
	post(t, token.Token, "", 403)

	for _, query := range []string{"?limit=", "?limit=no", "?limit=0", "?limit=201"} {
		get(pendingPath+query, adminSession, 400)
	}
	get(pendingPath+"?limit=1", adminSession, 200)
	get(pendingPath+"?limit=200", adminSession, 200)
	readPending := func(w *httptest.ResponseRecorder, wantModel string) []map[string]string {
		t.Helper()
		var out struct {
			Model         string              `json:"model"`
			PromptVersion string              `json:"prompt_version"`
			Jobs          []map[string]string `json:"jobs"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Model != wantModel || out.PromptVersion != extraction.PromptVersion || out.Jobs == nil {
			t.Fatalf("pending response metadata = %q, %q, jobs present = %v", out.Model, out.PromptVersion, out.Jobs != nil)
		}
		return out.Jobs
	}
	for _, j := range readPending(get(pendingPath+"?limit=200", adminSession, 200), model) {
		if j["id"] == discovered || j["id"] == pasted {
			t.Fatal("pending response listed a closed or pasted-in job")
		}
	}
	raw, err := json.Marshal(models.PendingExtraction{ID: "i", Title: "t", Description: "d", TextHash: "h"})
	if want := `{"id":"i","title":"t","description":"d","text_hash":"h"}`; err != nil || string(raw) != want {
		t.Fatalf("a pending job is sent as %s, want %s", raw, want)
	}

	const good = `{"required":[{"skills":["Go"],"quote":"Go required."}],"nice_to_have":[],"stack":[],"seniority":{"level":"unspecified","quote":null},"min_years":null,"constraints":[]}`
	request := body(discovered, hash, good)
	if got := errorText(t, post(t, adminSession, body(discovered, extraction.Hash(title, "old text"), good), 409)); got != extractionPostingChanged {
		t.Fatalf("stale hash error = %q", got)
	}
	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "not-a-uuid-" + marker, strings.ReplaceAll(discovered, "-", "x"), pasted} {
		post(t, adminSession, body(id, hash, good), 404)
	}
	for _, invalid := range []string{
		body(discovered, hash, strings.Repeat("x", 512<<10)),
		request + strings.Repeat(" ", 512<<10),
		strings.TrimSuffix(request, "}") + `,"` + marker + `":true}`,
		`{"text_hash":"x","answer":"x"}`,
		`{"job_id":"x","answer":"x"}`,
		`{"job_id":"x","text_hash":"x"}`,
		request + ` {}`,
	} {
		post(t, adminSession, invalid, 400)
	}

	s.ExtractionModel = ""
	if got := readPending(get(pendingPath, adminSession, 200), ""); len(got) != 0 {
		t.Fatal("pending returned jobs with extraction off")
	}
	post(t, adminSession, request, 409)
	s.ExtractionModel = model
	var count int
	if err := st.DB.QueryRow(ctx, `SELECT count(*) FROM job_extractions WHERE job_id IN ($1,$2)`, discovered, pasted).Scan(&count); err != nil || count != 0 {
		t.Fatalf("refused requests left %d rows, query error %v", count, err)
	}

	badQuote := strings.Replace(good, `"quote":"Go required."}`, `"quote":"Go required."},{"skills":["Fortran"],"quote":"Fortran required. `+marker+`"}`, 1)
	nulValue := strings.Replace(good, `"constraints":[]`, `"constraints":[{"kind":"other","value":"\u0000`+marker+`","hard":true,"quote":"Go required."}]`, 1)
	for _, tc := range []struct {
		name, answer, status, reason string
		dropped                      int
	}{
		{name: "valid", answer: good, status: "ok"},
		{name: "one bad quote", answer: badQuote, status: "ok", dropped: 1},
		{name: "invalid JSON", answer: marker, status: "rejected", reason: "not_json"},
		{name: "empty answer string", answer: "", status: "rejected", reason: "not_json"},
		{name: "NUL in constraint value", answer: nulValue, status: "rejected", reason: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := post(t, adminSession, body(discovered, hash, tc.answer), 200)
			var response map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"status": tc.status}
			if tc.status == "ok" {
				want["dropped"] = float64(tc.dropped)
			} else {
				want["reason"] = tc.reason
			}
			if !reflect.DeepEqual(response, want) {
				t.Fatalf("response = %v, want %v", response, want)
			}
			var status, reason, storedModel, prompt, textHash string
			var raw []byte
			err := st.DB.QueryRow(ctx, `SELECT status, reason, model, prompt_version, text_hash, extraction
				FROM job_extractions WHERE job_id=$1`, discovered).Scan(&status, &reason, &storedModel, &prompt, &textHash, &raw)
			if err != nil {
				t.Fatal(err)
			}
			if status != tc.status || reason != tc.reason || storedModel != model || prompt != extraction.PromptVersion || textHash != hash {
				t.Fatalf("stored status, reason, model, prompt or hash differs: %q %q %q %q %q", status, reason, storedModel, prompt, textHash)
			}
			if tc.status == "rejected" {
				if raw != nil {
					t.Fatal("rejected answer did not store SQL NULL")
				}
				return
			}
			var result models.Extraction
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if result.Dropped != tc.dropped || len(result.Required) != 1 || result.Required[0].Quote != "Go required." {
				t.Fatalf("stored extraction = %+v", result)
			}
		})
	}
}
