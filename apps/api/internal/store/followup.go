package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"jobpilot/core/api/internal/generator"
)

var ErrFollowUpLimit = errors.New("At most two follow-up drafts are allowed per application.")
var ErrFollowUpStatus = errors.New("Follow-up drafts require an applied or interview application.")

type FollowUpDraft struct {
	ID            string    `json:"id"`
	ApplicationID string    `json:"application_id"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Store) FollowUps(ctx context.Context, u, id string) ([]FollowUpDraft, error) {
	rows, err := s.DB.Query(ctx, `SELECT f.id,f.application_id,f.body,f.created_at FROM application_follow_ups f JOIN applications a ON a.id=f.application_id WHERE a.user_id=$1 AND a.id=$2 ORDER BY f.slot`, u, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	drafts := []FollowUpDraft{}
	for rows.Next() {
		var f FollowUpDraft
		if err = rows.Scan(&f.ID, &f.ApplicationID, &f.Body, &f.CreatedAt); err != nil {
			return nil, err
		}
		drafts = append(drafts, f)
	}
	return drafts, rows.Err()
}

func (s *Store) SaveFollowUp(ctx context.Context, u, id, body string) (FollowUpDraft, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return FollowUpDraft{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	var rejected bool
	err = tx.QueryRow(ctx, `SELECT a.status,COALESCE(m.rejected,false) FROM applications a LEFT JOIN job_matches m ON m.job_id=a.job_id AND m.user_id=a.user_id WHERE a.user_id=$1 AND a.id=$2 FOR UPDATE OF a`, u, id).Scan(&status, &rejected)
	if err != nil {
		return FollowUpDraft{}, err
	}
	if rejected {
		return FollowUpDraft{}, ErrApplicationBlocked
	}
	if status != "applied" && status != "interview" {
		return FollowUpDraft{}, ErrFollowUpStatus
	}
	var f FollowUpDraft
	err = tx.QueryRow(ctx, `INSERT INTO application_follow_ups(application_id,body) VALUES($1,$2) RETURNING id,application_id,body,created_at`, id, body).Scan(&f.ID, &f.ApplicationID, &f.Body, &f.CreatedAt)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.ConstraintName == "application_follow_ups_limit" {
		return f, ErrFollowUpLimit
	}
	if err != nil {
		return f, err
	}
	return f, tx.Commit(ctx)
}

func (s *Store) FollowUpMaterials(ctx context.Context, u, id string) (generator.FollowUpMaterials, error) {
	sent := generator.FollowUpMaterials{Answers: map[string]string{}, AnswerSources: map[string]string{}}
	var raw []byte
	// Prepared documents and later rewrites do not prove what reached the employer.
	err := s.DB.QueryRow(ctx, `SELECT j.title,j.company,COALESCE(c.extracted_text,''),COALESCE(o.cover_letter,''),COALESCE(o.answers,'[]'::jsonb)
 FROM applications a JOIN jobs j ON j.id=a.job_id LEFT JOIN application_outbox o ON o.application_id=a.id AND o.user_id=a.user_id
 AND o.status='sent' AND o.sent_at IS NOT NULL AND o.updated_at<=o.sent_at
 LEFT JOIN cv_documents c ON c.id=o.cv_document_id AND c.user_id=a.user_id AND c.updated_at<=o.sent_at
 WHERE a.user_id=$1 AND a.id=$2`, u, id).Scan(&sent.JobTitle, &sent.Company, &sent.CV, &sent.CoverLetter, &raw)
	if err != nil {
		return sent, err
	}
	var answers []struct {
		Label  string `json:"label"`
		Answer string `json:"answer"`
		Source string `json:"source"`
	}
	if err = json.Unmarshal(raw, &answers); err != nil {
		return sent, err
	}
	for _, a := range answers {
		sent.Answers[a.Label] = a.Answer
		sent.AnswerSources[a.Label] = a.Source
	}
	return sent, nil
}

func (s *Store) DismissQuietApplication(ctx context.Context, u, id string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var own string
	if err = tx.QueryRow(ctx, `SELECT id FROM applications WHERE user_id=$1 AND id=$2 FOR UPDATE`, u, id).Scan(&own); err != nil {
		return err
	}
	if err = OpenNotification(ctx, tx, u, "quiet_application", own, "Quiet application reminder dismissed", map[string]any{"href": "/applications/" + own}); err != nil {
		return err
	}
	if err = ResolveNotification(ctx, tx, u, "quiet_application", own); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
