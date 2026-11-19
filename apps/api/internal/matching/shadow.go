package matching

import (
	"time"

	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/models"
)

// Shadow compares the extraction with the rules only when the answer was accepted for this text.
func Shadow(profile models.Profile, blacklist []models.BlacklistEntry, filter *models.FilterPreset, job models.JobInput, rules models.Evaluation, x models.JobExtraction, now time.Time) *models.ShadowEvaluation {
	if x.Status != "ok" || x.Extraction == nil || x.TextHash != extraction.Hash(job.Title, job.Description) {
		return nil
	}
	return &models.ShadowEvaluation{
		ExtractionID:  x.ID,
		TextHash:      x.TextHash,
		AnsweredAt:    x.CreatedAt.UTC(),
		RulesScore:    rules.Score,
		RulesRejected: rules.Rejected,
		Evaluation:    EvaluateWith(profile, blacklist, filter, job, x.Extraction, now),
	}
}
