package sender

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"jobpilot/core/api/internal/generator"
	"jobpilot/core/api/internal/models"
)

// formPage is a plain application form like the one a CAPTCHA-free employer
// serves: a hidden token, text fields, a select whose option values are ids,
// the CV upload, a cover letter box, and a declaration left to the applicant.
const formPage = `<!doctype html><html><body>
<form id="search" method="get" action="/search"><input name="q"></form>
<form method="post" action="/fakeco/jobs/1/applications" enctype="multipart/form-data">
  <input type="hidden" name="token" value="t-123">
  <input name="first_name" required>
  <input name="email" type="email" required>
  <select name="question_9"><option value="">Choose</option><option value="901">Yes</option><option value="902">No</option></select>
  <input type="file" name="resume" required>
  <textarea name="cover_letter"></textarea>
  <input type="checkbox" name="question_7" value="1">
  <input name="disabled_one" disabled required>
  <button type="submit" name="go">Submit</button>
</form></body></html>`

func answers() []models.OutboxAnswer {
	return []models.OutboxAnswer{
		{Field: "first_name", Label: "First Name", Answer: "Ana", Source: "profile", Required: true},
		{Field: "email", Label: "Email", Answer: "ana@example.test", Source: "profile", Required: true},
		{Field: "question_9", Label: "Do you need sponsorship?", Answer: "No", Source: "profile", Type: "select"},
		{Field: "resume", Label: "Resume/CV", Answer: "(the approved CV is attached)", Source: "cv", Required: true},
		{Field: "cover_letter", Label: "Cover Letter", Answer: "(the composed cover letter is attached)", Source: "cover_letter"},
		{Field: "question_7", Label: "I confirm the wording is my own", Source: "declaration"},
	}
}

// employer serves formPage (or page) at /fakeco/jobs/1 and records each post.
type employer struct {
	mu       sync.Mutex
	page     string
	status   int
	reply    string
	location string
	posts    []*http.Request
	parts    map[string]string
	files    map[string]string
	agent    string
	server   *httptest.Server
}

func newEmployer(t *testing.T) *employer {
	e := &employer{page: formPage, status: http.StatusOK, reply: "<p>Thank you for applying.</p>"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /fakeco/jobs/1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, e.page)
	})
	mux.HandleFunc("POST /fakeco/jobs/1/applications", func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.posts = append(e.posts, r)
		e.agent = r.UserAgent()
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		e.parts, e.files = map[string]string{}, map[string]string{}
		for name, values := range r.MultipartForm.Value {
			e.parts[name] = values[0]
		}
		for name, headers := range r.MultipartForm.File {
			f, _ := headers[0].Open()
			data, _ := io.ReadAll(f)
			e.files[name] = headers[0].Filename + ":" + string(data)
		}
		if e.location != "" {
			w.Header().Set("Location", e.location)
		}
		w.WriteHeader(e.status)
		_, _ = io.WriteString(w, e.reply)
	})
	e.server = httptest.NewServer(mux)
	t.Cleanup(e.server.Close)
	return e
}

func (e *employer) posted() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.posts)
}

func submission(before *int) Submission {
	return Submission{
		Board: "fakeco", JobID: "1", Answers: answers(), CoverLetter: "Dear team,\nI would like to apply.",
		CV:         &File{Name: "ana-cv.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.4 ana")},
		BeforePost: func() error { *before++; return nil },
	}
}

