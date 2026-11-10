package store

import (
	"reflect"
	"testing"

	"jobpilot/core/api/internal/models"
)

func TestValidateHistoryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		in                    models.FilterPreset
		mode, lookback, since string
		invalid               bool
	}{
		{name: "defaults", mode: "warn", lookback: "all"},
		{name: "bad mode", in: models.FilterPreset{HistoryMode: "never"}, invalid: true},
		{name: "bad lookback", in: models.FilterPreset{HistoryLookback: "year"}, invalid: true},
		{name: "missing date", in: models.FilterPreset{HistoryLookback: "since"}, invalid: true},
		{name: "year zero", in: models.FilterPreset{HistoryLookback: "since", HistorySince: "0000-01-01"}, invalid: true},
		{name: "invalid day", in: models.FilterPreset{HistoryLookback: "since", HistorySince: "2026-02-30"}, invalid: true},
		{name: "timestamp", in: models.FilterPreset{HistoryLookback: "since", HistorySince: "2026-01-01T00:00:00Z"}, invalid: true},
		{name: "since", in: models.FilterPreset{HistoryMode: "company", HistoryLookback: "since", HistorySince: "2026-01-15"}, mode: "company", lookback: "since", since: "2026-01-15"},
		{name: "first year", in: models.FilterPreset{HistoryLookback: "since", HistorySince: "0001-01-01"}, mode: "warn", lookback: "since", since: "0001-01-01"},
		{name: "clear all date", in: models.FilterPreset{HistorySince: "not a date"}, mode: "warn", lookback: "all"},
		{name: "clear rolling date", in: models.FilterPreset{HistoryMode: "role", HistoryLookback: "three_months", HistorySince: "2026-01-15"}, mode: "role", lookback: "three_months"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in
			err := ValidateHistoryPolicy(&got)
			if (err != nil) != tc.invalid {
				t.Fatalf("error = %v, invalid = %v", err, tc.invalid)
			}
			if err == nil && (got.HistoryMode != tc.mode || got.HistoryLookback != tc.lookback || got.HistorySince != tc.since) {
				t.Fatalf("policy = %+v", got)
			}
		})
	}
}

func TestEmployerKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"Acme Robotics aka Acme Ltd", []string{"acme robotics", "acme", "acme robotics aka acme"}},
		{"Acme Ltd", []string{"acme"}},
	} {
		if got := employerKeys(tc.name); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("employerKeys(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHistoryScanners(t *testing.T) {
	raw := []byte(`{"mode":"company","lookback":"all","cutoff":null,"blocked":true,"entries":[{"id":"history-1","source":"JobPilot","job_id":"job-2","relation":"same_role","cv":{"document_id":"cv-1","filename":"resume.pdf","version_at":"2026-01-01T00:00:00Z"}}]}`)
	for _, value := range [][]byte{nil, raw, []byte(`{`)} {
		matchRow := jobMatchRow("https://example.test/apply", true)
		matchRow.values[37] = value
		match, err := scanJobMatch(matchRow)
		appRow := applicationRow("https://example.test/apply", true)
		appRow.values[26] = value
		app, appErr := scanApplication(appRow)
		if string(value) == "{" {
			if err == nil || appErr == nil {
				t.Fatal("malformed history was accepted")
			}
			continue
		}
		if err != nil || appErr != nil {
			t.Fatalf("scan errors: %v, %v", err, appErr)
		}
		if match.Job.ApplyURL != "" || app.Job.ApplyURL != "" {
			t.Fatal("rejected links leaked")
		}
		if value == nil {
			if match.Job.ApplicationHistory != nil || app.Job.ApplicationHistory != nil {
				t.Fatal("NULL history must stay nil")
			}
		} else {
			if !reflect.DeepEqual(match.Job.ApplicationHistory, app.Job.ApplicationHistory) {
				t.Fatal("scanners disagree")
			}
			h := match.Job.ApplicationHistory
			if h == nil || !h.Blocked || len(h.Entries) != 1 || h.Entries[0].CV == nil || h.Entries[0].CV.DocumentID != "cv-1" {
				t.Fatalf("lost history: %+v", h)
			}
		}
	}
}
