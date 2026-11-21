package matching

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"jobpilot/core/api/internal/models"
)

// Evaluate delegates to EvaluateWith with nil, so this pins the wrapper and the existing tests pin the unchanged scores.
func TestEvaluateWithNilExtraction(t *testing.T) {
	profile := models.Profile{Skills: []models.Skill{{Name: "Go"}}}
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name        string
		title       string
		filter      *models.FilterPreset
		wantReject  bool
		wantWarning bool
	}{
		{name: "rejected", title: "Unpaid Engineer", filter: &models.FilterPreset{ExcludedTitleWords: []string{"unpaid"}}, wantReject: true},
		{name: "warning", title: "Engineer", filter: &models.FilterPreset{Locations: []string{"Romania"}}, wantWarning: true},
		{name: "plain", title: "Engineer"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			job := models.JobInput{Title: tt.title, Company: "Quillfern Dynamics", Description: "Build services with Go."}
			want := Evaluate(profile, nil, tt.filter, job, now)
			if want.Rejected != tt.wantReject || (len(want.Warnings) > 0) != tt.wantWarning {
				t.Fatalf("unexpected rules result: %+v", want)
			}
			if got := EvaluateWith(profile, nil, tt.filter, job, nil, now); !reflect.DeepEqual(got, want) {
				t.Fatalf("EvaluateWith(nil) = %+v, Evaluate = %+v", got, want)
			}
		})
	}
}

