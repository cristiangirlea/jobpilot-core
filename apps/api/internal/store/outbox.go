package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/models"
)

const outboxColumns = `id,job_id,application_id,status,channel,ats_provider,cv_document_id,cv_decision,cover_letter,cover_letter_source,form,answers,blockers,notices,needs_review,error,created_at,updated_at,approved_at,sent_at`

func scanOutbox(row pgx.Row) (models.OutboxEntry, error) {
	var entry models.OutboxEntry
	var application, cvDocument *string
	var form, answers []byte
	err := row.Scan(&entry.ID, &entry.JobID, &application, &entry.Status, &entry.Channel, &entry.ATSProvider,
		&cvDocument, &entry.CVDecision, &entry.CoverLetter, &entry.CoverLetterSource,
		&form, &answers, &entry.Blockers, &entry.Notices, &entry.NeedsReview, &entry.Error,
		&entry.CreatedAt, &entry.UpdatedAt, &entry.ApprovedAt, &entry.SentAt)
	if err != nil {
		return entry, err
	}
	if application != nil {
		entry.ApplicationID = *application
	}
	if cvDocument != nil {
		entry.CVDocumentID = *cvDocument
	}
	if len(form) > 0 {
		_ = json.Unmarshal(form, &entry.Form)
	}
	entry.Answers = []models.OutboxAnswer{}
	if len(answers) > 0 {
		_ = json.Unmarshal(answers, &entry.Answers)
	}
	return entry, nil
}

// ErrOutboxSent is returned when a caller tries to rebuild an entry that has
// already gone out. A sent application cannot be un-sent, so the row is
// immutable rather than silently left alone.
var ErrOutboxSent = errors.New("this application has already been sent and cannot be composed again")

// ApprovalWithdrawnNotice is added to a recomposed entry that the user had
// already approved. Rebuilding clears the approval, which is the safe
// direction, but it must not happen quietly.
const ApprovalWithdrawnNotice = "You had approved this application. Rebuilding it withdrew that approval, so it needs approving again."

// SaveOutboxEntry composes or recomposes the queued application for one job.
//
// Recomposing an entry the user already approved resets it to await approval
// again: the CV, letter, or answers may have changed, and a prior approval was
// given for what the entry said then, not for whatever it says now. The
// notice is tied to that transition, so a second rebuild carries none: by
// then there is no approval left to withdraw.
//
// The read and the write share a transaction, and the read locks the row, so
// a sender marking the entry sent cannot slip between them and turn a clear
// conflict back into an unexplained failure.
func (s *Store) SaveOutboxEntry(ctx context.Context, userID string, entry models.OutboxEntry) (models.OutboxEntry, error) {
	form, err := json.Marshal(entry.Form)
	if err != nil {
		return models.OutboxEntry{}, err
	}
	if entry.Answers == nil {
		entry.Answers = []models.OutboxAnswer{}
	}
	answers, err := json.Marshal(entry.Answers)
	if err != nil {
		return models.OutboxEntry{}, err
	}
	if entry.Blockers == nil {
		entry.Blockers = []string{}
	}
	if entry.Notices == nil {
		entry.Notices = []string{}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return models.OutboxEntry{}, err
	}
	defer tx.Rollback(ctx)

	// Rebuilding clears approved_at. Say so on the new entry rather than
	// letting an approval disappear without a word.
	var previousStatus string
	var wasApproved bool
	err = tx.QueryRow(ctx, `SELECT status, approved_at IS NOT NULL FROM application_outbox WHERE user_id=$1 AND job_id=$2 FOR UPDATE`, userID, entry.JobID).Scan(&previousStatus, &wasApproved)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return models.OutboxEntry{}, err
	}
	if previousStatus == models.OutboxSent {
		return models.OutboxEntry{}, ErrOutboxSent
	}
	if wasApproved {
		entry.Notices = append(entry.Notices, ApprovalWithdrawnNotice)
	}

	var application, cvDocument any
	if entry.ApplicationID != "" {
		application = entry.ApplicationID
	}
	if entry.CVDocumentID != "" {
		cvDocument = entry.CVDocumentID
	}
	saved, err := scanOutbox(tx.QueryRow(ctx, `INSERT INTO application_outbox
		(user_id,job_id,application_id,status,channel,ats_provider,cv_document_id,cv_decision,cover_letter,cover_letter_source,form,answers,blockers,notices,needs_review)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (user_id,job_id) DO UPDATE SET
			application_id=EXCLUDED.application_id, status=EXCLUDED.status, channel=EXCLUDED.channel,
			ats_provider=EXCLUDED.ats_provider, cv_document_id=EXCLUDED.cv_document_id,
			cv_decision=EXCLUDED.cv_decision, cover_letter=EXCLUDED.cover_letter,
			cover_letter_source=EXCLUDED.cover_letter_source, form=EXCLUDED.form,
			answers=EXCLUDED.answers, blockers=EXCLUDED.blockers, notices=EXCLUDED.notices, needs_review=EXCLUDED.needs_review,
			error='', approved_at=NULL, updated_at=now()
		WHERE application_outbox.status <> 'sent'
		RETURNING `+outboxColumns,
		userID, entry.JobID, application, entry.Status, entry.Channel, entry.ATSProvider,
		cvDocument, entry.CVDecision, entry.CoverLetter, entry.CoverLetterSource,
		form, answers, entry.Blockers, entry.Notices, entry.NeedsReview))
	if err != nil {
		return models.OutboxEntry{}, err
	}
	return saved, tx.Commit(ctx)
}

