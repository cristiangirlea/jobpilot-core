package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/models"
)

const answerColumns = `id,question_key,question_text,question_normalized,answer,role_family,scope,COALESCE(job_id::text,''),technologies,source,approved,usage_count,created_at,updated_at,last_used_at`

// ErrAnswerScopeNeedsJob is returned when an answer is narrowed to one job
// without saying which.
var ErrAnswerScopeNeedsJob = errors.New("An answer for one job only needs the job it is for.")

// ErrAnswerScopeTaken is returned when the scope an answer is moved to already
// holds an answer to the same question.
var ErrAnswerScopeTaken = errors.New("You already have an answer to this question for that scope. Edit or delete that one instead.")

func scanAnswer(row pgx.Row) (models.AnswerEntry, error) {
	var entry models.AnswerEntry
	err := row.Scan(&entry.ID, &entry.QuestionKey, &entry.QuestionText, &entry.QuestionNormalized, &entry.Answer,
		&entry.RoleFamily, &entry.Scope, &entry.JobID, &entry.Technologies, &entry.Source, &entry.Approved, &entry.UsageCount,
		&entry.CreatedAt, &entry.UpdatedAt, &entry.LastUsedAt)
	return entry, err
}

// FindAnswer looks up a previously stored answer to this exact question for
// one job: an approved one before a draft, and among those the narrowest
// scope: an answer for this job only, then one for its role family, then a
// role-family answer filed under "general" (which has always served every
// family), then one for every job.
func (s *Store) FindAnswer(ctx context.Context, userID, normalized, roleFamily, jobID string) (models.AnswerEntry, bool, error) {
	entry, err := scanAnswer(s.DB.QueryRow(ctx, `SELECT `+answerColumns+` FROM answer_bank
		WHERE user_id=$1 AND question_normalized=$2 AND (
			(scope='job' AND job_id::text=$4) OR
			(scope='role_family' AND role_family IN ($3,'general','')) OR
			scope='global')
		ORDER BY approved DESC,
			CASE WHEN scope='job' THEN 0 WHEN scope='role_family' AND role_family=$3 THEN 1 WHEN scope='role_family' THEN 2 ELSE 3 END,
			usage_count DESC LIMIT 1`, userID, normalized, roleFamily, jobID))
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
// approved rows are returned, so an unreviewed draft can never be reused, and
// an answer for one job only is returned for that job alone. The limit applies
// to each scope separately, the role family's own answers first, so many
// answers in one scope cannot crowd out another.
func (s *Store) CandidateAnswers(ctx context.Context, userID, questionKey, roleFamily, jobID string, limit int) ([]models.AnswerEntry, error) {
	if questionKey == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.DB.Query(ctx, `SELECT `+answerColumns+` FROM (
			SELECT *, row_number() OVER (PARTITION BY scope ORDER BY role_family=$5 DESC, usage_count DESC, updated_at DESC) AS n
			FROM answer_bank
			WHERE user_id=$1 AND question_key=$2 AND approved AND (scope<>'job' OR job_id::text=$4)) candidates
		WHERE n <= $3 ORDER BY usage_count DESC, updated_at DESC`, userID, questionKey, limit, jobID, roleFamily)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAnswers(rows)
}

// SaveAnswer inserts or replaces the answer for a question in one scope: per
// role family by default, as before scopes existed. An existing approved
// answer is not silently downgraded by a generated draft: approval is only
// ever cleared when the answer text actually changes.
func (s *Store) SaveAnswer(ctx context.Context, userID string, entry models.AnswerEntry) (models.AnswerEntry, error) {
	if entry.RoleFamily == "" {
		entry.RoleFamily = "general"
	}
	if entry.Scope == "" {
		entry.Scope = models.AnswerScopeRoleFamily
	}
	if entry.Scope == models.AnswerScopeJob && entry.JobID == "" {
		return models.AnswerEntry{}, ErrAnswerScopeNeedsJob
	}
	if entry.Technologies == nil {
		entry.Technologies = []string{}
	}
	return scanAnswer(s.DB.QueryRow(ctx, `INSERT INTO answer_bank
		(user_id,question_key,question_text,question_normalized,answer,role_family,scope,job_id,technologies,source,approved)
		VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,$9,$10,$11)
		ON CONFLICT (user_id,question_normalized,scope_key) DO UPDATE SET
			answer=EXCLUDED.answer,
			question_text=EXCLUDED.question_text,
			question_key=EXCLUDED.question_key,
			role_family=EXCLUDED.role_family,
			job_id=COALESCE(EXCLUDED.job_id,answer_bank.job_id),
			technologies=EXCLUDED.technologies,
			source=EXCLUDED.source,
			approved=CASE WHEN answer_bank.answer=EXCLUDED.answer THEN answer_bank.approved ELSE EXCLUDED.approved END,
			updated_at=now()
		RETURNING `+answerColumns,
		userID, entry.QuestionKey, entry.QuestionText, entry.QuestionNormalized, entry.Answer,
		entry.RoleFamily, entry.Scope, entry.JobID, entry.Technologies, entry.Source, entry.Approved))
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
// approval usually is, so both travel together, and so does choosing where the
// answer is reused: scope and jobID are left as they are when empty. Narrowing
// to one job without a jobID uses the job the draft was written for.
func (s *Store) UpdateAnswer(ctx context.Context, userID, id, answer string, approved bool, scope, jobID string) (models.AnswerEntry, error) {
	entry, err := scanAnswer(s.DB.QueryRow(ctx, `UPDATE answer_bank
		SET answer=COALESCE(NULLIF($3,''),answer), approved=$4, source=CASE WHEN NULLIF($3,'') IS NULL THEN source ELSE 'human' END,
			scope=COALESCE(NULLIF($5,''),scope), job_id=COALESCE(NULLIF($6,'')::uuid,job_id), updated_at=now()
		WHERE user_id=$1 AND id=$2 AND (COALESCE(NULLIF($5,''),scope)<>'job' OR COALESCE(NULLIF($6,'')::uuid,job_id) IS NOT NULL)
		RETURNING `+answerColumns, userID, id, answer, approved, scope, jobID))
	if isUniqueViolation(err) {
		return models.AnswerEntry{}, ErrAnswerScopeTaken
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// Either this user has no such answer, or the update refused to narrow
		// it to a job it does not know.
		var exists bool
		if e := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM answer_bank WHERE user_id=$1 AND id=$2)`, userID, id).Scan(&exists); e != nil {
			return models.AnswerEntry{}, e
		}
		if exists {
			return models.AnswerEntry{}, ErrAnswerScopeNeedsJob
		}
		return models.AnswerEntry{}, pgx.ErrNoRows
	}
	return entry, err
}

func (s *Store) DeleteAnswer(ctx context.Context, userID, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM answer_bank WHERE user_id=$1 AND id=$2`, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
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
