package answers

import (
	"context"
	"strings"
	"testing"

	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

func TestClassifyGroupsRewordedQuestions(t *testing.T) {
	cases := map[string]string{
		"Tell us about yourself":                                 KeyAboutYou,
		"Please introduce yourself (optional)":                   KeyAboutYou,
		"Why are you interested in this role?":                   KeyWhyRole,
		"What attracts you about this position?":                 KeyWhyRole,
		"Why do you want to work at our company?":                KeyWhyCompany,
		"Why do you want to work at Larchmont?":                  KeyWhyCompany,
		"Why would you like to join Grafana Labs?":               KeyWhyCompany,
		"Why do you want to work here?":                          KeyWhyCompany,
		"Why do you want to apply for this role?":                KeyWhyRole,
		"Why do you want this job?":                              KeyWhyRole,
		"Why are you a good fit for this job?":                   KeyWhyFit,
		"What are your salary expectations?":                     KeySalary,
		"Expected day rate":                                      KeySalary,
		"What is your notice period?":                            KeyNotice,
		"Will you require visa sponsorship now or in future?":    KeySponsorship,
		"Are you legally authorized to work in the EU?":          KeyAuthorization,
		"How many years of professional experience do you have?": KeyYearsExperience,
		"How did you hear about us?":                             KeyReferralSource,
		"LinkedIn profile URL":                                   KeyLinkedIn,
		"GitHub":                                                 KeyGitHub,
		"Full name":                                              KeyFullName,
		"What is your English level?":                            KeyEnglishLevel,
		"Describe a time you solved a difficult problem":         KeyChallenge,
		"Do you consent to us storing your data?":                KeyConsent,
	}
	for question, want := range cases {
		if got := Classify(question); got != want {
			t.Errorf("Classify(%q) = %q, want %q", question, got, want)
		}
	}
}

// Found in early use: "Why do you want to work at Larchmont?" was
// left unanswered although the generated documents hold why-this-role text,
// because only "us", "our company" and "this company" counted as the
// company. A named employer counts too, but a technology or a way of working
// is not an employer.
func TestWhyCompanyNeedsAnEmployer(t *testing.T) {
	for _, question := range []string{
		"Why do you want to work with Go?",
		"Why do you want to work remotely?",
		"Why do you want to work in fintech?",
	} {
		if got := Classify(question); got == KeyWhyCompany {
			t.Errorf("Classify(%q) = %q; it names no employer", question, got)
		}
	}
}

// Classified, the question gets the generator's why-this-role text, held for
// review like every template answer, instead of staying unanswered.
func TestNamedEmployerQuestionGetsTheTemplateAnswer(t *testing.T) {
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(nil, nil, 0, 0)}
	s := subject()
	s.Documents.Answers["Why are you interested in this role?"] = "why this role"
	resolution, err := service.Resolve(context.Background(), "u1", Question{Text: "Why do you want to work at Larchmont?"}, s)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Source != SourceTemplate || resolution.Answer != "why this role" || !resolution.NeedsReview {
		t.Fatalf("got %q from %q (review %v), want the template answer held for review", resolution.Answer, resolution.Source, resolution.NeedsReview)
	}
}

func TestSponsorshipIsNotMistakenForAuthorization(t *testing.T) {
	// These want opposite answers, so conflating them would send a wrong answer
	// to an employer.
	if Classify("Do you need visa sponsorship?") == Classify("Are you authorized to work here?") {
		t.Fatal("sponsorship and authorization must classify differently")
	}
}

func TestNormalizeCollapsesDecoration(t *testing.T) {
	want := "why do you want to work here"
	for _, variant := range []string{
		"Why do you want to work here?*",
		"  WHY do you want to work here (optional)  ",
		"<p>Why do you want to work here?</p>",
		"Please, why do you want to work here?",
	} {
		if got := Normalize(variant); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", variant, got, want)
		}
	}
}

