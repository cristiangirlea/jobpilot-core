package discovery

import (
	"context"
	"errors"
	"time"

	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/matching"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// saveRunMatch scores one job of a run. With shadow scoring, a known job is read and saved under the matching lock, so this write and the one made when an answer is stored cannot overwrite each other's newer shadow.
func (s *Service) saveRunMatch(ctx context.Context, userID, filterID string, profile models.Profile, blacklist []models.BlacklistEntry, filter *models.FilterPreset, job models.Job, duplicate bool) (models.Evaluation, error) {
	now := time.Now()
	evaluation := matching.Evaluate(profile, blacklist, filter, job.JobInput, now)
	item := store.JobEvaluation{JobID: job.ID, Evaluation: evaluation}
	if duplicate && s.ShadowScoring {
		defer s.LockMatching(userID)()
		answers, err := s.CurrentExtractions(ctx, job.ID)
		if err != nil {
			return evaluation, err
		}
		item.Shadow = matching.Shadow(profile, blacklist, filter, job.JobInput, evaluation, answers[job.ID], now)
	}
	_, err := s.Store.SaveJobEvaluation(ctx, userID, filterID, item)
	return evaluation, err
}

// CurrentExtractions reads accepted answers for the current text only when shadow scoring is on.
func (s *Service) CurrentExtractions(ctx context.Context, jobIDs ...string) (map[string]models.JobExtraction, error) {
	if s == nil || !s.ShadowScoring {
		return nil, nil
	}
	return s.Store.CurrentExtractions(ctx, s.ExtractionModel, extraction.PromptVersion, jobIDs)
}

// RescoreShadow refreshes comparisons after an answer is stored without changing the visible match.
func (s *Service) RescoreShadow(ctx context.Context, jobID string) error {
	if s == nil || !s.ShadowScoring {
		return nil
	}
	return s.rescoreShadow(ctx, jobID, s.LockMatching)
}

func (s *Service) rescoreShadow(ctx context.Context, jobID string, lock func(userID string) func()) error {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	users, err := s.Store.MatchUsers(readCtx, jobID)
	cancel()
	if err != nil {
		return err
	}
	var failures []error
	for _, userID := range users {
		err := func() error {
			defer lock(userID)()
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()

			job, err := s.Store.CatalogJob(ctx, jobID)
			if err != nil {
				return err
			}
			answers, err := s.CurrentExtractions(ctx, jobID)
			if err != nil {
				return err
			}
			profile, err := s.Store.Profile(ctx, userID)
			if err != nil {
				return err
			}
			blacklist, err := s.Store.Blacklist(ctx, userID)
			if err != nil {
				return err
			}
			filter, err := s.Store.ActiveFilter(ctx, userID)
			if err != nil {
				return err
			}
			now := time.Now()
			rules := matching.Evaluate(profile, blacklist, filter, job.JobInput, now)
			shadow := matching.Shadow(profile, blacklist, filter, job.JobInput, rules, answers[jobID], now)
			return s.Store.SaveShadowEvaluation(ctx, userID, jobID, shadow)
		}()
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
