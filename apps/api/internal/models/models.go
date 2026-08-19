package models

import "time"

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
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
	Skills      []string `json:"skills"`
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
	DesiredTitles       []string         `json:"desired_titles"`
	Seniority           string           `json:"seniority,omitempty"`
	Seniorities         []string         `json:"seniorities"`
	YearsExperience     float64          `json:"years_experience"`
	Certifications      []string         `json:"certifications"`
	Languages           []string         `json:"languages"`
	SalaryExpectation   float64          `json:"salary_expectation"`
	RateExpectation     float64          `json:"rate_expectation"`
	Currency            string           `json:"currency"`
	WorkPreference      []string         `json:"work_preference"`
	PreferredCountries  []string         `json:"preferred_countries"`
	PreferredTimezones  []string         `json:"preferred_timezones"`
	WorkAuthorization   string           `json:"work_authorization"`
	NoticePeriod        string           `json:"notice_period"`
	IndustryPreferences []string         `json:"industry_preferences"`
	Summary             string           `json:"summary"`
	Experiences         []WorkExperience `json:"experiences"`
	Skills              []Skill          `json:"skills"`
	Projects            []Project        `json:"projects"`
}

type BlacklistEntry struct {
	ID             string    `json:"id,omitempty"`
	CompanyName    string    `json:"company_name"`
	CompanyDomain  string    `json:"company_domain,omitempty"`
	NormalizedName string    `json:"normalized_name,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
}

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

type Job struct {
	ID string `json:"id"`
	JobInput
	CompanyNormalized  string     `json:"company_normalized"`
	CreatedAt          time.Time  `json:"created_at"`
	SourceID           string     `json:"source_id,omitempty"`
	ExternalID         string     `json:"external_id,omitempty"`
	DiscoveredAt       *time.Time `json:"discovered_at,omitempty"`
	ReferralPartner    string     `json:"referral_partner,omitempty"`
	ReferralDisclosure string     `json:"referral_disclosure,omitempty"`
	SourceName         string     `json:"source_name,omitempty"`
	SourceProvider     string     `json:"source_provider,omitempty"`
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

type JobWithMatch struct {
	Job     Job        `json:"job"`
	Match   Evaluation `json:"match"`
	MatchID string     `json:"match_id,omitempty"`
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

type Documents struct {
	RecruiterMessage string            `json:"recruiter_message"`
	CoverLetter      string            `json:"cover_letter"`
	CandidateSummary string            `json:"candidate_summary"`
	Answers          map[string]string `json:"answers"`
}

type DiscoverySettings struct {
	Enabled         bool       `json:"enabled"`
	IntervalMinutes int        `json:"interval_minutes"`
	AutoPrepare     bool       `json:"auto_prepare"`
	MinScore        int        `json:"min_score"`
	MaxJobsPerRun   int        `json:"max_jobs_per_run"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
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

type DiscoveredJob struct {
	ExternalID string
	Input      JobInput
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