func TestRoleFamilyPrefersTheDominantSpecialisation(t *testing.T) {
	cases := map[string]string{
		"Senior DevOps Engineer":        "devops",
		"Backend Engineer (Go)":         "backend",
		"Full Stack Developer":          "fullstack",
		"Staff Machine Learning Engine": "data",
		"Frontend React Developer":      "frontend",
		"Software Engineer":             "engineering",
		"Chief Happiness Officer":       "general",
	}
	for title, want := range cases {
		if got := RoleFamily(title); got != want {
			t.Errorf("RoleFamily(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestTechnologySignatureIsSortedAndBounded(t *testing.T) {
	signature := TechnologySignature(4, "Senior Go Engineer", "We use Go, PostgreSQL, Kubernetes, Kafka and AWS.")
	if len(signature) != 4 {
		t.Fatalf("expected the limit to be enforced, got %v", signature)
	}
	for i := 1; i < len(signature); i++ {
		if signature[i-1] > signature[i] {
			t.Fatalf("signature must be sorted for comparability, got %v", signature)
		}
	}
}

func TestTechnologySignatureRespectsWordBoundaries(t *testing.T) {
	signature := TechnologySignature(20, "", "We are going to build trusted systems in Java.")
	for _, tech := range signature {
		if tech == "go" || tech == "rust" {
			t.Fatalf("substring match leaked into the signature: %v", signature)
		}
	}
	if len(signature) != 1 || signature[0] != "java" {
		t.Fatalf("expected only java, got %v", signature)
	}
}

func TestTechnologyAliasesCollapse(t *testing.T) {
	a := TechnologySignature(20, "", "Golang and Postgres")
	b := TechnologySignature(20, "", "Go and PostgreSQL")
	if Overlap(a, b) != 1 {
		t.Fatalf("aliases must collapse: %v vs %v", a, b)
	}
}

type fakeBank struct {
	entries []models.AnswerEntry
	saved   []models.AnswerEntry
	touched []string
}

func (f *fakeBank) FindAnswer(_ context.Context, _, normalized, roleFamily, _ string) (models.AnswerEntry, bool, error) {
	for _, entry := range f.entries {
		if entry.QuestionNormalized == normalized && (entry.RoleFamily == roleFamily || entry.RoleFamily == "") {
			return entry, true, nil
		}
	}
	return models.AnswerEntry{}, false, nil
}

func (f *fakeBank) CandidateAnswers(_ context.Context, _, questionKey, _, _ string, _ int) ([]models.AnswerEntry, error) {
	var out []models.AnswerEntry
	for _, entry := range f.entries {
		if entry.QuestionKey == questionKey {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (f *fakeBank) SaveAnswer(_ context.Context, _ string, entry models.AnswerEntry) (models.AnswerEntry, error) {
	entry.ID = "saved"
	f.saved = append(f.saved, entry)
	return entry, nil
}

func (f *fakeBank) TouchAnswer(_ context.Context, _, id string) error {
	f.touched = append(f.touched, id)
	return nil
}

type countingProvider struct{ calls int }

func (c *countingProvider) Name() string  { return "counting" }
func (c *countingProvider) Model() string { return "test" }
func (c *countingProvider) Complete(context.Context, llm.Request) (string, error) {
	c.calls++
	return "generated answer", nil
}

func subject() Subject {
	return Subject{
		Profile: models.Profile{
			Name: "Ana Pop", Email: "ana@example.com", CurrentTitle: "Backend Engineer",
			YearsExperience: 8, NoticePeriod: "30 days", WorkAuthorization: "EU citizen",
			Skills: []models.Skill{{Name: "Go", Years: 6}},
		},
		Job: models.Job{JobInput: models.JobInput{
			Title: "Senior Backend Engineer", Company: "Acme",
			Description: "Go, PostgreSQL and Kubernetes.",
		}},
		Documents: models.Documents{Answers: map[string]string{"Tell us about yourself.": "template about me"}},
	}
}

func TestProfileFactsNeverReachTheProvider(t *testing.T) {
	provider := &countingProvider{}
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(provider, nil, 0, 0)}
	for _, question := range []string{"Full name", "What is your notice period?", "Are you authorized to work in the EU?"} {
		resolution, err := service.Resolve(context.Background(), "u1", Question{Text: question}, subject())
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Source != SourceProfile {
			t.Fatalf("%q resolved from %q, want %q", question, resolution.Source, SourceProfile)
		}
		if resolution.NeedsReview {
			t.Fatalf("%q is a stored fact and should not need review", question)
		}
	}
	if provider.calls != 0 {
		t.Fatalf("stored facts must cost nothing, got %d provider calls", provider.calls)
	}
}

func TestApprovedAnswerIsReusedForTheSameQuestion(t *testing.T) {
	provider := &countingProvider{}
	bank := &fakeBank{entries: []models.AnswerEntry{{
		ID: "e1", QuestionKey: KeyWhyCompany, QuestionNormalized: Normalize("Why do you want to work at our company?"),
		Answer: "banked answer", RoleFamily: "backend", Approved: true,
	}}}
	service := &Service{Bank: bank, LLM: llm.New(provider, nil, 0, 0)}
	resolution, err := service.Resolve(context.Background(), "u1", Question{Text: "Why do you want to work at our company?"}, subject())
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Source != SourceBank || resolution.Answer != "banked answer" || !resolution.Reused {
		t.Fatalf("unexpected resolution: %+v", resolution)
	}
	if provider.calls != 0 {
		t.Fatalf("a bank hit must not call the provider, got %d calls", provider.calls)
	}
	if len(bank.touched) != 1 {
		t.Fatalf("expected usage to be recorded, got %v", bank.touched)
	}
}

func TestSimilarRoleReuseRequiresApprovalAndOverlap(t *testing.T) {
	provider := &countingProvider{}
	// Same canonical question, different wording, same stack and family.
	bank := &fakeBank{entries: []models.AnswerEntry{{
		ID: "e1", QuestionKey: KeyWhyFit, QuestionNormalized: "what makes you the right candidate",
		Answer: "reused across roles", RoleFamily: "backend",
		Technologies: []string{"go", "kubernetes", "postgresql"}, Approved: true,
	}}}
	service := &Service{Bank: bank, LLM: llm.New(provider, nil, 0, 0)}
	resolution, err := service.Resolve(context.Background(), "u1", Question{Text: "Why are you a good fit?"}, subject())
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Source != SourceBankSimilar || resolution.Answer != "reused across roles" {
		t.Fatalf("expected reuse from a similar role, got %+v", resolution)
	}
	if provider.calls != 0 {
		t.Fatalf("similar reuse must not call the provider, got %d calls", provider.calls)
	}
}

func TestUnapprovedAnswersAreNeverReused(t *testing.T) {
	provider := &countingProvider{}
	bank := &fakeBank{entries: []models.AnswerEntry{{
		ID: "e1", QuestionKey: KeyWhyFit, QuestionNormalized: Normalize("Why are you a good fit?"),
		Answer: "unreviewed draft", RoleFamily: "backend",
		Technologies: []string{"go"}, Approved: false,
	}}}
	service := &Service{Bank: bank, LLM: llm.New(provider, nil, 0, 0)}
	resolution, err := service.Resolve(context.Background(), "u1", Question{Text: "Why are you a good fit?"}, subject())
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Answer == "unreviewed draft" {
		t.Fatal("an unapproved answer must not be served to an employer")
	}
	if resolution.Source != SourceAI {
		t.Fatalf("expected a fresh generation, got %+v", resolution)
	}
}

func TestGeneratedAnswersAreStoredUnapproved(t *testing.T) {
	bank := &fakeBank{}
	service := &Service{Bank: bank, LLM: llm.New(&countingProvider{}, nil, 0, 0)}
	resolution, err := service.Resolve(context.Background(), "u1", Question{Text: "What is the strangest bug you have shipped?"}, subject())
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Source != SourceAI || !resolution.NeedsReview {
		t.Fatalf("a generated answer must be flagged for review, got %+v", resolution)
	}
	if len(bank.saved) != 1 || bank.saved[0].Approved {
		t.Fatalf("expected exactly one unapproved saved entry, got %+v", bank.saved)
	}
}

func TestGeneratedAnswersRememberTheirJob(t *testing.T) {
	bank := &fakeBank{}
	service := &Service{Bank: bank, LLM: llm.New(&countingProvider{}, nil, 0, 0)}
	s := subject()
	s.Job.ID = "job-1"
	if _, err := service.Resolve(context.Background(), "u1", Question{Text: "What is the strangest bug you have shipped?"}, s); err != nil {
		t.Fatal(err)
	}
	// The draft stays a role-family answer until approved, but keeps the job,
	// so approving it can narrow it to that job.
	if len(bank.saved) != 1 || bank.saved[0].JobID != "job-1" || bank.saved[0].Scope != "" {
		t.Fatalf("expected one draft for job-1 in the default scope, got %+v", bank.saved)
	}
}

func TestSimilarReuseByScope(t *testing.T) {
	// Same canonical question as "Why are you a good fit?", for a role of
	// another family with no stack in common.
	other := func(id, scope string) models.AnswerEntry {
		return models.AnswerEntry{ID: id, QuestionKey: KeyWhyFit, QuestionNormalized: "what makes you the right candidate " + id,
			Answer: id, RoleFamily: "frontend", Scope: scope, Technologies: []string{"react"}, Approved: true}
	}
	resolve := func(entries ...models.AnswerEntry) Resolution {
		t.Helper()
		service := &Service{Bank: &fakeBank{entries: entries}, LLM: llm.New(nil, nil, 0, 0)}
		resolution, err := service.Resolve(context.Background(), "u1", Question{Text: "Why are you a good fit?"}, subject())
		if err != nil {
			t.Fatal(err)
		}
		return resolution
	}
	if got := resolve(other("family", models.AnswerScopeRoleFamily)); got.Source == SourceBankSimilar {
		t.Fatalf("a role-family answer for unrelated roles must not be reused, got %+v", got)
	}
	if got := resolve(other("every job", models.AnswerScopeGlobal)); got.Source != SourceBankSimilar || got.Answer != "every job" {
		t.Fatalf("an answer for every job needs no overlap, got %+v", got)
	}
	// The store offers a job-only answer to its own job alone, and there it
	// wins over a closer match.
	near := models.AnswerEntry{ID: "close", QuestionKey: KeyWhyFit, QuestionNormalized: "close", Answer: "close",
		RoleFamily: "backend", Technologies: []string{"go", "kubernetes", "postgresql"}, Approved: true}
	if got := resolve(near, other("this job", models.AnswerScopeJob)); got.Answer != "this job" {
		t.Fatalf("this job's own answer comes first, got %+v", got)
	}
	// However much used, a broader answer never beats a narrower one.
	busy := near
	busy.ID, busy.Answer, busy.UsageCount, busy.Scope = "busy", "busy", 5000, models.AnswerScopeGlobal
	if got := resolve(busy, other("this job", models.AnswerScopeJob)); got.Answer != "this job" {
		t.Fatalf("a much-used answer for every job must not beat this job's, got %+v", got)
	}
	weak := other("weak family", models.AnswerScopeRoleFamily)
	weak.RoleFamily = "backend" // the job's family, with no stack in common
	if got := resolve(busy, weak); got.Answer != "weak family" {
		t.Fatalf("the role family's answer comes before one for every job, got %+v", got)
	}
}

func TestFallsBackToTemplateWithNoProvider(t *testing.T) {
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(nil, nil, 0, 0)}
	resolution, err := service.Resolve(context.Background(), "u1", Question{Text: "Tell us about yourself"}, subject())
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Source != SourceTemplate || resolution.Answer != "template about me" {
		t.Fatalf("expected the deterministic fallback, got %+v", resolution)
	}
}

func TestUnanswerableQuestionIsNotGuessed(t *testing.T) {
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(nil, nil, 0, 0)}
	resolution, err := service.Resolve(context.Background(), "u1", Question{Text: "Do you consent to our data policy?"}, subject())
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Answer != "" || resolution.Source != SourceUnanswered || !resolution.NeedsReview {
		t.Fatalf("consent must be left to a human, got %+v", resolution)
	}
}

func TestFactSheetOnlyContainsStoredFacts(t *testing.T) {
	sheet := factSheet(subject().Profile)
	for _, want := range []string{"Ana Pop", "Backend Engineer", "8", "EU citizen", "Go (6y)"} {
		if !strings.Contains(sheet, want) {
			t.Errorf("fact sheet is missing %q:\n%s", want, sheet)
		}
	}
	if strings.Contains(sheet, "Acme") {
		t.Error("the fact sheet must not carry employer-side text into candidate facts")
	}
}

func TestRoleFamilyRequiresWholeWords(t *testing.T) {
	// "Contractor" contains the letters of "cto"; substring matching filed this
	// title under management.
	if got := RoleFamily("Senior Payments & Backend Engineer - Contractor - Freelancer"); got != "backend" {
		t.Fatalf("RoleFamily = %q, want backend", got)
	}
	if got := RoleFamily("Senior Backend Engineer - Contractor"); got != "backend" {
		t.Fatalf("RoleFamily = %q, want backend", got)
	}
	if got := RoleFamily("CTO"); got != "management" {
		t.Fatalf("RoleFamily = %q, want management", got)
	}
}

func TestTechnologySignatureKeepsTheMostMentioned(t *testing.T) {
	// Alphabetical truncation would keep "angular" and drop "typescript"; the
	// signature must reflect what the text is actually about.
	text := "TypeScript everywhere. TypeScript and Go services. Go, Go. Mentions angular once."
	signature := TechnologySignature(2, "", text)
	if len(signature) != 2 {
		t.Fatalf("expected 2 entries, got %v", signature)
	}
	joined := strings.Join(signature, ",")
	if !strings.Contains(joined, "typescript") || !strings.Contains(joined, "go") {
		t.Fatalf("signature = %v, want the two most-mentioned technologies", signature)
	}
}

func TestSplitLocationDerivesCountryDeterministically(t *testing.T) {
	cases := []struct{ in, city, country string }{
		{"București, Romania", "București", "Romania"},
		{"Berlin, Germany", "Berlin", "Germany"},
		{"Austin, TX, USA", "Austin, TX", "United States"},
		{"London, UK", "London", "United Kingdom"},
		{"Amsterdam, The Netherlands", "Amsterdam", "Netherlands"},
		// No comma means no country: guessing one answers an eligibility
		// question wrongly.
		{"Remote", "Remote", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		city, country := SplitLocation(c.in)
		if city != c.city || country != c.country {
			t.Errorf("SplitLocation(%q) = (%q, %q), want (%q, %q)", c.in, city, country, c.city, c.country)
		}
	}
}

func TestMatchOptionAnswersLongSelectsWithoutAI(t *testing.T) {
	countries := []string{"Portugal", "Qatar", "Romania", "Russia", "Rwanda"}
	if got, ok := MatchOption("Romania", countries); !ok || got != "Romania" {
		t.Errorf("MatchOption = (%q, %v), want Romania", got, ok)
	}
	// A different spelling of the same country still resolves.
	if got, ok := MatchOption("UK", []string{"Ukraine", "United Kingdom", "Uruguay"}); !ok || got != "United Kingdom" {
		t.Errorf("MatchOption(UK) = (%q, %v), want United Kingdom", got, ok)
	}
}

func TestMatchOptionHandlesYesNo(t *testing.T) {
	if got, ok := MatchOption("No", []string{"Yes", "No"}); !ok || got != "No" {
		t.Errorf("MatchOption = (%q, %v), want No", got, ok)
	}
	// The real GitLab sponsorship question: one "No" and several "Yes, <scheme>".
	visa := []string{"No", "Yes, Netherlands Highly Skilled Migrant Visa", "Yes, EU Blue Card"}
	if got, ok := MatchBoolean(false, visa); !ok || got != "No" {
		t.Errorf("MatchBoolean(false) = (%q, %v), want No", got, ok)
	}
	// Which "Yes" applies is not something to infer, so it must not guess.
	if got, ok := MatchBoolean(true, visa); ok {
		t.Errorf("an ambiguous yes must not resolve, got %q", got)
	}
}

func TestMatchOptionRefusesAmbiguity(t *testing.T) {
	if got, ok := MatchOption("Ireland", []string{"Ireland", "Northern Ireland"}); !ok || got != "Ireland" {
		t.Errorf("an exact match must win over a substring: got (%q,%v)", got, ok)
	}
	if _, ok := MatchOption("Ger", []string{"Germany", "Germany (remote)"}); ok {
		t.Error("two candidates must not resolve")
	}
	if _, ok := MatchOption("Atlantis", []string{"Romania", "Germany"}); ok {
		t.Error("an unknown value must not resolve")
	}
}

func TestSelectQuestionsResolveFromStoredFacts(t *testing.T) {
	provider := &countingProvider{}
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(provider, nil, 0, 0)}
	s := subject()
	s.Profile.Location = "București, Romania"

	resolution, err := service.Resolve(context.Background(), "u1",
		Question{Text: "What is your current country of residence?", Kind: "select",
			Options: []string{"Poland", "Portugal", "Romania", "Spain"}}, s)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Source != SourceProfile || resolution.Answer != "Romania" {
		t.Fatalf("country select should resolve from the stored location, got %+v", resolution)
	}
	if provider.calls != 0 {
		t.Fatalf("a two-hundred-option country select must cost nothing, got %d calls", provider.calls)
	}
}

func TestFreeTextIsNeverSubmittedIntoASelect(t *testing.T) {
	// An approved free-text answer must not be poured into a dropdown.
	bank := &fakeBank{entries: []models.AnswerEntry{{
		ID: "e1", QuestionKey: KeyWhyFit, QuestionNormalized: Normalize("Why are you a good fit?"),
		Answer: "A long prose answer about my background.", RoleFamily: "backend", Approved: true,
	}}}
	service := &Service{Bank: bank, LLM: llm.New(nil, nil, 0, 0)}
	resolution, err := service.Resolve(context.Background(), "u1",
		Question{Text: "Why are you a good fit?", Options: []string{"Yes", "No"}}, subject())
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Answer == "A long prose answer about my background." {
		t.Fatalf("free text was submitted into a select: %+v", resolution)
	}
}

// Found in early use: the stored name "Pirvan Octavian" is surname
// first, and its first word went into Greenhouse's First Name as a verified
// fact. The given and family names, once stated, are the facts those fields
// ask for, whatever order the full name is written in.
func TestNameQuestionsUseTheStatedNameParts(t *testing.T) {
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(&countingProvider{}, nil, 0, 0)}
	s := subject()
	s.Profile.Name, s.Profile.GivenName, s.Profile.FamilyName = "Pirvan Octavian", "Octavian", "Pirvan"
	cases := map[string]string{
		"First Name":                            "Octavian",
		"Last Name":                             "Pirvan",
		"What name would you prefer us to use?": "Octavian",
		"Full name":                             "Pirvan Octavian",
	}
	for question, want := range cases {
		resolution, err := service.Resolve(context.Background(), "u1", Question{Text: question}, s)
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Source != SourceProfile || resolution.Answer != want || resolution.NeedsReview || !resolution.Approved {
			t.Errorf("%q = %q from %q (review %v), want %q as an approved profile fact", question, resolution.Answer, resolution.Source, resolution.NeedsReview, want)
		}
	}
}

// Without them, which word of one name field is the given name is a guess:
// the first word is offered, since that order is the common one, but held
// for review rather than sent as a fact.
func TestNameSplitFromTheFullNameNeedsReview(t *testing.T) {
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(&countingProvider{}, nil, 0, 0)}
	s := subject()
	s.Profile.Name = "Octavian Pîrvan"
	cases := map[string]string{
		"First Name":                            "Octavian",
		"Last Name":                             "Pîrvan",
		"What name would you prefer us to use?": "Octavian",
	}
	for question, want := range cases {
		resolution, err := service.Resolve(context.Background(), "u1", Question{Text: question}, s)
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Answer != want || !resolution.NeedsReview || resolution.Approved {
			t.Errorf("%q = %q (review %v, approved %v), want %q held for review", question, resolution.Answer, resolution.NeedsReview, resolution.Approved, want)
		}
	}

	// The full name is stored as written, so it is still a fact.
	resolution, err := service.Resolve(context.Background(), "u1", Question{Text: "Full name"}, s)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Answer != "Octavian Pîrvan" || resolution.NeedsReview {
		t.Errorf("full name = %q (review %v), want the stored name as a fact", resolution.Answer, resolution.NeedsReview)
	}

	// The candidate's own approved answer outranks the guess: bank entries
	// are per user and served only once approved, so this is their wording.
	banked := &Service{Bank: &fakeBank{entries: []models.AnswerEntry{{
		ID: "e1", QuestionKey: KeyLastName, QuestionNormalized: Normalize("Last Name"),
		Answer: "Pîrvan", Approved: true,
	}}}, LLM: llm.New(&countingProvider{}, nil, 0, 0)}
	s.Profile.Name = "Pîrvan Octavian"
	resolution, err = banked.Resolve(context.Background(), "u1", Question{Text: "Last Name"}, s)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Answer != "Pîrvan" || resolution.Source != SourceBank || resolution.NeedsReview {
		t.Errorf("an approved banked last name should be reused, got %q from %q (review %v)", resolution.Answer, resolution.Source, resolution.NeedsReview)
	}

	// A single word cannot be split at all: no surname is invented, and no
	// provider is asked for one.
	s.Profile.Name = "Octavian"
	resolution, err = service.Resolve(context.Background(), "u1", Question{Text: "Last Name"}, s)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Answer != "" || resolution.Source == SourceProfile {
		t.Errorf("a one-word name has no surname to offer, got %q from %q", resolution.Answer, resolution.Source)
	}
}

func TestPreviousEmployerQuestionUsesTheStoredHistory(t *testing.T) {
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(&countingProvider{}, nil, 0, 0)}
	s := subject()
	s.Profile.Experiences = []models.WorkExperience{{Title: "Engineer", Company: "Brightloom Ltd."}}

	// A company the candidate has not worked at.
	s.Job.Company = "GitLab"
	resolution, err := service.Resolve(context.Background(), "u1",
		Question{Text: "Have you previously worked at or consulted for GitLab?", Options: []string{"Yes", "No"}}, s)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Source != SourceProfile || resolution.Answer != "No" {
		t.Fatalf("expected a deterministic No, got %+v", resolution)
	}

	// One they have, matched through the company normaliser.
	s.Job.Company = "Brightloom"
	resolution, err = service.Resolve(context.Background(), "u1",
		Question{Text: "Have you previously worked at Brightloom?", Options: []string{"Yes", "No"}}, s)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Answer != "Yes" {
		t.Fatalf("expected Yes for a previous employer, got %+v", resolution)
	}
}

