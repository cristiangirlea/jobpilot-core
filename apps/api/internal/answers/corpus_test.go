package answers

import (
	"context"
	"strings"
	"testing"

	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

// The corpus below is real: every label was read from a live Greenhouse board
// (Canonical, Grafana Labs, GitLab, Mozilla, Datadog). Synthetic questions test
// the classifier against wording someone imagined; these test it against
// wording employers actually use.
//
// Each entry declares how the question should be handled, which makes the
// deterministic boundary an asserted property rather than an aspiration:
//
//	handledFact   a stored fact answers it, and no provider may be consulted
//	handledHuman  only the candidate can answer, and nothing may be invented
//	handledWrite  free text that needs drafting; a provider is legitimate here
//
// TestDeterministicCoverage turns the first group into a ratchet, so a change
// that quietly sends a known fact to a model fails the build.

type handling int

const (
	handledFact handling = iota
	handledHuman
	handledWrite
)

type corpusCase struct {
	label   string
	kind    handling
	options []string
	// want is the expected answer for handledFact cases.
	want string
}

var yesNo = []string{"Yes", "No"}

var corpus = []corpusCase{
	// Identity and contact: pure lookups.
	{label: "First Name", kind: handledFact, want: "Ana"},
	{label: "Last Name", kind: handledFact, want: "Pop"},
	{label: "Email", kind: handledFact, want: "ana@example.com"},
	{label: "Phone", kind: handledFact, want: "+40 700 000 000"},
	{label: "Preferred First Name", kind: handledFact, want: "Ana"},
	{label: "What's the name you'd prefer us to use throughout the interview process?", kind: handledFact, want: "Ana"},
	{label: "LinkedIn Profile", kind: handledFact, want: "https://linkedin.com/in/anapop"},
	{label: "Website", kind: handledFact, want: "https://anapop.dev"},
	{label: "Current Employer", kind: handledFact, want: "Northwind"},
	{label: "Current Job Title", kind: handledFact, want: "Senior Backend Engineer"},

	// Location: the stored location answers all of these.
	{label: "What is your current country of residence?", kind: handledFact,
		options: []string{"Portugal", "Romania", "Spain"}, want: "Romania"},
	{label: "Please choose the country in which you are located.", kind: handledFact,
		options: []string{"Portugal", "Romania", "Spain"}, want: "Romania"},
	{label: "In which country do you currently work?", kind: handledFact,
		options: []string{"Portugal", "Romania", "Spain"}, want: "Romania"},
	{label: "What country and time zone are you based in?", kind: handledFact, want: "Romania (EET)"},

	// Location gates, which otherwise block an application on a fact we hold.
	{label: "Are you currently located in Bangalore?", kind: handledFact, options: yesNo, want: "No"},
	{label: "Are you currently located in the United States?", kind: handledFact, options: yesNo, want: "No"},
	{label: "Are you currently located in Canada?", kind: handledFact, options: yesNo, want: "No"},
	{label: "Are you based in Germany, Spain, Sweden, Ireland or the UK?", kind: handledFact, options: yesNo, want: "No"},
	{label: "Are you based in the EU?", kind: handledFact, options: yesNo, want: "Yes"},
	{label: "Are you currently located in Romania?", kind: handledFact, options: yesNo, want: "Yes"},

	// Eligibility, from an explicit authorization statement only.
	{label: "Are you currently eligible to work in your country of residence?", kind: handledFact, options: yesNo, want: "Yes"},
	{label: "Are you authorized to work lawfully in the country to which you are applying?", kind: handledFact, options: yesNo, want: "Yes"},
	{label: "Are you legally authorised to work full-time in the country where this job is based?", kind: handledFact, options: yesNo, want: "Yes"},
	{label: "Will you now or in the future require sponsorship for a visa to remain in your current location?", kind: handledFact, options: yesNo, want: "No"},
	{label: "Do you now or in the future require visa sponsorship to continue working in your country?", kind: handledFact, options: yesNo, want: "No"},

	// Employment history already recorded on the profile.
	{label: "Have you previously worked at or consulted for Northwind?", kind: handledFact, options: yesNo, want: "Yes"},
	{label: "Have you been employed by Mozilla before?", kind: handledFact, options: yesNo, want: "No"},

	// Other stored facts.
	{label: "Please select all the languages you speak fluently.", kind: handledFact,
		options: []string{"English", "French", "Romanian", "German"}, want: "English, Romanian"},
	{label: "What is your notice period?", kind: handledFact, want: "30 days"},
	{label: "What are your salary expectations?", kind: handledFact, want: "85000 EUR"},
	{label: "How many years of professional experience do you have?", kind: handledFact, want: "9"},

	// Only the candidate can answer these. Nothing may be invented.
	{label: "How did you perform in mathematics at high school?", kind: handledHuman,
		options: []string{"Top 10%", "Above average", "Average"}},
	{label: "How did you perform in your native language at high school?", kind: handledHuman,
		options: []string{"Top 10%", "Above average", "Average"}},
	{label: "What was your bachelor's university degree result, or expected result if you have not yet graduated?", kind: handledHuman},
	{label: "Are you subject to any employment agreements and/or post-employment restrictions with your current employer?", kind: handledHuman, options: yesNo},
	{label: "We require all colleagues to meet in person 2-4 times a year, at internal company events. Are you able to travel?", kind: handledHuman, options: yesNo},
	{label: "It is important to us to create an accessible and inclusive interview experience. Let us know of any adjustments.", kind: handledHuman},
	{label: "How did you hear about this opportunity?", kind: handledHuman,
		options: []string{"LinkedIn", "A friend", "Our website"}},
	{label: "If referred by a current Mozilla employee, please tell us who.", kind: handledHuman},
	{label: "In what cities are you available to work?", kind: handledHuman,
		options: []string{"Berlin", "Paris", "Remote"}},
	{label: "Please list any Accounting qualifications that you have obtained.", kind: handledHuman},
	{label: "Do you have strong proficiency in at least one modern scripting language and a solid understanding of REST APIs?", kind: handledHuman, options: yesNo},

	// Free text that genuinely needs drafting.
	{label: "Why are you interested in this role?", kind: handledWrite},
	{label: "Why are you interested in Canonical, and what specifically motivates you about this opportunity?", kind: handledWrite},
	{label: "Give a brief overview of your software development experience", kind: handledWrite},
	{label: "Describe your experience with Linux and Containers", kind: handledWrite},
	{label: "What is your experience with Kubernetes, Grafana and/or Observability?", kind: handledWrite},
	{label: "Briefly describe your daily & weekly work habits that you find effective.", kind: handledWrite},
	{label: "Anything else you'd like to share with our hiring team?", kind: handledWrite},
	{label: "Tell us about yourself", kind: handledWrite},
}

func corpusSubject() Subject {
	return Subject{
		Profile: models.Profile{
			Name: "Ana Pop", GivenName: "Ana", FamilyName: "Pop",
			Email: "ana@example.com", Phone: "+40 700 000 000",
			Location: "București, Romania", LinkedIn: "https://linkedin.com/in/anapop",
			Portfolio: "https://anapop.dev", CurrentTitle: "Senior Backend Engineer",
			YearsExperience: 9, NoticePeriod: "30 days",
			WorkAuthorization: "EU citizen, authorized to work in the EU, no sponsorship required",
			SalaryExpectation: 85000, Currency: "EUR",
			Languages: []string{"English", "Romanian"}, PreferredTimezones: []string{"EET"},
			WorkPreference: []string{"remote"},
			Skills:         []models.Skill{{Name: "Go", Years: 7}, {Name: "Kubernetes", Years: 4}},
			Experiences: []models.WorkExperience{
				{Title: "Senior Backend Engineer", Company: "Northwind", StartDate: "Jan 2024", Current: true},
				{Title: "Backend Engineer", Company: "Fabrikam", StartDate: "Jan 2020", EndDate: "Dec 2023"},
			},
		},
		Job: models.Job{JobInput: models.JobInput{
			Title: "Senior Backend Engineer", Company: "Acme",
			Description: "Go and Kubernetes services.",
		}},
		Documents: models.Documents{Answers: map[string]string{"Tell us about yourself.": "template answer"}},
	}
}

// resolveCorpus answers every case with no provider configured, so anything
// that resolves did so from stored facts alone.
func resolveCorpus(t *testing.T) []Resolution {
	t.Helper()
	service := &Service{Bank: &fakeBank{}, LLM: llm.New(nil, nil, 0, 0)}
	out := make([]Resolution, 0, len(corpus))
	for _, c := range corpus {
		kind := ""
		if len(c.options) > 3 {
			kind = "select"
		}
		question := Question{Text: c.label, Options: c.options, Kind: kind}
		if strings.HasPrefix(c.label, "Please select all") {
			question.Kind = "multiselect"
		}
		resolution, err := service.Resolve(context.Background(), "u1", question, corpusSubject())
		if err != nil {
			t.Fatalf("Resolve(%q): %v", c.label, err)
		}
		out = append(out, resolution)
	}
	return out
}

func TestCorpusFactsResolveWithoutAProvider(t *testing.T) {
	resolutions := resolveCorpus(t)
	for i, c := range corpus {
		if c.kind != handledFact {
			continue
		}
		got := resolutions[i]
		if got.Source != SourceProfile {
			t.Errorf("%q resolved from %q, want a stored fact", c.label, got.Source)
			continue
		}
		if got.Answer != c.want {
			t.Errorf("%q = %q, want %q", c.label, got.Answer, c.want)
		}
		if got.NeedsReview {
			t.Errorf("%q is a stored fact and should not need review", c.label)
		}
	}
}

// A question only the candidate can answer must never be answered from stored
// facts. Getting this wrong sends a confident wrong answer to an employer,
// which is worse than sending none.
func TestCorpusPersonalQuestionsAreNeverGuessed(t *testing.T) {
	resolutions := resolveCorpus(t)
	for i, c := range corpus {
		if c.kind != handledHuman {
			continue
		}
		if got := resolutions[i]; got.Source == SourceProfile {
			t.Errorf("%q was answered %q from stored facts, but only the candidate can answer it", c.label, got.Answer)
		}
	}
}

// Free-text questions fall back to deterministic wording or to nothing, and are
// always flagged, so a template answer never goes out unreviewed.
func TestCorpusWritingQuestionsAreFlagged(t *testing.T) {
	resolutions := resolveCorpus(t)
	for i, c := range corpus {
		if c.kind != handledWrite {
			continue
		}
		got := resolutions[i]
		if got.Source == SourceProfile {
			t.Errorf("%q resolved as a stored fact; it needs writing", c.label)
		}
		if !got.NeedsReview {
			t.Errorf("%q produced %q without asking for review", c.label, got.Source)
		}
	}
}

// TestDeterministicCoverage is a ratchet. It records how much of a real
// application form is answered from stored facts, and fails if that share
// regresses, so the deterministic boundary cannot quietly erode.
func TestDeterministicCoverage(t *testing.T) {
	resolutions := resolveCorpus(t)
	facts := 0
	for _, resolution := range resolutions {
		if resolution.Source == SourceProfile {
			facts++
		}
	}
	expected := 0
	for _, c := range corpus {
		if c.kind == handledFact {
			expected++
		}
	}
	t.Logf("deterministic coverage: %d of %d corpus questions (%d%%); %d are stored facts",
		facts, len(corpus), facts*100/len(corpus), expected)
	if facts < expected {
		t.Errorf("deterministic coverage regressed: %d of the %d stored-fact questions resolved", facts, expected)
	}
	if facts > expected {
		t.Errorf("%d questions resolved from stored facts but only %d are declared as such; check nothing is being guessed", facts, expected)
	}
}

// The corpus is only meaningful if it keeps describing real forms.
func TestCorpusIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range corpus {
		key := strings.ToLower(c.label)
		if seen[key] {
			t.Errorf("duplicate corpus question: %q", c.label)
		}
		seen[key] = true
		if c.kind == handledFact && c.want == "" {
			t.Errorf("%q is declared a stored fact but has no expected answer", c.label)
		}
	}
}
