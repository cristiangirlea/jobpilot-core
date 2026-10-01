package ats

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// personioFormJSON mirrors the form a /job/<id>/apply page streams: standard
// fields with terse labels, the employer's own questions (an option field
// whose options are an id-to-label object), and document slots. Order on the
// page follows sortOrder, not the order here.
const personioFormJSON = `{"fields":[
  {"name":"email","label":"Email","required":true,"type":"text","options":null,"sortOrder":1},
  {"name":"first_name","label":"First","required":true,"type":"text","options":null,"sortOrder":0},
  {"name":"last_name","label":"Last","required":true,"type":"text","options":null,"sortOrder":0},
  {"name":"available_from","label":"Available from","required":true,"type":"text","options":null,"sortOrder":3},
  {"name":"custom_attribute_4441709","label":"Hast du einen festen Wohnsitz in Deutschland?","required":false,"type":"option",
   "options":{"custom_option_62559":"Ja","custom_option_62560":"Nein","custom_option_62607":"Noch nicht, aber in Kürze"},"sortOrder":7},
  {"name":"custom_attribute_9","label":"Which tools do you use?","required":false,"type":"multi_option",
   "options":{"custom_option_2":"Go","custom_option_1":"Kubernetes"},"sortOrder":8},
  {"name":"custom_attribute_10","label":"What is your gender?","required":false,"type":"option","options":{"custom_option_3":"Prefer not to say"},"sortOrder":9},
  {"name":"custom_attribute_11","label":"I agree to the privacy policy","required":true,"type":"option","options":{"custom_option_4":"Yes"},"sortOrder":10}
 ],
 "documentCategories":[
  {"name":"other","label":"Other","required":false,"sortOrder":2},
  {"name":"cv","label":"CV","required":true,"sortOrder":0},
  {"name":"cover_letter","label":"Cover letter","required":false,"sortOrder":1}
 ]}`

// personioPage wraps a payload the way the page streams it: string chunks
// pushed to self.__next_f, the form split across two of them.
func personioPage(t *testing.T, form string) string {
	t.Helper()
	payload := `1a:["$","div",null,{"children":["$","$L1c",null,{"form":` + form + `,"jobId":2798369}]}]`
	middle := len(payload) / 2
	var page strings.Builder
	page.WriteString("<html><body><h1>Backend Engineer</h1>")
	for _, chunk := range []string{`0:{"P":null}`, payload[:middle], payload[middle:]} {
		encoded, err := json.Marshal(chunk)
		if err != nil {
			t.Fatal(err)
		}
		page.WriteString(`<script>self.__next_f.push([1,` + string(encoded) + `])</script>`)
	}
	page.WriteString("</body></html>")
	return page.String()
}

func personioReader(t *testing.T, handler http.HandlerFunc) *Reader {
	t.Helper()
	reader, server := testReader(t, handler)
	reader.personioBase = server.URL
	return reader
}

func TestDetectRecognisesPersonioJobs(t *testing.T) {
	cases := []struct {
		link, board, id string
		ok              bool
	}{
		{"https://getpress-gmbh.jobs.personio.de/job/2798369?#apply?utm_source=arbeitnow.com", "getpress-gmbh", "2798369", true},
		{"https://acme.jobs.personio.com/job/42?language=en", "acme", "42", true},
		{"https://acme.jobs.personio.de/job/42/apply", "acme", "42", true},
		{"https://acme.jobs.personio.de/", "", "", false},
		{"https://acme.jobs.personio.de/job/", "", "", false},
	}
	for _, c := range cases {
		provider, board, id, ok := Detect(c.link)
		if ok != c.ok || (ok && (provider != ProviderPersonio || board != c.board || id != c.id)) {
			t.Errorf("Detect(%q) = %s %s %s %v", c.link, provider, board, id, ok)
		}
	}
}

