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
	"jobpilot/core/api/internal/discovery"
	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
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

func TestSaveExtractionRescoresShadowAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	const secret = "save-extraction-shadow-test-secret"
	admin, err := st.CreateUser(ctx, "shadow-admin-"+time.Now().Format("20060102150405.000000000")+"@example.test", "unused")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := st.DB.Exec(ctx, `DELETE FROM users WHERE id=$1`, admin.ID); err != nil {
			t.Error(err)
		}
	})
	token, err := auth.Token(admin.ID, admin.Email, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	const title, description = "Backend Engineer", "Go required."
	var jobID string
	err = st.DB.QueryRow(ctx, `INSERT INTO jobs (title, company, company_normalized, description, discovered_at, closed_at)
		VALUES ($1,'Example GmbH','example gmbh',$2,now(),now()) RETURNING id`, title, description).Scan(&jobID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := st.DB.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, jobID); err != nil {
			t.Error(err)
		}
	})
	if _, err := st.SaveJobMatch(ctx, admin.ID, jobID, models.Evaluation{Score: 55, Reasons: []string{"Example reason"}}, ""); err != nil {
		t.Fatal(err)
	}

	model := "http-shadow-" + jobID
	hash := extraction.Hash(title, description)
	const good = `{"required":[{"skills":["Go"],"quote":"Go required."}],"nice_to_have":[],"stack":[],"seniority":{"level":"unspecified","quote":null},"min_years":null,"constraints":[]}`
	for _, tc := range []struct {
		name, mode, answer, response string
		wantShadow                   bool
	}{
		{"off", "", good, `{"status":"ok","dropped":0}`, false},
		{"shadow", "shadow", good, `{"status":"ok","dropped":0}`, true},
		{"rejected", "shadow", "not JSON", `{"reason":"not_json","status":"rejected"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Keep the service enabled to verify that the route honors its own setting.
			svc := &discovery.Service{Store: st, ExtractionModel: model, ShadowScoring: true}
			s := &Server{Store: st, Discovery: svc, Secret: secret, AdminEmails: []string{admin.Email}, ExtractionModel: model, ExtractionScoring: tc.mode}
			var seeded *models.ShadowEvaluation
			if tc.name == "off" {
				seeded = &models.ShadowEvaluation{
					ExtractionID: "00000000-0000-0000-0000-000000000001",
					TextHash:     hash,
					AnsweredAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
					RulesScore:   55,
					Evaluation:   models.Evaluation{Score: 61},
				}
				if err := st.SaveShadowEvaluation(ctx, admin.ID, jobID, seeded); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := st.SaveShadowEvaluation(ctx, admin.ID, jobID, nil); err != nil {
						t.Error(err)
					}
				})
			}
			body, err := json.Marshal(map[string]string{"job_id": jobID, "text_hash": hash, "answer": tc.answer})
			if err != nil {
				t.Fatal(err)
			}
			w := call(t, s.Handler(), "POST", "/api/extractions", token, string(body))
			s.background.Wait()
			if w.Code != 200 || w.Body.String() != tc.response+"\n" {
				t.Fatalf("POST = %d %s, want 200 %s", w.Code, w.Body, tc.response)
			}
			var raw []byte
			if err := st.DB.QueryRow(ctx, `SELECT shadow_evaluation FROM job_matches WHERE user_id=$1 AND job_id=$2`, admin.ID, jobID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if seeded != nil {
				var got models.ShadowEvaluation
				if err := json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(&got, seeded) {
					t.Fatalf("stored shadow = %s, %v, want unchanged shadow %+v", raw, err, seeded)
				}
				return
			}
			if !tc.wantShadow {
				if raw != nil {
					t.Fatalf("stored shadow = %s, want SQL NULL", raw)
				}
				return
			}
			var shadow models.ShadowEvaluation
			if err := json.Unmarshal(raw, &shadow); err != nil {
				t.Fatal(err)
			}
			var answerID string
			var answeredAt time.Time
			if err := st.DB.QueryRow(ctx, `SELECT id, created_at FROM job_extractions
				WHERE job_id=$1 AND model=$2 AND prompt_version=$3`, jobID, model, extraction.PromptVersion).Scan(&answerID, &answeredAt); err != nil {
				t.Fatal(err)
			}
			if shadow.ExtractionID != answerID || shadow.TextHash != hash || !shadow.AnsweredAt.Equal(answeredAt) {
				t.Fatalf("stored shadow = %+v, want answer %s at %s", shadow, answerID, answeredAt)
			}
		})
	}
}

func shadowRouteAccount(t *testing.T, ctx context.Context, st *store.Store, secret string) (models.User, string) {
	t.Helper()
	u, err := st.CreateUser(ctx, "shadow-route-"+time.Now().Format("20060102150405.000000000")+"@example.test", "unused")
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

func shadowRouteJob(t *testing.T, ctx context.Context, st *store.Store, job models.JobInput, fingerprint string) string {
	t.Helper()
	var id string
	err := st.DB.QueryRow(ctx, `INSERT INTO jobs (title, company, company_normalized, description, apply_url, fingerprint, discovered_at, closed_at)
		VALUES ($1,$2,'example gmbh',$3,$4,$5,now(),now()) RETURNING id`,
		job.Title, job.Company, job.Description, job.ApplyURL, fingerprint).Scan(&id)
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

func saveShadowRouteAnswer(t *testing.T, ctx context.Context, st *store.Store, jobID, model string, job models.JobInput) {
	t.Helper()
	answer := models.JobExtraction{
		JobID: jobID, Model: model, PromptVersion: extraction.PromptVersion,
		TextHash: extraction.Hash(job.Title, job.Description), Status: "ok",
		Extraction: &models.Extraction{
			Required:  []models.ExtractedSkills{{Skills: []string{"Go"}, Quote: job.Description}},
			Seniority: models.ExtractedSeniority{Level: "unspecified"},
		},
	}
	if saved, err := st.SaveExtraction(ctx, answer); err != nil || !saved {
		t.Fatalf("SaveExtraction() = %t, %v", saved, err)
	}
}

func readRouteShadow(t *testing.T, ctx context.Context, st *store.Store, userID, jobID string) *models.ShadowEvaluation {
	t.Helper()
	var raw []byte
	if err := st.DB.QueryRow(ctx, `SELECT shadow_evaluation FROM job_matches WHERE user_id=$1 AND job_id=$2`, userID, jobID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == nil {
		return nil
	}
	var shadow models.ShadowEvaluation
	if err := json.Unmarshal(raw, &shadow); err != nil {
		t.Fatal(err)
	}
	return &shadow
}

func TestSaveExtractionAnswersWhileMatchingIsLockedAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	const secret = "extraction-shadow-lock-test-secret"
	admin, token := shadowRouteAccount(t, ctx, st, secret)
	job := models.JobInput{Title: "Backend Engineer", Company: "Example GmbH", Description: "Go required."}
	jobID := shadowRouteJob(t, ctx, st, job, "")
	if _, err := st.SaveJobMatch(ctx, admin.ID, jobID, models.Evaluation{Score: 55}, ""); err != nil {
		t.Fatal(err)
	}
	model := "http-shadow-lock-" + jobID
	hash := extraction.Hash(job.Title, job.Description)
	const answer = `{"required":[{"skills":["Go"],"quote":"Go required."}],"nice_to_have":[],"stack":[],"seniority":{"level":"unspecified","quote":null},"min_years":null,"constraints":[]}`
	body, err := json.Marshal(map[string]string{"job_id": jobID, "text_hash": hash, "answer": answer})
	if err != nil {
		t.Fatal(err)
	}
	svc := &discovery.Service{Store: st, ExtractionModel: model, ShadowScoring: true}
	s := &Server{Store: st, Discovery: svc, Secret: secret, AdminEmails: []string{admin.Email}, ExtractionModel: model, ExtractionScoring: "shadow"}
	h := s.Handler()

	unlock := svc.LockMatching(admin.ID)
	done := make(chan struct{})
	var w *httptest.ResponseRecorder
	go func() {
		w = call(t, h, "POST", "/api/extractions", token, string(body))
		close(done)
	}()
	defer func() {
		if unlock != nil {
			unlock()
		}
		<-done
		s.background.Wait()
	}()
	select {
	case <-done:
		if w.Code != 200 || w.Body.String() != `{"status":"ok","dropped":0}`+"\n" {
			t.Fatalf("POST = %d %s, want stored answer with status ok", w.Code, w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("POST blocked on the matching lock")
	}
	var answerID string
	if err := st.DB.QueryRow(ctx, `SELECT id FROM job_extractions WHERE job_id=$1 AND model=$2 AND text_hash=$3 AND status='ok'`, jobID, model, hash).Scan(&answerID); err != nil {
		t.Fatal(err)
	}
	if shadow := readRouteShadow(t, ctx, st, admin.ID, jobID); shadow != nil {
		t.Fatalf("while locked, shadow = %+v, want SQL NULL", shadow)
	}
	unlock()
	unlock = nil
	s.background.Wait()
	if shadow := readRouteShadow(t, ctx, st, admin.ID, jobID); shadow == nil || shadow.ExtractionID != answerID || shadow.TextHash != hash {
		t.Fatalf("after unlock, shadow = %+v, want answer %s with hash %s", shadow, answerID, hash)
	}
}

func TestImportJobUsesShadowForMatchingTextAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	const secret = "manual-import-shadow-test-secret"
	user, token := shadowRouteAccount(t, ctx, st, secret)
	job := models.JobInput{
		Title: "Backend Engineer", Company: "Example GmbH", Description: "Go required.",
		ApplyURL: "https://example.test/jobs/" + user.ID,
	}
	fingerprint := discovery.Fingerprint(job)
	jobID := shadowRouteJob(t, ctx, st, job, fingerprint)
	model := "http-shadow-import-" + jobID
	saveShadowRouteAnswer(t, ctx, st, jobID, model, job)
	svc := &discovery.Service{Store: st, ExtractionModel: model, ShadowScoring: true}
	s := &Server{Store: st, Discovery: svc, Secret: secret, ExtractionModel: model, ExtractionScoring: "shadow"}
	h := s.Handler()
	for _, tc := range []struct {
		name, description string
		wantShadow        bool
	}{
		{"same text", job.Description, true},
		{"changed text", "Go and SQL required.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := job
			input.Description = tc.description
			if discovery.Fingerprint(input) != fingerprint {
				t.Fatal("the import fixture no longer identifies the catalog job")
			}
			body, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			w := call(t, h, "POST", "/api/jobs", token, string(body))
			var saved models.JobWithMatch
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Job.ID != jobID {
				t.Fatalf("POST = %d %s, want catalog job %s", w.Code, w.Body, jobID)
			}
			shadow := readRouteShadow(t, ctx, st, user.ID, jobID)
			if tc.wantShadow {
				if shadow == nil || shadow.ExtractionID == "" || shadow.TextHash != extraction.Hash(job.Title, job.Description) {
					t.Fatalf("stored shadow = %+v, want comparison with the current answer", shadow)
				}
			} else if shadow != nil {
				t.Fatalf("stored shadow = %+v, want SQL NULL", shadow)
			}
		})
	}
}

func TestFilterChangeRescoresShadowAgainstPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	const secret = "filter-shadow-test-secret"
	job := models.JobInput{Title: "Backend Engineer", Company: "Example GmbH", Description: "Go required."}
	answered := shadowRouteJob(t, ctx, st, job, "")
	unanswered := shadowRouteJob(t, ctx, st, models.JobInput{
		Title: "Platform Engineer", Company: "Example GmbH", Description: "Linux required.",
	}, "")
	model := "http-shadow-filter-" + answered
	saveShadowRouteAnswer(t, ctx, st, answered, model, job)
	visible := make([][]models.JobWithMatch, 0, 2)
	for _, mode := range []string{"shadow", "off"} {
		user, token := shadowRouteAccount(t, ctx, st, secret)
		profile := models.Profile{Name: "Alex Example", DesiredTitles: []string{"Backend Engineer"}}
		if err := st.SaveProfile(ctx, user.ID, profile); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{answered, unanswered} {
			if _, err := st.SaveJobMatch(ctx, user.ID, id, models.Evaluation{Score: 55}, ""); err != nil {
				t.Fatal(err)
			}
		}
		svc := &discovery.Service{Store: st, ExtractionModel: model, ShadowScoring: mode == "shadow"}
		s := &Server{Store: st, Discovery: svc, Secret: secret, ExtractionModel: model, ExtractionScoring: mode}
		w := call(t, s.Handler(), "POST", "/api/filters", token, `{"name":"Backend roles","active":true,"excluded_title_words":["Platform"]}`)
		if w.Code != 201 {
			t.Fatalf("POST filter with scoring %s = %d %s", mode, w.Code, w.Body)
		}
		jobs, _, err := st.Jobs(ctx, user.ID, store.JobQuery{})
		if err != nil || len(jobs) != 2 {
			t.Fatalf("Jobs() = %d, %v, want two fixture jobs", len(jobs), err)
		}
		for i := range jobs {
			item := &jobs[i]
			id := item.Job.ID
			if item.Match.Rejected != (id == unanswered) {
				t.Fatalf("job %s rejected = %t after saving the filter", id, item.Match.Rejected)
			}
			shadow := readRouteShadow(t, ctx, st, user.ID, id)
			if mode == "shadow" && id == answered {
				if shadow == nil || shadow.TextHash != extraction.Hash(job.Title, job.Description) || shadow.RulesScore != item.Match.Score {
					t.Fatalf("answered job's shadow = %+v, want comparison with its re-scored match", shadow)
				}
			} else if shadow != nil {
				t.Fatalf("shadow = %+v for job %s with scoring %s, want SQL NULL", shadow, id, mode)
			}
			item.MatchID = ""
		}
		visible = append(visible, jobs)
	}
	if !reflect.DeepEqual(visible[0], visible[1]) {
		t.Fatalf("Jobs() differs with shadow scoring: %+v vs %+v", visible[0], visible[1])
	}
}
