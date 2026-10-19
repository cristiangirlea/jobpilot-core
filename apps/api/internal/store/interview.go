package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/models"
)

// InterviewDocuments loads only confirmed sent material, scoped to the user,
// application and job. Status checks and preparation belong to the generator.
func (s *Store) InterviewDocuments(ctx context.Context, userID, applicationID, jobID string) (*models.InterviewDocuments, error) {
	var docs models.InterviewDocuments
	var answers []byte
	// Any later CV update (including approval or a byte-identical re-upload)
	// makes the historical text uncertain; never substitute its current text.
	err := s.DB.QueryRow(ctx, `SELECT o.id,o.sent_at,
		CASE WHEN c.updated_at <= COALESCE(o.submit_attempted_at,o.sent_at) THEN NULLIF(c.extracted_text,'') END,
		o.cover_letter,o.answers
		FROM application_outbox o
		LEFT JOIN cv_documents c ON c.id=o.cv_document_id AND c.user_id=o.user_id
		WHERE o.user_id=$1 AND o.application_id=$2 AND o.job_id=$3 AND o.status='sent' AND o.sent_at IS NOT NULL`,
		userID, applicationID, jobID).Scan(&docs.OutboxID, &docs.SentAt, &docs.CVText, &docs.CoverLetter, &answers)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(answers, &docs.Answers); err != nil {
		return nil, err
	}
	return &docs, nil
}
