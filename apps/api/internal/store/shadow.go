package store

import (
	"context"
	"encoding/json"

	"jobpilot/core/api/internal/models"
)

// CurrentExtractions reads the accepted answers for these jobs' text as it is now, by job id.
func (s *Store) CurrentExtractions(ctx context.Context, model, promptVersion string, jobIDs []string) (map[string]models.JobExtraction, error) {
	return currentExtractions(ctx, s.DB, model, promptVersion, jobIDs)
}

func currentExtractions(ctx context.Context, db extractionDB, model, promptVersion string, jobIDs []string) (map[string]models.JobExtraction, error) {
	answers := map[string]models.JobExtraction{}
	if model == "" || len(jobIDs) == 0 {
		return answers, nil
	}
	rows, err := db.Query(ctx, `SELECT x.id, x.job_id, x.model, x.prompt_version, x.text_hash, x.status, x.extraction, x.created_at
		FROM job_extractions x JOIN jobs j ON j.id=x.job_id
		WHERE x.model=$1 AND x.prompt_version=$2 AND x.job_id=ANY($3::uuid[])
		AND x.status='ok' AND x.text_hash=`+extractionTextHash, model, promptVersion, jobIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x models.JobExtraction
		var raw []byte
		if err := rows.Scan(&x.ID, &x.JobID, &x.Model, &x.PromptVersion, &x.TextHash, &x.Status, &raw, &x.CreatedAt); err != nil {
			return nil, err
		}
		if raw != nil {
			if err := json.Unmarshal(raw, &x.Extraction); err != nil {
				return nil, err
			}
		}
		answers[x.JobID] = x
	}
	return answers, rows.Err()
}

// MatchUsers lists the accounts that have a match for the job.
func (s *Store) MatchUsers(ctx context.Context, jobID string) ([]string, error) {
	return matchUsers(ctx, s.DB, jobID)
}

