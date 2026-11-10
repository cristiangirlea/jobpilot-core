package matching

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"jobpilot/core/api/internal/models"
)

func TestReviewEligibility(t *testing.T) {
	for _, tc := range []struct {
		text            string
		warning, reject bool
	}{
		{"Good corporate citizenship is essential.", false, false},
		{"This role requires strong corporate citizenship.", false, false},
		{"Digital citizenship is required.", false, false},
		{"Global citizenship is mandatory.", false, false},
		{"Good citizenship is essential.", false, false},
		{"Social citizenship is required.", false, false},
		{"Responsible citizenship is mandatory.", false, false},
		{"Corporate citizenship is required and digital citizenship is essential.", false, false},
		{"Corporate citizenship is essential and this role requires US citizenship.", true, false},
		{"Must be a U.S. citizen or permanent resident.", true, false},
		{"Must be a U.S. citizen or a lawful permanent resident.", true, false},
		{"Must be a U.S. citizen or permanent resident; security clearance is required.", true, true},
		{"Must be able to obtain a Secret clearance.", true, true},
		{"Must be able to obtain and maintain a Secret clearance.", true, false},
		{"Must be able to obtain (and maintain) a Secret clearance.", true, false},
		{"This role requires the ability to obtain a security clearance.", true, false},
		{"You will need to coordinate security clearance applications.", false, false},
		{"Products require government clearance before import.", false, false},
		{"We cannot provide visas.", true, false},
		{"We are unable to offer visas.", true, false},
		{"Our platform requires deep integration with customs clearance systems.", false, false},
		{"You will need to get legal clearance for releases.", false, false},
		{"Must pass a background check and medical clearance.", false, false},
		{"This media role requires rights clearance.", false, false},
		{"Medical clearance is required before start.", false, false},
		{"Customs clearance required.", false, false},
		{"This role requires working closely with citizens and stakeholders.", false, false},
		{"This role requires empathy for senior citizens.", false, false},
		{"As engineers we must be good citizens of the open-source community.", false, false},
		{"As engineers we must be citizens of the open-source community.", false, false},
		{"You must be a citizen of the open-source community.", false, false},
		{"You must hold a valid work permit.", true, false},
		{"You must have a valid work permit.", true, false},
		{"Applicants must be eligible to work in the EU.", true, false},
		{"Eligible to work in the US.", true, false},
		{"Eligible to work in Canada.", true, false},
		{"You must be a US citizen to apply.", true, true},
		{"You must be a U.S. citizen or green card holder.", true, false},
		{"You must be a U.S. citizen or green card holder and security clearance is required.", true, true},
		{"This role requires US citizenship.", true, false},
		{"Applicants are restricted to US citizens.", true, false},
		{"Federal clearance required.", false, false},
		{"Government clearance is mandatory.", false, false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			e := Evaluate(models.Profile{}, nil, &models.FilterPreset{RejectEligibilityRequirements: true}, models.JobInput{Description: tc.text}, time.Time{})
			if (len(e.Warnings) > 0) != tc.warning || e.Rejected != tc.reject {
				t.Fatalf("%+v", e)
			}
		})
	}
}

