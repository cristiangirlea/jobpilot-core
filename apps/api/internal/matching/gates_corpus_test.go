package matching

import (
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/models"
)

// Fifth review: every explicit requirement still warns, but its alternatives,
// conditional context, limited scope or wrapped continuation forbids rejection.
var fifthReviewWarningOnly = []string{
	// Scope limits after a neighbouring term still prevent rejection.
	"German is required, French only for the Paris office.",
	"Security clearance is required, US citizenship only for on-site staff.",
	// Focused review B1/B2: later terms must not hide conditions or alternatives.
	"US citizenship is required, except for permanent residents.",
	"Security clearance is required, unless you are a US citizen.",
	"German is required, unless you speak French.",
	"Applicants must be a U.S. citizen, U.S. national, lawful permanent resident, refugee, or asylee.",
	"Applicants must be a U.S. citizen, a U.S. permanent resident or asylee.",
	// Focused review D1: capitalisation and punctuation do not hide continuations.
	"Security clearance is required\nUnless you work remotely.",
	"Security clearance is required<br>Unless you work remotely.",
	"US citizenship is required\nExcept for permanent residents.",
	"German is required\nIf you cover our French accounts.",
	"Must be a U.S. citizen\nEither that or a permanent resident.",
	"Must be a U.S. citizen\nAlternatively, a permanent resident.",
	"Must be a U.S. citizen\nNor may permanent residents apply.",
	"Must be a U.S. citizen\nOr, a permanent resident.",
	"Must be a U.S. citizen\nand/or a permanent resident.",
	// B1: the alternative can follow another comma-separated eligibility term.
	"Applicants must be a U.S. citizen, lawful permanent resident, or protected individual as defined by 8 U.S.C. 1324b(a)(3).",
	"Applicants must be a U.S. citizen, lawful permanent resident, or protected individual as defined by 8 U.S.C. 1324b(a)(3), or eligible to obtain the required authorizations.",
	"Applicants must be a U.S. citizen, lawful permanent resident, protected individual as defined by 8 U.S.C. 1324b(a)(3), or eligible to obtain the required authorizations.",
	// B2: only applying/eligibility is a strict predicate after "Only ... citizens".
	"Only EU citizens can be hired directly, and everyone else joins as a contractor through our partner.",
	"Only U.S. citizens can access the classified programme; this role is on the commercial team.",
	// B3: a neighbouring requirement must not hide the conditional prefix.
	"If the client requires a security clearance, U.S. citizenship is required.",
	"If you cover our French accounts, French is required.",
	"When the client requires a security clearance, U.S. citizenship is required.",
	"Where you cover our French accounts, French is required.",
	"Security clearance is required, unless you work remotely.",
	"German is required, unless you join the London team.",
	// B4: <br> is represented by Readable's newline at the matcher boundary.
	"Must be a U.S. citizen\nor lawful permanent resident.",
	"Must be a U.S. citizen<br>(green card holders also considered)",
	"Must be a U.S. citizen\n(green card holders also considered)",
	"Think you must speak German\nto join us? You don't.",
	"Must be a U.S. citizen\r\nOr lawful permanent resident.",
	"German is required\nand only for the Munich office.",
	"Security clearance is required\nBut only for on-site work.",
	// B5: company/client/office requirements do not prove this job's scope.
	"U.S. citizenship is required for certain positions due to export control regulations.",
	"A security clearance is required for some client projects.",
	"German is required for the Munich office.",
	"Security clearance is required, but only for on-site work.",
	"US citizenship is required, except for our Canadian office.",
	"German is required only for the Munich office.",
	"French is required, apart from our London team.",
}

func corpusPosting(text string) models.JobInput {
	// Discovery.Readable converts <br> to a newline before matching.
	return models.JobInput{Description: strings.ReplaceAll(text, "<br>", "\n")}
}

