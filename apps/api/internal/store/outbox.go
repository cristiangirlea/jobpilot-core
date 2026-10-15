package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/models"
)

const outboxColumns = `id,job_id,application_id,status,channel,ats_provider,cv_document_id,cv_decision,cover_letter,cover_letter_source,form,answers,blockers,notices,needs_review,error,created_at,updated_at,approved_at,sent_at`

// outboxSelect reads entries with the CV each one carries, in one join. The
// join is scoped to the entry's user, so a CV id that is not theirs never
// brings another user's CV along. Scan its rows with scanOutboxWithCV.
var outboxSelect = `SELECT o.` + strings.ReplaceAll(outboxColumns, ",", ",o.") + `,` + outboxCVColumns + `
	FROM application_outbox o LEFT JOIN cv_documents c ON c.id=o.cv_document_id AND c.user_id=o.user_id`

// outboxCVColumns are the CV summary the outbox shows: the coverage line comes
// from a tailored CV's parse, so the whole document is never read for it. The
// parse's shape is not enforced by the schema, so its values are read as raw
// JSON and decoded in Go: a malformed one is left out, never fails the query.
const outboxCVColumns = `c.id,c.filename,c.kind,c.approved,c.parsed->'tailored'->'alignment',c.parsed->'tailored'->'missing'`

func scanOutbox(row pgx.Row) (models.OutboxEntry, error) {
	return scanOutboxRow(row, false)
}

func scanOutboxWithCV(row pgx.Row) (models.OutboxEntry, error) {
	return scanOutboxRow(row, true)
}

