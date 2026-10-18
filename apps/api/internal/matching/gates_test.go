package matching

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"jobpilot/core/api/internal/models"
)

func gateFilter(t *testing.T, eligibility, language bool) *models.FilterPreset {
	t.Helper()
	var f models.FilterPreset
	b, _ := json.Marshal(map[string]bool{"reject_eligibility_requirements": eligibility, "reject_unlisted_languages": language})
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return &f
}

func TestEligibilityPhrases(t *testing.T) {
	for _, tc := range []struct {
		text            string
		warning, reject bool
	}{
		{"Applicants must be a citizen of Canada.", true, false},
		{"U.S. citizenship is required.", true, true},
		{"Applicants must be U.S. citizens.", true, false},
		{"This role is open to US citizens only.", true, false},
		{"Only US citizens may apply.", true, true},
		{"You must be a permanent resident of Australia.", true, true},
		{"Permanent residency is required.", true, true},
		{"Permanent resident status is required.", true, false},
		{"An active security clearance is required.", true, true},
		{"Candidates must be eligible to obtain a security clearance.", true, false},
		{"Must hold a Secret clearance.", true, false},
		{"Active TS/SCI clearance required.", true, false},
		{"This role requires SC clearance.", true, false},
		{"No sponsorship is available.", true, false},
		{"We do not provide visa sponsorship.", true, false},
		{"We cannot sponsor visas.", true, false},
		{"Applicants must already have the right to work in the UK.", true, false},
		{"You must be legally authorized to work in the US.", true, false},
		{"Applicants must have the legal right to work in Canada.", true, false},
		{"Visa sponsorship available.", true, false},
		{"International applicants welcome.", true, false},
		{"Citizen builds software for communities.", false, false},
		{"You must have experience with Citizen.", false, false},
		{"You must be a Citizen employee.", false, false},
		{"No clearance required.", false, false},
		{"A security clearance is not required.", false, false},
		{"You must not have a security clearance.", false, false},
		{"Active security clearance is not mandatory.", false, false},
		{"Citizenship is not required.", false, false},
		{"Permanent residency is not required.", false, false},
		{"We welcome citizens of all countries.", false, false},
		{"We build security clearance management software.", false, false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			p := models.Profile{WorkAuthorization: "Romanian citizen"}
			j := models.JobInput{Description: tc.text}
			off := Evaluate(p, nil, gateFilter(t, false, false), j, time.Time{})
			on := Evaluate(p, nil, gateFilter(t, true, false), j, time.Time{})
			if (len(off.Warnings) > 0) != tc.warning || off.Rejected || on.Rejected != tc.reject {
				t.Fatalf("off=%+v on=%+v", off, on)
			}
			for _, message := range append(off.Warnings, on.RejectionReasons...) {
				if !strings.Contains(message, `"`+tc.text+`"`) {
					t.Errorf("missing exact sentence: %s", message)
				}
			}
			p.WorkAuthorization = "No sponsorship required"
			if got := Evaluate(p, nil, gateFilter(t, false, false), j, time.Time{}); !reflect.DeepEqual(got, off) {
				t.Fatal("explicit authorization must not turn a posting requirement into an eligibility decision")
			}
		})
	}
}