func TestSponsorshipNeedsAnExplicitStatement(t *testing.T) {
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(nil, nil, 0, 0)}
	visa := []string{"No", "Yes, EU Blue Card"}

	s := subject()
	s.Profile.WorkAuthorization = "EU citizen, no sponsorship required"
	resolution, _ := service.Resolve(context.Background(), "u1",
		Question{Text: "Will you now or in the future require sponsorship for a visa?", Options: visa}, s)
	if resolution.Answer != "No" {
		t.Errorf("an explicit statement should resolve, got %+v", resolution)
	}

	// A citizenship alone says nothing about the jurisdiction the role is in.
	s.Profile.WorkAuthorization = "Romanian citizen"
	resolution, _ = service.Resolve(context.Background(), "u1",
		Question{Text: "Will you now or in the future require sponsorship for a visa?", Options: visa}, s)
	if resolution.Answer == "No" {
		t.Errorf("citizenship alone must not answer a sponsorship question, got %+v", resolution)
	}
}

func TestCountryQuestionWordingVariants(t *testing.T) {
	for _, question := range []string{
		"What is your current country of residence?",
		"Please choose the country in which you are located.",
		"Country where you are based",
	} {
		if got := Classify(question); got != KeyCountry {
			t.Errorf("Classify(%q) = %q, want %q", question, got, KeyCountry)
		}
	}
}

