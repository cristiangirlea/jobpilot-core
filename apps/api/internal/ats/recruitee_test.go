package ats

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// recruiteeOfferJSON mirrors /api/offers/<slug>: switchable standard fields, a
// shown location question, and open questions of every kind seen on real
// boards, with bodies HTML-escaped the way Recruitee sends them.
const recruiteeOfferJSON = `{"offer":{
  "id":2765523,"slug":"backend-engineer","title":"Senior Backend Engineer","company_name":"Acme GmbH","status":"published",
  "options_cv":"required","options_cover_letter":"optional","options_phone":"required","options_photo":"optional",
  "options_salutation":"required","options_title":"off",
  "location_question_visible":true,"locations_question":"Where would you like to work?","locations_question_required":true,
  "locations_question_type":"multiple_choice","locations":[{"name":"Berlin","city":"Berlin"},{"name":"","city":"Remote"}],
  "open_questions":[
    {"id":3,"kind":"salary","required":true,"body":"Desired gross annual salary?","position":3,"open_question_options":[]},
    {"id":1,"kind":"boolean","required":true,"body":"Are you authorized to work in the EU?","position":1,"open_question_options":[]},
    {"id":2,"kind":"single_choice","required":true,"body":"Which languages do you speak at C1?","position":2,
     "open_question_options":[{"body":"English","position":1},{"body":"German &amp; English","position":0}]},
    {"id":4,"kind":"text","required":false,"body":"Tell us about yourself","position":4,"open_question_options":[]},
    {"id":5,"kind":"file","required":true,"body":"Upload your certificates","position":5,"open_question_options":[]},
    {"id":6,"kind":"date","required":false,"body":"Earliest start date","position":6,"open_question_options":[]},
    {"id":7,"kind":"single_choice","required":true,"body":"What is your gender?","position":7,"open_question_options":[{"body":"Prefer not to say","position":0}]},
    {"id":8,"kind":"boolean","required":true,"body":"Data Privacy Consent","position":8,"open_question_options":[]}
  ]}}`

func TestDetectRecognisesRecruiteeOffers(t *testing.T) {
	cases := []struct {
		link, board, slug string
		ok                bool
	}{
		{"https://payflows.recruitee.com/o/backend-engineer?utm_source=arbeitnow.fr", "payflows", "backend-engineer", true},
		{"https://payflows.recruitee.com/o/backend-engineer/c/new", "payflows", "backend-engineer", true},
		{"https://payflows.recruitee.com/de/o/backend-engineer", "payflows", "backend-engineer", true},
		{"https://payflows.recruitee.com/pt-br/o/backend-engineer", "payflows", "backend-engineer", true},
		{"https://payflows.recruitee.com/", "", "", false},
		{"https://payflows.recruitee.com/o/", "", "", false},
	}
	for _, c := range cases {
		provider, board, slug, ok := Detect(c.link)
		if ok != c.ok || (ok && (provider != ProviderRecruitee || board != c.board || slug != c.slug)) {
			t.Errorf("Detect(%q) = %s %s %s %v", c.link, provider, board, slug, ok)
		}
	}
}

func TestRecruiteeFormIsNormalised(t *testing.T) {
	reader, server := testReader(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/offers/backend-engineer" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(recruiteeOfferJSON))
	})
	reader.recruiteeBase = server.URL
	form, err := reader.Fetch(context.Background(), "https://acme.recruitee.com/o/backend-engineer?utm_source=arbeitnow.com")
	if err != nil {
		t.Fatal(err)
	}
	if form.Provider != ProviderRecruitee || form.Board != "acme" || form.JobID != "backend-engineer" ||
		form.Title != "Senior Backend Engineer" || form.Company != "Acme GmbH" {
		t.Fatalf("form = %+v", form)
	}
	var names []string
	byName := map[string]Field{}
	for _, field := range form.Fields {
		names = append(names, field.Name)
		byName[field.Name] = field
	}
	// The applicant sees the standard fields, the location question, then the
	// employer's questions in their own order.
	want := "name email phone cv cover_letter photo salutation locations question_1 question_2 question_3 question_4 question_5 question_6 question_8"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("fields = %s\nwant     %s", got, want)
	}
	checks := []struct {
		name, fieldType, role string
		required              bool
	}{
		{"cv", TypeFile, RoleResume, true},
		{"cover_letter", TypeFile, RoleCoverLetter, false},
		{"phone", TypeText, RoleQuestion, true},
		// A photo is an upload only the applicant has.
		{"photo", TypeFile, RoleQuestion, false},
		// A salutation states gender: the applicant's to answer, never ours.
		{"salutation", TypeText, RolePersonal, true},
		{"locations", TypeMultiSelect, RoleQuestion, true},
		{"question_1", TypeSelect, RoleQuestion, true},
		{"question_2", TypeSelect, RoleQuestion, true},
		{"question_3", TypeText, RoleQuestion, true},
		{"question_4", TypeTextarea, RoleQuestion, false},
		{"question_5", TypeFile, RoleQuestion, true},
		// An unknown kind is kept as text, never dropped.
		{"question_6", TypeText, RoleQuestion, false},
		{"question_8", TypeSelect, RoleDeclaration, true},
	}
	for _, c := range checks {
		field := byName[c.name]
		if field.Type != c.fieldType || field.Role != c.role || field.Required != c.required {
			t.Errorf("%s = %+v, want type %s role %s required %v", c.name, field, c.fieldType, c.role, c.required)
		}
	}
	if options := byName["question_2"].Options; len(options) != 2 || options[0] != "German & English" {
		t.Errorf("options should be unescaped and in Recruitee's order, got %v", options)
	}
	if options := byName["locations"].Options; len(options) != 2 || options[1] != "Remote" || byName["locations"].Label != "Where would you like to work?" {
		t.Errorf("location question = %+v", byName["locations"])
	}
	if options := byName["question_1"].Options; len(options) != 2 || options[0] != "Yes" {
		t.Errorf("a yes/no question should offer Yes and No, got %v", options)
	}
	if !strings.Contains(strings.Join(form.Skipped, "|"), "What is your gender?") {
		t.Errorf("a protected question must be skipped, skipped = %v", form.Skipped)
	}
}