func TestScoreExtractedSkills(t *testing.T) {
	for _, tt := range []struct {
		name    string
		profile []models.Skill
		x       models.Extraction
		score   int
		matched []string
		missing []string
	}{
		{
			name:    "required alternative met and bonus unmet",
			profile: []models.Skill{{Name: "Rust"}},
			x: models.Extraction{
				Required:   []models.ExtractedSkills{{Skills: []string{"Go", "Rust"}, Quote: "Go or Rust"}},
				NiceToHave: []models.ExtractedSkills{{Skills: []string{"Python"}, Quote: "Python is a plus"}},
			},
			score:   20,
			matched: []string{"Rust"},
			missing: []string{},
		},
		{
			name:    "required alternative unmet and bonus met",
			profile: []models.Skill{{Name: "Python"}},
			x: models.Extraction{
				Required:   []models.ExtractedSkills{{Skills: []string{"Go", "Rust"}, Quote: "Go or Rust"}},
				NiceToHave: []models.ExtractedSkills{{Skills: []string{"Python"}, Quote: "Python is a plus"}},
			},
			score:   10,
			matched: []string{"Python"},
			missing: []string{"Go or Rust"},
		},
		{
			name:    "all entries met and matched names come from the profile",
			profile: []models.Skill{{Name: "Rust"}, {Name: " golang "}, {Name: "golang"}, {Name: "Python"}},
			x: models.Extraction{
				Required:   []models.ExtractedSkills{{Skills: []string{"Go", "Rust"}, Quote: "Go or Rust"}},
				NiceToHave: []models.ExtractedSkills{{Skills: []string{"Python"}, Quote: "Python is a plus"}},
			},
			score:   30,
			matched: []string{"Python", "Rust", "golang"},
			missing: []string{},
		},
		{
			name:    "embedded terms and phrases match whole names",
			profile: []models.Skill{{Name: "React"}, {Name: "AWS"}, {Name: "machine learning"}, {Name: "Java"}, {Name: " "}},
			x: models.Extraction{Required: []models.ExtractedSkills{
				{Skills: []string{"React.js"}, Quote: "React.js"},
				{Skills: []string{"AWS (EC2, S3)"}, Quote: "AWS (EC2, S3)"},
				{Skills: []string{"Machine Learning (ML)"}, Quote: "Machine Learning (ML)"},
				{Skills: []string{"JavaScript"}, Quote: "JavaScript"},
			}},
			score:   23,
			matched: []string{"AWS", "React", "machine learning"},
			missing: []string{"JavaScript"},
		},
		{
			name:    "stack and practice entries carry no weight",
			profile: []models.Skill{{Name: "Go"}, {Name: "Kafka"}},
			x: models.Extraction{
				Required: []models.ExtractedSkills{
					{Skills: []string{"Go"}, Quote: "Go required"},
					{Skills: []string{"Agile", "Scrum"}, Quote: "Agile or Scrum"},
				},
				Stack: []models.ExtractedSkills{{Skills: []string{"Kafka"}}, {Skills: []string{"Rust"}}},
			},
			score:   30,
			matched: []string{"Go"},
			missing: []string{},
		},
		{
			name:    "only practices",
			profile: []models.Skill{{Name: "Agile"}, {Name: "Scrum"}},
			x: models.Extraction{
				Required:   []models.ExtractedSkills{{Skills: []string{"Agile", "Scrum"}, Quote: "Agile or Scrum"}},
				NiceToHave: []models.ExtractedSkills{{Skills: []string{"Scrum"}, Quote: "Scrum is a plus"}},
			},
			score:   15,
			matched: []string{},
			missing: []string{},
		},
		{
			name:    "word fragment carries no weight",
			profile: []models.Skill{{Name: "Java"}},
			x: models.Extraction{Required: []models.ExtractedSkills{
				{Skills: []string{"Java"}, Quote: "Experience with JavaScript and React"},
			}},
			score:   15,
			matched: []string{},
			missing: []string{},
		},
		{
			name: "missing alternatives omit word fragments",
			x: models.Extraction{Required: []models.ExtractedSkills{
				{Skills: []string{"Java", "Kotlin"}, Quote: "JavaScript or Kotlin"},
			}},
			score:   0,
			matched: []string{},
			missing: []string{"Kotlin"},
		},
		{
			name:    "requirement that is not a technology is listed but does not lower the score",
			profile: []models.Skill{{Name: "Go"}},
			x: models.Extraction{Required: []models.ExtractedSkills{
				{Skills: []string{"Go"}, Quote: "Go required"},
				{Skills: []string{"technical pre-sales experience"}, Quote: "5+ years of technical pre-sales experience"},
			}},
			score:   30,
			matched: []string{"Go"},
			missing: []string{"technical pre-sales experience"},
		},
		{
			name:    "requirement that is not a technology counts when the profile holds it",
			profile: []models.Skill{{Name: "Go"}, {Name: "stakeholder management"}},
			x: models.Extraction{
				Required: []models.ExtractedSkills{
					{Skills: []string{"Go"}, Quote: "Go required"},
					{Skills: []string{"stakeholder management"}, Quote: "Strong stakeholder management"},
				},
				NiceToHave: []models.ExtractedSkills{{Skills: []string{"Kotlin"}, Quote: "Kotlin is a plus"}},
			},
			score:   24,
			matched: []string{"Go", "stakeholder management"},
			missing: []string{},
		},
		{
			name:    "one technology alternative makes the entry count",
			profile: []models.Skill{{Name: "Python"}},
			x: models.Extraction{Required: []models.ExtractedSkills{
				{Skills: []string{"Python"}, Quote: "Python required"},
				{Skills: []string{"stakeholder management", "Kotlin"}, Quote: "stakeholder management or Kotlin"},
			}},
			score:   15,
			matched: []string{"Python"},
			missing: []string{"stakeholder management or Kotlin"},
		},
		{
			name:    "only requirements that are not technologies",
			profile: []models.Skill{{Name: "Go"}},
			x: models.Extraction{
				Required:   []models.ExtractedSkills{{Skills: []string{"written and oral communication skills"}, Quote: "first class written and oral communication skills"}},
				NiceToHave: []models.ExtractedSkills{{Skills: []string{"pre-sales experience"}, Quote: "pre-sales experience is a plus"}},
			},
			score:   15,
			matched: []string{},
			missing: []string{"written and oral communication skills"},
		},
		{
			name:    "profile practice cannot meet a mixed entry",
			profile: []models.Skill{{Name: "Agile"}},
			x: models.Extraction{Required: []models.ExtractedSkills{
				{Skills: []string{"Go in an agile team"}, Quote: "Go in an agile team"},
			}},
			score:   0,
			matched: []string{},
			missing: []string{"Go in an agile team"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			score, matched, missing := scoreExtractedSkills(tt.profile, tt.x)
			if score != tt.score || !reflect.DeepEqual(matched, tt.matched) || !reflect.DeepEqual(missing, tt.missing) {
				t.Fatalf("got (%d, %v, %v), want (%d, %v, %v)", score, matched, missing, tt.score, tt.matched, tt.missing)
			}
		})
	}
}