func TestSubmitFillsTheFormAndPostsIt(t *testing.T) {
	e := newEmployer(t)
	before := 0
	if err := NewGreenhouse(e.server.URL).Submit(context.Background(), submission(&before)); err != nil {
		t.Fatalf("a plain form with every answer should send: %v", err)
	}
	if before != 1 || e.posted() != 1 {
		t.Fatalf("expected the attempt recorded once and one post, got %d and %d", before, e.posted())
	}
	want := map[string]string{
		"token": "t-123", "first_name": "Ana", "email": "ana@example.test",
		"question_9": "902", "cover_letter": "Dear team,\nI would like to apply.",
	}
	for name, value := range want {
		if e.parts[name] != value {
			t.Errorf("field %s: got %q, want %q", name, e.parts[name], value)
		}
	}
	if len(e.parts) != len(want) {
		t.Errorf("only the answers and the page's hidden values are sent, got %v", e.parts)
	}
	if e.files["resume"] != "ana-cv.pdf:%PDF-1.4 ana" {
		t.Errorf("the approved CV should be uploaded as the resume, got %q", e.files["resume"])
	}
	if !strings.HasPrefix(e.agent, "JobPilot") {
		t.Errorf("the sender must say what it is, got user agent %q", e.agent)
	}
}

func TestInterviewAnswersMatchSentForm(t *testing.T) {
	for _, tc := range []struct {
		name, source, value string
		required, review    bool
		want                bool
	}{
		{"profile", "profile", " Exact\n", false, false, true},
		{"bank", "bank", " Exact\n", false, false, true},
		{"human", "human", " Exact\n", false, false, true},
		{"AI draft", "ai", "Draft", false, true, false},
		{"template draft", "template", "Draft", false, true, false},
		{"required AI checked at approval", "ai", "Reviewed", true, true, true},
		{"required template checked at approval", "template", "Reviewed", true, true, true},
		{"reviewed AI", "ai", "Reviewed", false, false, true},
		{"reviewed template", "template", "Reviewed", false, false, true},
		{"declaration", "declaration", "Yes", true, false, false},
		{"personal", "personal", "Yes", true, false, false},
		{"upload", "upload", "File", true, false, false},
		{"blank", "human", " \n\t", false, false, false},
		{"empty", "profile", "", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEmployer(t)
			e.page = strings.Replace(formPage, `<textarea name="cover_letter">`, `<textarea name="value"></textarea><textarea name="cover_letter">`, 1)
			before := 0
			sub := submission(&before)
			sub.Answers = append(sub.Answers, models.OutboxAnswer{Field: "value", Source: tc.source, Answer: tc.value, Required: tc.required, NeedsReview: tc.review})
			if err := NewGreenhouse(e.server.URL).Submit(context.Background(), sub); err != nil {
				t.Fatal(err)
			}
			pack, err := generator.PrepareInterview(models.Application{Status: "interview"}, models.Profile{}, models.JobWithMatch{}, &models.InterviewDocuments{Answers: sub.Answers})
			if err != nil {
				t.Fatal(err)
			}
			_, sent := e.parts["value"]
			included := false
			for _, answer := range pack.SentDocuments.Answers {
				if answer.Field == "value" {
					included = true
					if answer.Answer != e.parts["value"] {
						t.Fatalf("pack and form must preserve the same wording: %q vs %q", answer.Answer, e.parts["value"])
					}
				}
			}
			if sent != tc.want || included != sent {
				t.Fatalf("want sent=%t, form sent=%t, pack included=%t", tc.want, sent, included)
			}
		})
	}
}