// The location gate is deliberately asymmetric: claiming to live somewhere the
// candidate does not would be a false statement to an employer, while failing
// to answer only costs a click.
func TestLocationGateAsymmetry(t *testing.T) {
	const home = "București, Romania"
	cases := []struct {
		question string
		yes, ok  bool
		why      string
	}{
		{"Are you currently located in Romania?", true, true, "their own country"},
		{"Are you based in București?", true, true, "their own city"},
		{"Are you based in the EU?", true, true, "a region containing their country"},
		{"Are you based in Europe?", true, true, "geographic Europe"},
		{"Are you currently located in Bangalore?", false, true, "an unrecognised city is somewhere they are not"},
		{"Are you currently located in the United States?", false, true, "a different country"},
		{"Are you based in Germany, Spain, Sweden, Ireland or the UK?", false, true, "a list they are not in"},
		{"Are you based in Germany, Romania or Spain?", true, true, "a list including their country"},
		{"Are you based in APAC?", false, true, "a region their country is not in"},
		// Groupings with no membership test must not be answered either way.
		{"Are you based in the Nordics?", false, false, "an unevaluable grouping"},
		{"Are you based in the Balkans?", false, false, "an unevaluable grouping"},
		{"Are you based in Eastern Europe?", false, false, "an unevaluable grouping"},
		// Not a location gate at all.
		{"What is your favourite editor?", false, false, "not a location question"},
	}
	for _, c := range cases {
		yes, ok := LocationGate(c.question, home)
		if yes != c.yes || ok != c.ok {
			t.Errorf("LocationGate(%q) = (%v,%v), want (%v,%v) — %s", c.question, yes, ok, c.yes, c.ok, c.why)
		}
	}
}

