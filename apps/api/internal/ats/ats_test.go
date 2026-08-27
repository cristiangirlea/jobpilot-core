package ats

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDetectRecognisesApplyURLs(t *testing.T) {
	cases := []struct {
		url      string
		provider string
		board    string
		jobID    string
		ok       bool
	}{
		{"https://job-boards.greenhouse.io/gitlab/jobs/8556658002", ProviderGreenhouse, "gitlab", "8556658002", true},
		{"https://boards.greenhouse.io/acme/jobs/12345", ProviderGreenhouse, "acme", "12345", true},
		{"https://job-boards.eu.greenhouse.io/acme/jobs/12345", ProviderGreenhouse, "acme", "12345", true},
		{"https://boards.greenhouse.io/embed/job_app?for=acme&token=999", ProviderGreenhouse, "acme", "999", true},
		{"https://jobs.lever.co/netlify/abc-123", ProviderLever, "netlify", "abc-123", true},
		{"https://jobs.ashbyhq.com/openai/some-id", ProviderAshby, "openai", "some-id", true},
		{"https://jobs.smartrecruiters.com/Acme/74000", ProviderSmartRecruiters, "Acme", "74000", true},
		// An aggregator link reaches no employer form.
		{"https://remotive.com/remote-jobs/software-development/senior-engineer-1", "", "", "", false},
		{"not a url", "", "", "", false},
		{"", "", "", "", false},
	}
	for _, c := range cases {
		provider, board, jobID, ok := Detect(c.url)
		if ok != c.ok || provider != c.provider || board != c.board || jobID != c.jobID {
			t.Errorf("Detect(%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)",
				c.url, provider, board, jobID, ok, c.provider, c.board, c.jobID, c.ok)
		}
	}
}

const greenhousePayload = `{
  "title": "AI Engineer",
  "company_name": "GitLab",
  "questions": [
    {"label":"First Name","required":true,"fields":[{"name":"first_name","type":"input_text"}]},
    {"label":"Resume/CV","required":true,"fields":[{"name":"resume_text","type":"textarea"},{"name":"resume","type":"input_file"}]},
    {"label":"Cover Letter","required":false,"fields":[{"name":"cover_letter","type":"input_file"}]},
    {"label":"What is your current country of residence?","required":true,"fields":[
       {"name":"question_1","type":"multi_value_single_select","values":[
         {"label":"Romania","value":1},{"label":"Portugal","value":2}]}]},
    {"label":"Hidden thing","required":false,"fields":[{"name":"h","type":"input_hidden"}]},
    {"label":"Unknown widget","required":false,"fields":[{"name":"u","type":"space_invaders"}]}
  ],
  "compliance": [{"questions":[
     {"label":"Disability Status","required":false,"fields":[{"name":"d","type":"multi_value_single_select","values":[{"label":"Yes","value":1}]}]}
  ]}],
  "demographic_questions": {"questions":[
     {"label":"Gender","required":false,"fields":[{"name":"g","type":"multi_value_single_select","values":[{"label":"Man","value":1}]}]}
  ]}
}`

func testReader(t *testing.T, handler http.HandlerFunc) (*Reader, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	reader := &Reader{client: server.Client()}
	reader.client.Timeout = 5 * time.Second
	return reader, server
}