// Ordinary posting prose must not make a job unavailable.
func TestGatesDoNotRejectOrdinaryPostingText(t *testing.T) {
	for _, text := range append([]string{
		"Our team in Berlin speaks German and English.",
		"We are a French company serving customers across Europe.",
		"Our offices in Canada and Australia collaborate daily.",
		"The German team builds tools for our Japanese customers.",
		"English is our company language.",
		"Our support team speaks Spanish, Portuguese and English.",
		"Attention to detail and polish is essential.",
		"UI polish is a must.",
		"This screen requires polish.",
		"You will build a native app for iOS and Android.",
		"Our product needs fluent animations and accessible navigation.",
		"We value clear communication and professional design.",
		"You will improve the quality of our native components.",
		"Our customers require Japanese localisation.",
		"This role requires German market experience.",
		"You will research the needs of French customers.",
		"The product requires English localisation before release.",
		"Our German market team needs better reporting.",
		"You will review Italian translations with our localisation team.",
		"Our platform integrates with customs clearance systems.",
		"Products require government clearance before import.",
		"You will need to coordinate security clearance applications for new starters.",
		"This media role requires rights clearance.",
		"Medical clearance is required before start.",
		"You will need to get legal clearance for releases.",
		"Customs clearance required for every shipment is tracked in our platform.",
		"You will manage clearance sales and stock reporting.",
		"Our HR team supports colleagues through security clearance renewals.",
		"Good corporate citizenship is essential to how we work.",
		"This role requires strong corporate citizenship.",
		"We promote responsible digital citizenship in schools.",
		"As engineers we must be good citizens of the open-source community.",
		"Our mission supports global citizenship and social responsibility.",
		"We build accessible services for senior citizens.",
		"We welcome citizens of all countries.",
		"German is preferred but not required.",
		"Basic Spanish is helpful.",
		"Advanced French is an advantage.",
		"Nice to have: fluent German.",
		"You must speak German, English or French.",
		"Required languages: German, English and/or Italian.",
		"Fluent French or German is required.",
		"Benefits include visa support for relocation.",
		"Visa sponsorship is available for successful applicants.",
		"International applicants are welcome.",
		"Our relocation team provides work permit guidance.",
		"The process includes a background check.",
		"We conduct a right-to-work check on day one.",
		"HR will explain the work authorization documents during onboarding.",
		"No security clearance is required.",
		"You don't need to speak German — our working language is English.",
		"You don’t need to speak German — our working language is English.",
		"This role does not require a security clearance.",
		"This role does not require US citizenship.",
		"Candidates do not need to be U.S. citizens.",
		"does not require permanent residency",
		"An active security clearance is a plus.",
		"A current Secret clearance is preferred.",
		"Candidates who hold an active security clearance are encouraged to apply.",
		"This role requires strong engineering citizenship.",
		"Team citizenship is essential.",
		"Good engineering citizenship is essential.",
		"You must have experience with citizenship applications.",
		"The role requires processing citizenship applications.",
		"must have experience with permanent residency applications",
		"requires handling permanent residency cases",
		"Government clearance is required before devices ship.",
		"Federal clearance is required before devices ship.",
		"This role requires German, Austrian and Swiss payroll knowledge.",
		"Applicants never need to have US citizenship.",
		"We no longer require US citizenship.",
		"Candidates don't need to be US citizens.",
		"Candidates don’t need to be US citizens.",
		"Apply without needing to have US citizenship.",
		"US citizenship is required ideally.",
		"Preferred: applicants must have US citizenship.",
		"Applicants must have US citizenship, a bonus for this project.",
		"Applicants never need to have permanent residency.",
		"We no longer require permanent residency.",
		"Applicants don't need to have permanent residency.",
		"Applicants don’t need to have permanent residency.",
		"Apply without needing to have permanent residency.",
		"Permanent residency is required, but optional for remote applicants.",
		"Applicants must hold permanent residency, which is desirable.",
		"Permanent residency is required ideally.",
		"Applicants never need to hold a security clearance.",
		"We no longer require a security clearance.",
		"You don't need to hold a security clearance.",
		"You don’t need to hold a security clearance.",
		"Apply without needing to hold a security clearance.",
		"An active security clearance is helpful.",
		"A current Secret clearance is nice to have.",
		"Candidates with an active security clearance are welcome.",
		"You never need to speak German.",
		"You no longer need to speak German.",
		"Apply without needing to speak German.",
		"This role doesn't require German.",
		"This role doesn’t require German.",
		"You must speak German, which is a plus.",
		"You must speak German, which is a bonus.",
		"Ideally, you must speak German.",
		"You must speak German, which is desirable.",
		"Applicants who must speak German are encouraged to apply.",
		"You must speak German, a welcome skill.",
		"You must speak German, which is helpful.",
		"You must speak German, which is preferred.",
		"Optional: you must speak German.",
		"Nice to have: you must speak German.",
		"We require valid citizenship applications.",
		"We require valid citizenship cases.",
		"We require valid citizenship process expertise.",
		"We require valid citizenship services.",
		"We require valid citizenship law knowledge.",
		"We require valid citizenship records.",
		"We require valid permanent residency applications.",
		"We require valid permanent residency cases.",
		"We require valid permanent residency process expertise.",
		"We require valid permanent residency services.",
		"We require valid permanent residency law knowledge.",
		"We require valid permanent residency records.",
		// Fourth review, A-C and E-F (verbatim).
		"No US citizenship required.",
		"Engineer (No US Citizenship Required)",
		"No sponsorship available, must be a US citizen",
		"No Australian permanent residency required.",
		"There is no need to be a US citizen.",
		"There's no need to hold a security clearance.",
		"No security clearance or US citizenship is required.",
		"Neither a degree nor a security clearance is required.",
		"Neither German nor French is required.",
		"We do not require a degree, nor do we require US citizenship.",
		"You don't need a degree, nor do you need to speak German.",
		"None of our roles require US citizenship.",
		"None of our positions require a security clearance.",
		"Do I need to be a US citizen? No, we hire worldwide.",
		"Does this role require a security clearance? No.",
		"Does the role require US citizenship? No.",
		"Is German required? No, English is our working language.",
		"Security Clearance Required: No",
		"Citizenship required: No",
		"Security clearance required: None",
		"This role requires US citizenship or permanent residency.",
		"U.S. citizenship or permanent residency is required.",
		"Applicants must be a U.S. citizen, or a lawful permanent resident.",
		"If you are not a US citizen, you must be a permanent resident.",
		"This role requires French, Italian and Spanish market knowledge.",
		"The product requires English, German and French localisation before release.",
		"This role requires German, Dutch and Belgian tax knowledge.",
		"Active TS/SCI clearance desired.",
		"Current SC clearance would be advantageous.",
		"An active security clearance is an asset.",
		"An active security clearance is a nice-to-have.",
		"Preferably you hold an active Secret clearance.",
		"Candidates with an active security clearance will be given priority.",
		"An active security clearance isn't required.",
		"A current security clearance is not needed.",
		"A current security clearance is not essential.",
		// Fourth review's lower-confidence classes.
		"You will support clients who need to become US citizens.",
		"You will guide new starters who need to obtain a security clearance.",
		"This role requires security clearance vetting experience.",
		"Maintain current security clearance records for all cleared staff.",
		"If a client requires a security clearance, we sponsor the process.",
		"If the role requires EU citizenship, we will tell you upfront.",
		"This is not a role that requires US citizenship.",
		"Polish is essential.",
		"We must be Good Citizens.",
		"requires Organizational Citizenship",
		"Nice to have\nActive Secret clearance",
		// Additional adversarial examples: negations, FAQ/fields, alternatives,
		// adjective lists and every preference class around strict templates.
		"No degree is necessary; US citizenship is required.",
		"No travel is expected, German is required.",
		"Neither experience nor French is required.",
		"None of these jobs say you must be a US citizen.",
		"Without a degree, you must be a permanent resident.",
		"We never say German is required.",
		"We don't say you must be a US citizen.",
		"We don’t say you must have an active Secret clearance.",
		"You must be a US citizen?",
		"Permanent residency is required? No.",
		"Must be able to obtain a Secret clearance? No.",
		"German (C1) is required? No.",
		"FAQ: must speak French? We train everyone.",
		"US citizenship is required: No",
		"Permanent residency is required: None",
		"Security clearance is required: N/A",
		"German is required: N/A",
		"Clearance: None",
		"N/A",
		"Citizenship required: N/A",
		"Only US citizens or permanent residents may apply.",
		"Must be a permanent resident or a US citizen.",
		"Must be a US citizen and/or a permanent resident.",
		"Either German is required or French is required.",
		"German is required, alternatively French is accepted.",
		"Required languages: German, French or Italian.",
		"Required languages: German and/or French.",
		"Required languages: German, French and Italian market knowledge.",
		"This role requires Spanish, Portuguese and French tax knowledge.",
		"The product requires Italian, German and Dutch localisation.",
		"US citizenship is required, a plus.",
		"Must be a permanent resident, a bonus.",
		"Security clearance is required, nice-to-have.",
		"German is required, preferred.",
		"Preferably, you must be a US citizen.",
		"Permanent residency is required, desired.",
		"Secret clearance is required, desirable.",
		"French is required, an advantage.",
		"Must speak German, advantageous.",
		"Must have an active Secret clearance, an asset.",
		"Must be a US citizen, beneficial.",
		"Must be a permanent resident, priority given.",
		"Candidates who must speak German are encouraged.",
		"Candidates who must have an active SC clearance are welcome.",
		"Must speak French, helpful.",
		"Ideally, permanent residency is required.",
		"Optional: only US citizens may apply.",
		"Nice-to-have: German (C1) is required.",
		"Must be a US citizen, or hold an equivalent work permit.",
		"If security clearance is required, we arrange it.",
		"Where German is required, training is provided.",
		"This role requires US citizenship.",
		"German is a must.",
		"German teams work with French teams, but German is required? No.",
	}, fifthReviewWarningOnly...) {
		t.Run(text, func(t *testing.T) {
			e := Evaluate(models.Profile{Languages: []string{"English"}}, nil, gateFilter(t, true, true), corpusPosting(text), time.Time{})
			if e.Rejected {
				t.Fatalf("ordinary text rejected: %+v", e)
			}
		})
	}
}

