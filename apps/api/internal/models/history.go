package models

import "time"

// ExternalApplicationInput contains historical facts only; dates stay empty when unknown.
// CVVersion is an opaque historical reference, never a selection or an approval.
type ExternalApplicationInput struct {
	ExternalKey  string `json:"external_key"`
	Employer     string `json:"employer"`
	Title        string `json:"title,omitempty"`
	Requisition  string `json:"requisition,omitempty"`
	CanonicalURL string `json:"canonical_url,omitempty"`
	AppliedAt    string `json:"applied_at,omitempty"`
	OutcomeAt    string `json:"outcome_at,omitempty"`
	Status       string `json:"status"`
	Source       string `json:"source"`
	EvidenceRef  string `json:"evidence_ref"`
	EvidenceKind string `json:"evidence_kind"`
	CVVersion    string `json:"cv_version,omitempty"`
}

type ExternalApplication struct {
	ID string `json:"id"`
	ExternalApplicationInput
	ImportedAt time.Time `json:"imported_at"`
}

type HistoryImport struct {
	Record                  ExternalApplicationInput `json:"record"`
	Mode                    string                   `json:"mode,omitempty"` // preview or commit
	PreviewToken            string                   `json:"preview_token,omitempty"`
	AllowPossibleDuplicates bool                     `json:"allow_possible_duplicates,omitempty"`
}

type HistoryCandidate struct {
	Record ExternalApplication `json:"record"`
	Kind   string              `json:"kind"` // exact or possible; never automatically merged
}

type HistoryPreview struct {
	Action       string               `json:"action"` // create, unchanged, conflict, review
	PreviewToken string               `json:"preview_token"`
	Candidates   []HistoryCandidate   `json:"candidates"`
	Record       *ExternalApplication `json:"record,omitempty"`
}

type HistoryPage struct {
	Items []ExternalApplication `json:"items"`
	Next  string                `json:"next"`
}