func TestLocationGateNeedsAKnownLocation(t *testing.T) {
	if _, ok := LocationGate("Are you currently located in Berlin?", ""); ok {
		t.Error("with no stored location nothing can be concluded")
	}
}

func TestEmployerQuestionNamesWinOverThePostingCompany(t *testing.T) {
	cases := map[string]string{
		"Have you previously worked at or consulted for GitLab?": "GitLab",
		"Have you been employed by Mozilla before?":              "Mozilla",
		"Have you previously worked at Acme Corp?":               "Acme Corp",
		"Why do you want this job?":                              "",
	}
	for question, want := range cases {
		if got := employerFromQuestion(question); got != want {
			t.Errorf("employerFromQuestion(%q) = %q, want %q", question, got, want)
		}
	}
}

func TestMultiSelectResolvesEveryMatchingValue(t *testing.T) {
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(&countingProvider{}, nil, 0, 0)}
	s := subject()
	s.Profile.Languages = []string{"English", "Romanian"}
	resolution, err := service.Resolve(context.Background(), "u1", Question{
		Text: "Please select all the languages you speak fluently.", Kind: "multiselect",
		Options: []string{"English", "French", "Romanian", "German"}}, s)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Source != SourceProfile || resolution.Answer != "English, Romanian" {
		t.Fatalf("got %+v, want both stored languages from the profile", resolution)
	}
}