func TestSubmitHandsBackWhatItMayNotOrCannotFill(t *testing.T) {
	cases := []struct {
		name   string
		page   string
		edit   func(*Submission)
		wantIn string
	}{
		{name: "reCAPTCHA", page: strings.Replace(formPage, "</form>", `<script src="https://www.recaptcha.net/recaptcha/enterprise.js"></script></form>`, 1), wantIn: "CAPTCHA"},
		{name: "hCaptcha", page: strings.Replace(formPage, "</form>", `<div class="h-captcha" data-sitekey="x"></div></form>`, 1), wantIn: "CAPTCHA"},
		{name: "Turnstile", page: strings.Replace(formPage, "</form>", `<div class="cf-turnstile"></div></form>`, 1), wantIn: "CAPTCHA"},
		{name: "a script-built form", page: `<html><body><div id="app"></div><script src="/app.js"></script></body></html>`, wantIn: "not a plain web form"},
		{name: "a question the form no longer has", page: strings.Replace(formPage, `name="email"`, `name="email_address"`, 1), wantIn: `"Email"`},
		{name: "an answer the select does not offer", edit: func(s *Submission) { s.Answers[2].Answer = "Maybe" }, wantIn: "as a choice"},
		{name: "a required field the entry does not carry", page: strings.Replace(formPage, `<textarea name="cover_letter">`, `<input name="phone" required><textarea name="cover_letter">`, 1), wantIn: `"phone"`},
		{name: "a form posting to another site", page: strings.Replace(formPage, `action="/fakeco/jobs/1/applications"`, `action="https://elsewhere.example/collect"`, 1), wantIn: "another site"},
		{name: "no CV to attach", edit: func(s *Submission) { s.CV = nil }, wantIn: "CV"},
		{name: "the CV slot is not an upload", page: strings.Replace(formPage, `type="file" name="resume"`, `name="resume"`, 1), wantIn: "as a file"},
		{name: "no job named", edit: func(s *Submission) { s.JobID = "" }, wantIn: "which Greenhouse job"},
		{name: "a page too large to read in full", page: formPage + "<!--" + strings.Repeat("x", pageLimit) + " captcha -->", wantIn: "too large"},
		{name: "a page nested too deep to parse", page: tooDeep(formPage), wantIn: "could not be read"},
		{name: "the attempt cannot be recorded", edit: func(s *Submission) {
			s.BeforePost = func() error { return errors.New("the application is no longer being sent") }
		}, wantIn: "stopped before anything reached the employer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEmployer(t)
			if c.page != "" {
				e.page = c.page
			}
			before := 0
			sub := submission(&before)
			if c.edit != nil {
				c.edit(&sub)
			}
			err := NewGreenhouse(e.server.URL).Submit(context.Background(), sub)
			var handBack HandBack
			if !errors.As(err, &handBack) || !strings.Contains(err.Error(), c.wantIn) {
				t.Fatalf("expected a hand-back mentioning %q, got %v", c.wantIn, err)
			}
			if e.posted() != 0 {
				t.Fatal("nothing may be posted when the entry is handed back")
			}
		})
	}
}

func TestSubmitReadsTheEmployersAnswer(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		reply    string
		location string
		sent     bool
		unknown  bool
	}{
		{name: "a confirmation", status: 200, reply: "<p>Thanks!</p>", sent: true},
		{name: "a redirect to a confirmation", status: 303, location: "/fakeco/jobs/1/confirmation", sent: true},
		{name: "a redirect with no destination", status: 303, unknown: true},
		{name: "a redirect back to the form", status: 302, location: "/fakeco/jobs/1?error=true", unknown: true},
		{name: "a redirect back to the post", status: 302, location: "/fakeco/jobs/1/applications/", unknown: true},
		{name: "a challenge in answer", status: 200, reply: `<div class="g-recaptcha"></div>`, unknown: true},
		{name: "an answer too large to read", status: 200, reply: strings.Repeat("x", pageLimit+1), unknown: true},
		{name: "the form again, with errors", status: 200, reply: formPage},
		{name: "an answer nested too deep to parse", status: 200, reply: tooDeep(formPage), unknown: true},
		{name: "a refusal", status: 422},
		{name: "rate limited", status: 429},
		{name: "a server error", status: 502, unknown: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEmployer(t)
			e.status, e.reply, e.location = c.status, c.reply, c.location
			before := 0
			err := NewGreenhouse(e.server.URL).Submit(context.Background(), submission(&before))
			var handBack HandBack
			var unknown Unknown
			switch {
			case c.sent && err != nil:
				t.Fatalf("expected sent, got %v", err)
			case c.unknown && !errors.As(err, &unknown):
				t.Fatalf("expected an unknown outcome, got %v", err)
			case !c.sent && !c.unknown && !errors.As(err, &handBack):
				t.Fatalf("expected a hand-back, got %v", err)
			}
			if e.posted() != 1 || before != 1 {
				t.Fatalf("expected one post after recording it, got %d posts and %d records", e.posted(), before)
			}
		})
	}
}

