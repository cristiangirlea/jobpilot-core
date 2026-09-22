package jobpilot

import "time"

// Wire types mirror the JSON the REST API returns. Unknown fields are ignored
// on decode, so additive API changes never break the tools. Summary types are
// what list tools return: they drop the job description so a page of matches
// stays small in the host model's context; get_job carries the full text.

type Job struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Company        string     `json:"company"`
	CompanyDomain  string     `json:"company_domain,omitempty"`
	Location       string     `json:"location"`
	RemoteType     string     `json:"remote_type"`
	SalaryMin      float64    `json:"salary_min"`
	SalaryMax      float64    `json:"salary_max"`
	Currency       string     `json:"currency"`
	EmploymentType string     `json:"employment_type"`
	Industry       string     `json:"industry"`
	Description    string     `json:"description"`
	ApplyURL       string     `json:"apply_url"`
	PostedAt       *time.Time `json:"posted_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	DiscoveredAt   *time.Time `json:"discovered_at,omitempty"`
	SourceName     string     `json:"source_name,omitempty"`
	SourceProvider string     `json:"source_provider,omitempty"`
}

type Evaluation struct {
	Rejected         bool           `json:"rejected"`
	RejectionReasons []string       `json:"rejection_reasons"`
	Warnings         []string       `json:"warnings"`
	Score            int            `json:"score"`
	Reasons          []string       `json:"reasons"`
	MatchedSkills    []string       `json:"matched_skills"`
	MissingSkills    []string       `json:"missing_skills"`
	RedFlags         []string       `json:"red_flags"`
	Breakdown        map[string]int `json:"breakdown"`
}

func (e *Evaluation) normalize() {
	e.RejectionReasons = nonNil(e.RejectionReasons)
	e.Warnings = nonNil(e.Warnings)
	e.Reasons = nonNil(e.Reasons)
	e.MatchedSkills = nonNil(e.MatchedSkills)
	e.MissingSkills = nonNil(e.MissingSkills)
	e.RedFlags = nonNil(e.RedFlags)
	if e.Breakdown == nil {
		e.Breakdown = map[string]int{}
	}
}

type JobWithMatch struct {
	Job     Job        `json:"job"`
	Match   Evaluation `json:"match"`
	MatchID string     `json:"match_id,omitempty"`
}

type JobSummary struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Company        string     `json:"company"`
	Location       string     `json:"location"`
	RemoteType     string     `json:"remote_type"`
	SalaryMin      float64    `json:"salary_min"`
	SalaryMax      float64    `json:"salary_max"`
	Currency       string     `json:"currency"`
	EmploymentType string     `json:"employment_type"`
	Industry       string     `json:"industry"`
	ApplyURL       string     `json:"apply_url"`
	PostedAt       *time.Time `json:"posted_at,omitempty"`
	Source         string     `json:"source,omitempty"`
}

func summarizeJob(j Job) JobSummary {
	source := j.SourceName
	switch {
	case source != "" && j.SourceProvider != "":
		source += " (" + j.SourceProvider + ")"
	case source == "":
		source = j.SourceProvider
	}
	return JobSummary{ID: j.ID, Title: j.Title, Company: j.Company, Location: j.Location, RemoteType: j.RemoteType, SalaryMin: j.SalaryMin, SalaryMax: j.SalaryMax, Currency: j.Currency, EmploymentType: j.EmploymentType, Industry: j.Industry, ApplyURL: j.ApplyURL, PostedAt: j.PostedAt, Source: source}
}

type MatchSummary struct {
	Job              JobSummary `json:"job"`
	Score            int        `json:"score"`
	Rejected         bool       `json:"rejected"`
	RejectionReasons []string   `json:"rejection_reasons"`
	Warnings         []string   `json:"warnings"`
	MatchedSkills    []string   `json:"matched_skills"`
	MissingSkills    []string   `json:"missing_skills"`
}

// summarizeMatch builds the list view. Rejected jobs lose their apply URL,
// matching the web UI, which withholds the apply action for them.
func summarizeMatch(x JobWithMatch) MatchSummary {
	x.Match.normalize()
	job := summarizeJob(x.Job)
	if x.Match.Rejected {
		job.ApplyURL = ""
	}
	return MatchSummary{Job: job, Score: x.Match.Score, Rejected: x.Match.Rejected, RejectionReasons: x.Match.RejectionReasons, Warnings: x.Match.Warnings, MatchedSkills: x.Match.MatchedSkills, MissingSkills: x.Match.MissingSkills}
}

type Application struct {
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	Status    string    `json:"status"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Job       *Job      `json:"job,omitempty"`
}

