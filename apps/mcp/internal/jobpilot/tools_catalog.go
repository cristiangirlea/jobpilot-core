package jobpilot

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type importJobInput struct {
	Title          string  `json:"title" jsonschema:"Job title as posted (required)"`
	Company        string  `json:"company" jsonschema:"Employer name as posted (required)"`
	Description    string  `json:"description" jsonschema:"Full posting text (required); matching reads it for skills and technologies"`
	CompanyDomain  string  `json:"company_domain,omitempty" jsonschema:"Employer domain, which sharpens the excluded-employer check"`
	Location       string  `json:"location,omitempty"`
	RemoteType     string  `json:"remote_type,omitempty" jsonschema:"remote, hybrid, or onsite when the posting states one"`
	SalaryMin      float64 `json:"salary_min,omitempty"`
	SalaryMax      float64 `json:"salary_max,omitempty"`
	Currency       string  `json:"currency,omitempty"`
	EmploymentType string  `json:"employment_type,omitempty" jsonschema:"full-time, part-time, contract, freelance, or internship"`
	Industry       string  `json:"industry,omitempty"`
	ApplyURL       string  `json:"apply_url,omitempty" jsonschema:"The employer's apply page, an http or https URL"`
	PostedAt       string  `json:"posted_at,omitempty" jsonschema:"When the job was posted, RFC3339, for example 2026-09-21T00:00:00Z"`
}

type sourceInput struct {
	Provider            string `json:"provider" jsonschema:"remotive, greenhouse, lever, ashby, smartrecruiters, or arbeitnow"`
	Name                string `json:"name" jsonschema:"A label for this source"`
	Identifier          string `json:"identifier,omitempty" jsonschema:"Board token or company slug; required for greenhouse, lever, ashby and smartrecruiters"`
	SearchQuery         string `json:"search_query,omitempty" jsonschema:"Positive OR terms; required for remotive. On an employer board, only titles containing one are kept"`
	ExcludedSearchQuery string `json:"excluded_search_query,omitempty" jsonschema:"Negative NOT terms: arbeitnow checks whole postings, employer boards check titles; ignored for remotive"`
	Active              *bool  `json:"active,omitempty" jsonschema:"Whether the scheduler runs this source; defaults to true"`
}

// setFiltersInput changes only what it names: a missing list or value keeps
// the stored one, and an empty list clears that rule.
type setFiltersInput struct {
	ID                   string   `json:"id,omitempty" jsonschema:"The preset to change; leave out to create one"`
	Name                 *string  `json:"name,omitempty" jsonschema:"Required when creating"`
	Active               *bool    `json:"active,omitempty" jsonschema:"true makes this the only active preset; a new one is inactive unless set"`
	IncludedTitles       []string `json:"included_titles,omitempty" jsonschema:"OR: the title should match one"`
	ExcludedTitleWords   []string `json:"excluded_title_words,omitempty" jsonschema:"NOT: any in the title rejects"`
	IncludedTechnologies []string `json:"included_technologies,omitempty" jsonschema:"OR: the posting must mention one"`
	ExcludedTechnologies []string `json:"excluded_technologies,omitempty" jsonschema:"NOT: any mentioned rejects"`
	RequiredSkills       []string `json:"required_skills,omitempty"`
	Locations            []string `json:"locations,omitempty"`
	RemoteTypes          []string `json:"remote_types,omitempty" jsonschema:"remote, hybrid, onsite"`
	EmploymentTypes      []string `json:"employment_types,omitempty" jsonschema:"for example full-time, contract"`
	ExcludedIndustries   []string `json:"excluded_industries,omitempty"`
	MinSalary            *float64 `json:"min_salary,omitempty" jsonschema:"Known pay below this rejects; 0 turns it off"`
	Currency             *string  `json:"currency,omitempty"`
	MaxAgeDays           *int     `json:"max_age_days,omitempty" jsonschema:"Older postings are rejected; 0 turns it off"`

	RejectEligibilityRequirements *bool `json:"reject_eligibility_requirements,omitempty" jsonschema:"Reject citizenship, permanent residency or clearance requirements; defaults to false"`
	RejectUnlistedLanguages       *bool `json:"reject_unlisted_languages,omitempty" jsonschema:"Reject required languages absent from the profile; defaults to false"`
}

