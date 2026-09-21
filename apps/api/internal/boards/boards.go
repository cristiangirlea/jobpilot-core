// Package boards helps a user find employer job boards worth following.
//
// A discovery source needs a board token, and a token is only discoverable by
// opening a company's careers page and reading the URL. That is a poor first
// experience, so this package offers two ways past it: a short curated list of
// employers verified to publish remote roles, and extraction of a token from any
// careers URL the user pastes.
package boards

import (
	"strings"

	"jobpilot/core/api/internal/ats"
)

// Suggestion is one employer board a user can follow.
//
// Deliberately no job counts: they change daily, and a stale number in source
// is worse than none. The existing source-validation endpoint fetches live
// counts when the user adds a board.
type Suggestion struct {
	Provider   string `json:"provider"`
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	Note       string `json:"note"`
	// RemoteFriendly marks employers whose postings are predominantly remote.
	RemoteFriendly bool `json:"remote_friendly"`
}

// Suggested lists employer boards confirmed to respond and to carry remote
// engineering roles. Every entry here was checked against the live board API.
//
// Note the location wording differences: Canonical labels remote roles "Home
// based - Worldwide" or "Home based - EMEA" rather than "Remote", which a
// naive remote filter misses entirely. Matching reads the location text, so
// these still come through, but it is why this list is curated by hand.
var Suggested = []Suggestion{
	{ats.ProviderGreenhouse, "canonical", "Canonical (Ubuntu)", "Fully remote. Roles are labelled \"Home based - Worldwide\" or \"- EMEA\" rather than \"Remote\".", true},
	{ats.ProviderGreenhouse, "grafanalabs", "Grafana Labs", "All-remote, with the strongest share of Europe-eligible roles of the boards checked.", true},
	{ats.ProviderGreenhouse, "gitlab", "GitLab", "All-remote. Large board, many Go and platform roles.", true},
	{ats.ProviderGreenhouse, "mozilla", "Mozilla", "Every posting is remote; a good share are Europe-eligible.", true},
	{ats.ProviderGreenhouse, "datadog", "Datadog", "Large board, mostly office-based, with a steady remote minority.", false},
	{ats.ProviderGreenhouse, "vercel", "Vercel", "Frontend and platform heavy; a minority of roles are remote.", false},
	{ats.ProviderGreenhouse, "typeform", "Typeform", "Barcelona-based, mostly remote, Europe-eligible.", true},
	{ats.ProviderGreenhouse, "sumup", "SumUp", "Berlin-based fintech. City-labelled roles across Europe rather than remote.", false},
	{ats.ProviderGreenhouse, "gocardless", "GoCardless", "Fintech with offices across Europe. City-labelled roles.", false},
	{ats.ProviderGreenhouse, "elastic", "Elastic", "Distributed company; roles are city-labelled rather than marked remote.", false},
	{ats.ProviderGreenhouse, "cockroachlabs", "Cockroach Labs", "Database engineering; a small remote share.", false},
	{ats.ProviderGreenhouse, "doximity", "Doximity", "Small board, mostly remote.", true},
	{ats.ProviderRemotive, "", "Remotive (aggregated)", "A general remote-jobs feed. Good for coverage, but its links stay on Remotive rather than reaching the employer's form.", true},
}

// Detected is a board token pulled out of a careers URL.
type Detected struct {
	Provider   string `json:"provider"`
	Identifier string `json:"identifier"`
	JobID      string `json:"job_id,omitempty"`
	// Supported reports whether discovery can follow this provider as a source.
	Supported bool `json:"supported"`
}

// discoverable lists the providers the discovery fetcher can follow. It is
// separate from what the ATS reader can parse: a board can be crawled for jobs
// even when its application form is not published.
var discoverable = map[string]bool{
	ats.ProviderGreenhouse:      true,
	ats.ProviderLever:           true,
	ats.ProviderAshby:           true,
	ats.ProviderSmartRecruiters: true,
}

// Extract pulls a board token out of any careers or posting URL, so a user can
// paste what they are looking at rather than work out which part is the token.
func Extract(rawURL string) (Detected, bool) {
	provider, board, jobID, ok := ats.Detect(rawURL)
	if ok {
		return Detected{Provider: provider, Identifier: board, JobID: jobID, Supported: discoverable[provider]}, true
	}
	// A board's index page carries no job id, so it is matched separately.
	if provider, board, ok = detectBoardRoot(rawURL); ok {
		return Detected{Provider: provider, Identifier: board, Supported: discoverable[provider]}, true
	}
	return Detected{}, false
}

// detectBoardRoot handles the listing pages, which have no job id:
// job-boards.greenhouse.io/acme, jobs.lever.co/acme, jobs.ashbyhq.com/acme.
func detectBoardRoot(rawURL string) (provider, board string, ok bool) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", "", false
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	// Reusing Detect with a synthetic job id keeps one definition of each host
	// pattern rather than a second copy that can drift out of step.
	for _, suffix := range []string{"/jobs/1", "/1"} {
		if provider, board, _, ok = ats.Detect(strings.TrimRight(trimmed, "/") + suffix); ok {
			return provider, board, true
		}
	}
	return "", "", false
}
