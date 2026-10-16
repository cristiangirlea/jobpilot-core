package jobpilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// applyRoutes adds the apply-engine endpoints to the fake API.
func (f *fakeAPI) applyRoutes(m *http.ServeMux, send func(http.ResponseWriter, int, string)) {
	m.HandleFunc("GET /api/ai/status", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, `{"enabled":false,"provider":"none"}`)
	})
	m.HandleFunc("GET /api/answers", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("status") == "pending" {
			send(w, 200, `[{"id":"a-2","question_key":"why_role","question_text":"Why this role?","answer":"Draft text.","role_family":"backend","technologies":["Go"],"source":"ai","approved":false,"usage_count":0}]`)
			return
		}
		send(w, 200, `[{"id":"a-1","question_key":"about_you","question_text":"Tell us about yourself","answer":"I build backends.","role_family":"backend","technologies":["Go"],"source":"human","approved":true,"usage_count":3},{"id":"a-2","question_key":"why_role","question_text":"Why this role?","answer":"Draft text.","role_family":"backend","technologies":["Go"],"source":"ai","approved":false,"usage_count":0}]`)
	})
	m.HandleFunc("POST /api/answers", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.answers = append(f.answers, body)
		f.mu.Unlock()
		approved, _ := body["approved"].(bool)
		send(w, 201, `{"id":"a-new","question_key":"about_you","question_text":"`+body["question_text"].(string)+`","answer":"`+body["answer"].(string)+`","role_family":"general","technologies":[],"source":"human","approved":`+boolJSON(approved)+`,"usage_count":0}`)
	})
	m.HandleFunc("PATCH /api/answers/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.answerPatches = append(f.answerPatches, body)
		f.mu.Unlock()
		approved, _ := body["approved"].(bool)
		send(w, 200, `{"id":"`+r.PathValue("id")+`","question_key":"why_role","question_text":"Why this role?","answer":"Draft text.","role_family":"backend","technologies":[],"source":"human","approved":`+boolJSON(approved)+`,"usage_count":0}`)
	})
	m.HandleFunc("POST /api/jobs/{id}/answers", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "job-b" {
			send(w, 409, `{"error":"application actions are blocked for this rejected job"}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.resolves = append(f.resolves, body)
		f.mu.Unlock()
		send(w, 200, `{"ai_enabled":false,"provider":"none","needs_review":1,"resolutions":[{"question":"Tell us about yourself","question_key":"about_you","answer":"I build backends.","source":"bank","approved":true,"needs_review":false,"entry_id":"a-1","reused":true},{"question":"Why this role?","question_key":"why_role","answer":"Template text.","source":"template","approved":false,"needs_review":true,"entry_id":"a-2","reused":false}]}`)
	})
	m.HandleFunc("GET /api/cv", func(w http.ResponseWriter, r *http.Request) {
		if f.cvListFails.Load() {
			send(w, 503, `{"error":"The database is unavailable."}`)
			return
		}
		sum := sha256.Sum256(approvedPDF)
		send(w, 200, `[{"id":"cv-1","kind":"base","filename":"cv.pdf","format":"pdf","size_bytes":120000,"approved":`+boolJSON(!f.listedCVUnapproved.Load())+`,"fingerprint":"`+hex.EncodeToString(sum[:])+`","created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}]`)
	})
	m.HandleFunc("GET /api/cv/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "cv-1" {
			send(w, 404, `{"error":"Record not found."}`)
			return
		}
		send(w, 200, `{"id":"cv-1","kind":"base","filename":"cv.pdf","format":"pdf","size_bytes":120000,"extracted_text":"Octavian Pirvan, backend engineer.","parsed":{"name":"Octavian Pirvan","warnings":["Could not read the education section"]},"approved":true,"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`)
	})
	m.HandleFunc("POST /api/cv", func(w http.ResponseWriter, r *http.Request) {
		if r.ParseMultipartForm(1<<20) != nil {
			send(w, 400, `{"error":"Upload a CV file as multipart form data under the field name \"file\"."}`)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			send(w, 400, `{"error":"No file was uploaded under the field name \"file\"."}`)
			return
		}
		data, _ := io.ReadAll(file)
		_ = file.Close()
		if header.Filename == "scanned.pdf" {
			send(w, 400, `{"error":"This PDF is a scanned image with no text layer; there is no OCR."}`)
			return
		}
		f.mu.Lock()
		f.cvUploads = append(f.cvUploads, cvUpload{filename: header.Filename, contentType: header.Header.Get("Content-Type"), data: data,
			label: r.FormValue("label"), fields: len(r.MultipartForm.File) + len(r.MultipartForm.Value), auth: r.Header.Get("Authorization")})
		f.mu.Unlock()
		send(w, 201, `{"id":"cv-up","kind":"base","label":"`+r.FormValue("label")+`","filename":"`+header.Filename+`","format":"pdf","size_bytes":`+strconv.Itoa(len(data))+`,"extracted_text":"Ana Test, platform engineer.","parsed":{"name":"Ana Test","current_title":"Platform Engineer","warnings":["No phone number was found."],"text":"Ana Test, platform engineer."},"role_family":"platform","approved":false,"created_at":"2026-10-16T00:00:00Z","updated_at":"2026-10-16T00:00:00Z"}`)
	})
	m.HandleFunc("POST /api/cv/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			send(w, 400, `{"error":"Invalid JSON request: EOF"}`)
			return
		}
		f.mu.Lock()
		f.cvApprovals = append(f.cvApprovals, body)
		f.mu.Unlock()
		approved, _ := body["approved"].(bool)
		send(w, 200, `{"id":"cv-1","kind":"base","filename":"cv.pdf","format":"pdf","size_bytes":120000,"approved":`+boolJSON(approved)+`,"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-02T00:00:00Z"}`)
	})
	m.HandleFunc("POST /api/jobs/{id}/cv", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "job-b" {
			send(w, 409, `{"error":"application actions are blocked for this rejected job"}`)
			return
		}
		send(w, 201, `{"decision":"generated","reason":"The base CV leans frontend","alignment":0.72,"approved":false,"highlighted":["Go","PostgreSQL"],"missing":["Kafka"],"cv":{"id":"cv-2","kind":"tailored","filename":"tailored.pdf","format":"pdf","size_bytes":90000,"approved":false,"parent_id":"cv-1","created_at":"2026-09-20T00:00:00Z","updated_at":"2026-09-20T00:00:00Z"}}`)
	})
	m.HandleFunc("POST /api/jobs/{id}/compose", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "job-b" {
			send(w, 409, `{"error":"application actions are blocked for this rejected job"}`)
			return
		}
		send(w, 201, outboxEntry)
	})
	m.HandleFunc("GET /api/outbox", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, "["+outboxEntry+"]")
	})
	m.HandleFunc("GET /api/outbox/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "ob-1" {
			send(w, 404, `{"error":"Record not found."}`)
			return
		}
		send(w, 200, outboxEntry)
	})
	m.HandleFunc("POST /api/outbox/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, strings.Replace(outboxEntry, `"status":"needs_review"`, `"status":"cancelled"`, 1))
	})
	m.HandleFunc("POST /api/discovery/detect", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.Contains(body["url"], "greenhouse") {
			send(w, 422, `{"error":"That link does not look like a supported job board."}`)
			return
		}
		send(w, 200, `{"provider":"greenhouse","identifier":"grafanalabs","supported":true}`)
	})
	m.HandleFunc("GET /api/discovery/suggestions", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, `[{"provider":"greenhouse","identifier":"grafanalabs","name":"Grafana Labs","note":"Remote-friendly","remote_friendly":true}]`)
	})
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// outboxEntry carries a blocker and an unanswered declaration, which is the
// shape that matters: an entry the user still has to act on.
const outboxEntry = `{"id":"ob-1","job_id":"job-a","application_id":"app-1","status":"needs_review","channel":"ats_form","ats_provider":"greenhouse","cv_document_id":"cv-2","cv_decision":"generated","cover_letter":"Dear Fresh Systems","cover_letter_source":"template","form":{"provider":"greenhouse","fields":[{"name":"first_name"}],"cv_preview":{"reason":"The base CV leans frontend","alignment":0.72}},"answers":[{"field":"first_name","label":"First name","type":"text","required":true,"answer":"Octavian","source":"profile","needs_review":false},{"field":"consent","label":"I certify the above is true","type":"text","required":true,"answer":"","source":"declaration","needs_review":true}],"blockers":["You have to make this declaration yourself on the employer's form: \"I certify the above is true\""],"notices":["The cover letter used the deterministic template"],"needs_review":1,"created_at":"2026-09-20T00:00:00Z","updated_at":"2026-09-20T00:00:00Z","job":{"id":"job-a","title":"Senior Backend Engineer","company":"Fresh Systems GmbH","location":"Bucharest, Romania","remote_type":"remote","description":"Long description A about Go and PostgreSQL.","apply_url":"https://example.test/apply-a","created_at":"2026-09-11T00:00:00Z"}}`