func TestLanguagePhrases(t *testing.T) {
	for _, tc := range []struct {
		text            string
		languages       []string
		warning, reject bool
	}{
		{"Fluent German is required.", []string{"English"}, true, true},
		{"You must speak French.", []string{"English"}, true, true},
		{"Native Spanish required.", []string{"Spanish B2"}, true, false},
		{"German at C1 level is required.", []string{"German (B1)"}, true, false},
		{"Business-level Japanese required.", []string{"Japanese basic"}, true, false},
		{"English fluency is required.", []string{"English intermediate"}, true, false},
		{"Required languages: Dutch, Italian.", []string{"Dutch native"}, true, true},
		{"Must be fluent in Portuguese.", []string{"English"}, true, true},
		{"Professional proficiency in Arabic is required.", []string{"English"}, true, true},
		{"Fluent German is required.", []string{"German C2"}, false, false},
		{"German at C1 level is required.", []string{"German C1"}, false, false},
		{"German (C1) required.", []string{"German B2"}, true, false},
		{"German B1 required, English C2 required.", []string{"German B1", "English C2"}, false, false},
		{"German C2 required, English B1 required.", []string{"German C2", "English B1"}, false, false},
		{"Fluent English is required; German is preferred.", []string{"English fluent"}, false, false},
		{"Fluent German is required.", []string{"German B1, English C2"}, true, false},
		{"Fluent English is required.", []string{"English"}, false, false},
		{"We are a German company.", []string{"English"}, false, false},
		{"Our French customers use Go.", []string{"English"}, false, false},
		{"German is a nice to have.", []string{"English"}, false, false},
		{"German is preferred but not required.", []string{"English"}, false, false},
		{"Fluent German is not required.", []string{"English"}, false, false},
		{"No German required.", []string{"English"}, false, false},
		{"English is our company language.", []string{"English"}, false, false},
		{"We require engineers for our German company.", []string{"English"}, false, false},
		{"Go is required.", []string{"English"}, false, false},
	} {
		t.Run(tc.text+strings.Join(tc.languages, ","), func(t *testing.T) {
			p := models.Profile{Languages: tc.languages}
			j := models.JobInput{Description: tc.text}
			off := Evaluate(p, nil, gateFilter(t, false, false), j, time.Time{})
			on := Evaluate(p, nil, gateFilter(t, false, true), j, time.Time{})
			if (len(off.Warnings) > 0) != tc.warning || off.Rejected || on.Rejected != tc.reject {
				t.Fatalf("off=%+v on=%+v", off, on)
			}
			for _, message := range append(off.Warnings, on.RejectionReasons...) {
				if !strings.Contains(message, `"`+tc.text+`"`) {
					t.Errorf("missing exact sentence: %s", message)
				}
			}
		})
	}
}

func TestGateQuotes(t *testing.T) {
	sentence := "Applicants must be a Canadian citizen for this role " + strings.Repeat("é", 210) + "."
	e := Evaluate(models.Profile{}, nil, gateFilter(t, true, true), models.JobInput{Description: "Build software.\n" + sentence + " Work remotely."}, time.Time{})
	if len(e.Warnings) != 1 || len(e.RejectionReasons) != 1 {
		t.Fatalf("%+v", e)
	}
	for _, message := range []string{e.Warnings[0], e.RejectionReasons[0]} {
		parts := strings.Split(message, `"`)
		if len(parts) != 3 || !utf8.ValidString(parts[1]) || len([]rune(parts[1])) > 200 || !strings.HasPrefix(sentence, parts[1]) {
			t.Fatalf("bad quote: %s", message)
		}
	}
	j := models.JobInput{Title: "Fluent German is required", Description: "Build software."}
	if e := Evaluate(models.Profile{Languages: []string{"English"}}, nil, gateFilter(t, false, true), j, time.Time{}); !e.Rejected || !strings.Contains(strings.Join(e.Warnings, ""), `"Fluent German is required"`) {
		t.Fatalf("title requirement: %+v", e)
	}
}

func TestGatesPreserveExistingEvaluation(t *testing.T) {
	p := sampleProfile()
	p.Languages = []string{"English"}
	j := models.JobInput{Title: "Backend Engineer", Company: "Other", Description: "Build Go services.", RemoteType: "remote"}
	before := Evaluate(p, nil, nil, j, time.Time{})
	for _, f := range []*models.FilterPreset{gateFilter(t, false, false), gateFilter(t, true, true)} {
		a, _ := json.Marshal(before)
		b, _ := json.Marshal(Evaluate(p, nil, f, j, time.Time{}))
		if string(a) != string(b) {
			t.Fatalf("silent posting changed: %s != %s", a, b)
		}
	}
	f := gateFilter(t, false, false)
	f.ExcludedTitleWords = []string{"Backend"}
	before = Evaluate(p, nil, f, j, time.Time{})
	j.Description += " Must be a citizen of Canada. Fluent German required."
	after := Evaluate(p, nil, f, j, time.Time{})
	if len(after.Warnings) != 2 {
		t.Fatalf("expected two warnings: %+v", after)
	}
	after.Warnings = before.Warnings
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatalf("existing scores/rejections changed: %s != %s", a, b)
	}
}

