package store

import (
	"context"

	"jobpilot/core/api/internal/models"
)

// UnscoredJobs returns up to limit open catalog jobs the user has no match for
// yet, newest first. A job is global and a match is per user, so these are
// the jobs someone else's discovery brought in. Only discovered jobs are
// shared: a job someone pasted in (no discovered_at) stays with them, so one
// account can neither see another's imports nor plant a posting in their
// inbox. discovered_at, unlike source_id, survives deleting the source.
func (s *Store) UnscoredJobs(ctx context.Context, u string, limit int) ([]models.Job, error) {
	rows, err := s.DB.Query(ctx, `SELECT j.id,j.title,j.company,j.company_domain,j.location,j.remote_type,j.salary_min,j.salary_max,j.currency,j.employment_type,j.industry,j.description,j.apply_url,j.posted_at
		FROM jobs j WHERE j.closed_at IS NULL AND j.discovered_at IS NOT NULL
		AND NOT EXISTS(SELECT 1 FROM job_matches m WHERE m.job_id=j.id AND m.user_id=$1)
		ORDER BY j.created_at DESC,j.id LIMIT $2`, u, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []models.Job{}
	for rows.Next() {
		var j models.Job
		if err := rows.Scan(&j.ID, &j.Title, &j.Company, &j.CompanyDomain, &j.Location, &j.RemoteType, &j.SalaryMin, &j.SalaryMax, &j.Currency, &j.EmploymentType, &j.Industry, &j.Description, &j.ApplyURL, &j.PostedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// UserIDs lists every account, oldest first.
func (s *Store) UserIDs(ctx context.Context) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT id FROM users ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CatalogSummary says how fresh the shared catalog (discovered jobs, see
// UnscoredJobs) is and how many open jobs fit the user, without anything
// about where the jobs came from.
func (s *Store) CatalogSummary(ctx context.Context, u string) (models.CatalogSummary, error) {
	var x models.CatalogSummary
	err := s.DB.QueryRow(ctx, `SELECT max(created_at),
		count(*) FILTER (WHERE created_at>=now()-interval '24 hours'),
		count(*) FILTER (WHERE created_at>=now()-interval '7 days'),
		count(*) FILTER (WHERE closed_at IS NULL)
		FROM jobs WHERE discovered_at IS NOT NULL`).Scan(&x.LastJobAt, &x.JobsLast24h, &x.JobsLast7d, &x.OpenJobs)
	if err != nil {
		return x, err
	}
	err = s.DB.QueryRow(ctx, `SELECT count(*) FROM job_matches m JOIN jobs j ON j.id=m.job_id
		WHERE m.user_id=$1 AND m.rejected=false AND j.closed_at IS NULL
		AND NOT EXISTS(SELECT 1 FROM job_reports r WHERE r.job_id=j.id AND r.user_id=$1)`, u).Scan(&x.OpenMatches)
	return x, err
}
