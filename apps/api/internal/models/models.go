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
	ApplyURL       string     `json:"apply_url,omitempty"`
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

// AnswerEntry is one reusable application answer. Answers are private per user
// and are only reused once approved, so an unreviewed generated answer can
// never reach an employer on its own.
type AnswerEntry struct {
	ID                 string     `json:"id,omitempty"`
	QuestionKey        string     `json:"question_key"`
	QuestionText       string     `json:"question_text"`
	QuestionNormalized string     `json:"question_normalized,omitempty"`
	Answer             string     `json:"answer"`
	RoleFamily         string     `json:"role_family"`
	Technologies       []string   `json:"technologies"`
	Source             string     `json:"source"`
	Approved           bool       `json:"approved"`
	UsageCount         int        `json:"usage_count"`
	CreatedAt          time.Time  `json:"created_at,omitempty"`
	UpdatedAt          time.Time  `json:"updated_at,omitempty"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
}

// CVDocument is one stored CV: the original file, what was read out of it, and
// whether the user has approved it for sending.
//
// Parsed is deliberately typed as a free-form map here so the models package
// does not depend on the parser; the cv package owns that shape.
type CVDocument struct {
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	Label         string         `json:"label,omitempty"`
	Filename      string         `json:"filename"`
	Format        string         `json:"format"`
	ContentType   string         `json:"content_type,omitempty"`
	SizeBytes     int            `json:"size_bytes"`
	ExtractedText string         `json:"extracted_text,omitempty"`
	Parsed        map[string]any `json:"parsed,omitempty"`
	RoleFamily    string         `json:"role_family,omitempty"`
	Technologies  []string       `json:"technologies,omitempty"`
	Approved      bool           `json:"approved"`
	ApprovedAt    *time.Time     `json:"approved_at,omitempty"`
	ParentID      string         `json:"parent_id,omitempty"`
	Fingerprint   string         `json:"fingerprint,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// Outbox statuses. Composition never sends: an entry waits at ready until the
// user approves it.
const (
	OutboxNeedsReview = "needs_review" // something is missing or unapproved
	OutboxReady       = "ready"        // complete, waiting for the user
	OutboxApproved    = "approved"     // released by the user
	OutboxSent        = "sent"
	OutboxFailed      = "failed"
	OutboxCancelled   = "cancelled"
)

// Channels an application can go out through.
const (
	ChannelATSForm = "ats_form" // the employer's own ATS form
	ChannelEmail   = "email"
	ChannelManual  = "manual" // the user applies themselves
)

// OutboxAnswer is one resolved form field, kept with its provenance so a review
// shows where each answer came from.
type OutboxAnswer struct {
	Field       string   `json:"field"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Answer      string   `json:"answer"`
	Source      string   `json:"source"`
	NeedsReview bool     `json:"needs_review"`
	EntryID     string   `json:"entry_id,omitempty"`
	Options     []string `json:"options,omitempty"`
}

// OutboxEntry is one application composed in full and held for approval.
type OutboxEntry struct {
	ID                string         `json:"id"`
	JobID             string         `json:"job_id"`
	ApplicationID     string         `json:"application_id,omitempty"`
	Status            string         `json:"status"`
	Channel           string         `json:"channel"`
	ATSProvider       string         `json:"ats_provider,omitempty"`
	CVDocumentID      string         `json:"cv_document_id,omitempty"`
	CVDecision        string         `json:"cv_decision,omitempty"`
	CoverLetter       string         `json:"cover_letter"`
	CoverLetterSource string         `json:"cover_letter_source"`
	Form              map[string]any `json:"form,omitempty"`
	Answers           []OutboxAnswer `json:"answers"`
	Blockers          []string       `json:"blockers"`
	Notices           []string       `json:"notices"`
	NeedsReview       int            `json:"needs_review"`
	Error             string         `json:"error,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	ApprovedAt        *time.Time     `json:"approved_at,omitempty"`
	SentAt            *time.Time     `json:"sent_at,omitempty"`
	Job               *Job           `json:"job,omitempty"`
}