type ApplicationSummary struct {
	ID        string      `json:"id"`
	JobID     string      `json:"job_id"`
	Status    string      `json:"status"`
	Notes     string      `json:"notes"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Job       *JobSummary `json:"job,omitempty"`
}

func summarizeApplication(a Application) ApplicationSummary {
	out := ApplicationSummary{ID: a.ID, JobID: a.JobID, Status: a.Status, Notes: a.Notes, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
	if a.Job != nil {
		job := summarizeJob(*a.Job)
		out.Job = &job
	}
	return out
}

type Documents struct {
	RecruiterMessage string            `json:"recruiter_message"`
	CoverLetter      string            `json:"cover_letter"`
	CandidateSummary string            `json:"candidate_summary"`
	Answers          map[string]string `json:"answers"`
}

func (d *Documents) normalize() {
	if d.Answers == nil {
		d.Answers = map[string]string{}
	}
}

// ApplicationDetail is the wire shape of GET /api/applications/{id}.
type ApplicationDetail struct {
	Application  Application `json:"application"`
	Documents    Documents   `json:"documents"`
	Blocked      bool        `json:"blocked"`
	BlockReasons []string    `json:"block_reasons"`
}

type ApplicationView struct {
	Application  ApplicationSummary `json:"application"`
	Documents    Documents          `json:"documents"`
	Blocked      bool               `json:"blocked"`
	BlockReasons []string           `json:"block_reasons"`
}

// Dashboard is the wire shape of GET /api/dashboard.
type Dashboard struct {
	ProfileCompleteness   int            `json:"profile_completeness"`
	JobsImported          int            `json:"jobs_imported"`
	JobsRejectedBlacklist int            `json:"jobs_rejected_blacklist"`
	JobsRejectedFilters   int            `json:"jobs_rejected_filters"`
	GoodMatches           int            `json:"good_matches"`
	ApplicationsDrafted   int            `json:"applications_drafted"`
	ApplicationsApplied   int            `json:"applications_applied"`
	TopJobs               []JobWithMatch `json:"top_jobs"`
	NextActions           []string       `json:"next_actions"`
}

type DashboardView struct {
	ProfileCompleteness   int            `json:"profile_completeness"`
	JobsImported          int            `json:"jobs_imported"`
	JobsRejectedBlacklist int            `json:"jobs_rejected_blacklist"`
	JobsRejectedFilters   int            `json:"jobs_rejected_filters"`
	GoodMatches           int            `json:"good_matches"`
	ApplicationsDrafted   int            `json:"applications_drafted"`
	ApplicationsApplied   int            `json:"applications_applied"`
	TopJobs               []MatchSummary `json:"top_jobs"`
	NextActions           []string       `json:"next_actions"`
}

type DiscoveryRun struct {
	ID                   string     `json:"id"`
	Status               string     `json:"status"`
	JobsSeen             int        `json:"jobs_seen"`
	JobsAdded            int        `json:"jobs_added"`
	JobsRejected         int        `json:"jobs_rejected"`
	ApplicationsPrepared int        `json:"applications_prepared"`
	DuplicatesSkipped    int        `json:"duplicates_skipped"`
	ErrorMessage         string     `json:"error_message,omitempty"`
	StartedAt            time.Time  `json:"started_at"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
}

type WorkExperience struct {
	ID            string `json:"id,omitempty"`
	Company       string `json:"company"`
	CompanyDomain string `json:"company_domain,omitempty"`
	Title         string `json:"title"`
	StartDate     string `json:"start_date,omitempty"`
	EndDate       string `json:"end_date,omitempty"`
	Current       bool   `json:"current"`
	Description   string `json:"description,omitempty"`
}

type Skill struct {
	ID    string  `json:"id,omitempty"`
	Name  string  `json:"name"`
	Level string  `json:"level,omitempty"`
	Years float64 `json:"years,omitempty"`
}

type Project struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	URL         string   `json:"url,omitempty"`
	Skills      []string `json:"skills,omitempty"`
}