func TestApplyToolCatalog(t *testing.T) {
	cs, _ := session(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
		if tool.Title == "" || tool.Description == "" {
			t.Errorf("%s: needs a title and a description", tool.Name)
		}
	}
	for _, want := range []string{
		"get_ai_status", "list_answers", "resolve_screening_answers", "save_answer", "approve_answer",
		"list_cvs", "get_cv", "upload_cv", "approve_cv", "tailor_cv_for_job",
		"compose_application", "list_outbox", "get_outbox_entry", "cancel_outbox_entry",
		"detect_board", "list_board_suggestions",
	} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
	// Approving an outbox entry is consent to send, and that belongs to the
	// user in the web app. Uploading and deleting stay out too, with one
	// exception the owner chose: upload_cv puts a CV into the user's own
	// JobPilot, unapproved, from the one folder they allowed. It sends
	// nothing to an employer.
	for name := range names {
		if name == "upload_cv" {
			continue
		}
		if strings.Contains(name, "approve_outbox") || strings.HasPrefix(name, "send") || strings.HasPrefix(name, "upload") || strings.HasPrefix(name, "delete") {
			t.Errorf("%s must not be exposed", name)
		}
	}
}

func TestInstructionsKeepTheOutboxGateWithTheUser(t *testing.T) {
	for _, want := range []string{"web app", "Never describe", "needs_review"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("server instructions should mention %q", want)
		}
	}
	if !strings.Contains(instructions, "does not send") && !strings.Contains(instructions, "Nothing here submits") {
		t.Error("server instructions must still say nothing here submits")
	}
}