func TestGateRequirementWindows(t *testing.T) {
	for _, tc := range []struct {
		text, warning, absent string
		reject                bool
	}{
		{"Fluent German is required, English is a plus.", "Posting requires German", "Posting requires English", true},
		{"German (C1) is required, French is an advantage.", "Posting requires German", "Posting requires French", true},
		{"Fluent German is required and English is preferred.", "Posting requires German", "Posting requires English", true},
		{"French is preferred, German is required.", "Posting requires German", "Posting requires French", true},
		{"German is required, French is not required.", "Posting requires German", "Posting requires French", false},
		{"Must be a U.S. citizen, active Secret clearance preferred.", "Posting requires eligibility", "", true},
		{"An active security clearance isn't required.", "", "Posting requires eligibility", false},
		{"A current security clearance is not needed.", "", "Posting requires eligibility", false},
		{"A current security clearance is not essential.", "", "Posting requires eligibility", false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				e := Evaluate(models.Profile{Languages: []string{"English"}}, nil, gateFilter(t, enabled, enabled), models.JobInput{Description: tc.text}, time.Time{})
				warnings := strings.Join(e.Warnings, "\n")
				if !strings.Contains(warnings, tc.warning) || tc.absent != "" && strings.Contains(warnings, tc.absent) || e.Rejected != (enabled && tc.reject) {
					t.Fatalf("rules=%t: %+v", enabled, e)
				}
			}
		})
	}
}

