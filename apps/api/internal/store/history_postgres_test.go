package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"jobpilot/core/api/internal/company"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
	"os"
	"sync"
	"testing"
	"time"
)

func TestExternalHistoryPostgres(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated throwaway database")
	}
	ctx := context.Background()
	pool, e := database.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	s := New(pool)
	run := fmt.Sprint(time.Now().UnixNano())
	var users []models.User
	for _, name := range []string{"ana", "bob", "carol"} {
		u, e := s.CreateUser(ctx, "history-"+name+"-"+run+"@example.test", "fake")
		if e != nil {
			t.Fatal(e)
		}
		users = append(users, u)
	}
	ana, bob, carol := users[0], users[1], users[2]
	defer pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2,$3)`, ana.ID, bob.ID, carol.ID)
	key := []byte("history-store-test-key")
	imp := func(user string, in models.HistoryImport) (models.HistoryPreview, error) {
		return s.ImportExternalHistory(ctx, user, in, key)
	}
	// all walks every page, so it also checks that the cursor neither repeats nor skips.
	all := func(user string) []models.ExternalApplication {
		t.Helper()
		var out []models.ExternalApplication
		seen := map[string]bool{}
		for after := ""; ; {
			page, e := s.ExternalHistory(ctx, user, after, 50)
			if e != nil {
				t.Fatal(e)
			}
			for _, x := range page.Items {
				if seen[x.ID] {
					t.Fatal("a page repeated a record", x.ID)
				}
				seen[x.ID] = true
			}
			out = append(out, page.Items...)
			if page.Next == "" {
				return out
			}
			after = page.Next
		}
	}
	bulk := func(user string, like models.ExternalApplicationInput, n int) {
		t.Helper()
		raw, _ := json.Marshal(like)
		_, e := pool.Exec(ctx, `INSERT INTO external_application_history(user_id,external_key,employer_normalized,requisition,canonical_url,record) SELECT $1,'bulk:'||n,$2,'BULK-'||n,'',$3::jsonb || jsonb_build_object('external_key','bulk:'||n,'requisition','BULK-'||n,'canonical_url','') FROM generate_series(1,$4::int) n`, user, company.Normalize(like.Employer), raw, n)
		if e != nil {
			t.Fatal(e)
		}
	}
	counts := func() string {
		var v string
		e := pool.QueryRow(ctx, `SELECT json_build_array((SELECT count(*) FROM jobs j WHERE j.user_id=$1 OR EXISTS (SELECT 1 FROM job_matches m WHERE m.job_id=j.id AND m.user_id=$1)),(SELECT count(*) FROM applications WHERE user_id=$1),(SELECT count(*) FROM application_outbox WHERE user_id=$1),(SELECT count(*) FROM outbound_clicks WHERE user_id=$1),(SELECT count(*) FROM discovery_runs WHERE user_id=$1))::text`, ana.ID).Scan(&v)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	before := counts()
	in := models.HistoryImport{Mode: "preview", Record: historyFixture()}
	preview, e := imp(ana.ID, in)
	if e != nil || preview.Action != "create" {
		t.Fatalf("preview %+v %v", preview, e)
	}
	if got := all(ana.ID); len(got) != 0 {
		t.Fatal("preview wrote history", got)
	}
	in.Mode = "commit"
	if _, e = imp(ana.ID, in); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("commit without preview", e)
	}
	// A digest anyone can work out from the request is not a preview token.
	unsigned, _ := json.Marshal(struct {
		User       string
		Record     models.ExternalApplicationInput
		Action     string
		Candidates []models.HistoryCandidate
	}{ana.ID, in.Record, "create", []models.HistoryCandidate{}})
	sum := sha256.Sum256(unsigned)
	forged := in
	forged.PreviewToken = hex.EncodeToString(sum[:])
	if _, e = imp(ana.ID, forged); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("accepted an unsigned token", e)
	}
	forged.PreviewToken = preview.PreviewToken
	if _, e = s.ImportExternalHistory(ctx, ana.ID, forged, []byte("another server's key")); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("accepted a token signed with another key", e)
	}
	if _, e = s.ImportExternalHistory(ctx, ana.ID, forged, nil); e == nil {
		t.Fatal("committed without a signing key")
	}
	// The token covers the record: an edit after the preview needs a new one.
	edited := in
	edited.PreviewToken = preview.PreviewToken
	edited.Record.Title = "Edited after the preview"
	if _, e = imp(ana.ID, edited); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("committed a record that was not previewed", e)
	}
	if got := all(ana.ID); len(got) != 0 {
		t.Fatal("a refused commit wrote history", got)
	}
	in.PreviewToken = preview.PreviewToken
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := imp(ana.ID, in); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("concurrent retry", e)
		}
	}
	if got := all(ana.ID); len(got) != 1 || got[0].ExternalApplicationInput != in.Record {
		t.Fatal("lost original facts", got)
	}
	retry, e := imp(ana.ID, in)
	if e != nil || retry.Action != "unchanged" {
		t.Fatal(retry, e)
	}
	changed := in
	changed.Record.EvidenceRef = "different:evidence"
	if _, e = imp(ana.ID, changed); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("overwrote evidence", e)
	}
	changed = in
	changed.Record.Status = "applied"
	changed.Record.EvidenceKind = "application_receipt"
	changed.Record.OutcomeAt = ""
	if _, e = imp(ana.ID, changed); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("overwrote rejection", e)
	}
	// A second source for the exact same requisition remains an exact conflict.
	alias := in
	alias.Mode = "preview"
	alias.Record.ExternalKey = "another-source"
	p, e := imp(ana.ID, alias)
	if e != nil || p.Action != "conflict" || p.Candidates[0].Kind != "exact" {
		t.Fatal(p, e)
	}
	alias.Mode = "commit"
	alias.PreviewToken = p.PreviewToken
	alias.AllowPossibleDuplicates = true
	if _, e = imp(ana.ID, alias); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("bypassed exact duplicate", e)
	}
	// Each identity is exact on its own: the URL under another employer, and
	// the employer's requisition under another URL or none.
	for name, change := range map[string]func(*models.ExternalApplicationInput){
		"same URL, another employer": func(x *models.ExternalApplicationInput) {
			x.Employer, x.Requisition = "Unrelated Example GmbH", ""
		},
		"same requisition, another URL": func(x *models.ExternalApplicationInput) { x.CanonicalURL = "https://example.test/elsewhere" },
		"same requisition, no URL":      func(x *models.ExternalApplicationInput) { x.CanonicalURL = "" },
	} {
		same := models.HistoryImport{Mode: "preview", Record: in.Record}
		same.Record.ExternalKey = "identity:" + name
		change(&same.Record)
		p, e = imp(ana.ID, same)
		if e != nil || p.Action != "conflict" || len(p.Candidates) != 1 || p.Candidates[0].Kind != "exact" {
			t.Fatal(name, p, e)
		}
	}
	// Different requisitions at the same employer are possible, never auto-merged.
	third := in
	third.Mode = "preview"
	third.Record.ExternalKey = "third-role"
	third.Record.Requisition = "REQ-3"
	third.Record.CanonicalURL = ""
	early, e := imp(ana.ID, third)
	if e != nil || early.Action != "review" || len(early.Candidates) != 1 {
		t.Fatal(early, e)
	}
	related := in
	related.Mode = "preview"
	related.Record.ExternalKey = "other-role"
	related.Record.Requisition = "REQ-2"
	related.Record.CanonicalURL = "https://example.test/jobs/2"
	related.Record.AppliedAt = ""
	related.Record.OutcomeAt = ""
	p, e = imp(ana.ID, related)
	if e != nil || p.Action != "review" || p.Candidates[0].Kind != "possible" {
		t.Fatal(p, e)
	}
	related.Mode = "commit"
	related.PreviewToken = p.PreviewToken
	if _, e = imp(ana.ID, related); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("possible duplicate not reviewed", e)
	}
	related.AllowPossibleDuplicates = true
	if _, e = imp(ana.ID, related); e != nil {
		t.Fatal(e)
	}
	// The token covers the candidates: the same record, previewed before the
	// second role existed, cannot be committed with that older preview.
	third.Mode = "commit"
	third.PreviewToken = early.PreviewToken
	third.AllowPossibleDuplicates = true
	if _, e = imp(ana.ID, third); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("accepted a preview older than its candidates", e)
	}
	third.Mode = "preview"
	p, e = imp(ana.ID, third)
	if e != nil || p.Action != "review" || len(p.Candidates) != 2 || p.PreviewToken == early.PreviewToken {
		t.Fatal(p, e)
	}
	page, e := s.ExternalHistory(ctx, ana.ID, "", 1)
	if e != nil || page.Next == "" {
		t.Fatal(page, e)
	}
	second, e := s.ExternalHistory(ctx, ana.ID, page.Next, 1)
	if e != nil || len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID || second.Next != "" {
		t.Fatal(second, e)
	}
	if _, e := s.ExternalHistory(ctx, ana.ID, "not-a-cursor", 50); !errors.Is(e, ErrHistoryCursor) {
		t.Fatal("unknown cursor", e)
	}
	if got := all(bob.ID); len(got) != 0 {
		t.Fatal("tenant leak", got)
	}
	if other, e := s.ExternalHistory(ctx, bob.ID, page.Next, 50); e != nil || len(other.Items) != 0 {
		t.Fatal("another tenant's cursor", other, e)
	}
	in.Mode = "preview"
	p, e = imp(bob.ID, in)
	if e != nil || p.Action != "create" || len(p.Candidates) != 0 || p.PreviewToken == preview.PreviewToken {
		t.Fatal("cross-tenant preview", p, e)
	}
	in.Mode = "commit"
	in.PreviewToken = preview.PreviewToken
	if _, e = imp(bob.ID, in); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("cross-tenant token", e)
	}
	in.PreviewToken = p.PreviewToken
	if _, e = imp(bob.ID, in); e != nil {
		t.Fatal("key should be tenant-scoped", e)
	}
	// Two sources previewed the same posting before either was saved: one wins.
	race := []models.HistoryImport{{Record: historyFixture()}, {Record: historyFixture()}}
	for i := range race {
		race[i].Record.ExternalKey = fmt.Sprint("race:", i)
		race[i].Record.Employer = fmt.Sprint("Race Example ", i, " Ltd")
		race[i].Record.Requisition = ""
		race[i].Mode = "preview"
		p, e = imp(carol.ID, race[i])
		if e != nil || p.Action != "create" {
			t.Fatal(p, e)
		}
		race[i].Mode, race[i].PreviewToken = "commit", p.PreviewToken
	}
	errs = make(chan error, len(race))
	for _, r := range race {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := imp(carol.ID, r); errs <- e }()
	}
	wg.Wait()
	close(errs)
	saved, refused := 0, 0
	for e := range errs {
		if e == nil {
			saved++
		} else if errors.Is(e, ErrHistoryConflict) {
			refused++
		} else {
			t.Fatal(e)
		}
	}
	won := all(carol.ID)
	if saved != 1 || refused != 1 || len(won) != 1 {
		t.Fatal("concurrent sources for one posting", saved, refused, won)
	}
	// One hundred candidates can still be reviewed and acknowledged.
	bulk(carol.ID, won[0].ExternalApplicationInput, 99)
	edge := models.HistoryImport{Mode: "preview", Record: won[0].ExternalApplicationInput}
	edge.Record.ExternalKey, edge.Record.Requisition, edge.Record.CanonicalURL = "edge:100", "EDGE", ""
	p, e = imp(carol.ID, edge)
	if e != nil || p.Action != "review" || len(p.Candidates) != 100 {
		t.Fatal(p.Action, len(p.Candidates), e)
	}
	edge.Mode, edge.PreviewToken = "commit", p.PreviewToken
	if _, e = imp(carol.ID, edge); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("one hundred candidates saved without acknowledgement", e)
	}
	edge.AllowPossibleDuplicates = true
	if _, e = imp(carol.ID, edge); e != nil {
		t.Fatal("one hundred reviewed candidates refused", e)
	}
	// The candidate cap must not break retries of an existing key.
	bulk(ana.ID, in.Record, 101)
	in.PreviewToken = preview.PreviewToken
	retry, e = imp(ana.ID, in)
	if e != nil || retry.Action != "unchanged" {
		t.Fatal("bounded candidates broke idempotency", retry, e)
	}
	p, e = imp(ana.ID, third)
	if e != nil || p.Action != "review" {
		t.Fatal(p, e)
	}
	third.Mode = "commit"
	third.PreviewToken = p.PreviewToken
	if _, e = imp(ana.ID, third); !errors.Is(e, ErrHistoryConflict) {
		t.Fatal("accepted truncated candidate review", e)
	}
	if got := all(ana.ID); len(got) != 103 {
		t.Fatal("pages lost or added records", len(got))
	}
	if got := counts(); got != before {
		t.Fatalf("history changed live tables: %s -> %s", before, got)
	}
}
