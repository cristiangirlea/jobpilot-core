package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func summaryCommand(t *testing.T, api string, args ...string) (int, string, string) {
	t.Helper()
	model := fakeServer(t, func(http.ResponseWriter, *http.Request) {
		t.Error("model contacted for a summary")
	})
	env := map[string]string{"JOBPILOT_API_URL": api, "JOBPILOT_TOKEN": testToken}
	var stdout, stderr bytes.Buffer
	args = append([]string{"--summary", "--model-url=" + model}, args...)
	code := realMain(context.Background(), args, func(key string) string { return env[key] }, &stdout, &stderr)
	if combined := stdout.String() + stderr.String(); strings.Contains(combined, marker) || strings.Contains(combined, testToken) {
		t.Fatal("private content appeared in output or error text")
	}
	return code, stdout.String(), stderr.String()
}

func TestSummaryRequestAndOutput(t *testing.T) {
	var requests atomic.Int32
	api := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/api/extractions/summary" || r.Header.Get("Authorization") != "Bearer "+testToken {
			t.Error("unexpected summary request method, path or authorization")
		}
		fmt.Fprint(w, `{
			"scoring": "shadow",
			"model": "example-model",
			"prompt_version": "prompt-v1",
			"open_jobs": 1200,
			"current_extractions": 1100,
			"dropped_seniority": 12,
			"rejected_answers": {"seniority_quote": 10, "quote": 25, "other": 1, "`+marker+`": 4},
			"single_line": {"open_jobs": 300, "answered": 280, "rejected": 30},
			"multi_line": {"open_jobs": 900, "answered": 860, "rejected": 10},
			"compared": 950,
			"score_differences": [
				{"min": -100, "max": -20, "count": 12},
				{"min": -19, "max": -10, "count": 16},
				{"min": -9, "max": -1, "count": 90},
				{"min": 0, "max": 0, "count": 400},
				{"min": 1, "max": 9, "count": 300},
				{"min": 10, "max": 19, "count": 113},
				{"min": 20, "max": 100, "count": 19}
			],
			"min_score": 75,
			"would_reach_min_score": 30,
			"would_fall_below_min_score": 22
		}`)
	})
	const want = `Scoring: shadow
Model: example-model, prompt prompt-v1
Open jobs: 1200; with a current extraction: 1100; seniority dropped: 12
Rejected answers: 40 (other 5, quote 25, seniority_quote 10)
Descriptions on one line: 300 open, 280 answered, 30 rejected
Other descriptions: 900 open, 860 answered, 10 rejected
This account: 950 matches compared
Score difference, extraction minus rules:
  -100 to -20: 12
  -19 to -10: 16
  -9 to -1: 90
  0: 400
  1 to 9: 300
  10 to 19: 113
  20 to 100: 19
Minimum score 75: 30 would reach it, 22 would fall below it
`
	code, stdout, stderr := summaryCommand(t, api, "--max=0")
	if code != 0 || stdout != want || stderr != "" || requests.Load() != 1 {
		t.Fatalf("code %d, stdout %q, stderr %q, API calls %d", code, stdout, stderr, requests.Load())
	}
}

