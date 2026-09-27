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
	Links   *LinkChecker
	mu      sync.Mutex
}

var ErrMissingDesiredTitle = errors.New("add a desired job title to your profile first")

func NewService(s *store.Store) *Service {
	return &Service{Store: s, Fetcher: NewFetcher(), Links: NewLinkChecker(false)}
}

// deadLinkReasons are the closures a newer repost undoes: the old link went
// dead, and the repost's link is the one now listed.
var deadLinkReasons = []string{ReasonGone, ReasonNotFound, ReasonBoard}

// fullBoard lists the providers whose feed is every open job on the board, so
// a job missing from it has been taken down. Aggregator and search feeds are
// partial, and SmartRecruiters is fetched a page at a time.
var fullBoard = map[string]bool{"greenhouse": true, "lever": true, "ashby": true}

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
	fetched := make([][]models.DiscoveredJob, len(sources))
	for i, source := range sources {
		jobs, fetchErr := s.Fetcher.Fetch(ctx, source)
		if fetchErr != nil {
			message := fetchErr.Error()
			_ = s.Store.UpdateDiscoverySourceResult(ctx, userID, source.ID, message)
			sourceErrors = append(sourceErrors, source.Name+": "+message)
			continue
		}
		succeeded++
		_ = s.Store.UpdateDiscoverySourceResult(ctx, userID, source.ID, "")
		if fullBoard[source.Provider] {
			listed := make([]string, 0, len(jobs))
			for _, candidate := range jobs {
				listed = append(listed, candidate.ExternalID)
			}
			if closed, err := s.Store.CloseUnlisted(ctx, source.Provider, source.Identifier, listed, ReasonUnlisted); err != nil {
				return s.finish(ctx, userID, run, err)
			} else if closed > 0 {
				slog.Info("closed jobs no longer listed", "provider", source.Provider, "board", source.Identifier, "count", closed)
			}
		}
		fetched[i] = jobs
	}
	var fingerprints []string
	for _, jobs := range fetched {
		for _, candidate := range jobs {
			fingerprints = append(fingerprints, Fingerprint(candidate.Input))
		}
	}
	known, err := s.Store.KnownFingerprints(ctx, fingerprints)
	if err != nil {
		return s.finish(ctx, userID, run, err)
	}
	for i := range fetched {
		fetched[i] = newFirst(fetched[i], known)
	}
	for _, next := range shareBudget(sources, fetched, settings.MaxJobsPerRun) {
		source, candidate := next.source, next.candidate
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
			// A board reposts an opening under a new link, which gives it a
			// new fingerprint. It is still the stored job, not another one.
			stored, repostErr := s.Store.RepostCandidates(ctx, source.Provider, company.Normalize(candidate.Input.Company))
			if repostErr != nil {
				return s.finish(ctx, userID, run, repostErr)
			}
			if original, ok := repostOf(candidate.Input, stored); ok {
				if err = s.Store.AttachRepost(ctx, original.ID, fingerprint, candidate.ExternalID, candidate.Input, deadLinkReasons); err != nil {
					return s.finish(ctx, userID, run, err)
				}
				job, duplicate = original, true
			}
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
		if err = s.Store.MarkSeen(ctx, job.ID, ReasonUnlisted); err != nil {
			return s.finish(ctx, userID, run, err)
		}
		if duplicate {
			run.DuplicatesSkipped++
			// Keep a known posting's text current: providers edit postings,
			// and text stored before Readable kept paragraphs was flattened.
			// Only text this provider stored is replaced, so a description a
			// user pasted into a manual import stays theirs.
			if job.SourceProvider == source.Provider && job.Description != candidate.Input.Description {
				if err = s.Store.RefreshDescription(ctx, job.ID, candidate.Input.Description); err != nil {
					return s.finish(ctx, userID, run, err)
				}
				job.Description = candidate.Input.Description
			}
		}
		evaluation := matching.Evaluate(profile, blacklist, filter, job.JobInput, time.Now())
		if _, err = s.Store.SaveJobMatch(ctx, userID, job.ID, evaluation, filterID); err != nil {
			return s.finish(ctx, userID, run, err)
		}
		if evaluation.Rejected {
			// Rejections count among the jobs this run added. A known
			// posting is re-scored too, but counting it here made a run
			// that added 6 jobs report 100 rejected.
			if !duplicate {
				run.JobsRejected++
			}
			continue
		}
		if settings.AutoPrepare && evaluation.Score >= settings.MinScore && job.ClosedAt == nil {
			prepared, prepErr := s.prepare(ctx, userID, job, evaluation)
			if prepErr != nil {
				return s.finish(ctx, userID, run, prepErr)
			}
			if prepared {
				run.ApplicationsPrepared++
			}
		}
	}
	var runErr error
	if succeeded == 0 {
		runErr = fmt.Errorf("all discovery sources failed: %s", strings.Join(sourceErrors, "; "))
	}
	return s.finish(ctx, userID, run, runErr)
}