func TestRecruiteeOfferGoneIsNotFound(t *testing.T) {
	reader, server := testReader(t, http.NotFound)
	reader.recruiteeBase = server.URL
	if _, err := reader.Fetch(context.Background(), "https://acme.recruitee.com/o/closed-role"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a 404 on recruitee.com should be ErrNotFound, got %v", err)
	}
}

// A server error on recruitee.com is a passing failure, not the offer gone.
func TestRecruiteeServerErrorIsNotAClosure(t *testing.T) {
	reader, server := testReader(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	reader.recruiteeBase = server.URL
	form, err := reader.Fetch(context.Background(), "https://acme.recruitee.com/o/backend-engineer")
	if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnsupported) || form.Provider != ProviderRecruitee {
		t.Fatalf("a 503 should leave the form unread for now, got %v (%+v)", err, form)
	}
}

// On an employer's own domain the API has to confirm the site is Recruitee
// before anything is read or closed, and a passing failure is neither.
func TestRecruiteeOnAnEmployersDomain(t *testing.T) {
	unreadable := errors.New("unread for now")
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    error
		fields  bool
	}{
		{"offer served", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/offers/backend-engineer" {
				w.Write([]byte(recruiteeOfferJSON))
				return
			}
			http.NotFound(w, r)
		}, nil, true},
		{"offer gone from a Recruitee site", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/offers/" {
				w.Write([]byte(`{"offers":[{"id":1}]}`))
				return
			}
			http.NotFound(w, r)
		}, ErrNotFound, false},
		{"a site that is not Recruitee", http.NotFound, ErrUnsupported, false},
		{"an HTML page", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("<html>careers</html>"))
		}, ErrUnsupported, false},
		{"a passing server error", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}, unreadable, false},
	}
	for _, c := range cases {
		// The API is asked over https whatever the link said.
		server := httptest.NewTLSServer(c.handler)
		reader := NewReader()
		reader.public = server.Client()
		link := "http" + strings.TrimPrefix(server.URL, "https") + "/o/backend-engineer"
		form, err := reader.Fetch(context.Background(), link)
		server.Close()
		switch c.want {
		case nil:
			if err != nil {
				t.Errorf("%s: err = %v, want the form", c.name, err)
			}
		case unreadable:
			if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnsupported) {
				t.Errorf("%s: err = %v, want the form unread for now", c.name, err)
			}
		default:
			if !errors.Is(err, c.want) {
				t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
			}
		}
		if c.fields && (form.Provider != ProviderRecruitee || len(form.Fields) == 0) {
			t.Errorf("%s: form = %+v", c.name, form)
		}
	}
}

// A job link can name any host, so the employer-domain probe must not be
// usable to reach the server's own network.
func TestRecruiteeProbeRefusesPrivateAddresses(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(recruiteeOfferJSON))
	}))
	defer server.Close()
	_, err := NewReader().Fetch(context.Background(), server.URL+"/o/backend-engineer")
	if err == nil || errors.Is(err, ErrNotFound) || hits.Load() != 0 {
		t.Fatalf("a loopback careers site must not be fetched: err = %v, requests = %d", err, hits.Load())
	}
}
