package ats

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ashbyPayload mirrors what the public job page's GraphQL call returns: one
// field of every type seen on real postings, a deactivated field, a protected
// question, a consent box and an equal-opportunity survey.
const ashbyPayload = `{"data":{"jobPosting":{
  "title":"Platform Engineer",
  "applicationForm":{"sections":[
    {"fieldEntries":[
      {"isRequired":true,"field":{"path":"_systemfield_name","title":"Name","type":"String"}},
      {"isRequired":true,"field":{"path":"_systemfield_email","title":"Email","type":"Email"}},
      {"isRequired":true,"field":{"path":"phone-1","title":"Phone number","type":"Phone"}},
      {"isRequired":true,"field":{"path":"_systemfield_location","title":"Where are you currently based?","type":"Location"}},
      {"isRequired":true,"field":{"path":"_systemfield_resume","title":"Resume","type":"File"}},
      {"isRequired":false,"field":{"path":"letter-1","title":"Cover letter","type":"File"}},
      {"isRequired":true,"field":{"path":"transcript-1","title":"Transcript of records","type":"File"}},
      {"isRequired":false,"field":{"path":"linkedin-1","title":"Link to your CV or LinkedIn","type":"Url"}},
      {"isRequired":false,"field":{"path":"old-1","title":"Old question","type":"String","isDeactivated":true}}
    ]},
    {"fieldEntries":[
      {"isRequired":true,"field":{"path":"relocate-1","title":"Willing to relocate","type":"Boolean"}},
      {"isRequired":true,"field":{"path":"auth-1","title":"Which best describes your work authorisation?","type":"ValueSelect",
        "selectableValues":[{"label":"EU citizen","value":"a"},{"label":"Need a visa","value":"b"}]}},
      {"isRequired":true,"field":{"path":"salary-1","title":"Annual salary expectation","type":"Number"}},
      {"isRequired":false,"field":{"path":"about-1","title":"Anything else?","type":"LongText"}},
      {"isRequired":false,"field":{"path":"date-1","title":"Earliest start date","type":"Date"}},
      {"isRequired":true,"field":{"path":"gender-1","title":"What is your gender?","type":"ValueSelect"}},
      {"isRequired":true,"field":{"path":"consent-1","title":"I confirm the information above is true","type":"MultiValueSelect",
        "selectableValues":[{"label":"Yes","value":"y"}]}},
      {"isRequired":true,"field":{"path":"privacy-1","title":"Data Privacy Consent","type":"MultiValueSelect",
        "selectableValues":[{"label":"I agree","value":"y"}]}}
    ]}
  ]},
  "surveyForms":[{"sections":[{"fieldEntries":[
    {"field":{"path":"eeo-1","title":"Race or ethnicity","type":"ValueSelect"}},
    {"field":{"path":"eeo-2","title":"Veteran status","type":"ValueSelect"}}
  ]}]}]
}}}`

func fetchAshbyAgainst(t *testing.T, body string) (Form, error) {
	return fetchAshbyWithStatus(t, body, http.StatusOK)
}