type Profile struct {
	Name                string           `json:"name"`
	Email               string           `json:"email"`
	Phone               string           `json:"phone"`
	Location            string           `json:"location"`
	LinkedIn            string           `json:"linkedin"`
	GitHub              string           `json:"github"`
	Portfolio           string           `json:"portfolio"`
	CurrentTitle        string           `json:"current_title"`
	DesiredTitles       []string         `json:"desired_titles,omitempty"`
	Seniorities         []string         `json:"seniorities,omitempty"`
	YearsExperience     float64          `json:"years_experience"`
	Certifications      []string         `json:"certifications,omitempty"`
	Languages           []string         `json:"languages,omitempty"`
	SalaryExpectation   float64          `json:"salary_expectation"`
	RateExpectation     float64          `json:"rate_expectation"`
	Currency            string           `json:"currency"`
	WorkPreference      []string         `json:"work_preference,omitempty"`
	PreferredCountries  []string         `json:"preferred_countries,omitempty"`
	PreferredTimezones  []string         `json:"preferred_timezones,omitempty"`
	WorkAuthorization   string           `json:"work_authorization"`
	NoticePeriod        string           `json:"notice_period"`
	IndustryPreferences []string         `json:"industry_preferences,omitempty"`
	Summary             string           `json:"summary"`
	Experiences         []WorkExperience `json:"experiences,omitempty"`
	Skills              []Skill          `json:"skills,omitempty"`
	Projects            []Project        `json:"projects,omitempty"`
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// FilterPreset mirrors the API's filter shape. Positive lists are OR rules
// and the excluded lists are NOT rules; only the active preset is applied.
type FilterPreset struct {
	ID                   string   `json:"id,omitempty"`
	Name                 string   `json:"name"`
	Active               bool     `json:"active"`
	IncludedTitles       []string `json:"included_titles"`
	ExcludedTitleWords   []string `json:"excluded_title_words"`
	Locations            []string `json:"locations"`
	RemoteTypes          []string `json:"remote_types"`
	MinSalary            float64  `json:"min_salary"`
	Currency             string   `json:"currency"`
	EmploymentTypes      []string `json:"employment_types"`
	ExcludedIndustries   []string `json:"excluded_industries"`
	RequiredSkills       []string `json:"required_skills"`
	IncludedTechnologies []string `json:"included_technologies"`
	ExcludedTechnologies []string `json:"excluded_technologies"`
	MaxAgeDays           int      `json:"max_age_days"`
}

type DiscoverySource struct {
	ID                  string     `json:"id,omitempty"`
	Provider            string     `json:"provider"`
	Name                string     `json:"name"`
	Identifier          string     `json:"identifier"`
	SearchQuery         string     `json:"search_query"`
	ExcludedSearchQuery string     `json:"excluded_search_query"`
	Active              bool       `json:"active"`
	LastCrawledAt       *time.Time `json:"last_crawled_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	CreatedAt           time.Time  `json:"created_at,omitempty"`
}

// ImportedJob is what import_job returns: the catalog job as a summary plus
// the user's evaluation of it. The description is omitted because the caller
// just supplied it.
type ImportedJob struct {
	Job     JobSummary `json:"job"`
	Match   Evaluation `json:"match"`
	MatchID string     `json:"match_id,omitempty"`
}

type SourceValidation struct {
	Count       int    `json:"count"`
	SampleTitle string `json:"sample_title"`
}

type FilterList struct {
	Filters []FilterPreset `json:"filters"`
}

type SourceList struct {
	Sources []DiscoverySource `json:"sources"`
}

// JobInput is the manual-import request body. The API decodes it with unknown
// fields disallowed, so this must carry exactly the fields it accepts.
type JobInput struct {
	Title          string     `json:"title"`
	Company        string     `json:"company"`
	CompanyDomain  string     `json:"company_domain,omitempty"`
	Location       string     `json:"location"`
	RemoteType     string     `json:"remote_type"`
	SalaryMin      float64    `json:"salary_min"`
	SalaryMax      float64    `json:"salary_max"`
	Currency       string     `json:"currency"`
	EmploymentType string     `json:"employment_type"`
	Industry       string     `json:"industry"`
	Description    string     `json:"description"`
	ApplyURL       string     `json:"apply_url"`
	PostedAt       *time.Time `json:"posted_at,omitempty"`
}
