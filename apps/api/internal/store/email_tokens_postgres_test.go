package store

import (
	"context"
	"os"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
)

func emailTokenStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated database to run this")
	}
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return New(pool)
}

// Links asked for at the same moment still leave exactly one that works, and
// using links for one user at once cannot deadlock with issuing them.
func TestConcurrentLinksLeaveOneLiveAgainstPostgres(t *testing.T) {
	s := emailTokenStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "links-"+time.Now().Format("20060102150405.000000")+"@example.test", "x")
	if err != nil {
		t.Fatal(err)
	}
	tokens := make(chan string, 6)
	for range 6 {
		go func() {
			token, err := s.CreateEmailToken(ctx, u.ID, PurposeReset, time.Hour)
			if err != nil {
				t.Error(err)
			}
			tokens <- token
		}()
	}
	all := []string{}
	for range 6 {
		all = append(all, <-tokens)
	}
	var live int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM email_tokens WHERE user_id=$1 AND purpose='reset' AND used_at IS NULL`, u.ID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("exactly one link should stay live, %d did", live)
	}

	// Using every link at once while more are issued: one reset at most
	// succeeds, and nothing deadlocks (Postgres would abort one side).
	resets := make(chan error, len(all))
	issued := make(chan error, 3)
	for _, token := range all {
		go func() { resets <- s.ResetPassword(ctx, token, "new hash") }()
	}
	for range 3 {
		go func() {
			_, err := s.CreateEmailToken(ctx, u.ID, PurposeReset, time.Hour)
			issued <- err
		}()
	}
	succeeded := 0
	for range all {
		switch err := <-resets; err {
		case nil:
			succeeded++
		case ErrLinkInvalid:
		default:
			t.Fatalf("unexpected error, possibly a deadlock: %v", err)
		}
	}
	for range 3 {
		if err := <-issued; err != nil {
			t.Fatalf("issuing a link failed, possibly a deadlock: %v", err)
		}
	}
	if succeeded > 1 {
		t.Fatalf("only the one live link may reset the password, %d did", succeeded)
	}
}

// While email is on, scheduled discovery leaves unverified accounts alone
// and their schedule waits; verifying lets it run.
func TestDueDiscoverySkipsUnverifiedAccountsAgainstPostgres(t *testing.T) {
	s := emailTokenStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "due-"+time.Now().Format("20060102150405.000000")+"@example.test", "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO discovery_settings(user_id,enabled,interval_minutes,next_run_at) VALUES($1,true,360,now()-interval '1 minute') ON CONFLICT(user_id) DO UPDATE SET enabled=true,next_run_at=now()-interval '1 minute'`, u.ID); err != nil {
		t.Fatal(err)
	}
	claimed := func(verifiedOnly bool) bool {
		ids, err := s.ClaimDueDiscoveryUsers(ctx, 1000, verifiedOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if id == u.ID {
				return true
			}
		}
		return false
	}
	if claimed(true) {
		t.Fatal("an unverified account's discovery must not run while email is on")
	}
	if _, err := s.DB.Exec(ctx, `UPDATE users SET email_verified_at=now() WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if !claimed(true) {
		t.Fatal("once verified, the waiting schedule should run")
	}
}