// A partial guess on a multi-select is still a wrong answer.
func TestMultiSelectRefusesWhenNothingMatches(t *testing.T) {
	if _, ok := MatchAll("Klingon, Dothraki", []string{"English", "French"}); ok {
		t.Error("no overlap must not resolve")
	}
	matched, ok := MatchAll("English, Klingon", []string{"English", "French"})
	if !ok || len(matched) != 1 || matched[0] != "English" {
		t.Errorf("expected only the value that could be placed, got %v", matched)
	}
}

// Found in early use: a template answer went into a Yes/No select
// verbatim, because the template branch was the one source that never fitted
// its answer to the question's options. "EU" is not an option; a question
// that cannot be answered with one of its own options stays unanswered.
func TestTemplateAnswersAreFittedToSelectOptions(t *testing.T) {
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(nil, nil, 0, 0)}
	s := subject()
	s.Profile.WorkAuthorization = "EU"
	s.Documents.Answers = map[string]string{"Work authorization.": "EU"}
	for _, text := range []string{
		"Are you currently eligible to work in your country of residence?",
		"Are you legally authorized to work in the European Union?",
	} {
		options := []string{"Yes", "No"}
		resolution, err := service.Resolve(context.Background(), "u1", Question{Text: text, Kind: "select", Options: options}, s)
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Answer != "" && resolution.Answer != "Yes" && resolution.Answer != "No" {
			t.Fatalf("%q: a select was answered with free text %q from %s", text, resolution.Answer, resolution.Source)
		}
		if resolution.Answer == "" && !resolution.NeedsReview {
			t.Fatalf("%q: an unanswered select must be flagged for review: %+v", text, resolution)
		}
	}
}

