package store

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"jobpilot/core/api/internal/models"
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

// jobMatchRow builds the 38 values scanJobMatch reads, in column order.
func jobMatchRow(applyURL string, rejected bool) *fakeRow {
	empty := []byte(`[]`)
	return &fakeRow{values: []any{
		"job-1", "Senior Backend Engineer", "Fresh Systems GmbH", "fresh systems", "fresh.example",
		"Bucharest", "remote", 95000.0, 110000.0, "EUR", "full-time", "Software",
		"Long description.", applyURL, (*time.Time)(nil), time.Now(),
		"", "", (*time.Time)(nil), "", "", "Remotive feed", "remotive",
		"match-1", rejected, 82, empty, empty, empty, empty, empty, empty, []byte(`{}`),
		"fresh-systems-backend", (*time.Time)(nil), "", "https://job-boards.greenhouse.io/fresh/jobs/1", []byte(nil),
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
	if accepted.Job.EmployerURL != "https://job-boards.greenhouse.io/fresh/jobs/1" {
		t.Fatalf("an accepted match must keep its employer URL, got %q", accepted.Job.EmployerURL)
	}

	rejected, err := scanJobMatch(jobMatchRow("https://example.test/apply", true))
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Job.ApplyURL != "" || rejected.Job.EmployerURL != "" {
		t.Fatalf("a rejected match must not carry an apply link, got %q and %q", rejected.Job.ApplyURL, rejected.Job.EmployerURL)
	}
	if !rejected.Match.Rejected || rejected.Job.ID != "job-1" {
		t.Fatalf("the rest of the row must survive: %+v", rejected.Job)
	}
}

// applicationRow builds the 27 values scanApplication reads, in column order:
// the joined rejected flag, the employer URL, then when and why the job closed.
func applicationRow(applyURL string, rejected bool) *fakeRow {
	return &fakeRow{values: []any{
		"app-1", "job-1", "ready", "keep me", time.Now(), time.Now(),
		"job-1", "Senior Backend Engineer", "Fresh Systems GmbH", "fresh systems", "fresh.example",
		"Bucharest", "remote", 95000.0, 110000.0, "EUR", "full-time", "Software",
		"Long description.", applyURL, (*time.Time)(nil), time.Now(), rejected,
		"https://job-boards.greenhouse.io/fresh/jobs/1", (*time.Time)(nil), "", []byte(nil),
	}}
}

// An application is the candidate's history, so a job that closes stays on
// it, and says it closed and why, rather than leaving with the job lists.
func TestScanApplicationKeepsAClosedJobAndSaysWhy(t *testing.T) {
	row := applicationRow("https://example.test/apply", false)
	closedAt := time.Now()
	row.values[24], row.values[25] = &closedAt, "its link now returns 404 Not Found"
	application, err := scanApplication(row)
	if err != nil {
		t.Fatal(err)
	}
	if application.Job == nil || application.Job.ClosedAt == nil || application.Job.ClosedReason != "its link now returns 404 Not Found" {
		t.Fatalf("the application should keep its job and say it closed, got %+v", application.Job)
	}
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
	if blocked.Job == nil || blocked.Job.ApplyURL != "" || blocked.Job.EmployerURL != "" {
		t.Fatalf("a blocked application must not carry an apply link, got %+v", blocked.Job)
	}
}

// Rows written before the matcher stopped producing nil lists hold JSON null.
// Clients index these lists directly, so a scan must hand back arrays.
func TestScanJobMatchTurnsNullListsIntoEmptyOnes(t *testing.T) {
	row := jobMatchRow("https://example.test/apply", false)
	for i := 26; i <= 31; i++ {
		row.values[i] = []byte(`null`)
	}
	got, err := scanJobMatch(row)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got.Match)
	for _, list := range []string{"rejection_reasons", "warnings", "reasons", "matched_skills", "missing_skills", "red_flags"} {
		if strings.Contains(string(encoded), `"`+list+`":null`) {
			t.Errorf("%s encodes as null after the scan, want []", list)
		}
	}
}

// Job pages are addressed by slug, but links and bookmarks made before slugs
// existed carry the id, so both must find the job.
func TestJobRefColumnTellsIdsFromSlugs(t *testing.T) {
	for ref, want := range map[string]string{
		"ff952caa-0d56-4fda-a1c8-bf48fd655059":                   "j.id",
		"FF952CAA-0D56-4FDA-A1C8-BF48FD655059":                   "j.id",
		"arbeitnow-remote-python-backend-engineer-berlin-100001": "j.slug",
		"lever-odaseva-big-data-engineer-af389cc6":               "j.slug",
		"ff952caa-0d56-4fda-a1c8-bf48fd655059-extra":             "j.slug",
	} {
		if got := jobRefColumn(ref); got != want {
			t.Errorf("jobRefColumn(%q) = %s, want %s", ref, got, want)
		}
	}
}

// A slug is made from the end of the apply URL, so a rejected match, which
// withholds that URL, must withhold the slug too.
func TestScanJobMatchWithholdsTheSlugForRejectedMatches(t *testing.T) {
	for rejected, want := range map[bool]string{false: "example-a-job", true: ""} {
		row := jobMatchRow("https://example.test/a-job", rejected)
		row.values[33] = "example-a-job"
		got, err := scanJobMatch(row)
		if err != nil {
			t.Fatal(err)
		}
		if got.Job.Slug != want {
			t.Errorf("rejected=%v: slug %q, want %q", rejected, got.Job.Slug, want)
		}
	}
}

// Found in early use: the dashboard said "Complete your candidate
// profile" at 91% and never which field was missing. The profile had no
// work experience, which also leaves the current-employer rule nothing to
// check. The action now names what to add.
func TestProfileActionNamesTheMissingFields(t *testing.T) {
	complete := models.Profile{
		Name: "Ana Pop", Email: "ana@example.com", Phone: "+40 700", Location: "Bucharest", CurrentTitle: "Engineer",
		DesiredTitles: []string{"Backend Engineer"}, Seniorities: []string{"senior"}, YearsExperience: 8,
		Skills: []models.Skill{{Name: "Go"}}, Experiences: []models.WorkExperience{{Company: "Acme"}},
		WorkAuthorization: "EU citizen", NoticePeriod: "30 days",
	}
	if done, all := profileCompleteness(complete); done != all || profileAction(complete) != "" {
		t.Fatalf("a complete profile should need nothing, got %d of %d and %q", done, all, profileAction(complete))
	}

	noWork := complete
	noWork.Experiences = nil
	if got, want := profileAction(noWork), "Complete your candidate profile: add work experience"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if done, all := profileCompleteness(noWork); done != all-1 {
		t.Fatalf("one missing field should count once, got %d of %d", done, all)
	}

	sparse := noWork
	sparse.Phone, sparse.NoticePeriod = "", ""
	if got, want := profileAction(sparse), "Complete your candidate profile: add a phone number, work experience and a notice period"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A CV's parse has no enforced shape: a coverage value the summary cannot use
// is left out, and the rest of the summary stays.
func TestCVSummaryLeavesOutMalformedCoverage(t *testing.T) {
	id, filename := "cv-1", "cv.pdf"
	for _, c := range []struct {
		alignment, missing string
		wantAlignment      string
		wantMissing        string
	}{
		{`0.62`, `["Rust","Kafka"]`, "0.62", "Rust,Kafka"},
		{`0`, `[]`, "0", ""},
		{`"unknown"`, `"Rust"`, "none", ""},
		{`true`, `["Rust",1]`, "none", ""},
		{`{"score":0.5}`, `{"Rust":true}`, "none", ""},
		{`null`, `null`, "none", ""},
		{`"0.5"`, `[1,2]`, "none", ""},
		{`1e400`, ``, "none", ""},
		{``, ``, "none", ""},
	} {
		raw := cvSummary{id: &id, filename: &filename, alignment: []byte(c.alignment), missing: []byte(c.missing)}
		cv := raw.summary()
		alignment := "none"
		if cv.Alignment != nil {
			alignment = strconv.FormatFloat(*cv.Alignment, 'g', -1, 64)
		}
		if cv.ID != id || cv.Filename != filename || alignment != c.wantAlignment || strings.Join(cv.Missing, ",") != c.wantMissing {
			t.Errorf("alignment %s, missing %s: got %+v (alignment %s)", c.alignment, c.missing, cv, alignment)
		}
	}
	if (&cvSummary{}).summary() != nil {
		t.Error("no CV must give no summary")
	}
}