func TestPersonioFormIsNormalised(t *testing.T) {
	reader := personioReader(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/2798369/apply" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(personioPage(t, personioFormJSON)))
	})
	form, err := reader.Fetch(context.Background(), "https://getpress-gmbh.jobs.personio.de/job/2798369?#apply")
	if err != nil {
		t.Fatal(err)
	}
	if form.Provider != ProviderPersonio || form.Board != "getpress-gmbh" || form.JobID != "2798369" {
		t.Fatalf("form = %+v", form)
	}
	var names []string
	byName := map[string]Field{}
	for _, field := range form.Fields {
		names = append(names, field.Name)
		byName[field.Name] = field
	}
	want := "first_name last_name email available_from custom_attribute_4441709 custom_attribute_9 custom_attribute_11 document:cv document:cover_letter document:other"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("fields = %s\nwant     %s", got, want)
	}
	checks := []struct {
		name, label, fieldType, role string
		required                     bool
	}{
		// Terse standard labels are named the way the answer resolver knows.
		{"first_name", "First name", TypeText, RoleQuestion, true},
		{"last_name", "Last name", TypeText, RoleQuestion, true},
		{"custom_attribute_4441709", "Hast du einen festen Wohnsitz in Deutschland?", TypeSelect, RoleQuestion, false},
		{"custom_attribute_9", "Which tools do you use?", TypeMultiSelect, RoleQuestion, false},
		{"custom_attribute_11", "I agree to the privacy policy", TypeSelect, RoleDeclaration, true},
		{"document:cv", "CV", TypeFile, RoleResume, true},
		{"document:cover_letter", "Cover letter", TypeFile, RoleCoverLetter, false},
		// Any other document is the applicant's own upload.
		{"document:other", "Other", TypeFile, RoleQuestion, false},
	}
	for _, c := range checks {
		field := byName[c.name]
		if field.Label != c.label || field.Type != c.fieldType || field.Role != c.role || field.Required != c.required {
			t.Errorf("%s = %+v, want %q %s %s %v", c.name, field, c.label, c.fieldType, c.role, c.required)
		}
	}
	if got := strings.Join(byName["custom_attribute_4441709"].Options, "|"); got != "Ja|Nein|Noch nicht, aber in Kürze" {
		t.Errorf("options should keep the page's order, got %s", got)
	}
	if got := strings.Join(byName["custom_attribute_9"].Options, "|"); got != "Go|Kubernetes" {
		t.Errorf("options should keep the page's order, not their ids', got %s", got)
	}
	if !strings.Contains(strings.Join(form.Skipped, "|"), "What is your gender?") {
		t.Errorf("a protected question must be skipped, skipped = %v", form.Skipped)
	}
}

func TestPersonioJobGoneIsNotFound(t *testing.T) {
	reader := personioReader(t, http.NotFound)
	if _, err := reader.Fetch(context.Background(), "https://acme.jobs.personio.de/job/42"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a 404 for the job should be ErrNotFound, got %v", err)
	}
}

// Personio sends a company it no longer hosts to personio.com: no postings.
func TestPersonioCompanyGoneIsNotFound(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>personio.com</html>"))
	}))
	defer elsewhere.Close()
	reader := personioReader(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/", http.StatusTemporaryRedirect)
	})
	if _, err := reader.Fetch(context.Background(), "https://gone-gmbh.jobs.personio.de/job/42"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a career site redirected away should be ErrNotFound, got %v", err)
	}
}

// A page that no longer carries the form says nothing about the job.
func TestPersonioPageWithoutTheFormIsUnreadable(t *testing.T) {
	answers := map[string]http.HandlerFunc{
		"no form": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(personioPage(t, `{"other":1}`))) },
		"plain page": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("<html><body>Apply</body></html>"))
		},
		"server error": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) },
	}
	for name, handler := range answers {
		form, err := personioReader(t, handler).Fetch(context.Background(), "https://acme.jobs.personio.de/job/42")
		if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnsupported) || form.Provider != ProviderPersonio {
			t.Errorf("%s: want the form unread, got %v (%+v)", name, err, form)
		}
	}
}

// realPersonioChunks is written the way a real /apply page streams its data:
// JSON string literals with \" and \n escapes and literal UTF-8, a talent-pool
// form ahead of the application form, and undefined values as "$undefined".
const realPersonioChunks = `<script>self.__next_f.push([1,"0:{\"P\":null}\n"])</script>` +
	`<script>self.__next_f.push([1,"12:[\"$\",\"$L9\",null,{\"form\":{\"fields\":[{\"name\":\"email\",\"label\":\"Email\",\"required\":true,\"type\":\"text\",\"options\":null,\"sortOrder\":0}]}}]\n"])</script>` +
	`<script>self.__next_f.push([1,"1a:[\"$\",\"div\",null,{\"children\":[\"$\",\"$L1c\",null,{\"form\":{\"fields\":[{\"name\":\"first_name\",\"label\":\"First\",\"required\":true,\"type\":\"text\",\"options\":null,\"sortOrder\":0},` +
	`{\"name\":\"custom_attribute_1\",\"label\":\"Hast du einen Wohnsitz in Deutschland?\",\"required\":\"$undefined\",\"type\":\"option\",\"options\":{\"custom_option_2\":\"Ja\",\"custom_option_1\":\"Noch nicht, aber in Kürze\",\"custom_option_3\":\"$undefined\"},\"sortOrder\":1},"])</script>` +
	`<script>self.__next_f.push([1,"{\"name\":\"custom_attribute_2\",\"label\":\"$undefined\",\"required\":false,\"type\":\"text\",\"options\":null,\"sortOrder\":2}],` +
	`\"documentCategories\":[{\"name\":\"cv\",\"label\":\"CV\",\"required\":true,\"sortOrder\":0}]},\"jobId\":2798369}]}]\n"])</script>`