var remoteTypes = []string{"remote", "hybrid", "onsite"}

// applyFilterChanges lays the named changes over a stored preset (or an empty
// one when creating) and checks the result the way the API would, so a bad
// value is explained before anything is saved. Remote types are checked
// against the values discovery stores, since any other word would reject
// every job that states its arrangement.
func applyFilterChanges(base FilterPreset, in setFiltersInput) (FilterPreset, error) {
	out := base
	if in.RejectEligibilityRequirements != nil {
		out.RejectEligibilityRequirements = *in.RejectEligibilityRequirements
	}
	if in.RejectUnlistedLanguages != nil {
		out.RejectUnlistedLanguages = *in.RejectUnlistedLanguages
	}
	if in.Name != nil {
		out.Name = strings.TrimSpace(*in.Name)
	}
	if in.Active != nil {
		out.Active = *in.Active
	}
	for _, field := range []struct {
		change []string
		stored *[]string
	}{
		{in.IncludedTitles, &out.IncludedTitles}, {in.ExcludedTitleWords, &out.ExcludedTitleWords},
		{in.IncludedTechnologies, &out.IncludedTechnologies}, {in.ExcludedTechnologies, &out.ExcludedTechnologies},
		{in.RequiredSkills, &out.RequiredSkills}, {in.Locations, &out.Locations},
		{in.RemoteTypes, &out.RemoteTypes}, {in.EmploymentTypes, &out.EmploymentTypes},
		{in.ExcludedIndustries, &out.ExcludedIndustries},
	} {
		if field.change != nil {
			*field.stored = cleanTerms(field.change)
		}
		if *field.stored == nil {
			*field.stored = []string{}
		}
	}
	if in.MinSalary != nil {
		out.MinSalary = *in.MinSalary
	}
	if in.Currency != nil {
		out.Currency = strings.ToUpper(strings.TrimSpace(*in.Currency))
	}
	if in.MaxAgeDays != nil {
		out.MaxAgeDays = *in.MaxAgeDays
	}
	if out.Name == "" {
		return out, errors.New("a filter preset needs a name")
	}
	if out.MinSalary < 0 || out.MaxAgeDays < 0 {
		return out, errors.New("min_salary and max_age_days cannot be negative; 0 turns the rule off")
	}
	// Only a new list is checked: one saved in the web app is free text and
	// must not block an unrelated change.
	if in.RemoteTypes != nil {
		for i, r := range out.RemoteTypes {
			out.RemoteTypes[i] = strings.ToLower(r)
			if !slices.Contains(remoteTypes, out.RemoteTypes[i]) {
				return out, fmt.Errorf("remote_types takes %s, not %q", strings.Join(remoteTypes, ", "), r)
			}
		}
	}
	return out, nil
}

