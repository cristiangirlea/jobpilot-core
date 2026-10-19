package models

import "time"

// InterviewDocuments contains stored material from a confirmed sent entry.
// The generator checks which documents and answers the form actually carried.
type InterviewDocuments struct {
	OutboxID    string         `json:"outbox_id"`
	SentAt      *time.Time     `json:"sent_at,omitempty"`
	CVText      *string        `json:"cv_text"`
	CoverLetter string         `json:"cover_letter"`
	Answers     []OutboxAnswer `json:"answers"`
}
