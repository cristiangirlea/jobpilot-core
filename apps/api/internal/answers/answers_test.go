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

func (f *fakeBank) FindAnswer(_ context.Context, _, normalized, roleFamily string) (models.AnswerEntry, bool, error) {
	for _, entry := range f.entries {
		if entry.QuestionNormalized == normalized && (entry.RoleFamily == roleFamily || entry.RoleFamily == "") {
			return entry, true, nil
		}
	}
	return models.AnswerEntry{}, false, nil
}

func (f *fakeBank) CandidateAnswers(_ context.Context, _, questionKey string, _ int) ([]models.AnswerEntry, error) {
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
