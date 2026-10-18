package matching

import (
	"testing"
	"time"

	"jobpilot/core/api/internal/models"
)

// Ordinary posting prose must not make a job unavailable.
func TestGatesDoNotRejectOrdinaryPostingText(t *testing.T) {
	for _, text := range []string{
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
	} {
		t.Run(text, func(t *testing.T) {
			e := Evaluate(models.Profile{Languages: []string{"English"}}, nil, gateFilter(t, true, true), models.JobInput{Description: text}, time.Time{})
			if e.Rejected {
				t.Fatalf("ordinary text rejected: %+v", e)
			}
		})
	}
}

// Conservative detection must still warn about explicit requirements.
func TestGatesWarnForRealRequirements(t *testing.T) {
	for _, text := range []string{
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
	} {
		t.Run(text, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				e := Evaluate(models.Profile{Languages: []string{"English"}}, nil, gateFilter(t, enabled, enabled), models.JobInput{Description: text}, time.Time{})
				if len(e.Warnings) == 0 {
					t.Fatalf("missing requirement warning (rules=%t): %+v", enabled, e)
				}
			}
		})
	}
}
