package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/models"
)

const answerColumns = `id,question_key,question_text,question_normalized,answer,role_family,technologies,source,approved,usage_count,created_at,updated_at,last_used_at`

func scanAnswer(row pgx.Row) (models.AnswerEntry, error) {
	var entry models.AnswerEntry
	err := row.Scan(&entry.ID, &entry.QuestionKey, &entry.QuestionText, &entry.QuestionNormalized, &entry.Answer,
		&entry.RoleFamily, &entry.Technologies, &entry.Source, &entry.Approved, &entry.UsageCount,
		&entry.CreatedAt, &entry.UpdatedAt, &entry.LastUsedAt)
	return entry, err
}

// FindAnswer looks up a previously stored answer to this exact question,
// preferring one written for the same role family over a generic one.
func (s *Store) FindAnswer(ctx context.Context, userID, normalized, roleFamily string) (models.AnswerEntry, bool, error) {
	entry, err := scanAnswer(s.DB.QueryRow(ctx, `SELECT `+answerColumns+` FROM answer_bank
		WHERE user_id=$1 AND question_normalized=$2 AND role_family IN ($3,'general','')
		ORDER BY (role_family=$3) DESC, approved DESC, usage_count DESC LIMIT 1`, userID, normalized, roleFamily))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.AnswerEntry{}, false, nil
	}
	if err != nil {
		return models.AnswerEntry{}, false, err
	}
	return entry, true, nil
}

// CandidateAnswers returns approved answers to the same canonical question,
// which the resolver then ranks by role family and technology overlap. Only
// approved rows are returned, so an unreviewed draft can never be reused.
func (s *Store) CandidateAnswers(ctx context.Context, userID, questionKey string, limit int) ([]models.AnswerEntry, error) {
	if questionKey == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.DB.Query(ctx, `SELECT `+answerColumns+` FROM answer_bank
		WHERE user_id=$1 AND question_key=$2 AND approved ORDER BY usage_count DESC, updated_at DESC LIMIT $3`, userID, questionKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAnswers(rows)
}

// SaveAnswer inserts or replaces the answer for a (question, role family) pair.
// An existing approved answer is not silently downgraded by a generated draft:
// approval is only ever cleared when the answer text actually changes.
func (s *Store) SaveAnswer(ctx context.Context, userID string, entry models.AnswerEntry) (models.AnswerEntry, error) {
	if entry.RoleFamily == "" {
		entry.RoleFamily = "general"
	}
	if entry.Technologies == nil {
		entry.Technologies = []string{}
	}
	return scanAnswer(s.DB.QueryRow(ctx, `INSERT INTO answer_bank
		(user_id,question_key,question_text,question_normalized,answer,role_family,technologies,source,approved)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (user_id,question_normalized,role_family) DO UPDATE SET
			answer=EXCLUDED.answer,
			question_text=EXCLUDED.question_text,
			question_key=EXCLUDED.question_key,
			technologies=EXCLUDED.technologies,
			source=EXCLUDED.source,
			approved=CASE WHEN answer_bank.answer=EXCLUDED.answer THEN answer_bank.approved ELSE EXCLUDED.approved END,
			updated_at=now()
		RETURNING `+answerColumns,
		userID, entry.QuestionKey, entry.QuestionText, entry.QuestionNormalized, entry.Answer,
		entry.RoleFamily, entry.Technologies, entry.Source, entry.Approved))
}

// TouchAnswer records that an answer was actually used, which feeds the ranking
// of competing answers for the same question.
func (s *Store) TouchAnswer(ctx context.Context, userID, id string) error {
	_, err := s.DB.Exec(ctx, `UPDATE answer_bank SET usage_count=usage_count+1,last_used_at=now() WHERE user_id=$1 AND id=$2`, userID, id)
	return err
}

// ListAnswers returns the user's answer bank. status is "pending", "approved",
// or anything else for all.
func (s *Store) ListAnswers(ctx context.Context, userID, status string) ([]models.AnswerEntry, error) {
	query := `SELECT ` + answerColumns + ` FROM answer_bank WHERE user_id=$1`
	switch status {
	case "pending":
		query += ` AND NOT approved`
	case "approved":
		query += ` AND approved`
	}
	rows, err := s.DB.Query(ctx, query+` ORDER BY approved, updated_at DESC LIMIT 500`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAnswers(rows)
}

// UpdateAnswer edits and/or approves one entry. Editing the text is what an
// approval usually is, so both travel together.
func (s *Store) UpdateAnswer(ctx context.Context, userID, id, answer string, approved bool) (models.AnswerEntry, error) {
	entry, err := scanAnswer(s.DB.QueryRow(ctx, `UPDATE answer_bank
		SET answer=COALESCE(NULLIF($3,''),answer), approved=$4, source=CASE WHEN NULLIF($3,'') IS NULL THEN source ELSE 'human' END, updated_at=now()
		WHERE user_id=$1 AND id=$2 RETURNING `+answerColumns, userID, id, answer, approved))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.AnswerEntry{}, pgx.ErrNoRows
	}
	return entry, err
}

func (s *Store) DeleteAnswer(ctx context.Context, userID, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM answer_bank WHERE user_id=$1 AND id=$2`, userID, id)
	return err
}

func collectAnswers(rows pgx.Rows) ([]models.AnswerEntry, error) {
	out := []models.AnswerEntry{}
	for rows.Next() {
		entry, err := scanAnswer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}