func TestExistingEvaluationGolden(t *testing.T) {
	j := models.JobInput{Title: "Backend Engineer", Company: "Other", Description: "Build Go services.", RemoteType: "remote"}
	const want = `{"rejected":false,"rejection_reasons":[],"warnings":[],"score":85,"reasons":["The job title aligns with a desired role.","Matched 1 profile skill(s): Go.","The location and work arrangement fit your preferences."],"matched_skills":["Go"],"missing_skills":[],"red_flags":["No salary or rate was provided.","The posting date is unknown."],"breakdown":{"domain_industry_match":3,"freshness":2,"location_remote_match":10,"salary_match":5,"seniority_match":10,"skill_match":30,"title_match":25}}`
	for _, rejected := range []bool{false, true} {
		filter := gateFilter(t, false, false)
		expected := want
		if rejected {
			filter.ExcludedTitleWords = []string{"Backend"}
			expected = strings.Replace(want, `"rejected":false,"rejection_reasons":[]`, `"rejected":true,"rejection_reasons":["Job title contains excluded words: Backend"]`, 1)
		}
		got, err := json.Marshal(Evaluate(sampleProfile(), nil, filter, j, time.Time{}))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != expected {
			t.Fatalf("evaluation changed: %s", got)
		}
	}
}

func TestJoinPostingLines(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"blank line", "Security clearance is required\n\nunless remote", "Security clearance is required\n\nunless remote"},
		{"lone carriage return", "Security clearance is required\rUnless remote", "Security clearance is required Unless remote"},
		{"whitespace-only line", "Security clearance is required\n \t \nunless remote", "Security clearance is required\n\nunless remote"},
		{"bullet", "Requirements:\n• must be a U.S. citizen", "Requirements:\n• must be a U.S. citizen"},
		{"plain-text bullet", "Requirements:\no must be a U.S. citizen", "Requirements:\no must be a U.S. citizen"},
		{"or comma", "Must be a U.S. citizen\nor, a permanent resident", "Must be a U.S. citizen or, a permanent resident"},
		{"and slash or", "Must be a U.S. citizen\nAnd/or a permanent resident", "Must be a U.S. citizen And/or a permanent resident"},
		{"capitalised requirement", "No clearance required\nMust be a U.S. citizen.", "No clearance required\nMust be a U.S. citizen."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := joinPostingLines(tc.text); got != tc.want {
				t.Fatalf("joinPostingLines(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
	for _, word := range []string{"If", "Where", "When", "Who", "Whether", "Unless", "Except", "Apart from", "Or", "Either", "Alternatively", "Nor", "And", "But", "Or,", "And/or"} {
		text := "Security clearance is required\n" + word + " remote"
		if got := joinPostingLines(text); got != strings.ReplaceAll(text, "\n", " ") {
			t.Errorf("continuation %q not joined: %q", word, got)
		}
	}
}

func TestGateWarningsAcrossReadings(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		warnings   []string
		reject     bool
	}{
		{"joined quote wins", "  Security clearance is required\r\n  Unless you work remotely.", []string{
			`Posting requires eligibility: "Security clearance is required Unless you work remotely."`,
		}, false},
		{"raw restores warning", "Requires German\niOS experience", []string{
			`Posting requires German beyond the listed profile languages or level: "Requires German"`,
		}, false},
		{"separate eligibility occurrence survives", "US citizenship is required.\nMust hold an active Secret clearance\ngRPC experience preferred", []string{
			`Posting requires eligibility: "US citizenship is required."`,
			`Posting requires eligibility: "Must hold an active Secret clearance"`,
		}, true},
		{"separate language occurrence survives", "German is required.\nRequires German\niOS experience", []string{
			`Posting requires German beyond the listed profile languages or level: "German is required."`,
			`Posting requires German beyond the listed profile languages or level: "Requires German"`,
		}, true},
		{"raw cannot reject", "Remote: no\nmust be a U.S. citizen", []string{
			`Posting requires eligibility: "must be a U.S. citizen"`,
		}, false},
		{"joined requirement", "US citizenship\nis required.", []string{
			`Posting requires eligibility: "US citizenship is required."`,
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				e := Evaluate(models.Profile{Languages: []string{"English"}}, nil, gateFilter(t, enabled, enabled), models.JobInput{Description: tc.text}, time.Time{})
				if !slices.Equal(e.Warnings, tc.warnings) || e.Rejected != (enabled && tc.reject) {
					t.Fatalf("rules=%t: warnings=%q, rejected=%t; want warnings=%q, rejected=%t", enabled, e.Warnings, e.Rejected, tc.warnings, enabled && tc.reject)
				}
			}
		})
	}
}
