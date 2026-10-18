package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestQuietMigrationBackfillPostgres(t *testing.T) {
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a throwaway database")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := tx.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	schema := pgx.Identifier{fmt.Sprintf("quiet_migration_%d", time.Now().UnixNano())}.Sanitize()
	exec("CREATE SCHEMA " + schema)
	exec("SET LOCAL search_path TO " + schema)
	files, err := filepath.Glob("../../../../migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 20 {
		t.Fatal("missing migration fixtures")
	}
	for _, file := range files {
		if filepath.Base(file) >= "026_quiet_applications.sql" {
			break
		}
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		exec(string(raw))
	}
	var uid, jid string
	if err = tx.QueryRow(ctx, `INSERT INTO users(email,password_hash) VALUES('migration@example.test','fake') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO discovery_settings(user_id) VALUES($1)`, uid)
	if err = tx.QueryRow(ctx, `INSERT INTO jobs(user_id,title,company,company_normalized,description) VALUES($1,'Engineer','Migration Test','migration test','Build') RETURNING id`, uid).Scan(&jid); err != nil {
		t.Fatal(err)
	}
	// Latest status event wins over both an older status and a newer notes event.
	var withEvent, withoutEvent string
	for _, dest := range []*string{&withEvent, &withoutEvent} {
		if err = tx.QueryRow(ctx, `INSERT INTO jobs(user_id,title,company,company_normalized,description) VALUES($1,'Engineer','Migration Test','migration test','Build') RETURNING id`, uid).Scan(&jid); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `INSERT INTO applications(user_id,job_id,status,updated_at) VALUES($1,$2,'applied','2026-09-20T00:00:00Z') RETURNING id`, uid, jid).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO application_events(user_id,application_id,event_type,created_at) VALUES
	 ($1,$2,'status_changed','2026-09-01T00:00:00Z'),($1,$2,'status_changed','2026-09-10T00:00:00Z'),
	 ($1,$2,'notes_updated','2026-09-19T00:00:00Z'),($1,$3,'notes_updated','2026-09-19T00:00:00Z')`, uid, withEvent, withoutEvent)
	raw, err := os.ReadFile("../../../../migrations/026_quiet_applications.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(raw))
	for _, tc := range []struct{ id, want string }{{withEvent, "2026-09-10"}, {withoutEvent, "2026-09-20"}} {
		var got time.Time
		if err = tx.QueryRow(ctx, `SELECT status_changed_at FROM applications WHERE id=$1`, tc.id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(got.UTC().Format(time.RFC3339), tc.want) {
			t.Errorf("backfill %s, want %s", got, tc.want)
		}
	}
	// A rerun must preserve a status clock already set by live activity.
	exec(`UPDATE applications SET status_changed_at='2026-09-25T00:00:00Z' WHERE id=$1`, withEvent)
	exec(string(raw))
	var preserved time.Time
	if err = tx.QueryRow(ctx, `SELECT status_changed_at FROM applications WHERE id=$1`, withEvent).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if preserved.UTC().Format("2006-01-02") != "2026-09-25" {
		t.Fatal("rerun reset clock", preserved)
	}
	var days int
	if err = tx.QueryRow(ctx, `SELECT quiet_application_days FROM discovery_settings WHERE user_id=$1`, uid).Scan(&days); err != nil || days != 10 {
		t.Fatal("migration default", days, err)
	}
}