func scanOutboxRow(row pgx.Row, withCV bool) (models.OutboxEntry, error) {
	var entry models.OutboxEntry
	var application, cvDocument *string
	var form, answers []byte
	dest := []any{&entry.ID, &entry.JobID, &application, &entry.Status, &entry.Channel, &entry.ATSProvider,
		&cvDocument, &entry.CVDecision, &entry.CoverLetter, &entry.CoverLetterSource,
		&form, &answers, &entry.Blockers, &entry.Notices, &entry.NeedsReview, &entry.Error,
		&entry.CreatedAt, &entry.UpdatedAt, &entry.ApprovedAt, &entry.SentAt}
	var cv cvSummary
	if withCV {
		dest = append(dest, cv.dest()...)
	}
	if err := row.Scan(dest...); err != nil {
		return entry, err
	}
	entry.CV = cv.summary()
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

// cvSummary receives outboxCVColumns, every one nullable since the join may
// find no CV.
type cvSummary struct {
	id, filename, kind *string
	approved           *bool
	alignment, missing []byte
}

func (c *cvSummary) dest() []any {
	return []any{&c.id, &c.filename, &c.kind, &c.approved, &c.alignment, &c.missing}
}

// summary is nil when there was no CV to read: deleted, or not the user's.
func (c *cvSummary) summary() *models.OutboxCV {
	if c.id == nil {
		return nil
	}
	cv := &models.OutboxCV{ID: *c.id}
	if c.filename != nil {
		cv.Filename = *c.filename
	}
	if c.kind != nil {
		cv.Kind = *c.kind
	}
	if c.approved != nil {
		cv.Approved = *c.approved
	}
	// Only a number is an alignment (JSON has no NaN or infinity), and only a
	// list of strings is a missing list; anything else is left out. A pointer,
	// because decoding JSON null succeeds and would otherwise read as zero.
	var alignment *float64
	if len(c.alignment) > 0 && json.Unmarshal(c.alignment, &alignment) == nil {
		cv.Alignment = alignment
	}
	// Pointers again, so a null element is told apart from an empty string.
	var missing []*string
	if len(c.missing) > 0 && json.Unmarshal(c.missing, &missing) == nil && !slices.Contains(missing, nil) {
		for _, technology := range missing {
			cv.Missing = append(cv.Missing, *technology)
		}
	}
	return cv
}

// ErrOutboxSent is returned when a caller tries to rebuild an entry that has
// already gone out. A sent application cannot be un-sent, so the row is
// immutable rather than silently left alone.
var ErrOutboxSent = errors.New("this application has already been sent and cannot be composed again")

// ApprovalWithdrawnNotice is added to a recomposed entry that the user had
// already approved. Rebuilding clears the approval, which is the safe
// direction, but it must not happen quietly.
const ApprovalWithdrawnNotice = "You had approved this application. Rebuilding it withdrew that approval, so it needs approving again."

// CVBlocker holds back an entry whose tailored CV awaits the user's approval,
// and BaseCVBlocker one whose uploaded CV had its approval withdrawn. They are
// added and cleared as the CV's approval changes, wherever that happens, so
// approving a CV frees every application carrying it without rebuilding them.
const (
	CVBlocker     = "A tailored CV was prepared for this role and needs your approval before it can be sent."
	BaseCVBlocker = "Your CV is no longer approved. Approve it again, or prepare this application again to attach another."
)

// CVWithdrawnNotice explains why an approved entry went back to review when
// its CV lost its approval.
const CVWithdrawnNotice = "You had approved this application, but its CV is no longer approved, so the application needs approving again."

func cvBlocker(kind string) string {
	if kind == "base" {
		return BaseCVBlocker
	}
	return CVBlocker
}

// syncCVBlocker carries a CV's approval into the entries that carry it. Sent
// and cancelled entries are history and stay as they are.
func syncCVBlocker(ctx context.Context, tx pgx.Tx, userID string, doc models.CVDocument) error {
	blocker := cvBlocker(doc.Kind)
	if doc.Approved {
		_, err := tx.Exec(ctx, `UPDATE application_outbox SET
				blockers=array_remove(blockers,$3),
				status=CASE WHEN status='needs_review' AND cardinality(array_remove(blockers,$3))=0 THEN 'ready' ELSE status END,
				updated_at=now()
			WHERE user_id=$1 AND cv_document_id=$2 AND $3=ANY(blockers) AND status IN ('needs_review','ready')`,
			userID, doc.ID, blocker)
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE application_outbox SET
			blockers=array_append(blockers,$3),
			notices=CASE WHEN approved_at IS NOT NULL THEN array_append(notices,$4) ELSE notices END,
			status='needs_review', approved_at=NULL, updated_at=now()
		WHERE user_id=$1 AND cv_document_id=$2 AND NOT ($3=ANY(blockers)) AND status IN ('needs_review','ready','approved')`,
		userID, doc.ID, blocker, CVWithdrawnNotice)
	return err
}

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
	if previousStatus == models.OutboxSending {
		return models.OutboxEntry{}, ErrOutboxSending
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
		WHERE application_outbox.status NOT IN ('sent','sending')
		RETURNING `+outboxColumns,
		userID, entry.JobID, application, entry.Status, entry.Channel, entry.ATSProvider,
		cvDocument, entry.CVDecision, entry.CoverLetter, entry.CoverLetterSource,
		form, answers, entry.Blockers, entry.Notices, entry.NeedsReview))
	if err != nil {
		return models.OutboxEntry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.OutboxEntry{}, err
	}
	return s.withCV(ctx, userID, saved), nil
}