// sourceJob is one fetched posting and the source it came from.
type sourceJob struct {
	source    models.DiscoverySource
	candidate models.DiscoveredJob
}

// newFirst puts the postings the catalog does not hold yet ahead of known
// ones, keeping each group's order. Once the budget is shared a source's share
// is small, and a board that lists known postings first would otherwise spend
// it re-scoring them every run and never reach a new one.
func newFirst(jobs []models.DiscoveredJob, known map[string]bool) []models.DiscoveredJob {
	out := make([]models.DiscoveredJob, 0, len(jobs))
	var seen []models.DiscoveredJob
	for _, candidate := range jobs {
		if known[Fingerprint(candidate.Input)] {
			seen = append(seen, candidate)
		} else {
			out = append(out, candidate)
		}
	}
	return append(out, seen...)
}

// shareBudget takes one posting from each source in turn until the run's
// budget is spent. Spent source by source, the budget went to the first
// sources every run and a later source was never read. A source with fewer
// postings than its share leaves the rest to the others, and each source's
// own order is kept.
func shareBudget(sources []models.DiscoverySource, fetched [][]models.DiscoveredJob, budget int) []sourceJob {
	var out []sourceJob
	for i := 0; len(out) < budget; i++ {
		took := false
		for s := range fetched {
			if i < len(fetched[s]) && len(out) < budget {
				out = append(out, sourceJob{source: sources[s], candidate: fetched[s][i]})
				took = true
			}
		}
		if !took {
			break
		}
	}
	return out
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
	prepared := "Automatically prepared from a discovered match. Review before submitting."
	_, err = s.Store.UpdateApplication(ctx, userID, application.ID, "ready", &prepared)
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
	// Link checks share this loop, so they never overlap a discovery run.
	links := time.NewTicker(linkCheckEvery)
	defer links.Stop()
	s.runDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDue(ctx)
		case <-links.C:
			s.checkLinks(ctx)
		}
	}
}

// Link checking is paced to stay polite to the sites being asked: a small
// batch every few minutes, a pause between requests, and each job at most
// once a day. That is about 7,000 checks a day, well above what a catalog of
// jobs people still match needs.
const (
	linkCheckEvery = 5 * time.Minute
	linkCheckBatch = 25
	linkCheckAgain = 24 * time.Hour
	linkCheckPause = 2 * time.Second
	// linkCheckTimeout bounds one check, so a batch of slow sites holds the
	// discovery loop for at most about five minutes.
	linkCheckTimeout = 10 * time.Second
)

func (s *Service) checkLinks(ctx context.Context) {
	due, err := s.Store.LinksToCheck(ctx, linkCheckAgain, linkCheckBatch)
	if err != nil {
		slog.Error("list links to check", "error", err)
		return
	}
	closed := 0
	for i, link := range due {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(linkCheckPause):
			}
		}
		if s.CheckJob(ctx, link, linkCheckTimeout).Closed {
			closed++
		}
	}
	if closed > 0 {
		slog.Info("closed jobs whose links are gone", "checked", len(due), "closed", closed)
	}
}

// LinkResult is what one check of a job's link found.
type LinkResult struct {
	Closed bool
	Reason string
	// EmployerURL is where the posting applies, when this check found it.
	EmployerURL string
}

// CheckJob checks one job's links now, giving each request at most timeout,
// and records the answer. A failed check counts as open, so an unreachable
// site never hides a job.
//
// A posting found through an aggregator is closed by either page: the
// aggregator removing it, or the employer, where the application would go.
// While the employer's page is not known, an open posting from an aggregator
// that hands over by redirect asks where it is, once.
func (s *Service) CheckJob(ctx context.Context, job store.LinkToCheck, timeout time.Duration) LinkResult {
	var result LinkResult
	for _, link := range []string{job.ApplyURL, job.EmployerURL} {
		if link == "" || result.Closed {
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		closed, reason, err := s.Links.Check(checkCtx, link)
		cancel()
		if err == nil {
			result.Closed, result.Reason = closed, reason
		}
	}
	if !result.Closed && job.EmployerURL == "" && RedirectsToEmployer(job.SourceProvider) {
		lookupCtx, cancel := context.WithTimeout(ctx, timeout)
		employer, err := s.Links.EmployerURL(lookupCtx, job.SourceProvider, job.ApplyURL)
		cancel()
		if err == nil {
			result.EmployerURL = employer
		}
	}
	if recordErr := s.Store.RecordLinkCheck(ctx, job.ID, result.Closed, result.Reason, result.EmployerURL); recordErr != nil {
		slog.Error("record link check", "job_id", job.ID, "error", recordErr)
	}
	return result
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