// fetchGreenhouseAgainst points the reader at a stub by calling the parser
// directly with a rewritten endpoint.
func fetchGreenhouseAgainst(t *testing.T, body string, status int) (Form, error) {
	t.Helper()
	var form Form
	var err error
	reader, server := testReader(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "questions=true") {
			t.Errorf("the form schema must be requested explicitly, got %q", r.URL.RawQuery)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	var payload struct {
		Title       string               `json:"title"`
		CompanyName string               `json:"company_name"`
		Questions   []greenhouseQuestion `json:"questions"`
		Compliance  []struct {
			Questions []greenhouseQuestion `json:"questions"`
		} `json:"compliance"`
		Demographic struct {
			Questions []greenhouseQuestion `json:"questions"`
		} `json:"demographic_questions"`
	}
	if err = reader.get(context.Background(), server.URL+"?questions=true", &payload); err != nil {
		return Form{}, err
	}
	form = Form{Provider: ProviderGreenhouse, Title: payload.Title, Company: payload.CompanyName}
	for _, question := range payload.Questions {
		if protectedPattern.MatchString(question.Label) {
			form.Skipped = append(form.Skipped, question.Label)
			continue
		}
		field, ok := convertGreenhouse(question)
		if !ok {
			form.Skipped = append(form.Skipped, question.Label)
			continue
		}
		if declarationPattern.MatchString(question.Label) {
			field.Role = RoleDeclaration
		}
		form.Fields = append(form.Fields, field)
	}
	for _, block := range payload.Compliance {
		for _, question := range block.Questions {
			form.Skipped = append(form.Skipped, question.Label)
		}
	}
	for _, question := range payload.Demographic.Questions {
		form.Skipped = append(form.Skipped, question.Label)
	}
	return form, nil
}

func TestGreenhouseFormIsNormalised(t *testing.T) {
	form, err := fetchGreenhouseAgainst(t, greenhousePayload, 200)
	if err != nil {
		t.Fatal(err)
	}
	if form.Title != "AI Engineer" || form.Company != "GitLab" {
		t.Errorf("form = %q at %q", form.Title, form.Company)
	}
	byName := map[string]Field{}
	for _, field := range form.Fields {
		byName[field.Name] = field
	}
	// An upload is preferred over pasted text for a document.
	resume, ok := byName["resume"]
	if !ok || resume.Type != TypeFile || resume.Role != RoleResume {
		t.Errorf("resume field = %+v", resume)
	}
	if _, pasted := byName["resume_text"]; pasted {
		t.Error("only one input per question should be chosen")
	}
	if letter := byName["cover_letter"]; letter.Role != RoleCoverLetter {
		t.Errorf("cover letter role = %q", letter.Role)
	}
	country := byName["question_1"]
	if country.Type != TypeSelect || len(country.Options) != 2 || country.Options[0] != "Romania" {
		t.Errorf("country field = %+v", country)
	}
	if !country.Required {
		t.Error("required flag lost")
	}
}

// Protected-characteristic questions are voluntary and must never be answered
// on someone's behalf. They are reported as skipped so their absence is visible.
func TestDemographicAndComplianceQuestionsAreNeverFields(t *testing.T) {
	form, err := fetchGreenhouseAgainst(t, greenhousePayload, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range form.Fields {
		switch field.Label {
		case "Disability Status", "Gender":
			t.Fatalf("a protected-characteristic question became an answerable field: %+v", field)
		}
	}
	skipped := strings.Join(form.Skipped, "|")
	for _, want := range []string{"Disability Status", "Gender", "Hidden thing", "Unknown widget"} {
		if !strings.Contains(skipped, want) {
			t.Errorf("expected %q to be reported as skipped, got %v", want, form.Skipped)
		}
	}
}

func TestUnreadableResponsesAreReported(t *testing.T) {
	if _, err := fetchGreenhouseAgainst(t, "not json", 200); err == nil {
		t.Error("a malformed response must be an error")
	}
	if _, err := fetchGreenhouseAgainst(t, "", 404); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestFetchRejectsUnsupportedURLs(t *testing.T) {
	reader := NewReader()
	if _, err := reader.Fetch(context.Background(), "https://remotive.com/remote-jobs/x"); err != ErrUnsupported {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
	// A detected but unreadable provider still reports which ATS it is, so the
	// composer can tell the user why it has to be done by hand.
	form, err := reader.Fetch(context.Background(), "https://jobs.lever.co/netlify/abc")
	if err != ErrUnsupported {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
	if form.Provider != ProviderLever {
		t.Errorf("provider = %q, want lever", form.Provider)
	}
}

// Employers do not reliably put protected-characteristic questions in the
// demographic block. Canonical asks for gender, nationality and race as
// ordinary required questions, so labels have to be checked too.
func TestProtectedQuestionsAreSkippedWhereverTheyAppear(t *testing.T) {
	payload := `{"title":"Engineer","company_name":"Acme","questions":[
	  {"label":"First Name","required":true,"fields":[{"name":"first_name","type":"input_text"}]},
	  {"label":"Which gender do you identify as?","required":true,"fields":[{"name":"q1","type":"multi_value_single_select","values":[{"label":"Man","value":1}]}]},
	  {"label":"Please indicate your race or ethnicity:","required":true,"fields":[{"name":"q2","type":"input_text"}]},
	  {"label":"Please indicate your nationality:","required":true,"fields":[{"name":"q3","type":"input_text"}]},
	  {"label":"Do you have a disability?","required":true,"fields":[{"name":"q4","type":"input_text"}]}
	]}`
	form, err := fetchGreenhouseAgainst(t, payload, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range form.Fields {
		if protectedPattern.MatchString(field.Label) {
			t.Errorf("a protected-characteristic question became answerable: %q", field.Label)
		}
	}
	if len(form.Fields) != 1 || form.Fields[0].Name != "first_name" {
		t.Errorf("expected only the name field to survive, got %+v", form.Fields)
	}
	if len(form.Skipped) != 4 {
		t.Errorf("expected 4 skipped questions, got %v", form.Skipped)
	}
}

func TestDeclarationsAreMarkedNotAnswered(t *testing.T) {
	payload := `{"title":"Engineer","company_name":"Acme","questions":[
	  {"label":"During this application process I agree to use only my own words","required":true,"fields":[{"name":"d1","type":"multi_value_single_select","values":[{"label":"I agree","value":1}]}]},
	  {"label":"Please confirm that you have read and agree to our Recruitment Privacy Notice","required":true,"fields":[{"name":"d2","type":"multi_value_single_select","values":[{"label":"Yes","value":1}]}]},
	  {"label":"How many years of Go experience do you have?","required":true,"fields":[{"name":"q","type":"input_text"}]}
	]}`
	form, err := fetchGreenhouseAgainst(t, payload, 200)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{}
	for _, field := range form.Fields {
		roles[field.Name] = field.Role
	}
	if roles["d1"] != RoleDeclaration || roles["d2"] != RoleDeclaration {
		t.Errorf("declarations not marked: %v", roles)
	}
	if roles["q"] != RoleQuestion {
		t.Errorf("an ordinary question was mistaken for a declaration: %v", roles)
	}
}