func TestResolveScreeningAnswers(t *testing.T) {
	cs, fake := session(t)

	res, text := call(t, cs, "resolve_screening_answers", map[string]any{"job_id": "job-a", "questions": []any{}})
	if !res.IsError || !strings.Contains(text, "at least one") {
		t.Fatalf("an empty question list must be refused: %s", text)
	}
	many := make([]any, 51)
	for i := range many {
		many[i] = map[string]any{"text": "Q"}
	}
	res, text = call(t, cs, "resolve_screening_answers", map[string]any{"job_id": "job-a", "questions": many})
	if !res.IsError || !strings.Contains(text, "at most 50") {
		t.Fatalf("over the cap must be refused: %s", text)
	}
	if len(fake.resolves) != 0 {
		t.Fatal("neither refusal should have reached the API")
	}

	res, text = call(t, cs, "resolve_screening_answers", map[string]any{"job_id": "job-b", "questions": []any{map[string]any{"text": "Why us?"}}})
	if !res.IsError || !strings.Contains(text, "blocked") {
		t.Fatalf("a rejected job must be refused: %s", text)
	}

	res, text = call(t, cs, "resolve_screening_answers", map[string]any{"job_id": "job-a", "questions": []any{
		map[string]any{"text": "Tell us about yourself"},
		map[string]any{"text": "Why this role?", "kind": "textarea", "max_length": 500},
	}})
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[ResolvedAnswers](t, res)
	if len(got.Resolutions) != 2 || got.NeedsReview != 1 || got.AIEnabled {
		t.Fatalf("unexpected resolution: %s", text)
	}
	if got.Resolutions[0].Source != "bank" || !got.Resolutions[1].NeedsReview {
		t.Fatalf("provenance must survive: %+v", got.Resolutions)
	}
}

