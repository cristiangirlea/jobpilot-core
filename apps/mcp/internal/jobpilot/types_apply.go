package jobpilot

import "time"

// Wire types for the apply engine: the answer bank, CV documents, and the
// outbox. A source says where an answer came from and is part of the API's
// contract: profile, bank, bank_similar, ai, template, unanswered, plus cv,
// cover_letter and declaration on an outbox answer.

type AnswerEntry struct {
	ID           string     `json:"id,omitempty"`
	QuestionKey  string     `json:"question_key"`
	QuestionText string     `json:"question_text"`
	Answer       string     `json:"answer"`
	RoleFamily   string     `json:"role_family"`
	Technologies []string   `json:"technologies,omitempty"`
	Source       string     `json:"source"`
	Approved     bool       `json:"approved"`
	UsageCount   int        `json:"usage_count"`
	CreatedAt    time.Time  `json:"created_at,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
}

type AnswerList struct {
	Answers []AnswerEntry `json:"answers"`
}

type Resolution struct {
	Question     string   `json:"question"`
	QuestionKey  string   `json:"question_key"`
	Answer       string   `json:"answer"`
	Source       string   `json:"source"`
	Approved     bool     `json:"approved"`
	NeedsReview  bool     `json:"needs_review"`
	EntryID      string   `json:"entry_id,omitempty"`
	Reused       bool     `json:"reused"`
	Technologies []string `json:"technologies,omitempty"`
}

type ResolvedAnswers struct {
	AIEnabled   bool         `json:"ai_enabled"`
	Provider    string       `json:"provider"`
	NeedsReview int          `json:"needs_review"`
	Resolutions []Resolution `json:"resolutions"`
}

// CVDocument omits the stored file bytes. extracted_text is the readable
// form and is blank in listings; get_cv returns it.
type CVDocument struct {
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	Label         string         `json:"label,omitempty"`
	Filename      string         `json:"filename"`
	Format        string         `json:"format"`
	SizeBytes     int            `json:"size_bytes"`
	ExtractedText string         `json:"extracted_text,omitempty"`
	Parsed        map[string]any `json:"parsed,omitempty"`
	RoleFamily    string         `json:"role_family,omitempty"`
	Technologies  []string       `json:"technologies,omitempty"`
	Approved      bool           `json:"approved"`
	ApprovedAt    *time.Time     `json:"approved_at,omitempty"`
	ParentID      string         `json:"parent_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type CVList struct {
	CVs []CVDocument `json:"cvs"`
}

type TailoredCV struct {
	Decision    string     `json:"decision"`
	Reason      string     `json:"reason"`
	Alignment   float64    `json:"alignment"`
	CV          CVDocument `json:"cv"`
	Approved    bool       `json:"approved"`
	Highlighted []string   `json:"highlighted,omitempty"`
	Missing     []string   `json:"missing,omitempty"`
}

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

// OutboxSummary leads with the fields that decide what the user must do next.
type OutboxSummary struct {
	ID          string      `json:"id"`
	Status      string      `json:"status"`
	Blockers    []string    `json:"blockers"`
	Notices     []string    `json:"notices"`
	NeedsReview int         `json:"needs_review"`
	Job         *JobSummary `json:"job,omitempty"`
	Channel     string      `json:"channel"`
	ATSProvider string      `json:"ats_provider,omitempty"`
	CVDecision  string      `json:"cv_decision,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type OutboxDetail struct {
	OutboxSummary
	ApplicationID     string         `json:"application_id,omitempty"`
	CVDocumentID      string         `json:"cv_document_id,omitempty"`
	CoverLetter       string         `json:"cover_letter"`
	CoverLetterSource string         `json:"cover_letter_source"`
	Answers           []OutboxAnswer `json:"answers"`
	CVPreview         map[string]any `json:"cv_preview,omitempty"`
	Error             string         `json:"error,omitempty"`
}

// OutboxEntry is the wire shape the API returns.
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
	Job               *Job           `json:"job,omitempty"`
}

func summarizeOutbox(x OutboxEntry) OutboxSummary {
	out := OutboxSummary{
		ID: x.ID, Status: x.Status, Blockers: nonNil(x.Blockers), Notices: nonNil(x.Notices),
		NeedsReview: x.NeedsReview, Channel: x.Channel, ATSProvider: x.ATSProvider,
		CVDecision: x.CVDecision, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt,
	}
	if x.Job != nil {
		job := summarizeJob(*x.Job)
		out.Job = &job
	}
	return out
}

// detailOutbox keeps everything the user has to review but drops the raw ATS
// form description, which is large and of no use to a reader.
func detailOutbox(x OutboxEntry) OutboxDetail {
	out := OutboxDetail{
		OutboxSummary: summarizeOutbox(x), ApplicationID: x.ApplicationID,
		CVDocumentID: x.CVDocumentID, CoverLetter: x.CoverLetter,
		CoverLetterSource: x.CoverLetterSource, Answers: x.Answers, Error: x.Error,
	}
	if out.Answers == nil {
		out.Answers = []OutboxAnswer{}
	}
	if preview, ok := x.Form["cv_preview"].(map[string]any); ok {
		out.CVPreview = preview
	}
	return out
}

type OutboxList struct {
	Entries []OutboxSummary `json:"entries"`
}

type AIStatus struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
}

type DetectedBoard struct {
	Provider   string `json:"provider"`
	Identifier string `json:"identifier"`
	JobID      string `json:"job_id,omitempty"`
	Supported  bool   `json:"supported"`
}

type BoardSuggestion struct {
	Provider       string `json:"provider"`
	Identifier     string `json:"identifier"`
	Name           string `json:"name"`
	Note           string `json:"note"`
	RemoteFriendly bool   `json:"remote_friendly"`
}

type BoardSuggestionList struct {
	Suggestions []BoardSuggestion `json:"suggestions"`
}
