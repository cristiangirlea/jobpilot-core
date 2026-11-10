package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/company"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

func TestApplicationHistoryPolicyPostgres(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated throwaway database")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := New(pool)
	run := fmt.Sprint(time.Now().UnixNano())
	sequence := 0
	user := func(t *testing.T) string {
		t.Helper()
		sequence++
		u, err := s.CreateUser(ctx, fmt.Sprintf("history-policy-%s-%d@example.test", run, sequence), "fake")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, u.ID); err != nil {
				t.Error(err)
			}
		})
		return u.ID
	}
	job := func(t *testing.T, u, employer, title, url string) string {
		t.Helper()
		sequence++
		key := fmt.Sprintf("history-policy-%s-%d", run, sequence)
		var id string
		err := pool.QueryRow(ctx, `INSERT INTO jobs(user_id,title,company,company_normalized,description,apply_url,employer_url,fingerprint,slug) VALUES($1,$2,$3,$4,'Build services.',$5,$5,$6,$6) RETURNING id`, u, title, employer, company.Normalize(employer), url, key).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, id); err != nil {
				t.Error(err)
			}
		})
		if _, err := pool.Exec(ctx, `INSERT INTO job_matches(user_id,job_id,rejected,score) VALUES($1,$2,false,80)`, u, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	history := func(t *testing.T, u, employer, title, url, date, status, evidence string) {
		t.Helper()
		sequence++
		in := models.HistoryImport{Mode: "preview", AllowPossibleDuplicates: true, Record: models.ExternalApplicationInput{
			ExternalKey: fmt.Sprintf("policy:%s:%d", run, sequence), Employer: employer, Title: title, CanonicalURL: url,
			AppliedAt: date, Status: status, Source: "Fixture ledger", EvidenceKind: evidence, EvidenceRef: fmt.Sprintf("fixture:%d", sequence),
		}}
		key := []byte("history-policy-test-key")
		preview, err := s.ImportExternalHistory(ctx, u, in, key)
		if err != nil {
			t.Fatal(err)
		}
		in.Mode, in.PreviewToken = "commit", preview.PreviewToken
		if _, err := s.ImportExternalHistory(ctx, u, in, key); err != nil {
			t.Fatal(err)
		}
	}
	policy := func(t *testing.T, u, mode, lookback, since string) models.FilterPreset {
		t.Helper()
		x, err := s.SaveFilter(ctx, u, models.FilterPreset{
			Name: "History policy", Active: true, HistoryMode: mode, HistoryLookback: lookback, HistorySince: since,
			IncludedTitles: []string{}, ExcludedTitleWords: []string{}, Locations: []string{}, RemoteTypes: []string{},
			EmploymentTypes: []string{}, ExcludedIndustries: []string{}, RequiredSkills: []string{}, IncludedTechnologies: []string{}, ExcludedTechnologies: []string{},
		})
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	read := func(t *testing.T, u, id string, rejected bool) models.JobWithMatch {
		t.Helper()
		got, err := s.Job(ctx, u, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Match.Rejected != rejected {
			t.Fatalf("rejected = %v, want %v: %+v", got.Match.Rejected, rejected, got)
		}
		if rejected && (got.Job.ApplyURL != "" || got.Job.EmployerURL != "" || got.Job.Slug != "") {
			t.Fatal("rejected job leaks links", got.Job)
		}
		return got
	}
	entry := func(t *testing.T, got models.JobWithMatch, blocked, warned bool, relation string) models.HistoryEntry {
		t.Helper()
		h := got.Job.ApplicationHistory
		if h == nil || h.Blocked != blocked || len(h.Entries) != 1 {
			t.Fatalf("history = %+v", h)
		}
		subject := "company"
		if relation == "same_role" {
			subject = "role"
		}
		hasWarning := strings.Contains(strings.Join(got.Match.Warnings, " "), "Application history: you applied to this "+subject+" before. Review the earlier applications first.")
		// An excluded job says so in its rejection reason and carries no warning besides.
		if hasWarning != (warned && !blocked) {
			t.Fatalf("warnings = %v, want history warning %v", got.Match.Warnings, warned && !blocked)
		}
		if blocked && !strings.Contains(strings.Join(got.Match.RejectionReasons, " "), "Application history: you already applied to this ") {
			t.Fatal("missing rejection reason", got.Match)
		}
		if relation != "" && h.Entries[0].Relation != relation {
			t.Fatalf("relation = %s, want %s", h.Entries[0].Relation, relation)
		}
		return h.Entries[0]
	}
	apply := func(t *testing.T, u, id string) models.Application {
		t.Helper()
		a, err := s.CreateApplication(ctx, u, id)
		if err != nil {
			t.Fatal(err)
		}
		a, err = s.UpdateApplication(ctx, u, a.ID, "applied", nil)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	t.Run("default warning and aliases", func(t *testing.T) {
		u := user(t)
		id := job(t, u, "Acme", "Engineer", "https://boards.greenhouse.io/acme/jobs/111")
		other := job(t, u, "Other Robotics", "Engineer", "https://example.test/other")
		history(t, u, "Acme Robotics aka Acme Ltd", "Engineer", "https://boards.greenhouse.io/acme/jobs/111", "2026-01-01", "applied", "application_receipt")
		got := read(t, u, id, false)
		entry(t, got, false, true, "same_role")
		if got.Job.ApplicationHistory.Mode != "warn" || got.Job.ApplicationHistory.Lookback != "all" {
			t.Fatal("default policy", got.Job.ApplicationHistory)
		}
		if read(t, u, other, false).Job.ApplicationHistory != nil {
			t.Fatal("unrelated company has history")
		}
		for _, summary := range []bool{false, true} {
			rows, count, err := s.Jobs(ctx, u, JobQuery{Summary: summary})
			if err != nil || count != 2 || len(rows) != 2 {
				t.Fatalf("jobs = %d/%d, %v", len(rows), count, err)
			}
			for _, row := range rows {
				if row.Job.ID == id {
					entry(t, row, false, true, "same_role")
				}
			}
		}
		matched, err := s.Match(ctx, u, got.MatchID)
		if err != nil {
			t.Fatal(err)
		}
		entry(t, matched, false, true, "same_role")
	})
	t.Run("company mode guards and readers", func(t *testing.T) {
		u := user(t)
		id := job(t, u, "Acme", "Engineer", "https://boards.greenhouse.io/acme/jobs/111")
		second := job(t, u, "Acme", "Designer", "https://boards.greenhouse.io/acme/jobs/222")
		a, err := s.CreateApplication(ctx, u, id)
		if err != nil {
			t.Fatal(err)
		}
		queued, err := s.SaveOutboxEntry(ctx, u, models.OutboxEntry{JobID: id, Status: models.OutboxReady, Channel: models.ChannelManual})
		if err != nil {
			t.Fatal(err)
		}
		history(t, u, "Acme", "Engineer", "https://boards.greenhouse.io/acme/jobs/111", "2026-01-01", "applied", "application_receipt")
		policy(t, u, "company", "all", "")
		entry(t, read(t, u, id, true), true, true, "same_role")
		read(t, u, second, true)
		if _, err := s.CreateApplication(ctx, u, second); !errors.Is(err, ErrApplicationBlocked) {
			t.Fatal("create guard", err)
		}
		if _, err := s.UpdateApplication(ctx, u, a.ID, "applied", nil); !errors.Is(err, ErrApplicationBlocked) {
			t.Fatal("status guard", err)
		}
		if _, _, _, err := s.RecordOutbound(ctx, u, id); !errors.Is(err, ErrApplicationBlocked) {
			t.Fatal("outbound guard", err)
		}
		if _, err := s.ApproveOutboxEntry(ctx, u, queued.ID); err != OutboxRefusal(ErrApplicationBlocked.Error()) {
			t.Fatal("approval guard", err)
		}
		detail, err := s.Application(ctx, u, a.ID)
		if err != nil || detail.Job == nil || detail.Job.ApplicationHistory == nil || detail.Job.ApplyURL != "" {
			t.Fatalf("application detail = %+v, %v", detail, err)
		}
		apps, err := s.Applications(ctx, u)
		if err != nil || len(apps) != 1 || apps[0].Job.ApplicationHistory == nil || apps[0].Job.EmployerURL != "" {
			t.Fatalf("applications = %+v, %v", apps, err)
		}
		out, err := s.OutboxEntry(ctx, u, queued.ID)
		if err != nil || out.Job == nil || out.Job.ApplicationHistory == nil || out.Job.ApplyURL != "" {
			t.Fatalf("outbox detail = %+v, %v", out, err)
		}
		outs, err := s.OutboxEntries(ctx, u, "")
		if err != nil || len(outs) != 1 || outs[0].Job == nil || outs[0].Job.ApplicationHistory == nil || outs[0].Job.EmployerURL != "" {
			t.Fatalf("outbox list = %+v, %v", outs, err)
		}
		attached := s.withJob(ctx, u, queued)
		if attached.Job == nil || attached.Job.ApplicationHistory == nil || attached.Job.ApplyURL != "" {
			t.Fatal("withJob", attached)
		}
		if _, err := pool.Exec(ctx, `UPDATE application_outbox SET status='approved',approved_at=now()-interval '1 minute' WHERE id=$1`, queued.ID); err != nil {
			t.Fatal(err)
		}
		claim, ok, err := s.ClaimOutboxEntry(ctx, 0, u)
		if err != nil || !ok || claim.Refused != ErrApplicationBlocked.Error() || claim.Entry.Status != models.OutboxFailed {
			t.Fatalf("claim = %+v, %v, %v", claim, ok, err)
		}
		for _, state := range []string{"accepted", "rejected"} {
			rows, count, err := s.Jobs(ctx, u, JobQuery{State: state})
			want := 0
			if state == "rejected" {
				want = 2
			}
			if err != nil || count != want || len(rows) != want {
				t.Fatalf("%s jobs = %d/%d, %v", state, len(rows), count, err)
			}
		}
		// Another user with the same jobs matched sees none of this imported history.
		other := user(t)
		if got := s.outboxJob(ctx, other, id); got == nil || got.ID != id || got.ApplicationHistory != nil {
			t.Fatal("an unmatched job should get the catalog copy", got)
		}
		if got := s.outboxJob(ctx, "not-a-user-id", id); got != nil {
			t.Fatal("a failed read fell back to the catalog copy and its links", got)
		}
		for _, shared := range []string{id, second} {
			if _, err := pool.Exec(ctx, `INSERT INTO job_matches(user_id,job_id,rejected,score) VALUES($1,$2,false,80)`, other, shared); err != nil {
				t.Fatal(err)
			}
		}
		policy(t, other, "company", "all", "")
		for _, shared := range []string{id, second} {
			if got := read(t, other, shared, false); got.Job.ApplicationHistory != nil || len(got.Match.Warnings) != 0 {
				t.Fatal("imported history leaked to another user", got)
			}
		}
		summary, err := s.CatalogSummary(ctx, u)
		if err != nil || summary.OpenMatches != 0 {
			t.Fatalf("catalog = %+v, %v", summary, err)
		}
		dashboard, err := s.Dashboard(ctx, u)
		if err != nil || dashboard["jobs_rejected_filters"] != 2 || dashboard["good_matches"] != 0 {
			t.Fatalf("dashboard = %+v, %v", dashboard, err)
		}
	})
	t.Run("role identity and renamed title", func(t *testing.T) {
		u := user(t)
		history(t, u, "Acme", "Engineer", "https://boards.greenhouse.io/acme/jobs/111", "2026-01-01", "applied", "application_receipt")
		policy(t, u, "role", "all", "")
		for _, tc := range []struct {
			title, url, relation string
			blocked, changed     bool
		}{
			{"Engineer", "https://boards.greenhouse.io/acme/jobs/111", "same_role", true, false},
			{"Engineer", "https://boards.greenhouse.io/acme/jobs/222", "different_role", false, false},
			{"Engineer", "https://example.test/careers", "uncertain_role", false, false},
			{"Senior Engineer", "https://boards.greenhouse.io/acme/jobs/111", "same_role", true, true},
		} {
			id := job(t, u, "Acme", tc.title, tc.url)
			e := entry(t, read(t, u, id, tc.blocked), tc.blocked, true, tc.relation)
			if e.TitleChanged != tc.changed {
				t.Fatalf("title_changed = %v, want %v", e.TitleChanged, tc.changed)
			}
		}
	})
	t.Run("lookback and unknown dates", func(t *testing.T) {
		var old, recent string
		if err := pool.QueryRow(ctx, `SELECT to_char((now() AT TIME ZONE 'UTC')::date-interval '4 months','YYYY-MM-DD'),to_char((now() AT TIME ZONE 'UTC')::date-7,'YYYY-MM-DD')`).Scan(&old, &recent); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name, date                         string
			blocked, warned, inWindow, unknown bool
		}{
			{"old", old, false, false, false, false},
			{"recent", recent, true, true, true, false},
			{"unknown", "", false, true, true, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				u := user(t)
				id := job(t, u, "Acme", "Engineer", "https://example.test/job")
				history(t, u, "Acme", "Engineer", "", tc.date, "applied", "application_receipt")
				policy(t, u, "company", "three_months", "")
				e := entry(t, read(t, u, id, tc.blocked), tc.blocked, tc.warned, "uncertain_role")
				if e.InWindow != tc.inWindow || e.DateUnknown != tc.unknown {
					t.Fatalf("date flags = %+v", e)
				}
				if tc.unknown {
					policy(t, u, "company", "all", "")
					entry(t, read(t, u, id, true), true, true, "uncertain_role")
				}
			})
		}
	})
	t.Run("a same-role application outside the lookback does not name the role", func(t *testing.T) {
		var old, recent string
		if err := pool.QueryRow(ctx, `SELECT to_char((now() AT TIME ZONE 'UTC')::date-interval '4 months','YYYY-MM-DD'),to_char((now() AT TIME ZONE 'UTC')::date-7,'YYYY-MM-DD')`).Scan(&old, &recent); err != nil {
			t.Fatal(err)
		}
		u := user(t)
		id := job(t, u, "Acme", "Engineer", "https://boards.greenhouse.io/acme/jobs/111")
		history(t, u, "Acme", "Engineer", "https://boards.greenhouse.io/acme/jobs/111", old, "applied", "application_receipt")
		history(t, u, "Acme", "Designer", "https://boards.greenhouse.io/acme/jobs/222", recent, "applied", "application_receipt")
		policy(t, u, "role", "three_months", "")
		got := read(t, u, id, false)
		h := got.Job.ApplicationHistory
		if h == nil || h.Blocked || len(h.Entries) != 2 {
			t.Fatalf("history = %+v", h)
		}
		// Newest first: the other role inside the lookback, then this role outside it.
		if h.Entries[0].Relation != "different_role" || !h.Entries[0].InWindow || h.Entries[1].Relation != "same_role" || h.Entries[1].InWindow {
			t.Fatalf("entries = %+v", h.Entries)
		}
		warnings := strings.Join(got.Match.Warnings, " ")
		if !strings.Contains(warnings, "you applied to this company before") || strings.Contains(warnings, "this role before") {
			t.Fatal("the warning should name the company only", got.Match.Warnings)
		}
		policy(t, u, "role", "all", "")
		got = read(t, u, id, true)
		if !strings.Contains(strings.Join(got.Match.RejectionReasons, " "), "you already applied to this role") {
			t.Fatal("with all history the same role excludes the job", got.Match)
		}
	})
	t.Run("alerts and generic receipts are not applications", func(t *testing.T) {
		u := user(t)
		id := job(t, u, "Acme", "Engineer", "https://example.test/job")
		for _, evidence := range []string{"alert", "generic_receipt"} {
			history(t, u, "Acme", "Engineer", "", "", "unknown", evidence)
		}
		policy(t, u, "company", "all", "")
		got := read(t, u, id, false)
		if got.Job.ApplicationHistory != nil || len(got.Match.Warnings) != 0 {
			t.Fatal("non-application history", got)
		}
	})
	t.Run("recorded applications and tenant isolation", func(t *testing.T) {
		u, other := user(t), user(t)
		a := job(t, u, "Acme", "Engineer", "https://boards.greenhouse.io/acme/jobs/111")
		b := job(t, u, "Acme", "Designer", "https://boards.greenhouse.io/acme/jobs/222")
		apply(t, u, a)
		policy(t, u, "company", "all", "")
		read(t, u, a, false)
		e := entry(t, read(t, u, b, true), true, true, "different_role")
		if e.Source != "JobPilot" || e.JobID != a || e.CanonicalURL != "" {
			t.Fatalf("recorded history = %+v", e)
		}
		if e.TitleChanged {
			t.Fatal("another role's title is not a renamed title", e)
		}
		for _, id := range []string{a, b} {
			if _, err := pool.Exec(ctx, `INSERT INTO job_matches(user_id,job_id,rejected,score) VALUES($1,$2,false,80)`, other, id); err != nil {
				t.Fatal(err)
			}
		}
		policy(t, other, "company", "all", "")
		for _, id := range []string{a, b} {
			if read(t, other, id, false).Job.ApplicationHistory != nil {
				t.Fatal("tenant history leaked")
			}
		}
		policy(t, u, "warn", "all", "")
		apply(t, u, b)
		policy(t, u, "company", "all", "")
		read(t, u, a, false)
		read(t, u, b, false)
		draft := job(t, u, "Unapplied Robotics", "Engineer", "https://example.test/draft")
		neighbor := job(t, u, "Unapplied Robotics", "Designer", "https://example.test/neighbor")
		d, err := s.CreateApplication(ctx, u, draft)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateApplication(ctx, u, d.ID, "withdrawn", nil); err != nil {
			t.Fatal(err)
		}
		if read(t, u, neighbor, false).Job.ApplicationHistory != nil {
			t.Fatal("withdrawn draft became history")
		}
	})
	t.Run("names stated before they were kept are filled in once", func(t *testing.T) {
		u := user(t)
		id := job(t, u, "Backfill", "Engineer", "https://example.test/backfill")
		history(t, u, "Backfill Robotics aka Backfill Ltd", "Engineer", "", "2026-01-01", "applied", "application_receipt")
		// As the migration left a record imported before the names were kept.
		if _, err := pool.Exec(ctx, `UPDATE external_application_history SET employer_keys=ARRAY[employer_normalized] WHERE user_id=$1`, u); err != nil {
			t.Fatal(err)
		}
		if read(t, u, id, false).Job.ApplicationHistory != nil {
			t.Fatal("the whole stated name should not match one of its parts")
		}
		if filled, err := s.BackfillHistoryEmployerKeys(ctx); err != nil || filled < 1 {
			t.Fatal(filled, err)
		}
		entry(t, read(t, u, id, false), false, true, "uncertain_role")
		var keys []string
		if err := pool.QueryRow(ctx, `SELECT employer_keys FROM external_application_history WHERE user_id=$1`, u).Scan(&keys); err != nil || len(keys) != 3 {
			t.Fatal(keys, err)
		}
		if _, err := s.BackfillHistoryEmployerKeys(ctx); err != nil {
			t.Fatal(err)
		}
		var again []string
		if err := pool.QueryRow(ctx, `SELECT employer_keys FROM external_application_history WHERE user_id=$1`, u).Scan(&again); err != nil || len(again) != 3 {
			t.Fatal("a second run changed the names", again, err)
		}
	})
	t.Run("cutoff calendar arithmetic", func(t *testing.T) {
		for _, tc := range []struct{ mode, since, today, want string }{
			{"three_months", "", "2026-05-31", "2026-02-28"},
			{"three_months", "", "2024-05-31", "2024-02-29"},
			{"since", "2026-01-15", "2026-05-31", "2026-01-15"},
			{"all", "", "2026-05-31", ""},
		} {
			var got *string
			err := pool.QueryRow(ctx, `SELECT to_char(history_cutoff($1,NULLIF($2,'')::date,$3::date),'YYYY-MM-DD')`, tc.mode, tc.since, tc.today).Scan(&got)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if got != nil {
					t.Fatal("all cutoff is not NULL", *got)
				}
			} else if got == nil || *got != tc.want {
				t.Fatalf("cutoff = %v, want %s", got, tc.want)
			}
		}
	})
	t.Run("filter round trip and since boundary", func(t *testing.T) {
		u := user(t)
		f := policy(t, u, "company", "since", "2026-01-15")
		check := func(want models.FilterPreset) {
			t.Helper()
			filters, err := s.Filters(ctx, u)
			if err != nil || len(filters) != 1 {
				t.Fatalf("filters = %+v, %v", filters, err)
			}
			got := filters[0]
			if got.HistoryMode != want.HistoryMode || got.HistoryLookback != want.HistoryLookback || got.HistorySince != want.HistorySince {
				t.Fatalf("filter = %+v, want %+v", got, want)
			}
		}
		check(f)
		old := job(t, u, "Old Robotics", "Engineer", "https://example.test/old")
		boundary := job(t, u, "New Robotics", "Engineer", "https://example.test/new")
		history(t, u, "Old Robotics", "Engineer", "", "2026-01-14", "applied", "application_receipt")
		history(t, u, "New Robotics", "Engineer", "", "2026-01-15", "applied", "application_receipt")
		entry(t, read(t, u, old, false), false, false, "uncertain_role")
		got := read(t, u, boundary, true)
		if got.Job.ApplicationHistory.Cutoff != "2026-01-15" {
			t.Fatal("since cutoff", got.Job.ApplicationHistory)
		}
		f.HistoryMode, f.HistoryLookback = "role", "three_months"
		f, err = s.SaveFilter(ctx, u, f)
		if err != nil {
			t.Fatal(err)
		}
		if f.HistorySince != "" {
			t.Fatal("date not cleared", f)
		}
		check(f)
	})
}
