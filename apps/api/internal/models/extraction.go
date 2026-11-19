package models

import "time"

// Extraction is what a posting asks of a candidate, as a model read it and
// code verified it against the posting's text.
type Extraction struct {
	Required    []ExtractedSkills     `json:"required"`
	NiceToHave  []ExtractedSkills     `json:"nice_to_have"`
	Stack       []ExtractedSkills     `json:"stack"`
	Seniority   ExtractedSeniority    `json:"seniority"`
	MinYears    *int                  `json:"min_years"`
	Constraints []ExtractedConstraint `json:"constraints"`

	// Dropped counts the items left out because their evidence failed.
	Dropped int `json:"dropped"`
	// SeniorityDropped marks a named level kept as unspecified because its evidence failed.
	SeniorityDropped bool `json:"seniority_dropped,omitempty"`
}

// ExtractedSkills is one entry: any one of Skills meets it, and Quote is the
// posting's own words for it.
type ExtractedSkills struct {
	Skills []string `json:"skills"`
	Quote  string   `json:"quote"`
}

// ExtractedSeniority has a nil Quote exactly when Level is "unspecified".
type ExtractedSeniority struct {
	Level string  `json:"level"`
	Quote *string `json:"quote"`
}

// ExtractedConstraint is a condition on the candidate that is not a skill.
// Value is the model's wording, not the posting's; Quote is the posting's.
type ExtractedConstraint struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Hard  bool   `json:"hard"`
	Quote string `json:"quote"`
}

// PendingExtraction is a posting waiting for the model: its text and nothing else.
type PendingExtraction struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	TextHash    string `json:"text_hash"`
}

// JobExtraction is one stored answer; Extraction is nil when the answer was rejected.
type JobExtraction struct {
	ID, JobID, Model, PromptVersion, TextHash, Status, Reason string
	Extraction                                                *Extraction
	CreatedAt                                                 time.Time
}

// ShadowEvaluation records what the extraction and rules scored from the same posting and profile.
type ShadowEvaluation struct {
	ExtractionID  string     `json:"extraction_id"`
	TextHash      string     `json:"text_hash"`
	AnsweredAt    time.Time  `json:"answered_at"`
	RulesScore    int        `json:"rules_score"`
	RulesRejected bool       `json:"rules_rejected"`
	Evaluation    Evaluation `json:"evaluation"`
}

// ExtractionSummary counts current catalog answers and the signed-in account's comparisons.
type ExtractionSummary struct {
	Scoring                string             `json:"scoring"`
	Model                  string             `json:"model"`
	PromptVersion          string             `json:"prompt_version"`
	OpenJobs               int                `json:"open_jobs"`
	CurrentExtractions     int                `json:"current_extractions"`
	DroppedSeniority       int                `json:"dropped_seniority"`
	RejectedAnswers        map[string]int     `json:"rejected_answers"`
	SingleLine             ExtractionTexts    `json:"single_line"`
	MultiLine              ExtractionTexts    `json:"multi_line"`
	Compared               int                `json:"compared"`
	ScoreDifferences       []ScoreDifferences `json:"score_differences"`
	MinScore               int                `json:"min_score"`
	WouldReachMinScore     int                `json:"would_reach_min_score"`
	WouldFallBelowMinScore int                `json:"would_fall_below_min_score"`
}

// ExtractionTexts counts open jobs of one text shape and the answers for their current text.
type ExtractionTexts struct {
	OpenJobs int `json:"open_jobs"`
	Answered int `json:"answered"`
	Rejected int `json:"rejected"`
}

// ScoreDifferences counts matches whose extraction score minus rules score lies in Min..Max.
type ScoreDifferences struct {
	Min   int `json:"min"`
	Max   int `json:"max"`
	Count int `json:"count"`
}