func matchUsers(ctx context.Context, db extractionDB, jobID string) ([]string, error) {
	if !uuidPattern.MatchString(jobID) {
		return nil, nil
	}
	rows, err := db.Query(ctx, `SELECT user_id FROM job_matches WHERE job_id=$1 ORDER BY user_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// shadowValue keeps a missing shadow SQL NULL instead of the JSON text null.
func shadowValue(shadow *models.ShadowEvaluation) any {
	if shadow == nil {
		return nil
	}
	return shadow
}

// SaveShadowEvaluation writes only the shadow, so the match the user sees keeps its score and date.
func (s *Store) SaveShadowEvaluation(ctx context.Context, u, jobID string, shadow *models.ShadowEvaluation) error {
	return saveShadowEvaluation(ctx, s.DB, u, jobID, shadow)
}

func saveShadowEvaluation(ctx context.Context, db extractionDB, u, jobID string, shadow *models.ShadowEvaluation) error {
	_, err := db.Exec(ctx, `UPDATE job_matches SET shadow_evaluation=$3 WHERE user_id=$1 AND job_id=$2`, u, jobID, shadowValue(shadow))
	return err
}

// shadowPart is one part of the score, the extraction's minus the rules' kept in the same comparison.
func shadowPart(extraction, rules string) string {
	return `COALESCE((m.shadow_evaluation->'evaluation'->'breakdown'->>'` + extraction + `')::int, 0)-COALESCE((m.shadow_evaluation->'rules_parts'->>'` + rules + `')::int, 0)`
}

// ExtractionSummary counts answers for open jobs and, of matches, only this account's; it selects no posting text.
func (s *Store) ExtractionSummary(ctx context.Context, u, model, promptVersion string) (models.ExtractionSummary, error) {
	return extractionSummary(ctx, s.DB, u, model, promptVersion)
}

func extractionSummary(ctx context.Context, db extractionDB, u, model, promptVersion string) (models.ExtractionSummary, error) {
	out := models.ExtractionSummary{Model: model, PromptVersion: promptVersion}
	settings, err := discoverySettings(ctx, db, u)
	if err != nil {
		return out, err
	}
	out.MinScore = settings.MinScore
	err = db.QueryRow(ctx, `WITH open_jobs AS (
			SELECT j.id AS job_id, position(E'\n' in j.description)=0 AS single_line,
				x.id AS extraction_id, x.status, x.reason, x.text_hash, x.created_at,
				x.extraction->>'seniority_dropped'='true' AS seniority_dropped
			FROM jobs j LEFT JOIN job_extractions x ON x.job_id=j.id AND x.model=$2 AND x.prompt_version=$3
				AND x.text_hash=`+extractionTextHash+`
			WHERE j.closed_at IS NULL AND j.discovered_at IS NOT NULL
		), ranges(min, max) AS (
			VALUES (-100,-20), (-19,-10), (-9,-1), (0,0), (1,9), (10,19), (20,100)
		), compared AS (
			SELECT (m.shadow_evaluation->>'rules_score')::int AS rules_score,
				(m.shadow_evaluation->>'rules_rejected')::boolean AS rules_rejected,
				(m.shadow_evaluation->'evaluation'->>'score')::int AS extraction_score,
				jsonb_typeof(m.shadow_evaluation->'rules_parts')='object' AS has_parts,
				`+shadowPart("skill_match", "skill")+` AS skill,
				`+shadowPart("seniority_match", "seniority")+` AS seniority,
				COALESCE((m.shadow_evaluation->'rules_parts'->>'matched_skills')::int, 0) AS rules_matched,
				CASE WHEN jsonb_typeof(m.shadow_evaluation->'evaluation'->'matched_skills')='array'
					THEN jsonb_array_length(m.shadow_evaluation->'evaluation'->'matched_skills') ELSE 0 END AS extraction_matched
			FROM job_matches m JOIN open_jobs x ON x.job_id=m.job_id
			WHERE m.user_id=$1 AND m.shadow_evaluation IS NOT NULL AND x.status='ok'
				AND x.extraction_id::text=m.shadow_evaluation->>'extraction_id'
				AND x.text_hash=m.shadow_evaluation->>'text_hash'
				AND x.created_at=(m.shadow_evaluation->>'answered_at')::timestamptz
		), rejected AS (
			SELECT reason, count(*) AS count FROM open_jobs WHERE status='rejected' GROUP BY reason
		), differences AS (
			SELECT min, max, count(c.extraction_score) AS count
			FROM ranges LEFT JOIN compared c ON c.extraction_score-c.rules_score BETWEEN min AND max
			GROUP BY min, max
		), split AS (
			SELECT extraction_score-rules_score AS difference, skill, seniority, rules_matched, extraction_matched
			FROM compared WHERE has_parts
		), parts AS (
			SELECT min, max, count(s.difference) AS matches,
				COALESCE(sum(s.skill), 0) AS skill, COALESCE(sum(s.seniority), 0) AS seniority,
				COALESCE(sum(s.difference-s.skill-s.seniority), 0) AS other,
				COALESCE(sum(s.rules_matched), 0) AS rules_matched_skills,
				COALESCE(sum(s.extraction_matched), 0) AS extraction_matched_skills,
				count(*) FILTER (WHERE s.rules_matched>0 AND s.extraction_matched=0) AS extraction_matched_none
			FROM ranges LEFT JOIN split s ON s.difference BETWEEN min AND max
			GROUP BY min, max
		), threshold AS (
			SELECT count(*) AS compared,
				count(*) FILTER (WHERE NOT rules_rejected AND rules_score<$4 AND extraction_score>=$4) AS would_reach,
				count(*) FILTER (WHERE NOT rules_rejected AND rules_score>=$4 AND extraction_score<$4) AS would_fall
			FROM compared
		)
		SELECT count(*), count(*) FILTER (WHERE status='ok'), count(*) FILTER (WHERE status='ok' AND seniority_dropped),
			count(*) FILTER (WHERE single_line), count(extraction_id) FILTER (WHERE single_line),
			count(*) FILTER (WHERE single_line AND status='rejected'),
			count(*) FILTER (WHERE NOT single_line), count(extraction_id) FILTER (WHERE NOT single_line),
			count(*) FILTER (WHERE NOT single_line AND status='rejected'),
			(SELECT COALESCE(jsonb_object_agg(reason, count), '{}'::jsonb) FROM rejected),
			(SELECT jsonb_agg(differences ORDER BY min) FROM differences),
			(SELECT jsonb_agg(parts ORDER BY min) FROM parts),
			(SELECT compared FROM threshold), (SELECT would_reach FROM threshold), (SELECT would_fall FROM threshold)
		FROM open_jobs`, u, model, promptVersion, out.MinScore).Scan(
		&out.OpenJobs, &out.CurrentExtractions, &out.DroppedSeniority,
		&out.SingleLine.OpenJobs, &out.SingleLine.Answered, &out.SingleLine.Rejected,
		&out.MultiLine.OpenJobs, &out.MultiLine.Answered, &out.MultiLine.Rejected,
		&out.RejectedAnswers, &out.ScoreDifferences, &out.DifferenceParts,
		&out.Compared, &out.WouldReachMinScore, &out.WouldFallBelowMinScore,
	)
	return out, err
}