// A draft a model or the template wrote is sent only once the user approved
// it: approval checks the required ones, so an optional draft stays unsent.
func TestSubmitLeavesOptionalUnreviewedDraftsOut(t *testing.T) {
	e := newEmployer(t)
	e.page = strings.Replace(formPage, `<textarea name="cover_letter">`, `<textarea name="question_why"></textarea><textarea name="question_more"></textarea><textarea name="cover_letter">`, 1)
	before := 0
	sub := submission(&before)
	sub.Answers = append(sub.Answers,
		models.OutboxAnswer{Field: "question_why", Label: "Why us?", Answer: "A drafted reason.", Source: "ai", NeedsReview: true},
		models.OutboxAnswer{Field: "question_more", Label: "Anything else?", Answer: "Template wording.", Source: "template", NeedsReview: true},
		models.OutboxAnswer{Field: "question_9", Label: "Do you need sponsorship?", Answer: "No", Source: "ai", NeedsReview: true, Required: true},
	)
	sub.Answers = append(sub.Answers[:2], sub.Answers[3:]...) // the required AI answer replaces the profile one
	if err := NewGreenhouse(e.server.URL).Submit(context.Background(), sub); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.parts["question_why"]; ok {
		t.Error("an optional AI draft nobody approved must not be sent")
	}
	if _, ok := e.parts["question_more"]; ok {
		t.Error("optional template wording nobody approved must not be sent")
	}
	if e.parts["question_9"] != "902" {
		t.Errorf("a required AI answer passed approval's bank check and is sent, got %q", e.parts["question_9"])
	}
}

func TestSubmitTreatsADroppedConnectionAsUnknown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /fakeco/jobs/1", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, formPage) })
	mux.HandleFunc("POST /fakeco/jobs/1/applications", func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	before := 0
	err := NewGreenhouse(server.URL).Submit(context.Background(), submission(&before))
	var unknown Unknown
	if !errors.As(err, &unknown) {
		t.Fatalf("a post with no answer may have arrived and must read as unknown, got %v", err)
	}
}

func TestSubmitHandsBackAFormPageThatIsGoneOrMoved(t *testing.T) {
	for status, wantIn := range map[int]string{404: "is gone", 302: "moved", 503: "answered 503"} {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Error("nothing may be posted")
			}
			if status == 302 {
				w.Header().Set("Location", "https://careers.example.test/apply")
			}
			w.WriteHeader(status)
		})
		server := httptest.NewServer(mux)
		before := 0
		err := NewGreenhouse(server.URL).Submit(context.Background(), submission(&before))
		server.Close()
		var handBack HandBack
		if !errors.As(err, &handBack) || !strings.Contains(err.Error(), wantIn) || before != 0 {
			t.Fatalf("%d: expected a hand-back mentioning %q before any record, got %v", status, wantIn, err)
		}
	}
}

func TestGreenhouseFormsLiveOnTheHostedBoard(t *testing.T) {
	for base, want := range map[string]string{
		"":                                 "https://job-boards.greenhouse.io",
		"https://boards-api.greenhouse.io": "https://job-boards.greenhouse.io",
		"http://localhost:18999/":          "http://localhost:18999",
	} {
		if got := NewGreenhouse(base).origin; got != want {
			t.Errorf("%q: got %q, want %q", base, got, want)
		}
	}
}

// tooDeep wraps a page in more nested elements than html.Parse accepts (512).
func tooDeep(page string) string {
	return strings.Repeat("<div>", 600) + page + strings.Repeat("</div>", 600)
}