func TestSummaryPrintsWhereTheDifferenceComesFrom(t *testing.T) {
	api := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{
			"scoring": "shadow", "model": "example-model", "prompt_version": "prompt-v1", "compared": 31,
			"score_differences": [
				{"min": -100, "max": -31, "count": 12},
				{"min": -30, "max": -30, "count": 1},
				{"min": 0, "max": 0, "count": 10},
				{"min": 1, "max": 100, "count": 8}
			],
			"difference_parts": [
				{"min": -100, "max": -31, "matches": 10, "skill": -215, "seniority": -100, "other": -5,
					"rules_matched_skills": 52, "extraction_matched_skills": 11, "extraction_matched_none": 4},
				{"min": -30, "max": -30, "matches": 1, "skill": -30, "seniority": 0, "other": 0,
					"rules_matched_skills": 501, "extraction_matched_skills": 0, "extraction_matched_none": 1},
				{"min": 0, "max": 0, "matches": 10, "skill": -20, "seniority": 20, "other": 0,
					"rules_matched_skills": 0, "extraction_matched_skills": 30, "extraction_matched_none": 0},
				{"min": 1, "max": 100, "matches": 0, "skill": 0, "seniority": 0, "other": 0,
					"rules_matched_skills": 0, "extraction_matched_skills": 0, "extraction_matched_none": 0}
			]
		}`)
	})
	const want = `Where the difference comes from, for 21 of 31 matches (means per match, extraction minus rules):
  -100 to -31: 10 matches; skills -21.5, seniority -10.0, other -0.5; matched skills: rules 5.2, extraction 1.1; none matched by the extraction: 4
  -30: 1 matches; skills -30.0, seniority +0.0, other +0.0; matched skills: rules 501.0, extraction 0.0; none matched by the extraction: 1
  0: 10 matches; skills -2.0, seniority +2.0, other +0.0; matched skills: rules 0.0, extraction 3.0; none matched by the extraction: 0
  1 to 100: 0 matches
`
	code, stdout, stderr := summaryCommand(t, api)
	if code != 0 || !strings.HasSuffix(stdout, "would fall below it\n"+want) || stderr != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestSummaryWithNullPartsPrintsNone(t *testing.T) {
	api := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"compared": 1, "score_differences": [{"min": 0, "max": 0, "count": 1}], "difference_parts": null}`)
	})
	code, stdout, stderr := summaryCommand(t, api)
	if code != 0 || !strings.HasSuffix(stdout, "would fall below it\n") || stderr != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestSummaryChecksLargeSumsExactly(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("the sums need 64-bit integers")
	}
	// Above 2^53 a float cannot tell these two sums apart.
	const matches, bound int64 = 100000000000000, 10000000000000000
	for sum, want := range map[int64]int{bound: 0, bound + 1: 1} {
		api := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `{"compared": %d, "score_differences": [{"min": 100, "max": 100, "count": %d}],
				"difference_parts": [{"min": 100, "max": 100, "matches": %d, "skill": %d, "other": %d}]}`,
				matches, matches, matches, sum, bound-sum)
		})
		if code, stdout, stderr := summaryCommand(t, api); code != want || (code == 1) != (stdout == "") {
			t.Fatalf("skill sum %d: code %d, stdout %q, stderr %q", sum, code, stdout, stderr)
		}
	}
}