// OutboxEntries lists the queue. status "" means every entry.
func (s *Store) OutboxEntries(ctx context.Context, userID, status string) ([]models.OutboxEntry, error) {
	query := outboxSelect + ` WHERE o.user_id=$1`
	args := []any{userID}
	if status != "" {
		query += ` AND o.status=$2`
		args = append(args, status)
	}
	rows, err := s.DB.Query(ctx, query+` ORDER BY o.created_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.OutboxEntry{}
	for rows.Next() {
		entry, scanErr := scanOutboxWithCV(rows)
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
	entry, err := scanOutboxWithCV(s.DB.QueryRow(ctx, outboxSelect+` WHERE o.user_id=$1 AND o.id=$2`, userID, id))
	if err != nil {
		return entry, err
	}
	if job, jobErr := s.CatalogJob(ctx, entry.JobID); jobErr == nil {
		entry.Job = &job
	}
	return entry, nil
}

// OutboxEntryForJob returns the entry already queued for a job, if there is one.
func (s *Store) OutboxEntryForJob(ctx context.Context, userID, jobID string) (models.OutboxEntry, error) {
	return scanOutbox(s.DB.QueryRow(ctx, `SELECT `+outboxColumns+` FROM application_outbox WHERE user_id=$1 AND job_id=$2`, userID, jobID))
}

// OutboxRefusal is why an entry cannot be approved or cancelled as asked: the
// user's to fix, not a server fault, so the API answers 409 with it.
type OutboxRefusal string

func (r OutboxRefusal) Error() string { return string(r) }

// ApproveOutboxEntry releases an entry for sending. Approving is the user's
// consent to send exactly what the entry says, so it is refused unless all of
// this holds now, not only when the entry was composed:
//
//   - the entry waits for approval (ready, or needs_review with its blockers
//     cleared); one already approved is returned as it is;
//   - no blocker stands;
//   - its CV still exists and is approved;
//   - every required answer drafted by a provider or a template has been
//     reviewed: an AI draft is approved in the answer bank with the wording the
//     entry carries, and template wording is never taken as reviewed;
//   - the posting is still open.
//
// The checks live here rather than in the handler because they are a safety
// rule, whatever the caller believes. The entry stays locked from the checks to
// the approval, so a recompose cannot slip in between them. The answers it
// reads stay locked too (FOR SHARE): a revocation waits for the approval, so
// the answers approved were approved together, and their writers lock only
// their own row, so this order cannot deadlock with them. The CV is not
// locked: SetCVApproval locks the CV before the entries, the other way round,
// and it already carries a withdrawal into the entry (syncCVBlocker waits for
// this lock, then moves an approved entry back to needs_review).
func (s *Store) ApproveOutboxEntry(ctx context.Context, userID, id string) (models.OutboxEntry, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return models.OutboxEntry{}, err
	}
	defer tx.Rollback(ctx)
	entry, err := scanOutbox(tx.QueryRow(ctx, `SELECT `+outboxColumns+` FROM application_outbox WHERE user_id=$1 AND id=$2 FOR UPDATE`, userID, id))
	if err != nil {
		return models.OutboxEntry{}, err
	}
	switch entry.Status {
	case models.OutboxApproved:
		// Done with the transaction before withJob takes another connection
		// from the pool: holding this one while waiting for that could
		// exhaust a small pool.
		if err := tx.Rollback(ctx); err != nil {
			return models.OutboxEntry{}, err
		}
		return s.withJob(ctx, userID, entry), nil
	case models.OutboxReady, models.OutboxNeedsReview:
	case models.OutboxSent:
		return models.OutboxEntry{}, OutboxRefusal("This application has already been sent.")
	case models.OutboxSending:
		return models.OutboxEntry{}, sendingRefusal
	default:
		return models.OutboxEntry{}, OutboxRefusal(fmt.Sprintf("This application is %s. Prepare it again to approve it.", entry.Status))
	}
	refusal, err := approvalRefusal(ctx, tx, userID, entry)
	if err != nil {
		return models.OutboxEntry{}, err
	}
	if refusal != "" {
		return models.OutboxEntry{}, refusal
	}
	approved, err := scanOutbox(tx.QueryRow(ctx, `UPDATE application_outbox
		SET status='approved', approved_at=now(), updated_at=now()
		WHERE user_id=$1 AND id=$2 RETURNING `+outboxColumns, userID, id))
	if err != nil {
		return models.OutboxEntry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.OutboxEntry{}, err
	}
	return s.withJob(ctx, userID, approved), nil
}

// approvalRefusal is the first reason the entry cannot be approved now, or "".
func approvalRefusal(ctx context.Context, tx pgx.Tx, userID string, entry models.OutboxEntry) (OutboxRefusal, error) {
	if len(entry.Blockers) > 0 {
		return OutboxRefusal("This application cannot be approved yet: " + entry.Blockers[0]), nil
	}

	// The CV. Deleting it leaves the entry without one (the column is set to
	// null), which no blocker records.
	if entry.CVDocumentID == "" {
		return OutboxRefusal("This application's CV was deleted. Prepare it again to attach another."), nil
	}
	var kind string
	var cvApproved bool
	err := tx.QueryRow(ctx, `SELECT kind, approved FROM cv_documents WHERE user_id=$1 AND id=$2`, userID, entry.CVDocumentID).Scan(&kind, &cvApproved)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboxRefusal("This application's CV was deleted. Prepare it again to attach another."), nil
	}
	if err != nil {
		return "", err
	}
	if !cvApproved {
		return OutboxRefusal(cvBlocker(kind)), nil
	}

	// Required answers someone has to have read. The sources are answers.SourceAI
	// and answers.SourceTemplate; a model or the template wrote them, not the user.
	for _, answer := range entry.Answers {
		if !answer.Required || !answer.NeedsReview {
			continue
		}
		switch answer.Source {
		case "template":
			return OutboxRefusal(fmt.Sprintf("A required question carries template wording you have not reviewed: %q. Save your own answer to it in the answer bank, then prepare this application again.", answer.Label)), nil
		case "ai":
			var banked string
			var bankApproved bool
			err := pgx.ErrNoRows // a draft the bank never stored has nothing to approve
			if answer.EntryID != "" {
				err = tx.QueryRow(ctx, `SELECT answer, approved FROM answer_bank WHERE user_id=$1 AND id=$2 FOR SHARE`, userID, answer.EntryID).Scan(&banked, &bankApproved)
			}
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && !bankApproved) {
				return OutboxRefusal(fmt.Sprintf("A required question carries a drafted answer you have not approved: %q. Approve it in the answer bank first.", answer.Label)), nil
			}
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(banked) != strings.TrimSpace(answer.Answer) {
				return OutboxRefusal(fmt.Sprintf("You changed your answer to %q after this application was prepared. Prepare it again so it carries the answer you approved.", answer.Label)), nil
			}
		}
	}

	// The posting may have closed after the entry was composed.
	var reason string
	err = tx.QueryRow(ctx, `SELECT closed_reason FROM jobs WHERE id=$1 AND closed_at IS NOT NULL`, entry.JobID).Scan(&reason)
	if err == nil {
		return OutboxRefusal(fmt.Sprintf("This posting has closed: %s. It can no longer be applied to.", reason)), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	return "", nil
}

// CancelOutboxEntry drops an entry from the queue. A sent one is history and
// stays as it is, and one being sent can no longer be stopped.
func (s *Store) CancelOutboxEntry(ctx context.Context, userID, id string) (models.OutboxEntry, error) {
	current, err := s.OutboxEntry(ctx, userID, id)
	if err != nil {
		return models.OutboxEntry{}, err
	}
	if current.Status == models.OutboxSent {
		return models.OutboxEntry{}, OutboxRefusal("This application has already been sent.")
	}
	if current.Status == models.OutboxSending {
		return models.OutboxEntry{}, sendingRefusal
	}
	cancelled, err := scanOutbox(s.DB.QueryRow(ctx, `UPDATE application_outbox
		SET status='cancelled', updated_at=now()
		WHERE user_id=$1 AND id=$2 AND status NOT IN ('sent','sending') RETURNING `+outboxColumns, userID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		// Taken for sending between the read and the update.
		return models.OutboxEntry{}, sendingRefusal
	}
	if err != nil {
		return models.OutboxEntry{}, err
	}
	return s.withJob(ctx, userID, cancelled), nil
}

// withJob attaches the job an entry is for, since a queue entry is unreadable
// without it, and the summary of the CV it carries, as OutboxEntry reads them.
func (s *Store) withJob(ctx context.Context, userID string, entry models.OutboxEntry) models.OutboxEntry {
	if job, err := s.CatalogJob(ctx, entry.JobID); err == nil {
		entry.Job = &job
	}
	return s.withCV(ctx, userID, entry)
}

// withCV attaches the summary of the CV an entry carries, as the outbox lists
// read it. It runs after the write it reports on (a compose, an approval, a
// cancellation) has happened, so a failed lookup leaves the summary out and is
// logged, rather than turning that answer into an error.
func (s *Store) withCV(ctx context.Context, userID string, entry models.OutboxEntry) models.OutboxEntry {
	if entry.CVDocumentID == "" {
		return entry
	}
	var cv cvSummary
	err := s.DB.QueryRow(ctx, `SELECT `+outboxCVColumns+` FROM cv_documents c WHERE c.user_id=$1 AND c.id=$2`,
		userID, entry.CVDocumentID).Scan(cv.dest()...)
	switch {
	case err == nil:
		entry.CV = cv.summary()
	case !errors.Is(err, pgx.ErrNoRows):
		slog.Warn("could not read the CV summary of an outbox entry", "entry_id", entry.ID, "error", err)
	}
	// No row: the CV was deleted, or is not the user's, so no summary.
	return entry
}

func (s *Store) DeleteOutboxEntry(ctx context.Context, userID, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM application_outbox WHERE user_id=$1 AND id=$2`, userID, id)
	return err
}
