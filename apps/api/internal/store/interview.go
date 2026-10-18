package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/generator"
)

func (s *Store) InterviewPrep(ctx context.Context, userID, applicationID string) (generator.InterviewPack, error) {
	app, err := s.Application(ctx, userID, applicationID)
	if err != nil {
		return generator.InterviewPack{}, err
	}
	if app.Status != "interview" {
		return generator.InterviewPack{}, generator.ErrInterviewStatus
	}
	posting, err := s.Job(ctx, userID, app.JobID)
	if err != nil {
		return generator.InterviewPack{}, err
	}
	profile, err := s.Profile(ctx, userID)
	if err != nil {
		return generator.InterviewPack{}, err
	}
	var docs generator.InterviewDocuments
	var answers []byte
	// A later CV parse cannot stand in for the text available when sending.
	err = s.DB.QueryRow(ctx, `SELECT o.id,o.sent_at,
		CASE WHEN c.updated_at <= COALESCE(o.submit_attempted_at,o.sent_at) THEN NULLIF(c.extracted_text,'') END,
		o.cover_letter,o.answers
		FROM application_outbox o
		LEFT JOIN cv_documents c ON c.id=o.cv_document_id AND c.user_id=o.user_id
		WHERE o.user_id=$1 AND o.application_id=$2 AND o.job_id=$3 AND o.status='sent' AND o.sent_at IS NOT NULL`,
		userID, applicationID, app.JobID).Scan(&docs.OutboxID, &docs.SentAt, &docs.CVText, &docs.CoverLetter, &answers)
	if errors.Is(err, pgx.ErrNoRows) {
		return generator.PrepareInterview(app, profile, posting, nil)
	}
	if err != nil {
		return generator.InterviewPack{}, err
	}
	if err := json.Unmarshal(answers, &docs.Answers); err != nil {
		return generator.InterviewPack{}, err
	}
	return generator.PrepareInterview(app, profile, posting, &docs)
}
