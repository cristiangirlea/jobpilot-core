package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"jobpilot/core/api/internal/models"
)

// Notification kinds this package derives from the tables that hold the
// waiting things. Other kinds are opened and resolved by their own code
// through OpenNotification and ResolveNotification.
const (
	NotificationAnswer = "answer" // an answer bank entry waiting for approval
	NotificationCV     = "cv"     // an uploaded or tailored CV waiting for approval
	NotificationOutbox = "outbox" // a composed application to review or approve
)

// waitingSources select, for user $1, every item of a derived kind that waits
// for the user now: its kind, its id, a title in the user's terms, the detail
// a reader needs to show it, and since when it has waited. A new derived kind
// is one more entry here; the table takes any kind.
var waitingSources = []struct{ kind, query string }{
	{NotificationAnswer, `SELECT 'answer' AS kind, id::text AS subject_id,
		'Approve the answer to: ' || question_text AS title,
		jsonb_build_object('question', question_text, 'answer', answer, 'role_family', role_family, 'source', source, 'href', '/answers') AS detail,
		updated_at AS since
		FROM answer_bank WHERE user_id=$1 AND NOT approved`},
	{NotificationCV, `SELECT 'cv', id::text,
		'Approve the ' || CASE WHEN kind='tailored' THEN 'tailored CV' ELSE 'CV' END || ': ' || COALESCE(NULLIF(label,''), NULLIF(filename,''), 'untitled'),
		jsonb_build_object('cv_kind', kind, 'label', label, 'filename', filename, 'href', '/cv'),
		updated_at
		FROM cv_documents WHERE user_id=$1 AND NOT approved`},
	{NotificationOutbox, `SELECT 'outbox', o.id::text,
		CASE WHEN o.status='ready' THEN 'Approve' ELSE 'Review' END || ' the application to ' || j.title || ' at ' || j.company,
		jsonb_build_object('status', o.status, 'job_id', o.job_id, 'job_title', j.title, 'company', j.company, 'blockers', o.blockers, 'href', '/outbox'),
		o.updated_at
		FROM application_outbox o JOIN jobs j ON j.id=o.job_id
		WHERE o.user_id=$1 AND o.status IN ('needs_review','ready')`},
}

func waitingQuery() (string, []string) {
	parts := make([]string, len(waitingSources))
	kinds := make([]string, len(waitingSources))
	for i, source := range waitingSources {
		parts[i] = source.query
		kinds[i] = source.kind
	}
	return strings.Join(parts, "\nUNION ALL\n"), kinds
}

const notificationColumns = `id,kind,subject_id,title,detail,created_at,updated_at,resolved_at`

// execer is a pool or a transaction, so a change and its notification can
// commit together.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// OpenNotification records that a thing of this kind now waits for the user.
// Opening one that is already open refreshes its title and detail.
func OpenNotification(ctx context.Context, q execer, userID, kind, subjectID, title string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO notifications (user_id,kind,subject_id,title,detail) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (user_id,kind,subject_id) WHERE resolved_at IS NULL
		DO UPDATE SET title=EXCLUDED.title, detail=EXCLUDED.detail, updated_at=now()`, userID, kind, subjectID, title, raw)
	return err
}

// ResolveNotification records that the thing no longer waits. Resolving one
// that is not open does nothing.
func ResolveNotification(ctx context.Context, q execer, userID, kind, subjectID string) error {
	_, err := q.Exec(ctx, `UPDATE notifications SET resolved_at=now(), updated_at=now()
		WHERE user_id=$1 AND kind=$2 AND subject_id=$3 AND resolved_at IS NULL`, userID, kind, subjectID)
	return err
}

// Notifications brings the user's derived notifications up to date and
// returns them with the number still open. The derived kinds are reconciled
// against their tables on every read rather than hooked into each write, so
// no path that changes an approval or an outbox status can leave the list
// wrong. state "all" includes resolved rows; anything else returns open ones,
// oldest first, which is the order to ask the user about them.
func (s *Store) Notifications(ctx context.Context, userID, state string, limit int) (models.NotificationFeed, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return models.NotificationFeed{}, err
	}
	defer tx.Rollback(ctx)
	// One reconciliation per user at a time, so two readers cannot reopen a
	// row the other has just resolved from an older snapshot.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('notifications'), hashtext($1::text))`, userID); err != nil {
		return models.NotificationFeed{}, err
	}
	waiting, kinds := waitingQuery()
	if _, err := tx.Exec(ctx, `INSERT INTO notifications (user_id,kind,subject_id,title,detail,created_at,updated_at)
		SELECT $1, w.kind, w.subject_id, w.title, w.detail, w.since, w.since FROM (`+waiting+`) w
		ON CONFLICT (user_id,kind,subject_id) WHERE resolved_at IS NULL DO UPDATE
		SET title=EXCLUDED.title, detail=EXCLUDED.detail, updated_at=now()
		WHERE notifications.title IS DISTINCT FROM EXCLUDED.title OR notifications.detail IS DISTINCT FROM EXCLUDED.detail`, userID); err != nil {
		return models.NotificationFeed{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE notifications n SET resolved_at=now(), updated_at=now()
		WHERE n.user_id=$1 AND n.resolved_at IS NULL AND n.kind = ANY($2)
		AND NOT EXISTS (SELECT 1 FROM (`+waiting+`) w WHERE w.kind=n.kind AND w.subject_id=n.subject_id)`, userID, kinds); err != nil {
		return models.NotificationFeed{}, err
	}
	feed := models.NotificationFeed{Items: []models.Notification{}}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND resolved_at IS NULL`, userID).Scan(&feed.Open); err != nil {
		return models.NotificationFeed{}, err
	}
	query := `SELECT ` + notificationColumns + ` FROM notifications WHERE user_id=$1`
	if state != "all" {
		query += ` AND resolved_at IS NULL`
	}
	rows, err := tx.Query(ctx, query+` ORDER BY resolved_at IS NOT NULL, CASE WHEN resolved_at IS NULL THEN created_at END, resolved_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return models.NotificationFeed{}, err
	}
	for rows.Next() {
		n, scanErr := scanNotification(rows)
		if scanErr != nil {
			rows.Close()
			return models.NotificationFeed{}, scanErr
		}
		feed.Items = append(feed.Items, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return models.NotificationFeed{}, err
	}
	return feed, tx.Commit(ctx)
}

func scanNotification(row pgx.Row) (models.Notification, error) {
	var n models.Notification
	var detail []byte
	err := row.Scan(&n.ID, &n.Kind, &n.SubjectID, &n.Title, &detail, &n.CreatedAt, &n.UpdatedAt, &n.ResolvedAt)
	if err != nil {
		return n, err
	}
	n.Detail = map[string]any{}
	if len(detail) > 0 {
		_ = json.Unmarshal(detail, &n.Detail)
	}
	return n, nil
}