func TestSummaryRedactsSettings(t *testing.T) {
	api := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		reply(w, extractionSummary{
			Scoring: marker, Model: "example " + marker, PromptVersion: testToken,
			ScoreDifferences: []scoreDifference{{}},
		})
	})
	code, stdout, stderr := summaryCommand(t, api)
	if code != 0 || !strings.HasPrefix(stdout, "Scoring: unknown\nModel: [redacted], prompt [redacted]\n") || stderr != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestSummaryErrors(t *testing.T) {
	const badResponse = "summary response failed (bad_response)"
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"unauthorized", http.StatusUnauthorized, marker + testToken, refusedToken(http.StatusUnauthorized).Error()},
		{"forbidden", http.StatusForbidden, marker + testToken, refusedToken(http.StatusForbidden).Error()},
		{"not found", http.StatusNotFound, marker + testToken, "this server has no extraction summary"},
		{"server error", http.StatusInternalServerError, marker + testToken, "summary returned HTTP 500"},
		{"not JSON", http.StatusOK, marker + testToken, badResponse},
		{"empty report", http.StatusOK, `{}`, badResponse},
		{"oversized", http.StatusOK, "{}" + strings.Repeat(" ", 64<<10), badResponse},
		{"high minimum score", http.StatusOK, `{"score_differences":[{}],"min_score":101}`, badResponse},
		{"count out of range", http.StatusOK, `{"score_differences":[{}],"open_jobs":100000000000000000000000000000}`, badResponse},
		{"rejected answer sum overflows", http.StatusOK, fmt.Sprintf(`{"score_differences":[{}],"rejected_answers":{"quote":%d,"invalid":1}}`, math.MaxInt), badResponse},
		{"reversed range", http.StatusOK, `{"score_differences":[{"min":2,"max":1,"count":1}]}`, badResponse},
		{"low bound", http.StatusOK, `{"score_differences":[{"min":-101,"max":0,"count":1}]}`, badResponse},
		{"high bound", http.StatusOK, `{"score_differences":[{"min":0,"max":101,"count":1}]}`, badResponse},
		{"too many ranges", http.StatusOK, `{"score_differences":[` + strings.Repeat("{},", 20) + `{}]}`, badResponse},
		{"negative range count", http.StatusOK, `{"score_differences":[{"min":0,"max":0,"count":-1}]}`, badResponse},
		{"negative reason count", http.StatusOK, `{"score_differences":[{}],"rejected_answers":{"quote":-1}}`, badResponse},
		{"negative description count", http.StatusOK, `{"score_differences":[{}],"single_line":{"answered":-1}}`, badResponse},
		{"negative total", http.StatusOK, `{"score_differences":[{}],"compared":-1}`, badResponse},
		{"an empty list of parts", http.StatusOK, `{"score_differences":[{}],"difference_parts":[]}`, badResponse},
		{"more parts than ranges", http.StatusOK, `{"score_differences":[{}],"difference_parts":[{},{}]}`, badResponse},
		{"a range without parts", http.StatusOK, `{"score_differences":[{},{"min":1,"max":1}],"difference_parts":[{}]}`, badResponse},
		{"parts of another range", http.StatusOK, `{"score_differences":[{}],"difference_parts":[{"min":1,"max":2}]}`, badResponse},
		{"a range's parts twice", http.StatusOK, `{"score_differences":[{},{"min":1,"max":1}],"difference_parts":[{},{}]}`, badResponse},
		{"a range and its parts twice", http.StatusOK, `{"score_differences":[{},{}],"difference_parts":[{},{}]}`, badResponse},
		{"negative part matches", http.StatusOK, `{"score_differences":[{"count":5}],"compared":5,"difference_parts":[{"matches":-1}]}`, badResponse},
		{"more split than the range holds", http.StatusOK, `{"score_differences":[{"count":1}],"compared":5,"difference_parts":[{"matches":2}]}`, badResponse},
		{"more split than compared", http.StatusOK, `{"score_differences":[{"count":9}],"compared":1,"difference_parts":[{"matches":2}]}`, badResponse},
		{"part sum without matches", http.StatusOK, `{"score_differences":[{"count":5}],"compared":5,"difference_parts":[{"skill":1,"other":-1}]}`, badResponse},
		{"matched skills without matches", http.StatusOK, `{"score_differences":[{"count":5}],"compared":5,"difference_parts":[{"extraction_matched_skills":1}]}`, badResponse},
		{"part sum beyond a score", http.StatusOK, `{"score_differences":[{"count":5}],"compared":5,"difference_parts":[{"matches":2,"seniority":-201,"other":201}]}`, badResponse},
		{"parts outside their range", http.StatusOK, `{"score_differences":[{"min":-100,"max":-20,"count":1}],"compared":1,"difference_parts":[{"min":-100,"max":-20,"matches":1,"skill":-19}]}`, badResponse},
		{"negative matched skills", http.StatusOK, `{"score_differences":[{"count":5}],"compared":5,"difference_parts":[{"matches":2,"rules_matched_skills":-1}]}`, badResponse},
		{"more unmatched than matches", http.StatusOK, `{"score_differences":[{"count":5}],"compared":5,"difference_parts":[{"matches":2,"rules_matched_skills":9,"extraction_matched_none":3}]}`, badResponse},
		{"sums that wrap in 64 bits", http.StatusOK, `{"score_differences":[{"min":-100,"max":100,"count":92233720368547758}],"compared":92233720368547758,` +
			`"difference_parts":[{"min":-100,"max":100,"matches":92233720368547758,"skill":9223372036854775800,"seniority":9223372036854775800,"other":9223372036854775807}]}`, badResponse},
		{"unmatched without rules skills", http.StatusOK, `{"score_differences":[{"count":5}],"compared":5,"difference_parts":[{"matches":2,"extraction_matched_none":1}]}`, badResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			code, stdout, stderr := summaryCommand(t, api)
			if code != 1 || stdout != "" || stderr != tc.want+"\n" {
				t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
			}
		})
	}
}

func TestSummaryDoesNotFollowRedirects(t *testing.T) {
	target := fakeServer(t, func(http.ResponseWriter, *http.Request) {
		t.Error("summary followed a redirect")
	})
	api := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	})
	code, stdout, stderr := summaryCommand(t, api)
	if code != 1 || stdout != "" || stderr != "summary returned HTTP 302\n" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestSummaryRefusesBatchFlags(t *testing.T) {
	api := fakeServer(t, func(http.ResponseWriter, *http.Request) {
		t.Error("API contacted with refused flags")
	})
	for _, args := range [][]string{{"--dry-run"}, {"--max", "5"}} {
		t.Run(args[0], func(t *testing.T) {
			code, stdout, stderr := summaryCommand(t, api, args...)
			if code != 1 || stdout != "" || stderr != "summary cannot be combined with dry-run or a positive max\n" {
				t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
			}
		})
	}
}