func TestEvaluateWithExtractedSkills(t *testing.T) {
	profile := models.Profile{Skills: []models.Skill{{Name: "Kafka"}}}
	job := models.JobInput{Title: "Systems Engineer", Company: "Brambleglass Labs", Description: "Go or Rust required. Kafka, Kafka, Kafka."}
	now := time.Time{}
	rules := Evaluate(profile, nil, nil, job, now)
	x := models.Extraction{
		Required:   []models.ExtractedSkills{{Skills: []string{"Go", "Rust"}, Quote: "Go or Rust required."}},
		NiceToHave: []models.ExtractedSkills{{Skills: []string{"Python"}, Quote: "Python is a plus."}},
	}
	got := EvaluateWith(profile, nil, nil, job, &x, now)
	if got.Breakdown["skill_match"] != 0 || !reflect.DeepEqual(got.MatchedSkills, []string{}) || !reflect.DeepEqual(got.MissingSkills, []string{"Go or Rust"}) {
		t.Fatalf("unexpected extracted skills: %+v", got)
	}
	if !slices.Contains(got.RedFlags, "The job requires skills your profile does not list: Go or Rust") {
		t.Fatalf("unexpected red flags: %v", got.RedFlags)
	}
	assertExtractionRejections(t, got, rules)

	goOnly := models.Extraction{Required: []models.ExtractedSkills{{Skills: []string{"Go"}, Quote: "Go or Rust required."}}}
	got = EvaluateWith(profile, nil, nil, job, &goOnly, now)
	if got.Breakdown["skill_match"] != 0 || !reflect.DeepEqual(got.MatchedSkills, []string{}) || !reflect.DeepEqual(got.MissingSkills, []string{"Go"}) {
		t.Fatalf("repeated Kafka mentions affected extracted skills: %+v", got)
	}
	assertExtractionRejections(t, got, rules)

	stackOnly := models.Extraction{Stack: []models.ExtractedSkills{{Skills: []string{"Python"}}}}
	got = EvaluateWith(profile, nil, nil, job, &stackOnly, now)
	if got.Breakdown["skill_match"] != rules.Breakdown["skill_match"] || !reflect.DeepEqual(got.MatchedSkills, rules.MatchedSkills) || !reflect.DeepEqual(got.MissingSkills, rules.MissingSkills) {
		t.Fatalf("stack-only skills differ from the rules: got %+v, rules %+v", got, rules)
	}
	if !reflect.DeepEqual(got.RedFlags, rules.RedFlags) {
		t.Fatalf("stack-only red flags = %v, rules = %v", got.RedFlags, rules.RedFlags)
	}
	assertExtractionRejections(t, got, rules)
}

func TestEvaluateWithExtractedRequirementOutsideTechnologies(t *testing.T) {
	profile := models.Profile{Skills: []models.Skill{{Name: "Go"}}}
	job := models.JobInput{Title: "Solutions Engineer", Company: "Brambleglass Labs", Description: "Go required. 5+ years of technical pre-sales experience."}
	x := models.Extraction{Required: []models.ExtractedSkills{
		{Skills: []string{"Go"}, Quote: "Go required."},
		{Skills: []string{"technical pre-sales experience"}, Quote: "5+ years of technical pre-sales experience."},
	}}
	got := EvaluateWith(profile, nil, nil, job, &x, time.Time{})
	if got.Breakdown["skill_match"] != 30 || !reflect.DeepEqual(got.MissingSkills, []string{"technical pre-sales experience"}) {
		t.Fatalf("unexpected extracted skills: %+v", got)
	}
	if !slices.Contains(got.RedFlags, "The job requires skills your profile does not list: technical pre-sales experience") {
		t.Fatalf("unexpected red flags: %v", got.RedFlags)
	}
}