// Conservative detection must still warn about explicit requirements.
func TestGatesWarnForRealRequirements(t *testing.T) {
	for _, text := range append([]string{
		"Applicants must be a citizen of Canada.",
		"U.S. citizenship is required.",
		"Only US citizens may apply.",
		"Must be a U.S. citizen or permanent resident.",
		"You must be a permanent resident of Australia.",
		"Permanent resident status is required.",
		"An active security clearance is required.",
		"Candidates must be eligible to obtain a security clearance.",
		"Must be able to obtain (and maintain) a Secret clearance.",
		"This role requires the ability to obtain a security clearance.",
		"Active TS/SCI clearance required.",
		"Fluent German is required.",
		"You must speak French.",
		"Required languages: Dutch, Italian.",
		"Professional proficiency in Arabic is required.",
		"Fluent French or German is required.",
		"We cannot provide visas.",
		"We are unable to offer visas.",
		"No sponsorship is available.",
		"Applicants must already have the right to work in the UK.",
		"You must hold a valid work permit.",
		"You must be legally authorized to work in the US.",
		"German and English are required.",
		"English and German are required.",
		"German, French and English are required.",
		"This role requires German, French or Italian.",
		"Applicants must have dual US citizenship.",
		"This role requires Canadian citizenship.",
		"Applicants must hold valid permanent residency.",
		// Fourth review D: the other requirement's preference cannot hide these.
		"Fluent German is required, English is a plus.",
		"German (C1) is required, French is an advantage.",
		"Fluent German is required and English is preferred.",
		"Must be a U.S. citizen, active Secret clearance preferred.",
		"French is preferred, German is required.",
		"Active Secret clearance preferred, US citizenship is required.",
		"Is German required? No, English is our working language.",
		"You will support clients who need to become US citizens.",
		"This role requires security clearance vetting experience.",
		"German is a must.",
		// Focused review B3: unrelated lowercase lines must not erase warnings.
		"Must hold an active Secret clearance\ngRPC experience preferred",
		"Requirements:<br>must be a U.S. citizen<br>iOS experience is a plus",
		"Requires German\niOS experience",
		"Remote: no\nmust be a U.S. citizen",
		"Requirements:\no must be a U.S. citizen\no iOS experience is a plus",
	}, fifthReviewWarningOnly...) {
		t.Run(text, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				e := Evaluate(models.Profile{Languages: []string{"English"}}, nil, gateFilter(t, enabled, enabled), corpusPosting(text), time.Time{})
				if len(e.Warnings) == 0 {
					t.Fatalf("missing requirement warning (rules=%t): %+v", enabled, e)
				}
			}
		})
	}
}