func TestReviewLanguages(t *testing.T) {
	for _, tc := range []struct {
		text            string
		languages       []string
		warning, reject bool
	}{
		{"You must speak German, French or Italian.", []string{"French"}, false, false},
		{"Must be fluent in German, French or Italian.", []string{"Italian"}, false, false},
		{"Required: fluent German, French or Dutch", []string{"Dutch"}, false, false},
		{"You must speak German, French, or Italian.", []string{"French"}, false, false},
		{"You must speak German, French and/or Italian.", []string{"Italian"}, false, false},
		{"Required languages: German, French and Italian.", []string{"French"}, true, true},
		{"Required languages: Dutch, Italian.", []string{"Dutch"}, true, true},
		{"Our customers require Japanese localisation.", []string{"English"}, false, false},
		{"This role requires German market experience.", []string{"English"}, false, false},
		{"This role requires German-speaking customer research.", []string{"English"}, false, false},
		{"This role requires German (market experience).", []string{"English"}, false, false},
		{"Attention to detail and polish is essential.", []string{"English"}, false, false},
		{"UI polish is a must.", []string{"English"}, false, false},
		{"This screen requires polish.", []string{"English"}, false, false},
		{"Polish is required.", []string{"English"}, true, true},
		{"German is required.", []string{"english"}, true, true},
		{"Fluent English is required.", []string{"EN (native)"}, false, false},
		{"Fluent English is required.", []string{"EN (native)", "DE B1"}, false, false},
		{"Fluent English is required.", []string{"Türkçe", "Русский"}, false, false},
		{"Fluent German is required.", []string{"English", "DE B1"}, true, false},
		{"Fluent German is required.", []string{"English", "Türkçe"}, true, false},
		{"This role requires German.", []string{"English"}, true, false},
		{"This role requires German (C1).", []string{"English"}, true, false},
		{"This role requires German language skills.", []string{"English"}, true, false},
		{"This role requires German proficiency.", []string{"English"}, true, false},
		{"This role requires German fluency.", []string{"English"}, true, false},
		{"Nice to have: fluent German", []string{"English"}, false, false},
		{"Nice to have\nFluent German", []string{"English"}, false, false},
		{"Advanced German is an advantage.", []string{"English"}, false, false},
		{"Basic Spanish is helpful.", []string{"English"}, false, false},
		{"Fluent English or German is required.", []string{"English"}, false, false},
		{"Fluent English and/or German is required.", []string{"English"}, false, false},
		{"Fluent English or German is required.", []string{"French"}, true, false},
		{"You must speak English or German.", []string{"German"}, false, false},
		{"English and German are required.", []string{"English"}, true, false},
		{"Required languages: English, German (nice to have)", []string{"English"}, false, false},
		{"Our customers need Japanese localisation.", []string{"English"}, false, false},
		{"German teams require Germanium.", []string{"English"}, false, false},
		{"German products and OldGerman is required.", []string{"English"}, false, false},
		{"German C1 is required.", []string{"English"}, true, false},
		{"Fluent German is a must.", []string{"English"}, true, false},
		{"For this role, German is a must.", []string{"English"}, true, false},
		{"Fluent English is required.", nil, false, false},
		{"Fluent English is required.", []string{}, false, false},
		{"Fluent Chinese is required.", []string{"Mandarin fluent"}, false, false},
		{"Mandarin C1 is required.", []string{"Chinese B1"}, true, false},
		{"Languages: fluent German", []string{"English"}, true, false},
		{"Required: fluent German", []string{"English"}, true, false},
		{"About us: fluent German", []string{"English"}, false, false},
	} {
		t.Run(tc.text+strings.Join(tc.languages, ","), func(t *testing.T) {
			e := Evaluate(models.Profile{Languages: tc.languages}, nil, &models.FilterPreset{RejectUnlistedLanguages: true}, models.JobInput{Description: tc.text}, time.Time{})
			if (len(e.Warnings) > 0) != tc.warning || e.Rejected != tc.reject {
				t.Fatalf("%+v", e)
			}
		})
	}
}

func TestReviewLanguageAliases(t *testing.T) {
	for _, tc := range []struct{ name, alias string }{
		{"German", "Deutsch"}, {"French", "Français"}, {"Spanish", "Español"}, {"Italian", "Italiano"},
		{"Dutch", "Nederlands"}, {"Romanian", "Română"}, {"Polish", "Polski"}, {"Portuguese", "Português"},
		{"Swedish", "Svenska"}, {"Norwegian", "Norsk"}, {"Danish", "Dansk"}, {"Finnish", "Suomi"},
		{"Hungarian", "Magyar"}, {"Czech", "Čeština"}, {"Greek", "Ελληνικά"},
	} {
		for _, direction := range []struct{ posting, profile string }{{tc.name, tc.alias}, {tc.alias, tc.name}, {tc.alias, "English"}} {
			t.Run(direction.posting+direction.profile, func(t *testing.T) {
				e := Evaluate(models.Profile{Languages: []string{direction.profile}}, nil, &models.FilterPreset{RejectUnlistedLanguages: true}, models.JobInput{Description: direction.posting + " is required."}, time.Time{})
				want := direction.profile == "English"
				if e.Rejected != want || (len(e.Warnings) > 0) != want {
					t.Fatalf("%+v", e)
				}
			})
		}
	}
}