func TestEvaluateWithExtractedSkillsStillAppliesTechnologyFilters(t *testing.T) {
	job := models.JobInput{Title: "Systems Engineer", Company: "Brambleglass Labs", Description: "Build services with Go and Kafka."}
	for _, tt := range []struct {
		name   string
		filter models.FilterPreset
		entry  models.ExtractedSkills
		want   string
	}{
		{
			name:   "excluded technology in posting",
			filter: models.FilterPreset{ExcludedTechnologies: []string{"Kafka"}},
			entry:  models.ExtractedSkills{Skills: []string{"Go"}, Quote: "Build services with Go and Kafka."},
			want:   "Job mentions an excluded technology: Kafka",
		},
		{
			name:   "required technology only in extraction",
			filter: models.FilterPreset{IncludedTechnologies: []string{"Rust"}},
			entry:  models.ExtractedSkills{Skills: []string{"Rust"}, Quote: "Rust required."},
			want:   "Job does not mention any required technology: Rust",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			x := models.Extraction{Required: []models.ExtractedSkills{tt.entry}}
			got := EvaluateWith(models.Profile{}, nil, &tt.filter, job, &x, time.Time{})
			if !got.Rejected || !reflect.DeepEqual(got.RejectionReasons, []string{tt.want}) {
				t.Fatalf("got rejected=%v and reasons %v, want rejection %q", got.Rejected, got.RejectionReasons, tt.want)
			}
		})
	}
}

func TestEvaluateWithExtractedSeniority(t *testing.T) {
	profile := models.Profile{Seniorities: []string{"senior"}}
	job := models.JobInput{Title: "Systems Engineer", Company: "Brambleglass Labs", Description: "Reporting to the Director of Engineering."}
	now := time.Time{}
	rules := Evaluate(profile, nil, nil, job, now)
	if rules.Breakdown["seniority_match"] != 2 {
		t.Fatalf("unexpected rules seniority score: %v", rules.Breakdown)
	}
	for _, tt := range []struct {
		level string
		want  int
	}{
		{"senior", 15},
		{"lead", 9},
		{"mid", 9},
		{"intern", 2},
		{"unspecified", 10},
		{"unrecognised", 10},
	} {
		t.Run(tt.level, func(t *testing.T) {
			x := models.Extraction{Seniority: models.ExtractedSeniority{Level: tt.level}}
			got := EvaluateWith(profile, nil, nil, job, &x, now)
			if got.Breakdown["seniority_match"] != tt.want {
				t.Fatalf("seniority score = %d, want %d", got.Breakdown["seniority_match"], tt.want)
			}
			assertExtractionRejections(t, got, rules)
		})
	}
}