// cleanTerms trims each term and drops empty ones, so a stray comma cannot
// become a rule that matches everything.
func cleanTerms(terms []string) []string {
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// addCatalogTools registers the tools that shape what the catalog contains:
// manual import, the filters that explain rejections, and discovery sources.
func addCatalogTools(s *mcp.Server, c *Client) {
	addTool(s, &mcp.Tool{
		Name:  "import_job",
		Title: "Import a job posting",
		Description: "Add one job to the shared catalog by hand and evaluate it for the user, for a posting discovery does not cover. " +
			"A job the user's rules reject is still imported and returned with rejected set and its reasons listed; it stays visible for explanation but cannot be applied to. " +
			"An identical posting already in the catalog is reused rather than duplicated.",
		// Repeating an import re-uses the catalog row and upserts the match.
		Annotations: writeTool("Import a job posting", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in importJobInput) (*mcp.CallToolResult, ImportedJob, error) {
		// These mirror the API's own validation so a malformed call is
		// explained without a round trip.
		if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Company) == "" || strings.TrimSpace(in.Description) == "" {
			return nil, ImportedJob{}, errors.New("title, company, and description are required")
		}
		if in.ApplyURL != "" {
			// The same check the API makes, so a malformed URL is explained
			// here rather than coming back as a 422.
			parsed, err := url.ParseRequestURI(in.ApplyURL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return nil, ImportedJob{}, fmt.Errorf("apply_url must be an http or https URL, not %q", in.ApplyURL)
			}
		}
		if in.SalaryMin < 0 || in.SalaryMax < 0 || (in.SalaryMax > 0 && in.SalaryMin > in.SalaryMax) {
			return nil, ImportedJob{}, errors.New("salary range is invalid: neither value may be negative and the minimum cannot exceed the maximum")
		}
		body := JobInput{
			Title: in.Title, Company: in.Company, CompanyDomain: in.CompanyDomain,
			Location: in.Location, RemoteType: in.RemoteType, SalaryMin: in.SalaryMin,
			SalaryMax: in.SalaryMax, Currency: in.Currency, EmploymentType: in.EmploymentType,
			Industry: in.Industry, Description: in.Description, ApplyURL: in.ApplyURL,
		}
		if raw := strings.TrimSpace(in.PostedAt); raw != "" {
			posted, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return nil, ImportedJob{}, fmt.Errorf("posted_at must be an RFC3339 timestamp such as 2026-09-21T00:00:00Z, not %q", in.PostedAt)
			}
			body.PostedAt = &posted
		}
		x, err := c.ImportJob(ctx, body)
		if err != nil {
			return nil, ImportedJob{}, toolError(err)
		}
		x.Match.normalize()
		job := summarizeJob(x.Job)
		if x.Match.Rejected {
			job.ApplyURL = ""
		}
		return nil, ImportedJob{Job: job, Match: x.Match, MatchID: x.MatchID}, nil
	})

	addTool(s, &mcp.Tool{
		Name:  "get_filters",
		Title: "Get filter presets",
		Description: "Return the user's filter presets. Only the active one is applied to matching. " +
			"Included titles and technologies are OR rules, excluded ones are NOT rules where any match rejects the job. " +
			"Read this to explain why a particular job was rejected.",
		Annotations: readOnly("Get filter presets"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, FilterList, error) {
		filters, err := c.Filters(ctx)
		if err != nil {
			return nil, FilterList{}, toolError(err)
		}
		if filters == nil {
			filters = []FilterPreset{}
		}
		return nil, FilterList{Filters: filters}, nil
	})

	addTool(s, &mcp.Tool{
		Name:  "set_filters",
		Title: "Create or change a filter preset",
		Description: "Create a filter preset, or change one from get_filters by its id. Fields left out keep their value; an empty list clears a rule. " +
			"active true makes it the only active preset. Every save re-scores the user's jobs, so tell the user what changed. " +
			"Seniority is a profile setting and minimum score a list_jobs option; neither belongs to a preset.",
		Annotations: writeTool("Create or change a filter preset", false),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in setFiltersInput) (*mcp.CallToolResult, FilterPreset, error) {
		if (in.RejectEligibilityRequirements != nil || in.RejectUnlistedLanguages != nil) && !c.supports(ctx, filterGatesContract) {
			return nil, FilterPreset{}, errors.New("this API predates reject_eligibility_requirements and reject_unlisted_languages, or its version could not be confirmed, and would ignore them; omit these fields or update the API to contract 11 or later")
		}
		base := FilterPreset{}
		if id := strings.TrimSpace(in.ID); id != "" {
			// Only the caller's own presets are listed, so an id that is not
			// there is someone else's or gone, and nothing is sent.
			filters, err := c.Filters(ctx)
			if err != nil {
				return nil, FilterPreset{}, toolError(err)
			}
			found := false
			for _, f := range filters {
				if f.ID == id {
					base, found = f, true
				}
			}
			if !found {
				return nil, FilterPreset{}, fmt.Errorf("no filter preset has id %q; call get_filters for the ids, or leave id out to create one", id)
			}
		}
		preset, err := applyFilterChanges(base, in)
		if err != nil {
			return nil, FilterPreset{}, err
		}
		out, err := c.SaveFilter(ctx, preset)
		if err != nil {
			return nil, FilterPreset{}, toolError(err)
		}
		return nil, out, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "list_discovery_sources",
		Title:       "List discovery sources",
		Description: "Return the configured discovery sources with their provider, query terms, active state, and the error from their last crawl if one failed.",
		Annotations: readOnly("List discovery sources"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, SourceList, error) {
		sources, err := c.DiscoverySources(ctx)
		if err != nil {
			return nil, SourceList{}, toolError(err)
		}
		if sources == nil {
			sources = []DiscoverySource{}
		}
		return nil, SourceList{Sources: sources}, nil
	})

	addTool(s, &mcp.Tool{
		Name:  "validate_discovery_source",
		Title: "Validate a discovery source",
		Description: "Fetch a small sample from a provider to check a board token or company slug before saving it. " +
			"Changes nothing, but does call the provider, so it can take a few seconds. Always validate before add_discovery_source.",
		Annotations: &mcp.ToolAnnotations{Title: "Validate a discovery source", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sourceInput) (*mcp.CallToolResult, SourceValidation, error) {
		source, err := normalizeSource(in)
		if err != nil {
			return nil, SourceValidation{}, err
		}
		out, err := c.ValidateDiscoverySource(ctx, source)
		if err != nil {
			return nil, SourceValidation{}, toolError(err)
		}
		return nil, out, nil
	})

	addTool(s, &mcp.Tool{
		Name:  "add_discovery_source",
		Title: "Add a discovery source",
		Description: "Save a discovery source so the scheduler crawls it. Validate it first: a wrong board token saves cleanly and then quietly returns nothing. " +
			"A source with the same provider, identifier and queries already exists is refused.",
		Annotations: writeTool("Add a discovery source", false),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sourceInput) (*mcp.CallToolResult, DiscoverySource, error) {
		source, err := normalizeSource(in)
		if err != nil {
			return nil, DiscoverySource{}, err
		}
		out, err := c.AddDiscoverySource(ctx, source)
		if err != nil {
			return nil, DiscoverySource{}, toolError(err)
		}
		return nil, out, nil
	})
}

// normalizeSource applies the API's own rules before a request is made, so a
// bad provider or a missing identifier is explained rather than round-tripped.
func normalizeSource(in sourceInput) (DiscoverySource, error) {
	out := DiscoverySource{
		Provider:            strings.ToLower(strings.TrimSpace(in.Provider)),
		Name:                strings.TrimSpace(in.Name),
		Identifier:          strings.TrimSpace(in.Identifier),
		SearchQuery:         strings.TrimSpace(in.SearchQuery),
		ExcludedSearchQuery: strings.TrimSpace(in.ExcludedSearchQuery),
		Active:              in.Active == nil || *in.Active,
	}
	supported := false
	for _, p := range discoveryProviders {
		if p == out.Provider {
			supported = true
		}
	}
	if !supported || out.Name == "" {
		return out, fmt.Errorf("choose a supported provider (%s) and give the source a name", strings.Join(discoveryProviders, ", "))
	}
	if out.Provider == "remotive" && out.SearchQuery == "" {
		return out, errors.New("remotive sources require a focused search query, for example \"go backend engineer\"")
	}
	if out.Provider != "remotive" && out.Provider != "arbeitnow" && out.Identifier == "" {
		return out, errors.New("employer-direct sources require a board or company identifier")
	}
	// The API clears this for remotive, which searches on its own side; do
	// the same so the stored source matches what was asked for.
	if out.Provider == "remotive" {
		out.ExcludedSearchQuery = ""
	}
	return out, nil
}