func TestReviewQuoteWindow(t *testing.T) {
	for _, requirement := range []string{"Security clearance is required", "German is required", "Visa sponsorship available"} {
		t.Run(requirement, func(t *testing.T) {
			prefix := strings.Repeat("é software ", 35) + "; "
			if requirement == "German is required" {
				prefix = "German products " + strings.Repeat("é software ", 35)
			}
			e := Evaluate(models.Profile{Languages: []string{"English"}}, nil, &models.FilterPreset{RejectEligibilityRequirements: true, RejectUnlistedLanguages: true}, models.JobInput{Description: prefix + requirement + strings.Repeat(" for teams", 30)}, time.Time{})
			if len(e.Warnings) == 0 {
				t.Fatal("missing finding")
			}
			for _, message := range append(e.Warnings, e.RejectionReasons...) {
				parts := strings.Split(message, `"`)
				if len(parts) != 3 || !strings.Contains(parts[1], requirement) || !utf8.ValidString(parts[1]) || utf8.RuneCountInString(parts[1]) > 200 {
					t.Fatal(message)
				}
			}
		})
	}
}

func BenchmarkEvaluate(b *testing.B) {
	p := sampleProfile()
	p.Languages = []string{"English fluent", "Romanian native"}
	description := strings.Repeat("Build reliable Go services with PostgreSQL and Kubernetes for our customers across Europe. Collaborate with product engineers on testing, observability and secure releases. ", 23) + "Fluent English is required. German is preferred. Applicants must have the right to work in the EU."
	j := models.JobInput{Title: "Senior Backend Engineer", Company: "Example Systems", RemoteType: "remote", Description: description}
	f := &models.FilterPreset{RejectEligibilityRequirements: true, RejectUnlistedLanguages: true}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Evaluate(p, nil, f, j, time.Time{})
	}
	b.ReportMetric(float64(len(description)), "chars/job")
}

// A definition in brackets after "citizen", or "to fill this position" where
// "to apply" was expected, hid the requirement. It warns, with or without the
// filter; rejecting stays with the smaller strict grammar.
func TestCitizenshipWithADefinitionOrARoleWarns(t *testing.T) {
	for _, tc := range []struct {
		text    string
		warning bool
	}{
		{"You must be a United States citizen to apply.", true},
		{"Due to customer requirements, you must be a United States Citizen (defined as any individual who is a citizen by law, birth, or naturalization) to fill this position.", true},
		{"You must be a U.S. citizen (as defined by statute).", true},
		{"Applicants must be a United States citizen to fill this position.", true},
		{"You must be a Swiss citizen to be considered for the role.", true},
		{"Candidates need to be a Swiss citizen for this position", true},
		{"Applicants must be Swiss citizens to hold this job.", true},
		// Without a nationality the words say nothing about a legal status,
		// and a capital letter does not make a nationality.
		{"We must be citizens (in the broad sense) of our community.", false},
		{"You must be a citizen (and a neighbour) of the open-source community.", false},
		{"You must be a good citizen for this role.", false},
		{"You must be a Good Citizen for this role.", false},
		{"We must be Global Citizens (people who contribute to our community).", false},
		{"We must be Good Citizens.", false},
		// A country after the noun is a legal status again.
		{"Applicants must be Good Citizens of Canada.", true},
		{"You do not need to be a United States citizen (or a resident) for this role.", false},
		{"Ideally you would be a United States citizen (defined by law) for this role.", false},
	} {
		for name, filter := range map[string]*models.FilterPreset{"filter on": {RejectEligibilityRequirements: true}, "no filter": nil} {
			t.Run(name+": "+tc.text, func(t *testing.T) {
				e := Evaluate(models.Profile{}, nil, filter, models.JobInput{Description: tc.text}, time.Time{})
				warned := false
				for _, warning := range e.Warnings {
					warned = warned || strings.HasPrefix(warning, "Posting requires eligibility: ")
				}
				if warned != tc.warning || e.Rejected {
					t.Fatalf("warned %v, want %v; rejected %v: %+v", warned, tc.warning, e.Rejected, e.Warnings)
				}
			})
		}
	}
}