func TestSaveAnswerIsUnapprovedUnlessConfirmed(t *testing.T) {
	cs, fake := session(t)
	res, text := call(t, cs, "save_answer", map[string]any{"question_text": "Why us?", "answer": "Because."})
	if res.IsError {
		t.Fatal(text)
	}
	if len(fake.answers) != 1 {
		t.Fatalf("expected one save, got %d", len(fake.answers))
	}
	// The API treats a hand-written answer as self-approving when the field
	// is absent, so the agent must always send it.
	if approved, sent := fake.answers[0]["approved"]; !sent || approved != false {
		t.Fatalf("an agent's answer must be saved unapproved and explicitly so, sent %v", fake.answers[0])
	}
	if got := structured[AnswerEntry](t, res); got.Approved {
		t.Fatal("the saved answer should come back unapproved")
	}

	if _, text = call(t, cs, "save_answer", map[string]any{"question_text": "Why us?", "answer": "Because.", "approved": true}); text == "" {
		t.Fatal("expected a result")
	}
	if fake.answers[1]["approved"] != true {
		t.Fatalf("an explicit approval must be passed through, sent %v", fake.answers[1])
	}

	res, text = call(t, cs, "save_answer", map[string]any{"question_text": "", "answer": "x"})
	if !res.IsError || !strings.Contains(text, "question_text") {
		t.Fatalf("a blank question must be refused: %s", text)
	}
}

func TestApproveAnswerDefaultsToApproving(t *testing.T) {
	cs, fake := session(t)
	if res, text := call(t, cs, "approve_answer", map[string]any{"answer_id": "a-2"}); res.IsError {
		t.Fatal(text)
	}
	if fake.answerPatches[0]["approved"] != true {
		t.Fatalf("approve_answer should approve by default, sent %v", fake.answerPatches[0])
	}
	no := false
	if res, text := call(t, cs, "approve_answer", map[string]any{"answer_id": "a-2", "approved": no}); res.IsError {
		t.Fatal(text)
	}
	if fake.answerPatches[1]["approved"] != false {
		t.Fatalf("approval can be withdrawn, sent %v", fake.answerPatches[1])
	}
}

func TestAnswerScopeIsPassedThrough(t *testing.T) {
	cs, fake := session(t)
	if res, text := call(t, cs, "save_answer", map[string]any{"question_text": "Why us?", "answer": "Because.", "scope": "job", "job_id": "job-a"}); res.IsError {
		t.Fatal(text)
	}
	if fake.answers[0]["scope"] != "job" || fake.answers[0]["job_id"] != "job-a" {
		t.Fatalf("the scope and its job must be sent, sent %v", fake.answers[0])
	}
	if res, text := call(t, cs, "save_answer", map[string]any{"question_text": "Why us?", "answer": "Because."}); res.IsError {
		t.Fatal(text)
	}
	if _, sent := fake.answers[1]["scope"]; sent {
		t.Fatalf("no scope means the API's default, sent %v", fake.answers[1])
	}
	if res, text := call(t, cs, "approve_answer", map[string]any{"answer_id": "a-2", "scope": "global"}); res.IsError {
		t.Fatal(text)
	}
	if fake.answerPatches[0]["scope"] != "global" || fake.answerPatches[0]["approved"] != true {
		t.Fatalf("approving for every job, sent %v", fake.answerPatches[0])
	}
	// Narrowing a draft to its own job needs no job_id: the API knows it.
	if res, text := call(t, cs, "approve_answer", map[string]any{"answer_id": "a-2", "scope": "job"}); res.IsError {
		t.Fatal(text)
	}

	for _, bad := range []map[string]any{
		{"question_text": "Why us?", "answer": "Because.", "scope": "job"},
		{"question_text": "Why us?", "answer": "Because.", "scope": "everywhere"},
	} {
		if res, text := call(t, cs, "save_answer", bad); !res.IsError || !(strings.Contains(text, "job_id") || strings.Contains(text, "scope must be")) {
			t.Fatalf("%v must be refused: %s", bad, text)
		}
	}
	if len(fake.answers) != 2 || len(fake.answerPatches) != 2 {
		t.Fatal("refusals must not reach the API")
	}
}