// OutboxEntries lists the queue. status "" means every entry.
func (s *Store) OutboxEntries(ctx context.Context, userID, status string) ([]models.OutboxEntry, error) {
	query := `SELECT ` + outboxColumns + ` FROM application_outbox WHERE user_id=$1`
	args := []any{userID}
	if status != "" {
		query += ` AND status=$2`
		args = append(args, status)
	}
	rows, err := s.DB.Query(ctx, query+` ORDER BY created_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.OutboxEntry{}
	for rows.Next() {
		entry, scanErr := scanOutbox(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, entry)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// A queue is unreadable without knowing which job each row is for.
	for i := range out {
		if job, jobErr := s.CatalogJob(ctx, out[i].JobID); jobErr == nil {
			out[i].Job = &job
		}
	}
	return out, nil
}

func (s *Store) OutboxEntry(ctx context.Context, userID, id string) (models.OutboxEntry, error) {
	entry, err := scanOutbox(s.DB.QueryRow(ctx, `SELECT `+outboxColumns+` FROM application_outbox WHERE user_id=$1 AND id=$2`, userID, id))
	if err != nil {
		return entry, err
	}
	if job, jobErr := s.CatalogJob(ctx, entry.JobID); jobErr == nil {
		entry.Job = &job
	}
	return entry, nil
}

// SetOutboxStatus moves an entry through the queue.
//
// Approval is refused while blockers stand. The check lives here rather than in
// the handler because it is a safety rule: an entry with an unapproved CV or an
// unanswered required question must not become sendable, whatever the caller
// believes.
func (s *Store) SetOutboxStatus(ctx context.Context, userID, id, status string) (models.OutboxEntry, error) {
	current, err := s.OutboxEntry(ctx, userID, id)
	if err != nil {
		return models.OutboxEntry{}, err
	}
	if current.Status == models.OutboxSent {
		return models.OutboxEntry{}, errors.New("this application has already been sent")
	}
	if status == models.OutboxApproved && len(current.Blockers) > 0 {
		return models.OutboxEntry{}, fmt.Errorf("this application cannot be approved yet: %s", current.Blockers[0])
	}
	if status == models.OutboxApproved {
		// The posting may have closed after the entry was composed.
		var reason string
		if err := s.DB.QueryRow(ctx, `SELECT closed_reason FROM jobs WHERE id=$1 AND closed_at IS NOT NULL`, current.JobID).Scan(&reason); err == nil {
			return models.OutboxEntry{}, fmt.Errorf("this posting has closed: %s. It can no longer be applied to", reason)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return models.OutboxEntry{}, err
		}
	}
	return scanOutbox(s.DB.QueryRow(ctx, `UPDATE application_outbox
		SET status=$3, approved_at=CASE WHEN $3='approved' THEN now() ELSE approved_at END, updated_at=now()
		WHERE user_id=$1 AND id=$2 RETURNING `+outboxColumns, userID, id, status))
}

func (s *Store) DeleteOutboxEntry(ctx context.Context, userID, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM application_outbox WHERE user_id=$1 AND id=$2`, userID, id)
	return err
}