func fetchAshbyWithStatus(t *testing.T, body string, status int) (Form, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var request struct {
			Variables map[string]string `json:"variables"`
			Query     string            `json:"query"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/non-user-graphql" || json.Unmarshal(raw, &request) != nil {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if request.Variables["organizationHostedJobsPageName"] != "acme" || request.Variables["jobPostingId"] != "6afff2cc" {
			http.Error(w, "wrong posting", http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	reader := NewReader()
	reader.ashbyBase = server.URL
	return reader.Fetch(context.Background(), "https://jobs.ashbyhq.com/acme/6afff2cc/application?utm_source=arbeitnow.com")
}

func TestAshbyFormIsNormalised(t *testing.T) {
	form, err := fetchAshbyAgainst(t, ashbyPayload)
	if err != nil {
		t.Fatal(err)
	}
	if form.Provider != ProviderAshby || form.Board != "acme" || form.JobID != "6afff2cc" || form.Title != "Platform Engineer" {
		t.Fatalf("form = %+v", form)
	}
	byName := map[string]Field{}
	for _, field := range form.Fields {
		byName[field.Name] = field
	}
	cases := []struct {
		name, fieldType, role string
		required              bool
	}{
		{"_systemfield_name", TypeText, RoleQuestion, true},
		{"_systemfield_email", TypeText, RoleQuestion, true},
		{"phone-1", TypeText, RoleQuestion, true},
		{"_systemfield_location", TypeText, RoleQuestion, true},
		{"_systemfield_resume", TypeFile, RoleResume, true},
		{"letter-1", TypeFile, RoleCoverLetter, false},
		// Any other upload is a question the user has to answer.
		{"transcript-1", TypeFile, RoleQuestion, true},
		// A URL field mentioning a CV wants a link, not the attached document.
		{"linkedin-1", TypeText, RoleQuestion, false},
		{"relocate-1", TypeSelect, RoleQuestion, true},
		{"auth-1", TypeSelect, RoleQuestion, true},
		{"salary-1", TypeText, RoleQuestion, true},
		{"about-1", TypeTextarea, RoleQuestion, false},
		// An unknown type is kept as text, never dropped.
		{"date-1", TypeText, RoleQuestion, false},
		{"consent-1", TypeMultiSelect, RoleDeclaration, true},
		// Ashby's consent boxes are often labelled with the one word; consent
		// is the applicant's own to give.
		{"privacy-1", TypeMultiSelect, RoleDeclaration, true},
	}
	for _, c := range cases {
		field, ok := byName[c.name]
		if !ok {
			t.Errorf("%s: missing from the form", c.name)
			continue
		}
		if field.Type != c.fieldType || field.Role != c.role || field.Required != c.required {
			t.Errorf("%s = %+v, want type %s role %s required %v", c.name, field, c.fieldType, c.role, c.required)
		}
	}
	if options := byName["relocate-1"].Options; len(options) != 2 || options[0] != "Yes" {
		t.Errorf("a yes/no question should offer Yes and No, got %v", options)
	}
	if options := byName["auth-1"].Options; len(options) != 2 || options[0] != "EU citizen" {
		t.Errorf("select options = %v", options)
	}
	if _, ok := byName["old-1"]; ok {
		t.Error("a deactivated field is not on the page and must not be on the form")
	}
	if len(form.Fields) != len(cases) {
		t.Errorf("form has %d fields, want %d", len(form.Fields), len(cases))
	}
}

// As on Greenhouse, protected characteristics and equal-opportunity surveys are
// counted as skipped and never become fields.
func TestAshbyProtectedAndSurveyQuestionsAreSkipped(t *testing.T) {
	form, err := fetchAshbyAgainst(t, ashbyPayload)
	if err != nil {
		t.Fatal(err)
	}
	skipped := strings.Join(form.Skipped, " | ")
	for _, want := range []string{"What is your gender?", "Race or ethnicity", "Veteran status"} {
		if !strings.Contains(skipped, want) {
			t.Errorf("%q should be skipped, skipped = %s", want, skipped)
		}
	}
	for _, field := range form.Fields {
		if protectedPattern.MatchString(field.Label) {
			t.Errorf("protected question %q became a field", field.Label)
		}
	}
}

// Ashby answers a posting it no longer has with null and a 200, which must
// read as the posting being gone so the job is closed.
func TestAshbyNullPostingIsNotFound(t *testing.T) {
	if _, err := fetchAshbyAgainst(t, `{"data":{"jobPosting":null}}`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a null posting should be ErrNotFound, got %v", err)
	}
}

// A query Ashby rejects, or its endpoint answering 404, says nothing about the
// posting: the form is unreadable, never the job closed.
func TestAshbyEndpointFailuresNeverCloseTheJob(t *testing.T) {
	for name, fetch := range map[string]func() (Form, error){
		"query error":  func() (Form, error) { return fetchAshbyAgainst(t, `{"errors":[{"message":"Cannot query field"}]}`) },
		"endpoint 404": func() (Form, error) { return fetchAshbyWithStatus(t, `not found`, http.StatusNotFound) },
	} {
		_, err := fetch()
		if !errors.Is(err, errAshbyUnavailable) || errors.Is(err, ErrNotFound) {
			t.Errorf("%s: want the form reported unreadable, got %v", name, err)
		}
	}
}