func TestPersonioReadsARealPagesEscapingAndPicksTheApplicationForm(t *testing.T) {
	form, ok := personioFormIn([]byte("<html><body>" + realPersonioChunks + "</body></html>"))
	if !ok {
		t.Fatal("the application form should be found")
	}
	if len(form.DocumentCategories) != 1 || len(form.Fields) != 3 {
		t.Fatalf("the talent-pool form was read instead of the application form: %+v", form)
	}
	converted := convertPersonio(form, Form{Provider: ProviderPersonio})
	var names []string
	for _, field := range converted.Fields {
		names = append(names, field.Name)
	}
	// The field whose label is undefined is left out; the others read.
	if got := strings.Join(names, " "); got != "first_name custom_attribute_1 document:cv" {
		t.Errorf("fields = %s", got)
	}
	residence := converted.Fields[1]
	if residence.Required || strings.Join(residence.Options, "|") != "Ja|Noch nicht, aber in Kürze" {
		t.Errorf("an undefined required flag reads as optional and undefined options are dropped, got %+v", residence)
	}
}

func TestPersonioUndecodableChunkLeavesTheFormUnread(t *testing.T) {
	broken := strings.Replace(realPersonioChunks, `{\"P\":null}`, `{\"P\":\x3c}`, 1)
	if _, ok := personioFormIn([]byte(broken)); ok {
		t.Fatal("a chunk that does not decode must not leave a silent hole in the form")
	}
}

func TestPersonioSiteGoneOnlyForCareerSiteLinks(t *testing.T) {
	home, _ := url.Parse("https://www.personio.com/")
	elsewhere, _ := url.Parse("https://acme.jobs.personio.de/job/42")
	cases := []struct {
		link  string
		final *url.URL
		want  bool
	}{
		{"https://acme.jobs.personio.de/job/42", home, true},
		{"https://acme.jobs.personio.de/job/42", elsewhere, false},
		// Someone linking to Personio's own site is not a career site gone.
		{"https://www.personio.com/", home, false},
		{"https://example.com/careers", home, false},
	}
	for _, c := range cases {
		if got := PersonioSiteGone(c.link, c.final); got != c.want {
			t.Errorf("PersonioSiteGone(%s, %s) = %v, want %v", c.link, c.final, got, c.want)
		}
	}
}

// Only Personio's own home page means the company is gone; any other page
// a request ends on leaves the job alone.
func TestPersonioHomepageMeansTheCompanyIsGone(t *testing.T) {
	for link, want := range map[string]bool{
		"https://personio.com/":                      true,
		"https://www.personio.de/de/":                true,
		"https://www.personio.com/en-gb":             true,
		"https://www.personio.com/pricing/":          false,
		"https://status.personio.com/":               false,
		"https://acme.jobs.personio.de/job/42/apply": false,
		"https://personio.com.evil.example/":         false,
	} {
		parsed, _ := url.Parse(link)
		if got := personioHomepage(parsed); got != want {
			t.Errorf("personioHomepage(%s) = %v, want %v", link, got, want)
		}
	}
}

func TestPersonioOptionsKeepTheirOrder(t *testing.T) {
	cases := map[string]string{
		`{"b":"Second?","a":"First?"}`: "Second?|First?",
		`["One","Two"]`:                "One|Two",
		`null`:                         "",
		``:                             "",
		`{"a":""}`:                     "",
	}
	for raw, want := range cases {
		if got := strings.Join(personioOptions(json.RawMessage(raw)), "|"); got != want {
			t.Errorf("personioOptions(%s) = %q, want %q", raw, got, want)
		}
	}
}
