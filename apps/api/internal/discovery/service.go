package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/company"
	"jobpilot/core/api/internal/generator"
	"jobpilot/core/api/internal/matching"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

type Service struct {
	Store   *store.Store
	Fetcher *Fetcher
	mu      sync.Mutex
}

var ErrMissingDesiredTitle = errors.New("add a desired job title to your profile first")

func NewService(s *store.Store) *Service { return &Service{Store: s, Fetcher: NewFetcher()} }

func (s *Service) ValidateSource(ctx context.Context, source models.DiscoverySource) (int, string, error) {
	jobs, err := s.Fetcher.Fetch(ctx, source)
	if err != nil {
		return 0, "", err
	}
	sample := ""
	if len(jobs) > 0 {
		sample = jobs[0].Input.Title
	}
	return len(jobs), sample, nil
}

func (s *Service) RunRecommended(ctx context.Context, userID string) (models.DiscoveryRun, error) {
	profile, err := s.Store.Profile(ctx, userID)
	if err != nil {
		return models.DiscoveryRun{}, err
	}
	query := ""
	for _, title := range profile.DesiredTitles {
		if strings.TrimSpace(title) != "" {
			query = strings.TrimSpace(title)
			break
		}
	}
	if query == "" {
		query = strings.TrimSpace(profile.CurrentTitle)
	}
	if query == "" {
		return models.DiscoveryRun{}, ErrMissingDesiredTitle
	}
	if _, err = s.Store.EnsureRecommendedSource(ctx, userID, query); err != nil {
		return models.DiscoveryRun{}, err
	}
	return s.RunUser(ctx, userID)
}

func (s *Service) RunUser(ctx context.Context, userID string) (models.DiscoveryRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.Store.StartDiscoveryRun(ctx, userID)
	if err != nil {
		return run, err
	}
	settings, err := s.Store.DiscoverySettings(ctx, userID)
	if err != nil {
		return s.finish(ctx, userID, run, err)
	}
	sources, err := s.Store.DiscoverySources(ctx, userID, true)
	if err != nil {
		return s.finish(ctx, userID, run, err)
	}
	if len(sources) == 0 {
		return s.finish(ctx, userID, run, errors.New("add at least one active discovery source"))
	}
	profile, err := s.Store.Profile(ctx, userID)
	if err != nil {
		return s.finish(ctx, userID, run, err)
	}
	blacklist, err := s.Store.Blacklist(ctx, userID)
	if err != nil {
		return s.finish(ctx, userID, run, err)
	}
	filter, err := s.Store.ActiveFilter(ctx, userID)
	if err != nil {
		return s.finish(ctx, userID, run, err)
	}
	filterID := ""
	if filter != nil {
		filterID = filter.ID
	}
	succeeded := 0
	var sourceErrors []string
	for _, source := range sources {
		jobs, fetchErr := s.Fetcher.Fetch(ctx, source)
		if fetchErr != nil {
			message := fetchErr.Error()
			_ = s.Store.UpdateDiscoverySourceResult(ctx, userID, source.ID, message)
			sourceErrors = append(sourceErrors, source.Name+": "+message)
			continue
		}
		succeeded++
		_ = s.Store.UpdateDiscoverySourceResult(ctx, userID, source.ID, "")
		for _, candidate := range jobs {
			if run.JobsSeen >= settings.MaxJobsPerRun {
				break
			}
			run.JobsSeen++
			if strings.TrimSpace(candidate.Input.Title) == "" || strings.TrimSpace(candidate.Input.Company) == "" || strings.TrimSpace(candidate.Input.Description) == "" {
				continue
			}
			fingerprint := Fingerprint(candidate.Input)
			job, getErr := s.Store.CatalogJobByFingerprint(ctx, fingerprint)
			duplicate := getErr == nil
			if getErr != nil && !errors.Is(getErr, pgx.ErrNoRows) {
				return s.finish(ctx, userID, run, getErr)
			}
			if !duplicate {
				job, getErr = s.Store.CreateDiscoveredJob(ctx, userID, source.ID, source.Provider, source.Name, candidate.ExternalID, fingerprint, candidate.Input)
				if getErr != nil {
					job, getErr = s.Store.CatalogJobByFingerprint(ctx, fingerprint)
					if getErr != nil {
						return s.finish(ctx, userID, run, getErr)
					}
					duplicate = true
				} else {
					run.JobsAdded++
				}
			}
			if duplicate {
				run.DuplicatesSkipped++
			}
			evaluation := matching.Evaluate(profile, blacklist, filter, job.JobInput, time.Now())
			if _, err = s.Store.SaveJobMatch(ctx, userID, job.ID, evaluation, filterID); err != nil {
				return s.finish(ctx, userID, run, err)
			}
			if evaluation.Rejected {
				run.JobsRejected++
				continue
			}
			if settings.AutoPrepare && evaluation.Score >= settings.MinScore {
				prepared, prepErr := s.prepare(ctx, userID, job, evaluation)
				if prepErr != nil {
					return s.finish(ctx, userID, run, prepErr)
				}
				if prepared {
					run.ApplicationsPrepared++
				}
			}
		}
		if run.JobsSeen >= settings.MaxJobsPerRun {
			break
		}
	}
	var runErr error
	if succeeded == 0 {
		runErr = fmt.Errorf("all discovery sources failed: %s", strings.Join(sourceErrors, "; "))
	}
	return s.finish(ctx, userID, run, runErr)
}

func (s *Service) prepare(ctx context.Context, userID string, job models.Job, evaluation models.Evaluation) (bool, error) {
	application, err := s.Store.CreateApplication(ctx, userID, job.ID)
	if err != nil {
		return false, err
	}
	if application.Status != "draft" {
		return false, nil
	}
	profile, err := s.Store.Profile(ctx, userID)
	if err != nil {
		return false, err
	}
	docs := generator.Generate(profile, job, evaluation)
	if err = s.Store.SaveDocuments(ctx, userID, job.ID, docs); err != nil {
		return false, err
	}
	_, err = s.Store.UpdateApplication(ctx, userID, application.ID, "ready", "Automatically prepared from a discovered match. Review before submitting.")
	return err == nil, err
}

func (s *Service) finish(ctx context.Context, userID string, run models.DiscoveryRun, runErr error) (models.DiscoveryRun, error) {
	finished, err := s.Store.FinishDiscoveryRun(ctx, userID, run, runErr)
	_ = s.Store.TouchDiscoverySchedule(ctx, userID)
	return finished, err
}

func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	s.runDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDue(ctx)
		}
	}
}
func (s *Service) runDue(ctx context.Context) {
	ids, err := s.Store.ClaimDueDiscoveryUsers(ctx, 10)
	if err != nil {
		slog.Error("claim discovery schedules", "error", err)
		return
	}
	for _, id := range ids {
		run, runErr := s.RunUser(ctx, id)
		if runErr != nil {
			slog.Error("scheduled discovery failed", "user_id", id, "error", runErr)
		} else {
			slog.Info("scheduled discovery completed", "user_id", id, "status", run.Status, "jobs_added", run.JobsAdded)
		}
	}
}

func Fingerprint(j models.JobInput) string {
	apply := ""
	if u, err := url.Parse(j.ApplyURL); err == nil {
		apply = strings.ToLower(u.Host + u.Path)
	}
	raw := strings.Join([]string{company.Normalize(j.Company), strings.ToLower(strings.Join(strings.Fields(j.Title), " ")), strings.ToLower(strings.Join(strings.Fields(j.Location), " ")), apply}, "|")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
