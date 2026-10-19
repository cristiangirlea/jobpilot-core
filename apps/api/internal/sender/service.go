package sender

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"jobpilot/core/api/internal/ats"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// Sending is paced like the link checker: a few entries a sweep with a pause
// between them, and nothing left in flight for long.
const (
	sweepEvery = time.Minute
	sweepBatch = 10
	sendPause  = 2 * time.Second
	// sendTimeout bounds one send, reading the form included.
	sendTimeout = 2 * time.Minute
	// staleAfter writes off an entry left sending, well past sendTimeout so
	// a send still running is never taken for a stopped one.
	staleAfter = 10 * time.Minute
)

// Service sends approved outbox entries. An entry waits Delay after its
// approval before it goes, so the user can still withdraw it.
type Service struct {
	Store      *store.Store
	Greenhouse *Greenhouse
	Delay      time.Duration
	kick       chan struct{}
	// onlyUser keeps a test's sweeps to its own user's entries.
	onlyUser string
}

func New(st *store.Store, greenhouse *Greenhouse, delay time.Duration) *Service {
	return &Service{Store: st, Greenhouse: greenhouse, Delay: delay, kick: make(chan struct{}, 1)}
}

// Kick asks for a sweep once the delay has passed, after an approval.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Start sweeps every minute, and once more Delay after each approval.
func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(sweepEvery)
	defer ticker.Stop()
	soon := time.NewTimer(s.Delay)
	defer soon.Stop()
	s.Sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Sweep(ctx)
		case <-s.kick:
			// A second of slack, so an entry approved just now is due.
			soon.Reset(s.Delay + time.Second)
		case <-soon.C:
			s.Sweep(ctx)
		}
	}
}

// Sweep writes off stopped sends, then sends what is due, up to a batch.
func (s *Service) Sweep(ctx context.Context) {
	if failed, err := s.Store.FailStaleSends(ctx, staleAfter); err != nil {
		slog.Error("write off interrupted sends", "error", err)
	} else if failed > 0 {
		slog.Warn("wrote off interrupted sends", "count", failed)
	}
	for i := 0; i < sweepBatch; i++ {
		claim, ok, err := s.Store.ClaimOutboxEntry(ctx, s.Delay, s.onlyUser)
		if err != nil {
			slog.Error("take an approved application for sending", "error", err)
			return
		}
		if !ok {
			return
		}
		if claim.Refused != "" {
			slog.Info("approved application no longer sendable", "entry_id", claim.Entry.ID)
			continue
		}
		s.send(ctx, claim)
		select {
		case <-ctx.Done():
			return
		case <-time.After(sendPause):
		}
	}
}

func (s *Service) send(ctx context.Context, claim store.OutboxClaim) {
	entry := claim.Entry
	err := s.submit(ctx, claim.UserID, entry)
	sent, reason := err == nil, ""
	var handBack HandBack
	var unknown Unknown
	switch {
	case sent:
	case errors.As(err, &handBack), errors.As(err, &unknown):
		reason = err.Error()
	default:
		// Only failures before the post reach here (submit wraps the post's
		// own), so nothing left.
		slog.Error("send an approved application", "entry_id", entry.ID, "error", err)
		reason = "Sending failed before anything reached the employer. Prepare the application again, or apply on the employer's site."
	}
	// Finish on a fresh context, so a shutdown mid-send still records what
	// happened; if even that fails, the entry is written off as interrupted.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.Store.FinishOutboxSend(finish, claim.UserID, entry.ID, sent, reason); err != nil {
		slog.Error("record how a send ended", "entry_id", entry.ID, "sent", sent, "error", err)
		return
	}
	slog.Info("approved application handled", "entry_id", entry.ID, "sent", sent)
}

func (s *Service) submit(ctx context.Context, userID string, entry models.OutboxEntry) error {
	if entry.ATSProvider != ats.ProviderGreenhouse || entry.Channel != models.ChannelATSForm {
		name := entry.ATSProvider
		if name == "" {
			name = "this employer's"
		}
		return HandBack(fmt.Sprintf("JobPilot cannot send %s forms yet, so nothing was sent. Open the employer's form and send the application yourself.", name))
	}
	board, _ := entry.Form["board"].(string)
	jobID, _ := entry.Form["job_id"].(string)
	sub := Submission{Board: board, JobID: jobID, Answers: entry.Answers, CoverLetter: entry.CoverLetter}
	var cvUpdatedAt *time.Time
	for _, answer := range entry.Answers {
		if answer.Source != "cv" {
			continue
		}
		doc, data, err := s.Store.CVFile(ctx, userID, entry.CVDocumentID)
		if err != nil {
			return HandBack("The approved CV could not be read, so nothing was sent. Prepare the application again.")
		}
		sub.CV = &File{Name: doc.Filename, ContentType: doc.ContentType, Data: data}
		cvUpdatedAt = &doc.UpdatedAt
		break
	}
	sub.BeforePost = func() error { return s.Store.RecordSubmitAttempt(ctx, userID, entry.ID, cvUpdatedAt) }
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	return s.Greenhouse.Submit(ctx, sub)
}
