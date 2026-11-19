package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

const summaryPath = "/api/extractions/summary"
const summaryLimit = 64 << 10

// Restrict server metadata to identifiers so it cannot add arbitrary text to the report.
var safeSummarySetting = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)

type descriptionCounts struct {
	OpenJobs int `json:"open_jobs"`
	Answered int `json:"answered"`
	Rejected int `json:"rejected"`
}

type scoreDifference struct {
	Min   int `json:"min"`
	Max   int `json:"max"`
	Count int `json:"count"`
}

type extractionSummary struct {
	Scoring                string            `json:"scoring"`
	Model                  string            `json:"model"`
	PromptVersion          string            `json:"prompt_version"`
	OpenJobs               int               `json:"open_jobs"`
	CurrentExtractions     int               `json:"current_extractions"`
	DroppedSeniority       int               `json:"dropped_seniority"`
	RejectedAnswers        map[string]int    `json:"rejected_answers"`
	SingleLine             descriptionCounts `json:"single_line"`
	MultiLine              descriptionCounts `json:"multi_line"`
	Compared               int               `json:"compared"`
	ScoreDifferences       []scoreDifference `json:"score_differences"`
	MinScore               int               `json:"min_score"`
	WouldReachMinScore     int               `json:"would_reach_min_score"`
	WouldFallBelowMinScore int               `json:"would_fall_below_min_score"`
}

func runSummary(ctx context.Context, cfg config, out io.Writer) error {
	client := newAPIClient(cfg.apiURL)
	defer client.CloseIdleConnections()
	resp, err := doRequest(ctx, client, http.MethodGet, cfg.apiURL+summaryPath, cfg.token, nil)
	if err != nil {
		return fmt.Errorf("summary request failed (%s)", transportKind(err))
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return refusedToken(resp.StatusCode)
	case http.StatusNotFound:
		return errors.New("this server has no extraction summary")
	default:
		return fmt.Errorf("summary returned HTTP %d", resp.StatusCode)
	}

	var report extractionSummary
	if err := readJSON(resp, summaryLimit, &report); err != nil {
		return fmt.Errorf("summary response failed (%s)", responseKind(err))
	}
	return report.write(out, cfg.token)
}

func (s extractionSummary) validCounts() bool {
	for _, count := range []int{
		s.OpenJobs, s.CurrentExtractions, s.DroppedSeniority, s.Compared,
		s.SingleLine.OpenJobs, s.SingleLine.Answered, s.SingleLine.Rejected,
		s.MultiLine.OpenJobs, s.MultiLine.Answered, s.MultiLine.Rejected,
		s.WouldReachMinScore, s.WouldFallBelowMinScore,
	} {
		if count < 0 {
			return false
		}
	}
	if len(s.ScoreDifferences) == 0 || len(s.ScoreDifferences) > 20 || s.MinScore < 0 || s.MinScore > 100 {
		return false
	}
	for _, diff := range s.ScoreDifferences {
		if diff.Min < -100 || diff.Max > 100 || diff.Min > diff.Max || diff.Count < 0 {
			return false
		}
	}
	return true
}

func (s extractionSummary) write(out io.Writer, token string) error {
	if !s.validCounts() {
		return errors.New("summary response failed (bad_response)")
	}
	reasons := make(map[string]int)
	total := 0
	for reason, count := range s.RejectedAnswers {
		if count < 0 || count > math.MaxInt-total {
			return errors.New("summary response failed (bad_response)")
		}
		if !safeReason.MatchString(reason) || publicSetting(reason, token) != reason {
			reason = "other"
		}
		reasons[reason] += count
		total += count
	}
	keys := make([]string, 0, len(reasons))
	for reason := range reasons {
		keys = append(keys, reason)
	}
	sort.Strings(keys)
	var breakdown []string
	for _, reason := range keys {
		breakdown = append(breakdown, fmt.Sprintf("%s %d", reason, reasons[reason]))
	}

	if s.Scoring != "off" && s.Scoring != "shadow" {
		s.Scoring = "unknown"
	}
	var lines strings.Builder
	fmt.Fprintf(&lines, "Scoring: %s\n", s.Scoring)
	fmt.Fprintf(&lines, "Model: %s, prompt %s\n", summarySetting(s.Model, token), summarySetting(s.PromptVersion, token))
	fmt.Fprintf(&lines, "Open jobs: %d; with a current extraction: %d; seniority dropped: %d\n", s.OpenJobs, s.CurrentExtractions, s.DroppedSeniority)
	fmt.Fprintf(&lines, "Rejected answers: %d", total)
	if len(breakdown) > 0 {
		fmt.Fprintf(&lines, " (%s)", strings.Join(breakdown, ", "))
	}
	fmt.Fprintln(&lines)
	fmt.Fprintf(&lines, "Descriptions on one line: %d open, %d answered, %d rejected\n", s.SingleLine.OpenJobs, s.SingleLine.Answered, s.SingleLine.Rejected)
	fmt.Fprintf(&lines, "Other descriptions: %d open, %d answered, %d rejected\n", s.MultiLine.OpenJobs, s.MultiLine.Answered, s.MultiLine.Rejected)
	fmt.Fprintf(&lines, "This account: %d matches compared\n", s.Compared)
	fmt.Fprintln(&lines, "Score difference, extraction minus rules:")
	for _, diff := range s.ScoreDifferences {
		if diff.Min == diff.Max {
			fmt.Fprintf(&lines, "  %d: %d\n", diff.Min, diff.Count)
		} else {
			fmt.Fprintf(&lines, "  %d to %d: %d\n", diff.Min, diff.Max, diff.Count)
		}
	}
	fmt.Fprintf(&lines, "Minimum score %d: %d would reach it, %d would fall below it\n", s.MinScore, s.WouldReachMinScore, s.WouldFallBelowMinScore)
	if _, err := io.WriteString(out, lines.String()); err != nil {
		return errors.New("cannot write extraction summary")
	}
	return nil
}

func summarySetting(value, token string) string {
	if value != "" && !safeSummarySetting.MatchString(value) {
		return "[redacted]"
	}
	return publicSetting(value, token)
}