func TestEvaluateWithExtractedEligibility(t *testing.T) {
	profile := models.Profile{Languages: []string{"English (C1)"}}
	filter := models.FilterPreset{RejectEligibilityRequirements: true, RejectUnlistedLanguages: true}
	now := time.Time{}
	job := models.JobInput{Title: "Systems Engineer", Company: "Larkspindle Systems", Description: "Selection is restricted to holders of an American passport. Kenntnisse in Deutsch sind Pflicht."}
	x := models.Extraction{Constraints: []models.ExtractedConstraint{
		{Kind: "citizenship", Hard: true, Quote: "holders of an American passport"},
		{Kind: "language", Value: "German", Hard: true, Quote: "Kenntnisse in Deutsch sind Pflicht"},
	}}
	got := EvaluateWith(profile, nil, &filter, job, &x, now)
	want := []string{
		`Posting requires eligibility: "holders of an American passport"`,
		`Posting requires German beyond the listed profile languages or level: "Kenntnisse in Deutsch sind Pflicht"`,
	}
	if got.Rejected || !reflect.DeepEqual(got.Warnings, want) {
		t.Fatalf("got rejected=%v and warnings %v, want warnings %v", got.Rejected, got.Warnings, want)
	}
	assertExtractionRejections(t, got, Evaluate(profile, nil, &filter, job, now))

	job.Description = "Applicants must be a U.S. citizen."
	x = models.Extraction{Constraints: []models.ExtractedConstraint{{Kind: "citizenship", Hard: true, Quote: "must be a U.S. citizen"}}}
	rules := Evaluate(profile, nil, nil, job, now)
	got = EvaluateWith(profile, nil, nil, job, &x, now)
	want = []string{`Posting requires eligibility: "Applicants must be a U.S. citizen."`}
	if !reflect.DeepEqual(rules.Warnings, want) || !reflect.DeepEqual(got.Warnings, want) {
		t.Fatalf("warnings with extraction = %v, rules warnings = %v, want %v", got.Warnings, rules.Warnings, want)
	}
	assertExtractionRejections(t, got, rules)

	job.Description = "No sponsorship."
	x = models.Extraction{Constraints: []models.ExtractedConstraint{
		{Kind: "citizenship", Hard: true, Quote: "No sponsorship. Applicants must hold Elmwick citizenship."},
	}}
	got = EvaluateWith(profile, nil, nil, job, &x, now)
	want = []string{
		`Posting states work authorization or sponsorship: "No sponsorship."`,
		`Posting requires eligibility: "No sponsorship. Applicants must hold Elmwick citizenship."`,
	}
	if !reflect.DeepEqual(got.Warnings, want) {
		t.Fatalf("warnings = %v, want %v", got.Warnings, want)
	}

	job.Description = "U.S. citizenship is required."
	rules = Evaluate(profile, nil, &filter, job, now)
	got = EvaluateWith(profile, nil, &filter, job, &models.Extraction{}, now)
	if !rules.Rejected || len(rules.RejectionReasons) == 0 {
		t.Fatalf("expected a rules rejection: %+v", rules)
	}
	assertExtractionRejections(t, got, rules)
}

func TestExtractedLanguageWarnings(t *testing.T) {
	languages := []string{"English (C1)", "German (B1)"}
	for _, tt := range []struct {
		name      string
		languages []string
		value     string
		quote     string
		existing  string
		want      []string
	}{
		{
			name: "below required level", languages: languages, value: "German (fluent)", quote: "Fließende Deutschkenntnisse erforderlich",
			want: []string{`Posting requires German beyond the listed profile languages or level: "Fließende Deutschkenntnisse erforderlich"`},
		},
		{name: "level met", languages: languages, value: "English (fluent)", want: []string{}},
		{name: "alternative met", languages: languages, value: "German or English (fluent)", want: []string{}},
		{
			name: "each language required", languages: []string{"German (C1)"}, value: "German and French", quote: "German and French are required.",
			want: []string{`Posting requires French beyond the listed profile languages or level: "German and French are required."`},
		},
		{
			name: "both languages missing", languages: []string{"English (C1)"}, value: "German and French (fluent)", quote: "German and fluent French are required.",
			want: []string{
				`Posting requires German beyond the listed profile languages or level: "German and fluent French are required."`,
				`Posting requires French beyond the listed profile languages or level: "German and fluent French are required."`,
			},
		},
		{
			name: "level applies to its own language", languages: []string{"German (B1)", "French (B1)"}, value: "German and French (fluent)", quote: "German and fluent French are required.",
			want: []string{`Posting requires French beyond the listed profile languages or level: "German and fluent French are required."`},
		},
		{name: "alternative listed below required level", languages: []string{"German (B1)"}, value: "German or French (fluent)", want: []string{}},
		{name: "level unknown", languages: []string{"German"}, value: "German (fluent)", want: []string{}},
		{
			name: "unlisted language", languages: languages, value: "French (basic)", quote: "Basic French is required.",
			want: []string{`Posting requires French beyond the listed profile languages or level: "Basic French is required."`},
		},
		{name: "no profile languages", value: "German (fluent)", want: []string{}},
		{
			name: "unrecognised language with no profile languages", value: "Esperanto (fluent)", quote: "Fluent Esperanto is required.",
			want: []string{`Posting states a condition (language): "Fluent Esperanto is required."`},
		},
		{
			name: "unrecognised language", languages: languages, value: "Esperanto (fluent)", quote: "Fluent Esperanto is required.",
			want: []string{`Posting states a condition (language): "Fluent Esperanto is required."`},
		},
		{
			name: "rules already warned", languages: languages, value: "German (fluent)", quote: "Fließende Deutschkenntnisse erforderlich",
			existing: `Posting requires German beyond the listed profile languages or level: "German (fluent) is required"`,
			want:     []string{`Posting requires German beyond the listed profile languages or level: "German (fluent) is required"`},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := models.Evaluation{Warnings: []string{}}
			if tt.existing != "" {
				e.Warnings = append(e.Warnings, tt.existing)
			}
			x := models.Extraction{Constraints: []models.ExtractedConstraint{{Kind: "language", Value: tt.value, Hard: true, Quote: tt.quote}}}
			addExtractedWarnings(&e, models.Profile{Languages: tt.languages}, x)
			if !reflect.DeepEqual(e.Warnings, tt.want) {
				t.Fatalf("warnings = %v, want %v", e.Warnings, tt.want)
			}
		})
	}
}

