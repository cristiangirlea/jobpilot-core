package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"jobpilot/core/api/internal/models"
)

// Notification kinds that triggers on their own tables keep current
// (migrations/020_notifications.sql). Other kinds are opened and resolved by
// their own code through OpenNotification and ResolveNotification.
const (
	NotificationAnswer = "answer" // an answer bank entry waiting for approval
	NotificationCV     = "cv"     // an uploaded or tailored CV waiting for approval
	NotificationOutbox = "outbox" // a composed application to review or approve
)

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
	_, err = q.Exec(ctx, `SELECT notification_open($1,$2,$3,$4,$5)`, userID, kind, subjectID, title, raw)
	return err
}

// ResolveNotification records that the thing no longer waits. Resolving one
// that is not open does nothing.
func ResolveNotification(ctx context.Context, q execer, userID, kind, subjectID string) error {
	_, err := q.Exec(ctx, `SELECT notification_resolve($1,$2,$3)`, userID, kind, subjectID)
	return err
}

// Notifications returns a page of the user's notifications and the number
// still open. state "all" includes resolved rows, newest first; anything else
// returns open ones, oldest first, which is the order to ask the user about
// them. after is the id of the last item of the previous page; Next is set
// when another page may follow. Paging by position rather than offset keeps a
// walk correct while items resolve under it.
func (s *Store) Notifications(ctx context.Context, userID, state, after string, limit int) (models.NotificationFeed, error) {
	if err := s.openQuietApplications(ctx, userID); err != nil {
		return models.NotificationFeed{}, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	feed := models.NotificationFeed{Items: []models.Notification{}}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND resolved_at IS NULL`, userID).Scan(&feed.Open); err != nil {
		return models.NotificationFeed{}, err
	}
	query := `SELECT ` + notificationColumns + ` FROM notifications WHERE user_id=$1`
	order, cmp := ` ORDER BY created_at, id`, `>`
	if state == "all" {
		order, cmp = ` ORDER BY created_at DESC, id DESC`, `<`
	} else {
		query += ` AND resolved_at IS NULL`
	}
	args := []any{userID, limit}
	if after != "" {
		query += ` AND (created_at,id) ` + cmp + ` (SELECT created_at,id FROM notifications WHERE user_id=$1 AND id::text=$3)`
		args = append(args, after)
	}
	rows, err := s.DB.Query(ctx, query+order+` LIMIT $2`, args...)
	if err != nil {
		return models.NotificationFeed{}, err
	}
	defer rows.Close()
	for rows.Next() {
		n, scanErr := scanNotification(rows)
		if scanErr != nil {
			return models.NotificationFeed{}, scanErr
		}
		feed.Items = append(feed.Items, n)
	}
	if err := rows.Err(); err != nil {
		return models.NotificationFeed{}, err
	}
	if len(feed.Items) == limit {
		feed.Next = feed.Items[len(feed.Items)-1].ID
	}
	return feed, nil
}

func (s *Store) openQuietApplications(ctx context.Context, userID string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT a.id FROM applications a
        LEFT JOIN discovery_settings d ON d.user_id=a.user_id
        WHERE a.user_id=$1 AND a.status IN ('applied','interview')
        AND a.status_changed_at <= now()-make_interval(days=>COALESCE(d.quiet_application_days,10))
        AND a.status_changed_at >= now()-interval '60 days'
        AND NOT EXISTS (SELECT 1 FROM notifications n WHERE n.user_id=a.user_id AND n.kind='quiet_application'
            AND n.subject_id=a.id::text AND n.created_at>=a.status_changed_at)
        ORDER BY a.id FOR UPDATE OF a`, userID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		_, err = tx.Exec(ctx, `SELECT notification_open(a.user_id,'quiet_application',a.id::text,
            'Follow up on the application to '||j.title||' at '||j.company,
            jsonb_build_object('application_id',a.id,'status',a.status,'href','/applications/'||a.id),
            a.status_changed_at+make_interval(days=>COALESCE(d.quiet_application_days,10)))
            FROM applications a JOIN jobs j ON j.id=a.job_id LEFT JOIN discovery_settings d ON d.user_id=a.user_id
            WHERE a.user_id=$1 AND a.id=$2 AND a.status IN ('applied','interview')
            AND NOT EXISTS (SELECT 1 FROM notifications n WHERE n.user_id=a.user_id AND n.kind='quiet_application'
                AND n.subject_id=a.id::text AND n.created_at>=a.status_changed_at)`, userID, id)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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