// Found in early use: "How many years of professional Python
// experience do you have?" was answered with total years, approved as a
// profile fact. A years question about one technology must answer with that
// skill's recorded years, or not at all.
func TestYearsQuestionsAboutOneSkillUseThatSkill(t *testing.T) {
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(nil, nil, 0, 0)}
	s := subject()
	s.Profile.YearsExperience = 13
	s.Profile.Skills = []models.Skill{{Name: "Python", Years: 6}, {Name: "Go", Years: 6}, {Name: "Kubernetes"}}
	cases := []struct {
		question string
		want     string
	}{
		{"How many years of professional Python experience do you have?", "6"},
		{"How many years of experience do you have with Golang?", "6"},
		{"How many years of experience do you have?", "13"},
		{"Years of professional experience", "13"},
		// Named, but no years recorded: the total would be a false claim.
		{"How many years of Kubernetes experience do you have?", ""},
		// Not on the profile at all.
		{"How many years of Rust experience do you have?", ""},
		// Two technologies: no single honest number.
		{"How many years of experience do you have with Python and Go?", ""},
	}
	for _, c := range cases {
		resolution, err := service.Resolve(context.Background(), "u1", Question{Text: c.question}, s)
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Answer != c.want {
			t.Errorf("%q: got %q from %s, want %q", c.question, resolution.Answer, resolution.Source, c.want)
		}
		if c.want == "" && !resolution.NeedsReview {
			t.Errorf("%q: an unanswered years question must be flagged for review", c.question)
		}
		if c.want != "" && resolution.Source != SourceProfile {
			t.Errorf("%q: a recorded fact should come from the profile, got %s", c.question, resolution.Source)
		}
	}
}

