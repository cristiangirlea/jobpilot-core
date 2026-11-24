package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
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

// differenceParts holds one range's sums over the matches that could be split.
type differenceParts struct {
	Min                     int `json:"min"`
	Max                     int `json:"max"`
	Matches                 int `json:"matches"`
	Skill                   int `json:"skill"`
	Seniority               int `json:"seniority"`
	Other                   int `json:"other"`
	RulesMatchedSkills      int `json:"rules_matched_skills"`
	ExtractionMatchedSkills int `json:"extraction_matched_skills"`
	ExtractionMatchedNone   int `json:"extraction_matched_none"`
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
	DifferenceParts        []differenceParts `json:"difference_parts"`
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

// perMatch reports whether a sum over this many matches lies within low..high for each match.
func perMatch(sum *big.Int, low, high, matches int) bool {
	count := big.NewInt(int64(matches))
	return sum.Cmp(new(big.Int).Mul(big.NewInt(int64(low)), count)) >= 0 &&
		sum.Cmp(new(big.Int).Mul(big.NewInt(int64(high)), count)) <= 0
}

// splitMatches counts the matches the parts cover; false when they contradict the ranges' counts or no scores could give them.
func (s extractionSummary) splitMatches() (int, bool) {
	// An older server sends no parts; an empty list is not that.
	if s.DifferenceParts == nil {
		return 0, true
	}
	if len(s.DifferenceParts) != len(s.ScoreDifferences) {
		return 0, false
	}
	total := 0
	for i, part := range s.DifferenceParts {
		diff := s.ScoreDifferences[i]
		if part.Min != diff.Min || part.Max != diff.Max || (i > 0 && part.Min <= s.DifferenceParts[i-1].Max) ||
			part.Matches < 0 || part.Matches > diff.Count || part.Matches > s.Compared-total ||
			part.RulesMatchedSkills < 0 || part.ExtractionMatchedSkills < 0 ||
			part.ExtractionMatchedNone < 0 || part.ExtractionMatchedNone > part.Matches ||
			part.ExtractionMatchedNone > part.RulesMatchedSkills ||
			(part.Matches == 0 && (part.RulesMatchedSkills != 0 || part.ExtractionMatchedSkills != 0)) {
			return 0, false
		}
		// A part lies in 0..100, and the parts of a match add up to its difference.
		skill, seniority := big.NewInt(int64(part.Skill)), big.NewInt(int64(part.Seniority))
		whole := new(big.Int).Add(new(big.Int).Add(skill, seniority), big.NewInt(int64(part.Other)))
		if !perMatch(skill, -100, 100, part.Matches) || !perMatch(seniority, -100, 100, part.Matches) ||
			!perMatch(whole, part.Min, part.Max, part.Matches) {
			return 0, false
		}
		total += part.Matches
	}
	return total, true
}

func (s extractionSummary) write(out io.Writer, token string) error {
	if !s.validCounts() {
		return errors.New("summary response failed (bad_response)")
	}
	split, ok := s.splitMatches()
	if !ok {
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
	if len(s.DifferenceParts) > 0 {
		fmt.Fprintf(&lines, "Where the difference comes from, for %d of %d matches (means per match, extraction minus rules):\n", split, s.Compared)
	}
	for _, part := range s.DifferenceParts {
		label := fmt.Sprintf("%d to %d", part.Min, part.Max)
		if part.Min == part.Max {
			label = fmt.Sprint(part.Min)
		}
		if part.Matches == 0 {
			fmt.Fprintf(&lines, "  %s: 0 matches\n", label)
			continue
		}
		mean := func(sum int) float64 { return float64(sum) / float64(part.Matches) }
		fmt.Fprintf(&lines, "  %s: %d matches; skills %+.1f, seniority %+.1f, other %+.1f; matched skills: rules %.1f, extraction %.1f; none matched by the extraction: %d\n",
			label, part.Matches, mean(part.Skill), mean(part.Seniority), mean(part.Other),
			mean(part.RulesMatchedSkills), mean(part.ExtractionMatchedSkills), part.ExtractionMatchedNone)
	}
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
