package store

import (
	"testing"
	"time"
)

// fakeRow satisfies the scanner interface so the row-to-model helpers can be
// exercised without a database. It assigns by position, matching the column
// order of the query the helper is paired with.
type fakeRow struct{ values []any }

func (f *fakeRow) Scan(dest ...any) error {
	for i, d := range dest {
		if i >= len(f.values) {
			break
		}
		switch target := d.(type) {
		case *string:
			*target, _ = f.values[i].(string)
		case *float64:
			*target, _ = f.values[i].(float64)
		case *int:
			*target, _ = f.values[i].(int)
		case *bool:
			*target, _ = f.values[i].(bool)
		case *time.Time:
			*target, _ = f.values[i].(time.Time)
		case **time.Time:
			*target, _ = f.values[i].(*time.Time)
		case *[]byte:
			*target, _ = f.values[i].([]byte)
		}
	}
	return nil
}

// jobMatchRow builds the 33 values scanJobMatch reads, in column order.
func jobMatchRow(applyURL string, rejected bool) *fakeRow {
	empty := []byte(`[]`)
	return &fakeRow{values: []any{
		"job-1", "Senior Backend Engineer", "Fresh Systems GmbH", "fresh systems", "fresh.example",
		"Bucharest", "remote", 95000.0, 110000.0, "EUR", "full-time", "Software",
		"Long description.", applyURL, (*time.Time)(nil), time.Now(),
		"", "", (*time.Time)(nil), "", "", "Remotive feed", "remotive",
		"match-1", rejected, 82, empty, empty, empty, empty, empty, empty, []byte(`{}`),
	}}
}

func TestScanJobMatchWithholdsApplyURLForRejectedMatches(t *testing.T) {
	accepted, err := scanJobMatch(jobMatchRow("https://example.test/apply", false))
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Job.ApplyURL != "https://example.test/apply" {
		t.Fatalf("an accepted match must keep its apply URL, got %q", accepted.Job.ApplyURL)
	}

	rejected, err := scanJobMatch(jobMatchRow("https://example.test/apply", true))
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Job.ApplyURL != "" {
		t.Fatalf("a rejected match must not carry an apply URL, got %q", rejected.Job.ApplyURL)
	}
	if !rejected.Match.Rejected || rejected.Job.ID != "job-1" {
		t.Fatalf("the rest of the row must survive: %+v", rejected.Job)
	}
}

// applicationRow builds the 22 values scanApplication reads, in column order,
// ending with the joined rejected flag.
func applicationRow(applyURL string, rejected bool) *fakeRow {
	return &fakeRow{values: []any{
		"app-1", "job-1", "ready", "keep me", time.Now(), time.Now(),
		"job-1", "Senior Backend Engineer", "Fresh Systems GmbH", "fresh systems", "fresh.example",
		"Bucharest", "remote", 95000.0, 110000.0, "EUR", "full-time", "Software",
		"Long description.", applyURL, (*time.Time)(nil), time.Now(), rejected,
	}}
}

func TestScanApplicationWithholdsApplyURLWhenBlocked(t *testing.T) {
	open, err := scanApplication(applicationRow("https://example.test/apply", false))
	if err != nil {
		t.Fatal(err)
	}
	if open.Job == nil || open.Job.ApplyURL != "https://example.test/apply" {
		t.Fatalf("an open application must keep its apply URL, got %+v", open.Job)
	}
	if open.Notes != "keep me" {
		t.Fatalf("notes must survive the scan, got %q", open.Notes)
	}

	blocked, err := scanApplication(applicationRow("https://example.test/apply", true))
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Job == nil || blocked.Job.ApplyURL != "" {
		t.Fatalf("a blocked application must not carry an apply URL, got %+v", blocked.Job)
	}
}