// Strict templates still make final rejections when their own rule is on.
func TestGatesRejectStrictRequirements(t *testing.T) {
	for _, tc := range []struct {
		name        string
		eligibility bool
		texts       []string
	}{
		{"citizenship", true, []string{
			"Must be a US citizen.", "Applicants must be a U.S. citizen.",
			"You must be a Canadian citizen.", "Must be an Australian citizen.",
			"Must be a British citizen.", "Must be a Romanian citizen.",
			"US citizenship is required.", "U.S. citizenship is required.",
			"Canadian citizenship is required.", "Australian citizenship is required.",
			"British citizenship is required.", "Only US citizens may apply.",
			"Only U.S. citizens can apply.", "Only Canadian citizens are eligible.",
			"Only Australian citizens.",
			"US citizenship is required for the position.",
			"Must be a U.S. citizen, active Secret clearance preferred.",
			"No clearance required\nMust be a U.S. citizen.",
			"No clearance required\n• Must be a U.S. citizen.",
		}},
		{"residency", true, []string{
			"Must be a permanent resident.", "You must be a permanent resident.",
			"Applicants must be a permanent resident.", "Candidates must be a permanent resident.",
			"Must be a permanent resident of Australia.", "Must be a permanent resident of Canada.",
			"You must be a permanent resident to apply.", "Must be a permanent resident for this role.",
			"Permanent residency is required.", "Permanent residency is required for this role.",
			"Permanent residency is required before starting.", "Permanent residency is required to apply.",
			"Eligibility: permanent residency is required.", "Permanent residency is required!",
			"Must be a permanent resident, Secret clearance preferred.",
		}},
		{"clearance", true, []string{
			"Security clearance is required.", "Secret clearance is required.",
			"Top Secret clearance is required.", "TS/SCI clearance is required.",
			"SC clearance is required.", "DV clearance is required.",
			"Public Trust clearance is required.", "NV1 clearance is required.",
			"Must hold an active Secret clearance.", "Must have an active security clearance.",
			"Applicants must hold an active TS/SCI clearance.", "Must have an active DV clearance.",
			"Must be able to obtain a Secret clearance.", "Must be able to obtain a security clearance.",
			"Must be able to obtain a Public Trust clearance.",
		}},
		{"languages", false, []string{
			"German is required.", "French is required.", "Dutch is required.",
			"German (C1) is required.", "French (B2) is required.",
			"Spanish (native) is required.", "You must speak German.",
			"Must speak French.", "Must be fluent in Portuguese.",
			"You must be fluent in Italian.", "Required languages: Dutch, Italian.",
			"Required languages: German and French.",
			"Required languages: German, French and Italian.",
			"Required languages: English, German.", "Required languages: German.",
			"Fluent German is required, English is a plus.",
			"German (C1) is required, French is an advantage.",
			"Fluent German is required and English is preferred.",
			"German is required for this role.", "French is required for the job.",
			"No French required\n• German is required.",
		}},
	} {
		for _, text := range tc.texts {
			t.Run(tc.name+"/"+text, func(t *testing.T) {
				for _, enabled := range []bool{false, true} {
					f := gateFilter(t, enabled && tc.eligibility, enabled && !tc.eligibility)
					e := Evaluate(models.Profile{Languages: []string{"English"}}, nil, f, models.JobInput{Description: text}, time.Time{})
					if len(e.Warnings) == 0 || e.Rejected != enabled {
						t.Fatalf("rule=%t: %+v", enabled, e)
					}
				}
			})
		}
	}
}