// A salary goes out as an amount with its period, and is held for review:
// the period is how matching reads the stored number, not something the
// candidate wrote, and an employer will hold them to it. A notice period of
// zero says so in words; a bare number, whose unit is unknown, is held.
func TestLogisticsAnswersAreWordedAndHeldWhereUnclear(t *testing.T) {
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(nil, nil, 0, 0)}
	s := subject()
	s.Profile.SalaryExpectation, s.Profile.Currency, s.Profile.NoticePeriod = 48000, "EUR", "0"
	for _, c := range []struct {
		notice, question, want string
		review                 bool
	}{
		{"0", "What are your salary expectations?", "48,000 EUR per year", true},
		{"0", "What is your notice period?", "Available immediately", false},
		{"30", "What is your notice period?", "30", true},
		{"30 days", "What is your notice period?", "30 days", false},
	} {
		s.Profile.NoticePeriod = c.notice
		resolution, err := service.Resolve(context.Background(), "u1", Question{Text: c.question}, s)
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Source != SourceProfile || resolution.Answer != c.want || resolution.NeedsReview != c.review || resolution.Approved == c.review {
			t.Errorf("%q with notice %q = %q (review %v, approved %v), want %q with review %v", c.question, c.notice, resolution.Answer, resolution.NeedsReview, resolution.Approved, c.want, c.review)
		}
	}
}