// An API from before scopes ignores the field, so a narrower scope would be
// reused more widely than the user chose; it is refused instead.
func TestAnswerScopeIsRefusedAgainstAnOldAPI(t *testing.T) {
	cs, fake := sessionAgainstOldAPI(t)
	for _, tool := range []struct {
		name string
		args map[string]any
	}{
		{"save_answer", map[string]any{"question_text": "Why us?", "answer": "Because.", "scope": "job", "job_id": "job-a"}},
		{"approve_answer", map[string]any{"answer_id": "a-2", "scope": "global"}},
	} {
		if res, text := call(t, cs, tool.name, tool.args); !res.IsError || !strings.Contains(text, "predates answer scopes") {
			t.Fatalf("%s: expected a refusal, got %s", tool.name, text)
		}
	}
	if len(fake.answers) != 0 || len(fake.answerPatches) != 0 {
		t.Fatal("the refusals must happen before any request")
	}
	if res, text := call(t, cs, "save_answer", map[string]any{"question_text": "Why us?", "answer": "Because."}); res.IsError {
		t.Fatalf("an answer without a scope still works on an old API: %s", text)
	}
}

// A server that could not check the API at startup checks before sending a
// scope, so an old API that came up later is still caught.
func TestAnswerScopeChecksAnUncheckedAPI(t *testing.T) {
	cs, fake := sessionUnchecked(t, true)
	if res, text := call(t, cs, "approve_answer", map[string]any{"answer_id": "a-2", "scope": "job"}); !res.IsError || !strings.Contains(text, "predates answer scopes") {
		t.Fatalf("an old API found late must still refuse a scope, got %s", text)
	}
	if len(fake.answerPatches) != 0 {
		t.Fatal("the refusal must happen before any request")
	}

	cs, fake = sessionUnchecked(t, false)
	if res, text := call(t, cs, "approve_answer", map[string]any{"answer_id": "a-2", "scope": "job"}); res.IsError {
		t.Fatalf("a current API found late must accept a scope: %s", text)
	}
	if len(fake.answerPatches) != 1 || fake.answerPatches[0]["scope"] != "job" {
		t.Fatalf("the scope must be sent, got %v", fake.answerPatches)
	}
}

func TestCVTools(t *testing.T) {
	cs, fake := session(t)

	res, text := call(t, cs, "list_cvs", nil)
	if got := structured[CVList](t, res); len(got.CVs) != 1 || got.CVs[0].ExtractedText != "" {
		t.Fatalf("listings must omit the CV text: %s", text)
	}

	res, text = call(t, cs, "get_cv", map[string]any{"cv_id": "cv-1"})
	got := structured[CVDocument](t, res)
	if got.ExtractedText == "" || got.Parsed["warnings"] == nil {
		t.Fatalf("get_cv must return the text and the parse warnings: %s", text)
	}

	// The endpoint decodes a body, so a bodiless POST would be a 400.
	if res, text = call(t, cs, "approve_cv", map[string]any{"cv_id": "cv-1"}); res.IsError {
		t.Fatal(text)
	}
	if len(fake.cvApprovals) != 1 || fake.cvApprovals[0]["approved"] != true {
		t.Fatalf("approve_cv must send a body, sent %v", fake.cvApprovals)
	}

	res, text = call(t, cs, "tailor_cv_for_job", map[string]any{"job_id": "job-a"})
	tailored := structured[TailoredCV](t, res)
	if tailored.Decision != "generated" || tailored.Approved || tailored.CV.ParentID != "cv-1" {
		t.Fatalf("a newly written CV must be unapproved and linked to its base: %s", text)
	}
	if res, text = call(t, cs, "tailor_cv_for_job", map[string]any{"job_id": "job-b"}); !res.IsError || !strings.Contains(text, "blocked") {
		t.Fatalf("a rejected job must be refused: %s", text)
	}
}

