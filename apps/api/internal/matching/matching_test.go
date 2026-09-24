package matching

import (
	"encoding/json"
	"jobpilot/core/api/internal/models"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func sampleProfile() models.Profile {
	return models.Profile{DesiredTitles: []string{"Backend Engineer"}, Seniorities: []string{"senior"}, WorkPreference: []string{"remote"}, SalaryExpectation: 90000, Experiences: []models.WorkExperience{{Company: "Acme Inc.", Current: true}}, Skills: []models.Skill{{Name: "Go"}, {Name: "PostgreSQL"}}}
}

func TestHardRejectEmployer(t *testing.T) {
	e := Evaluate(sampleProfile(), nil, nil, models.JobInput{Title: "Backend Engineer", Company: "ACME LLC"}, time.Now())
	if !e.Rejected {
		t.Fatal("expected current employer rejection")
	}
}
func TestFilterRejections(t *testing.T) {
	f := models.FilterPreset{ExcludedTitleWords: []string{"unpaid"}, RemoteTypes: []string{"remote"}, MinSalary: 80000}
	e := Evaluate(sampleProfile(), nil, &f, models.JobInput{Title: "Unpaid Backend Engineer", Company: "Other", RemoteType: "onsite", SalaryMax: 50000}, time.Now())
	if len(e.RejectionReasons) != 3 {
		t.Fatalf("reasons=%v", e.RejectionReasons)
	}
}
func TestScoreBreakdown(t *testing.T) {
	e := Evaluate(sampleProfile(), nil, nil, models.JobInput{Title: "Backend Engineer", Company: "Other", RemoteType: "remote", SalaryMax: 100000, Description: "Go and PostgreSQL", Industry: "Software"}, time.Now())
	if e.Score < 70 || e.Score > 100 {
		t.Fatalf("unexpected score %d", e.Score)
	}
	if e.Breakdown["title_match"] != 25 {
		t.Fatal(e.Breakdown)
	}
}

func TestTechnologyFilters(t *testing.T) {
	f := models.FilterPreset{IncludedTechnologies: []string{"Go", "Rust"}, ExcludedTechnologies: []string{"PHP"}}
	accepted := Evaluate(sampleProfile(), nil, &f, models.JobInput{Title: "Backend Engineer", Company: "Other", Description: "Build services with Go and PostgreSQL."}, time.Now())
	if accepted.Rejected {
		t.Fatalf("expected technology match, got %v", accepted.RejectionReasons)
	}
	rejected := Evaluate(sampleProfile(), nil, &f, models.JobInput{Title: "Backend Engineer", Company: "Other", Description: "Maintain a PHP application."}, time.Now())
	if len(rejected.RejectionReasons) != 2 {
		t.Fatalf("expected missing-required and excluded-technology reasons, got %v", rejected.RejectionReasons)
	}
}

func TestRemoteRoleWithDifferentLocationWarns(t *testing.T) {
	f := models.FilterPreset{Locations: []string{"Romania"}, RemoteTypes: []string{"remote"}}
	e := Evaluate(sampleProfile(), nil, &f, models.JobInput{Title: "Backend Engineer", Company: "Other", Location: "Europe", RemoteType: "remote", Description: "Go services"}, time.Now())
	if e.Rejected || len(e.Warnings) != 1 {
		t.Fatalf("expected warning without rejection, rejected=%v warnings=%v", e.Rejected, e.Warnings)
	}
}

func TestIncludedTitleAllowsStrongTokenOverlap(t *testing.T) {
	f := models.FilterPreset{IncludedTitles: []string{"Backend Engineer"}}
	e := Evaluate(sampleProfile(), nil, &f, models.JobInput{Title: "Senior Backend Software Engineer", Company: "Other", Description: "Go services"}, time.Now())
	if e.Rejected {
		t.Fatalf("expected similar title to pass, got %v", e.RejectionReasons)
	}
}

func TestSeniorityUsesBestSelectedLevel(t *testing.T) {
	p := sampleProfile()
	p.Seniorities = []string{"mid", "senior"}
	e := Evaluate(p, nil, nil, models.JobInput{Title: "Mid Backend Engineer", Company: "Other", Description: "Go services"}, time.Now())
	if e.Breakdown["seniority_match"] != 15 {
		t.Fatalf("expected exact match from selected levels, got %v", e.Breakdown)
	}
}

// splitWords replaced a regexp in the hot path of every evaluation, so it has
// to tokenise exactly as the pattern it replaced did.
func TestSplitWordsMatchesTheWordPattern(t *testing.T) {
	pattern := regexp.MustCompile(`[a-z0-9+#.]+`)
	for _, text := range []string{
		"", "   ", "go", "Senior Go/PostgreSQL engineer (m/w/d)",
		"C++, C#, .NET and Node.js; 5+ years", "trailing.dot. and ...dots",
		"Überweisung für Entwickler — naïve café", "ÀÉÎ ßẞ ǅ İstanbul", "tabs\tand\nnewlines\r\n",
		"emoji 🚀 inside, go-live, e-mail", `under_score and back\slash`,
	} {
		lower := strings.ToLower(text)
		want := pattern.FindAllString(lower, -1)
		got := splitWords(lower)
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("splitWords(%q) = %q, want %q", lower, got, want)
		}
	}
}

// A nil slice encodes as JSON null, and clients index these lists directly: the
// job page crashed on missing_skills.length for every job whose skills all
// matched. Both lists must encode as arrays whichever way the match goes.
func TestSkillListsAreNeverNull(t *testing.T) {
	allMatched := Evaluate(sampleProfile(), nil, nil, models.JobInput{Title: "Backend Engineer", Description: "Go and PostgreSQL."}, time.Now())
	noneMatched := Evaluate(sampleProfile(), nil, &models.FilterPreset{RequiredSkills: []string{"Rust"}}, models.JobInput{Title: "Backend Engineer", Description: "Rust."}, time.Now())
	for name, lists := range map[string][2][]string{
		"all matched":  {allMatched.MatchedSkills, allMatched.MissingSkills},
		"none matched": {noneMatched.MatchedSkills, noneMatched.MissingSkills},
	} {
		for _, list := range lists {
			if encoded, _ := json.Marshal(list); string(encoded) == "null" {
				t.Errorf("%s: a skill list encodes as null, want an array", name)
			}
		}
	}
}