func TestExtractedConditionWarnings(t *testing.T) {
	existing := `Posting states work authorization or sponsorship: "Current work permit required"`
	e := models.Evaluation{Score: 44, Rejected: true, RejectionReasons: []string{"Rules rejection"}, Warnings: []string{existing}}
	before := e
	x := models.Extraction{Constraints: []models.ExtractedConstraint{
		{Kind: "location", Hard: true, Quote: "Onsite in Elmwick"},
		{Kind: "work_authorization", Hard: true, Quote: "Employer will not file permits"},
		{Kind: "clearance", Hard: true, Quote: "Current work\n permit required"},
		{Kind: "travel", Quote: "Travel is welcome"},
		{Kind: "clearance", Hard: true, Quote: "Active clearance required"},
	}}
	addExtractedWarnings(&e, models.Profile{}, x)
	want := []string{
		existing,
		`Posting states a condition (location): "Onsite in Elmwick"`,
		`Posting states work authorization or sponsorship: "Employer will not file permits"`,
		`Posting requires eligibility: "Active clearance required"`,
	}
	if !reflect.DeepEqual(e.Warnings, want) || e.Score != 44 {
		t.Fatalf("got warnings %v and score %d, want warnings %v and score 44", e.Warnings, e.Score, want)
	}
	assertExtractionRejections(t, e, before)
}

func TestExtractedYearsWarnings(t *testing.T) {
	one, two, three, five := 1, 2, 3, 5
	for _, tt := range []struct {
		name  string
		years float64
		min   *int
		want  []string
	}{
		{"higher", 3, &five, []string{"Posting asks for 5 years of experience; the profile lists 3"}},
		{"singular", 0.5, &one, []string{"Posting asks for 1 year of experience; the profile lists 0.5"}},
		{"equal", 3, &three, []string{}},
		{"lower", 3, &two, []string{}},
		{"no profile years", 0, &five, []string{}},
		{"no minimum", 3, nil, []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := models.Evaluation{Warnings: []string{}}
			addExtractedWarnings(&e, models.Profile{YearsExperience: tt.years}, models.Extraction{MinYears: tt.min})
			if !reflect.DeepEqual(e.Warnings, tt.want) {
				t.Fatalf("warnings = %v, want %v", e.Warnings, tt.want)
			}
		})
	}
}

func assertExtractionRejections(t *testing.T, got, rules models.Evaluation) {
	t.Helper()
	if got.Rejected != rules.Rejected || !reflect.DeepEqual(got.RejectionReasons, rules.RejectionReasons) {
		t.Fatalf("got rejected=%v, reasons=%v, want rejected=%v, reasons=%v", got.Rejected, got.RejectionReasons, rules.Rejected, rules.RejectionReasons)
	}
}
