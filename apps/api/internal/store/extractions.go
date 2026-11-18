package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"jobpilot/core/api/internal/models"
)

// extractionTextHash must equal extraction.Hash of the job's title and description.
const extractionTextHash = `encode(sha256(convert_to(j.title || E'\n' || j.description, 'UTF8')), 'hex')`

// extractionDB is the pool, or a transaction in tests so their jobs stay invisible to other tests.
type extractionDB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PendingExtractions lists open discovered jobs without an answer for their text as it is now.
// A pasted-in job stays with its account and is never listed.
func (s *Store) PendingExtractions(ctx context.Context, model, promptVersion string, limit int) ([]models.PendingExtraction, error) {
	return pendingExtractions(ctx, s.DB, model, promptVersion, limit)
}

func pendingExtractions(ctx context.Context, db extractionDB, model, promptVersion string, limit int) ([]models.PendingExtraction, error) {
	rows, err := db.Query(ctx, `SELECT j.id, j.title, j.description, `+extractionTextHash+`
		FROM jobs j
		WHERE j.closed_at IS NULL AND j.discovered_at IS NOT NULL
		AND NOT EXISTS (
			SELECT 1 FROM job_extractions x
			WHERE x.job_id=j.id AND x.model=$1 AND x.prompt_version=$2 AND x.text_hash=`+extractionTextHash+`
		)
		ORDER BY j.created_at DESC, j.id LIMIT $3`, model, promptVersion, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []models.PendingExtraction{}
	for rows.Next() {
		var job models.PendingExtraction
		if err := rows.Scan(&job.ID, &job.Title, &job.Description, &job.TextHash); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ExtractionSource is the text of a discovered job, the only kind an answer is stored for.
func (s *Store) ExtractionSource(ctx context.Context, jobID string) (title, description string, err error) {
	return extractionSource(ctx, s.DB, jobID)
}

func extractionSource(ctx context.Context, db extractionDB, jobID string) (title, description string, err error) {
	if !uuidPattern.MatchString(jobID) {
		return "", "", pgx.ErrNoRows
	}
	err = db.QueryRow(ctx, `SELECT title, description FROM jobs WHERE id=$1 AND discovered_at IS NOT NULL`, jobID).Scan(&title, &description)
	return title, description, err
}

// SaveExtraction stores an answer only while the job's text still has the answer's hash.
func (s *Store) SaveExtraction(ctx context.Context, x models.JobExtraction) (bool, error) {
	return saveExtraction(ctx, s.DB, x)
}

func saveExtraction(ctx context.Context, db extractionDB, x models.JobExtraction) (bool, error) {
	// Lock the source text until the upsert finishes so a concurrent edit cannot slip past the hash check.
	tag, err := db.Exec(ctx, `INSERT INTO job_extractions (job_id, model, prompt_version, text_hash, status, reason, extraction)
		SELECT j.id, $2, $3, $4, $5, $6, $7::jsonb FROM jobs j
		WHERE j.id=$1 AND j.discovered_at IS NOT NULL AND `+extractionTextHash+`=$4
		FOR SHARE OF j
		ON CONFLICT (job_id, model, prompt_version) DO UPDATE
		SET text_hash=EXCLUDED.text_hash, status=EXCLUDED.status, reason=EXCLUDED.reason,
			extraction=EXCLUDED.extraction, created_at=now()`,
		x.JobID, x.Model, x.PromptVersion, x.TextHash, x.Status, x.Reason, x.Extraction)
	return tag.RowsAffected() != 0, err
}