func TestOutboxTools(t *testing.T) {
	cs, _ := session(t)

	res, text := call(t, cs, "compose_application", map[string]any{"job_id": "job-a"})
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[OutboxDetail](t, res)
	if got.Status != "needs_review" || len(got.Blockers) != 1 {
		t.Fatalf("a composed entry must carry its status and blockers: %s", text)
	}
	if got.CoverLetter == "" || len(got.Answers) != 2 {
		t.Fatalf("the detail must include the letter and every answer: %s", text)
	}
	if got.CVPreview == nil || got.CVPreview["reason"] == nil {
		t.Fatalf("the CV decision preview should survive: %s", text)
	}
	// The raw ATS form description is large and of no use to a reader.
	if strings.Contains(text, `"fields"`) {
		t.Fatalf("the raw form must not be returned: %s", text)
	}
	if strings.Contains(text, "Long description A") {
		t.Fatal("the job description must not be echoed into the outbox detail")
	}

	if res, text = call(t, cs, "compose_application", map[string]any{"job_id": "job-b"}); !res.IsError || !strings.Contains(text, "blocked") {
		t.Fatalf("a rejected job must be refused: %s", text)
	}

	res, text = call(t, cs, "list_outbox", nil)
	list := structured[OutboxList](t, res)
	if len(list.Entries) != 1 || list.Entries[0].Job == nil || len(list.Entries[0].Blockers) != 1 {
		t.Fatalf("unexpected outbox listing: %s", text)
	}

	if res, text = call(t, cs, "get_outbox_entry", map[string]any{"outbox_id": "nope"}); !res.IsError || !strings.Contains(text, "404") {
		t.Fatalf("a missing entry must surface the API error: %s", text)
	}

	res, text = call(t, cs, "cancel_outbox_entry", map[string]any{"outbox_id": "ob-1"})
	if cancelled := structured[OutboxDetail](t, res); cancelled.Status != "cancelled" {
		t.Fatalf("cancelling should report the new status: %s", text)
	}
}

func TestBoardTools(t *testing.T) {
	cs, _ := session(t)

	res, text := call(t, cs, "detect_board", map[string]any{"url": "https://job-boards.greenhouse.io/grafanalabs"})
	if got := structured[DetectedBoard](t, res); got.Provider != "greenhouse" || got.Identifier != "grafanalabs" || !got.Supported {
		t.Fatalf("unexpected detection: %s", text)
	}
	if res, text = call(t, cs, "detect_board", map[string]any{"url": "https://example.test/careers"}); !res.IsError || !strings.Contains(text, "supported job board") {
		t.Fatalf("the API's own message should be surfaced: %s", text)
	}
	if res, text = call(t, cs, "detect_board", map[string]any{"url": "  "}); !res.IsError || !strings.Contains(text, "url is required") {
		t.Fatalf("a blank url must be refused locally: %s", text)
	}

	res, text = call(t, cs, "list_board_suggestions", nil)
	if got := structured[BoardSuggestionList](t, res); len(got.Suggestions) != 1 || !got.Suggestions[0].RemoteFriendly {
		t.Fatalf("unexpected suggestions: %s", text)
	}

	res, text = call(t, cs, "get_ai_status", nil)
	if got := structured[AIStatus](t, res); got.Enabled || got.Provider != "none" {
		t.Fatalf("unexpected AI status: %s", text)
	}
}
