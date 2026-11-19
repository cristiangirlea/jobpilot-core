package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/models"
)

func TestExtractionsAgainstPostgres(t *testing.T) {
	s := emailTokenStore(t)
	ctx := context.Background()
	// Rolled back, so no other package's test ever scores or lists these jobs.
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	const title = "Entwickler für Prüfungen"
	const description = "Große Geräte prüfen.\nMaßnahmen planen."
	insert := func(created string, discovered, closed bool) string {
		t.Helper()
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO jobs (title, company, company_normalized, description, created_at, discovered_at, closed_at)
			VALUES ($1, 'Example GmbH', 'example gmbh', $2, $3::text::timestamptz,
				CASE WHEN $4 THEN now() END, CASE WHEN $5 THEN now() END)
			RETURNING id`, title, description, created, discovered, closed).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	open := insert("2200-06-01T00:00:00Z", true, false)
	older := insert("2200-01-01T00:00:00Z", true, false)
	closed := insert("2199-01-01T00:00:00Z", true, true)
	pasted := insert("2199-01-01T00:00:00Z", false, false)
	model := "store-extraction-" + open
	const prompt = extraction.PromptVersion
	hash := extraction.Hash(title, description)

	pending := func(limit int) []models.PendingExtraction {
		t.Helper()
		jobs, err := pendingExtractions(ctx, tx, model, prompt, limit)
		if err != nil {
			t.Fatal(err)
		}
		var own []models.PendingExtraction
		for _, job := range jobs {
			switch job.ID {
			case open, older, closed, pasted:
				own = append(own, job)
			}
		}
		return own
	}
	if got := pending(1); len(got) != 1 || got[0].ID != open {
		t.Fatalf("pending with limit 1 = %+v, want the newest job", got)
	}
	want := []models.PendingExtraction{
		{ID: open, Title: title, Description: description, TextHash: hash},
		{ID: older, Title: title, Description: description, TextHash: hash},
	}
	if got := pending(200); !slices.Equal(got, want) {
		t.Fatalf("pending = %+v, want %+v", got, want)
	}
	if gotTitle, gotDescription, err := extractionSource(ctx, tx, closed); err != nil || gotTitle != title || gotDescription != description {
		t.Fatalf("closed discovered source = %q, %q, %v", gotTitle, gotDescription, err)
	}
	if _, _, err := extractionSource(ctx, tx, pasted); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("pasted source error = %v, want not found", err)
	}

	save := func(id, model, prompt, hash, status string, wantSaved bool) {
		t.Helper()
		x := models.JobExtraction{JobID: id, Model: model, PromptVersion: prompt, TextHash: hash, Status: status}
		if status == "ok" {
			x.Extraction = &models.Extraction{}
		} else {
			x.Reason = "not_json"
		}
		saved, err := saveExtraction(ctx, tx, x)
		if err != nil || saved != wantSaved {
			t.Fatalf("SaveExtraction(%s) = %v, %v, want %v", id, saved, err, wantSaved)
		}
	}
	count := func(id string) int {
		t.Helper()
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM job_extractions WHERE job_id=$1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	save(open, model, prompt, extraction.Hash(title, "Old text"), "ok", false)
	save(pasted, model, prompt, hash, "ok", false)
	if count(open) != 0 || count(pasted) != 0 {
		t.Fatal("a stale or private extraction was written")
	}
	save(open, model+"-other", prompt, hash, "ok", true)
	save(older, model, prompt+"-other", hash, "ok", true)
	if got := pending(200); !slices.Equal(got, want) {
		t.Fatalf("other model or prompt hid a job: %+v", got)
	}
	save(open, model, prompt, hash, "ok", true)
	if got := pending(200); !slices.Equal(got, want[1:]) {
		t.Fatalf("pending after accepted answer = %+v, want the older job", got)
	}
	save(older, model, prompt, hash, "rejected", true)
	if got := pending(200); len(got) != 0 {
		t.Fatalf("pending after rejected answer = %+v", got)
	}

	const edited = description + "\nGo-Kenntnisse erforderlich."
	if _, err := tx.Exec(ctx, `UPDATE jobs SET description=$2 WHERE id=$1`, open, edited); err != nil {
		t.Fatal(err)
	}
	newHash := extraction.Hash(title, edited)
	want[0].Description, want[0].TextHash = edited, newHash
	if got := pending(200); !slices.Equal(got, want[:1]) {
		t.Fatalf("pending after edit = %+v, want the edited job", got)
	}
	save(open, model, prompt, newHash, "rejected", true)
	var storedHash, status, reason string
	if err := tx.QueryRow(ctx, `SELECT text_hash, status, reason FROM job_extractions
		WHERE job_id=$1 AND model=$2 AND prompt_version=$3`, open, model, prompt).Scan(&storedHash, &status, &reason); err != nil {
		t.Fatal(err)
	}
	if count(open) != 2 || storedHash != newHash || status != "rejected" || reason != "not_json" {
		t.Fatalf("replacement has hash %q, status %q, reason %q, or left an extra row", storedHash, status, reason)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, open); err != nil {
		t.Fatal(err)
	}
	save(open, model, prompt, newHash, "ok", false)
	if count(open) != 0 {
		t.Fatal("deleting the job did not remove its extractions")
	}
}

// An answer for text that changes while it is being stored is refused, not stored.
func TestSaveExtractionWaitsForAConcurrentEditAgainstPostgres(t *testing.T) {
	s := emailTokenStore(t)
	ctx := context.Background()
	const title, description = "Backend Engineer", "Go required."
	var id string
	// Closed, so no other package's test scores or lists it.
	err := s.DB.QueryRow(ctx, `INSERT INTO jobs (title, company, company_normalized, description, discovered_at, closed_at)
		VALUES ($1, 'Example GmbH', 'example gmbh', $2, now(), now()) RETURNING id`, title, description).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.DB.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, id); err != nil {
			t.Error(err)
		}
	})
	x := models.JobExtraction{JobID: id, Model: "store-extraction-race-" + id, PromptVersion: extraction.PromptVersion,
		TextHash: extraction.Hash(title, description), Status: "ok", Extraction: &models.Extraction{}}

	edit, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = edit.Rollback(ctx) }()
	if _, err := edit.Exec(ctx, `UPDATE jobs SET description=$2 WHERE id=$1`, id, description+" Rust required."); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		saved bool
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		saved, err := s.SaveExtraction(ctx, x)
		done <- outcome{saved, err}
	}()
	var editor int
	if err := edit.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&editor); err != nil {
		t.Fatal(err)
	}
	// Commit only once the save is waiting for the edit; a save that did not wait answers first.
	var got outcome
	answered := false
	for deadline := time.Now().Add(20 * time.Second); !answered; {
		var waiting int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, editor).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case got = <-done:
			answered = true
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the save neither waited for the edit nor answered")
		}
	}
	if err := edit.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if !answered {
		got = <-done
	}
	if got.err != nil || got.saved {
		t.Fatalf("SaveExtraction during an edit = %v, %v, want refused", got.saved, got.err)
	}
	var n int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM job_extractions WHERE job_id=$1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("an answer for the old text was stored: %d rows, %v", n, err)
	}
}
