package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/models"
)

// Why an entry came back from sending without an answer from the employer.
// Both stop it from ever being sent again.
const (
	SendInterrupted     = "Sending was interrupted before anything reached the employer. Prepare the application again to send it, or apply on the employer's site."
	SendOutcomeUnknown  = "Sending was interrupted while the application was on its way to the employer, so it may have arrived. Check with them before applying again; JobPilot will not send it a second time."
	SendAttemptedBefore = "An earlier send of this application may have reached the employer, so JobPilot will not send it again. Check with them, and apply on their site if it did not arrive."
)

// ErrOutboxSending is returned when an entry is rebuilt while it is being sent.
var ErrOutboxSending = errors.New("this application is being sent right now and cannot be composed again")

// sendingRefusal answers an approval or a cancellation of an entry in flight.
const sendingRefusal = OutboxRefusal("This application is being sent right now.")

// OutboxClaim is one approved entry taken for sending. When Refused is set,
// the entry was not sendable any more and is already marked failed with it.
type OutboxClaim struct {
	UserID  string
	Entry   models.OutboxEntry
	Refused string
}

// ClaimOutboxEntry takes the oldest entry approved at least delay ago and
// marks it sending, so no other sender takes it. The approval is checked again
// first, as approving checked it: the CV, the reviewed answers, the posting,
// and the user's rules for the job may all have changed since. An entry that
// fails those checks, or that was posted once before, is marked failed instead.
// ok is false when nothing is due. onlyUser limits the claim to one user's
// entries, so a test never takes another's; the sender passes "".
func (s *Store) ClaimOutboxEntry(ctx context.Context, delay time.Duration, onlyUser string) (OutboxClaim, bool, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return OutboxClaim{}, false, err
	}
	defer tx.Rollback(ctx)

	var claim OutboxClaim
	var id string
	var attempted bool
	err = tx.QueryRow(ctx, `SELECT id, user_id, submit_attempted_at IS NOT NULL FROM application_outbox
		WHERE status='approved' AND approved_at <= now() - make_interval(secs => $1) AND ($2='' OR user_id::text=$2)
		ORDER BY approved_at LIMIT 1 FOR UPDATE SKIP LOCKED`, delay.Seconds(), onlyUser).Scan(&id, &claim.UserID, &attempted)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboxClaim{}, false, nil
	}
	if err != nil {
		return OutboxClaim{}, false, err
	}
	claim.Entry, err = scanOutbox(tx.QueryRow(ctx, `SELECT `+outboxColumns+` FROM application_outbox WHERE id=$1`, id))
	if err != nil {
		return OutboxClaim{}, false, err
	}

	refusal := OutboxRefusal("")
	if attempted {
		refusal = SendAttemptedBefore
	}
	if refusal == "" {
		var rejected bool
		err = tx.QueryRow(ctx, `SELECT rejected FROM job_matches WHERE user_id=$1 AND job_id=$2`, claim.UserID, claim.Entry.JobID).Scan(&rejected)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return OutboxClaim{}, false, err
		}
		if rejected {
			refusal = OutboxRefusal(ErrApplicationBlocked.Error())
		}
	}
	if refusal == "" {
		if refusal, err = approvalRefusal(ctx, tx, claim.UserID, claim.Entry); err != nil {
			return OutboxClaim{}, false, err
		}
	}

	status, message := models.OutboxSending, ""
	if refusal != "" {
		status, message, claim.Refused = models.OutboxFailed, string(refusal), string(refusal)
	}
	claim.Entry, err = scanOutbox(tx.QueryRow(ctx, `UPDATE application_outbox SET status=$2, error=$3, updated_at=now()
		WHERE id=$1 RETURNING `+outboxColumns, id, status, message))
	if err != nil {
		return OutboxClaim{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboxClaim{}, false, err
	}
	return claim, true, nil
}

// RecordSubmitAttempt notes, just before an entry is posted, that it may reach
// the employer from now on. It fails unless the entry is still being sent, so
// an entry already written off as interrupted is never posted after all.
func (s *Store) RecordSubmitAttempt(ctx context.Context, userID, id string) error {
	tag, err := s.DB.Exec(ctx, `UPDATE application_outbox SET submit_attempted_at=now(), updated_at=now()
		WHERE user_id=$1 AND id=$2 AND status='sending'`, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("the application is no longer being sent")
	}
	return nil
}

// FinishOutboxSend records how a send ended: sent, or failed with the reason
// the user reads. A sent entry's application is marked applied, unless it has
// already moved further along.
func (s *Store) FinishOutboxSend(ctx context.Context, userID, id string, sent bool, reason string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	status := models.OutboxFailed
	if sent {
		status, reason = models.OutboxSent, ""
	}
	var application *string
	err = tx.QueryRow(ctx, `UPDATE application_outbox SET status=$3, error=$4,
			sent_at=CASE WHEN $3='sent' THEN now() ELSE sent_at END, updated_at=now()
		WHERE user_id=$1 AND id=$2 AND status='sending' RETURNING application_id`,
		userID, id, status, reason).Scan(&application)
	if err != nil {
		return err
	}
	if sent && application != nil {
		var previous string
		err = tx.QueryRow(ctx, `SELECT status FROM applications WHERE id=$1 AND user_id=$2 FOR UPDATE`, *application, userID).Scan(&previous)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if previous == "draft" || previous == "ready" {
			if _, err = tx.Exec(ctx, `UPDATE applications SET status='applied', updated_at=now() WHERE id=$1 AND user_id=$2`, *application, userID); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO application_events(user_id,application_id,event_type,from_status,to_status,metadata)
				VALUES($1,$2,'status_changed',$3,'applied','{"via":"employer_form"}')`, userID, *application, previous); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// FailStaleSends writes off entries left sending for longer than after, which
// only a stopped or crashed sender leaves behind. One that was posted may have
// arrived, and says so.
func (s *Store) FailStaleSends(ctx context.Context, after time.Duration) (int64, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE application_outbox SET status='failed', updated_at=now(),
			error=CASE WHEN submit_attempted_at IS NULL THEN $2 ELSE $3 END
		WHERE status='sending' AND updated_at < now() - make_interval(secs => $1)`,
		after.Seconds(), SendInterrupted, SendOutcomeUnknown)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
