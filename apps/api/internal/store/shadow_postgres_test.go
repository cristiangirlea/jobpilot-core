package store

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/models"
)

const shadowPostingTitle = "Backend Engineer"

func shadowStoreTx(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()
	s := emailTokenStore(t)
	ctx := context.Background()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	return ctx, tx
}

func insertShadowUser(t *testing.T, ctx context.Context, tx pgx.Tx) string {
	t.Helper()
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO users (email, password_hash)
		VALUES (gen_random_uuid()::text || '@example.test', 'unused') RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertShadowJob(t *testing.T, ctx context.Context, tx pgx.Tx, description string, closed bool) string {
	t.Helper()
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO jobs (title, company, company_normalized, description, discovered_at, closed_at)
		VALUES ($1, 'Example GmbH', 'example gmbh', $2, now(), CASE WHEN $3 THEN now() END) RETURNING id`,
		shadowPostingTitle, description, closed).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func storeShadowAnswer(t *testing.T, ctx context.Context, tx pgx.Tx, x models.JobExtraction) models.JobExtraction {
	t.Helper()
	if saved, err := saveExtraction(ctx, tx, x); err != nil || !saved {
		t.Fatalf("saveExtraction() = %v, %v", saved, err)
	}
	if err := tx.QueryRow(ctx, `SELECT id, created_at FROM job_extractions
		WHERE job_id=$1 AND model=$2 AND prompt_version=$3`, x.JobID, x.Model, x.PromptVersion).Scan(&x.ID, &x.CreatedAt); err != nil {
		t.Fatal(err)
	}
	return x
}

func TestCurrentExtractionsAgainstPostgres(t *testing.T) {
	ctx, tx := shadowStoreTx(t)
	model := "example-model-" + insertShadowUser(t, ctx, tx)
	const description = "Go required."
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = insertShadowJob(t, ctx, tx, description, false)
	}
	base := models.JobExtraction{
		Model: model, PromptVersion: extraction.PromptVersion, TextHash: extraction.Hash(shadowPostingTitle, description),
		Status: "ok", Extraction: &models.Extraction{
			Required: []models.ExtractedSkills{{Skills: []string{"Go"}, Quote: description}},
		},
	}
	ok, otherModel, otherPrompt, rejected, stale := base, base, base, base, base
	ok.JobID = ids[0]
	otherModel.JobID, otherModel.Model = ids[1], model+"-other"
	otherPrompt.JobID, otherPrompt.PromptVersion = ids[2], "example-other-prompt"
	rejected.JobID, rejected.Status, rejected.Reason, rejected.Extraction = ids[3], "rejected", "not_json", nil
	stale.JobID = ids[4]
	want := storeShadowAnswer(t, ctx, tx, ok)
	for _, x := range []models.JobExtraction{otherModel, otherPrompt, rejected, stale} {
		storeShadowAnswer(t, ctx, tx, x)
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET description='Rust required.' WHERE id=$1`, stale.JobID); err != nil {
		t.Fatal(err)
	}
	got, err := currentExtractions(ctx, tx, model, extraction.PromptVersion, ids)
	if err != nil || !reflect.DeepEqual(got, map[string]models.JobExtraction{ok.JobID: want}) || want.CreatedAt.IsZero() {
		t.Fatalf("currentExtractions() = %+v, %v, want only %+v", got, err, want)
	}
	if got, err := currentExtractions(ctx, nil, "", extraction.PromptVersion, ids); got == nil || len(got) != 0 || err != nil {
		t.Fatalf("currentExtractions() with empty model = %v, %v", got, err)
	}
	if got, err := currentExtractions(ctx, nil, model, extraction.PromptVersion, nil); got == nil || len(got) != 0 || err != nil {
		t.Fatalf("currentExtractions() with no IDs = %v, %v", got, err)
	}
}

func TestShadowMatchWritesAgainstPostgres(t *testing.T) {
	ctx, tx := shadowStoreTx(t)
	first, second := insertShadowUser(t, ctx, tx), insertShadowUser(t, ctx, tx)
	job := insertShadowJob(t, ctx, tx, "Go required.", false)
	item := JobEvaluation{
		JobID: job,
		Evaluation: models.Evaluation{
			Score: 60, Reasons: []string{"Example reason"}, Warnings: []string{"Example warning"},
			MatchedSkills: []string{"Go"}, MissingSkills: []string{"Rust"}, Breakdown: map[string]int{"skills": 20},
		},
		Shadow: &models.ShadowEvaluation{
			ExtractionID: "example-answer", TextHash: "example-hash", RulesScore: 60,
			AnsweredAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Evaluation: models.Evaluation{Score: 70},
		},
	}
	read := func() (string, *models.ShadowEvaluation) {
		t.Helper()
		var visible string
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT (to_jsonb(m)-'shadow_evaluation')::text, m.shadow_evaluation
			FROM job_matches m WHERE user_id=$1 AND job_id=$2`, first, job).Scan(&visible, &raw)
		if err != nil {
			t.Fatal(err)
		}
		if raw == nil {
			return visible, nil
		}
		var shadow models.ShadowEvaluation
		if err := json.Unmarshal(raw, &shadow); err != nil {
			t.Fatal(err)
		}
		return visible, &shadow
	}
	matchID, err := saveJobEvaluation(ctx, tx, first, "", item)
	if err != nil || matchID == "" {
		t.Fatalf("saveJobEvaluation() = %q, %v", matchID, err)
	}
	if _, got := read(); !reflect.DeepEqual(got, item.Shadow) {
		t.Fatalf("stored shadow = %+v, want %+v", got, item.Shadow)
	}
	public, err := jobByRef(ctx, tx, first, job)
	if err != nil || public.Match.Score != item.Evaluation.Score {
		t.Fatalf("Job() = %+v, %v", public, err)
	}
	raw, err := json.Marshal(public)
	if err != nil || strings.Contains(string(raw), "shadow") || strings.Contains(string(raw), "rules_score") {
		t.Fatalf("Job() JSON = %s, %v", raw, err)
	}

	without := JobEvaluation{JobID: job, Evaluation: item.Evaluation}
	if id, err := saveJobEvaluation(ctx, tx, first, "", without); err != nil || id != matchID {
		t.Fatalf("saveJobEvaluation() on conflict = %q, %v", id, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE job_matches SET created_at=now()-interval '1 day' WHERE id=$1`, matchID); err != nil {
		t.Fatal(err)
	}
	before, got := read()
	if got != nil {
		t.Fatalf("upsert without shadow stored %+v, want SQL NULL", got)
	}
	if err := saveShadowEvaluation(ctx, tx, first, job, item.Shadow); err != nil {
		t.Fatal(err)
	}
	if after, got := read(); after != before || !reflect.DeepEqual(got, item.Shadow) {
		t.Fatalf("shadow-only write affected the match or failed to set the shadow: %+v", got)
	}

	if _, err := saveJobEvaluation(ctx, tx, second, "", without); err != nil {
		t.Fatal(err)
	}
	unrelated := insertShadowUser(t, ctx, tx)
	otherJob := insertShadowJob(t, ctx, tx, "Rust required.", false)
	if _, err := saveJobEvaluation(ctx, tx, unrelated, "", JobEvaluation{JobID: otherJob}); err != nil {
		t.Fatal(err)
	}
	want := []string{first, second}
	slices.Sort(want)
	if got, err := matchUsers(ctx, tx, job); err != nil || !slices.Equal(got, want) {
		t.Fatalf("matchUsers() = %v, %v, want %v", got, err, want)
	}
	if got, err := matchUsers(ctx, nil, "not-a-uuid"); len(got) != 0 || err != nil {
		t.Fatalf("matchUsers() for invalid ID = %v, %v", got, err)
	}
}

func TestExtractionSummaryAgainstPostgres(t *testing.T) {
	ctx, tx := shadowStoreTx(t)
	user, other := insertShadowUser(t, ctx, tx), insertShadowUser(t, ctx, tx)
	model, prompt := "summary-"+user, extraction.PromptVersion
	if _, err := tx.Exec(ctx, `INSERT INTO discovery_settings (user_id, min_score) VALUES ($1, 60)`, user); err != nil {
		t.Fatal(err)
	}
	parts := func(skill, seniority int) map[string]int {
		return map[string]int{"skill_match": skill, "seniority_match": seniority, "title_match": 20}
	}
	rulesParts := func(skill, seniority, matched int) *models.RulesParts {
		return &models.RulesParts{Skill: skill, Seniority: seniority, MatchedSkills: matched}
	}
	for _, tc := range []struct {
		name, description     string
		rules, extracted      int
		blocked, closed, drop bool
		rulesParts            *models.RulesParts
		extractedParts        map[string]int
		extractedSkills       []string
	}{
		{name: "reach", description: "Go required.", rules: 39, extracted: 60, drop: true,
			rulesParts: rulesParts(9, 10, 1), extractedParts: parts(30, 10), extractedSkills: []string{"Go", "PostgreSQL"}},
		{name: "fall", description: "Go required.\nRust optional.", rules: 60, extracted: 35,
			rulesParts: rulesParts(25, 15, 2), extractedParts: parts(5, 10), extractedSkills: []string{}},
		{name: "rules rejection", description: "Go required.", rules: 50, extracted: 70, blocked: true,
			rulesParts: rulesParts(10, 0, 1), extractedParts: map[string]int{"skill_match": 25, "title_match": 45}, extractedSkills: []string{"Go"}},
		{name: "second small positive", description: "Go required.", rules: 70, extracted: 75,
			rulesParts: rulesParts(15, 5, 2), extractedParts: parts(20, 5)},
		{name: "middle negative", description: "Go required.", rules: 90, extracted: 71,
			rulesParts: rulesParts(30, 15, 1), extractedParts: parts(15, 15), extractedSkills: []string{"Go"}},
		{name: "small negative, stored before the parts were", description: "Go required.\nRust optional.", rules: 90, extracted: 81,
			extractedParts: parts(21, 15)},
		{name: "tie, parts without seniority", description: "Go required.", rules: 65, extracted: 65, rulesParts: rulesParts(10, 5, 0), extractedParts: parts(5, 10)},
		{name: "small positive", description: "Go required.", rules: 62, extracted: 71, rulesParts: rulesParts(12, 0, 0), extractedParts: parts(12, 9)},
		{name: "middle positive, null parts", description: "Go required.", rules: 70, extracted: 89,
			rulesParts: rulesParts(1, 0, 1), extractedParts: parts(20, 0), extractedSkills: []string{"Go"}},
		{name: "stale hash", description: "Go required.\nRust optional.", rules: 59, extracted: 80},
		{name: "stale ID", description: "Go required.", rules: 59, extracted: 80},
		{name: "closed", description: "Go required.", rules: 59, extracted: 80, closed: true, drop: true},
	} {
		job := insertShadowJob(t, ctx, tx, tc.description, tc.closed)
		x := storeShadowAnswer(t, ctx, tx, models.JobExtraction{
			JobID: job, Model: model, PromptVersion: prompt, TextHash: extraction.Hash(shadowPostingTitle, tc.description),
			Status: "ok", Extraction: &models.Extraction{Seniority: models.ExtractedSeniority{Level: "unspecified"}, SeniorityDropped: tc.drop},
		})
		shadow := models.ShadowEvaluation{
			ExtractionID: x.ID, TextHash: x.TextHash, AnsweredAt: x.CreatedAt.UTC(),
			RulesScore: tc.rules, RulesRejected: tc.blocked, RulesParts: tc.rulesParts,
			Evaluation: models.Evaluation{Score: tc.extracted, Breakdown: tc.extractedParts, MatchedSkills: tc.extractedSkills},
		}
		if _, err := saveJobEvaluation(ctx, tx, other, "", JobEvaluation{JobID: job, Shadow: &shadow}); err != nil {
			t.Fatal(err)
		}
		switch tc.name {
		case "stale hash":
			shadow.TextHash = extraction.Hash(shadowPostingTitle, "Old description.")
		case "stale ID":
			shadow.ExtractionID = other
		}
		// The match shows parts of another scoring; the summary must not read them.
		rules := models.Evaluation{Score: tc.rules, Rejected: tc.blocked, Breakdown: parts(1, 2), MatchedSkills: []string{"Kotlin", "Java", "AWS"}}
		if _, err := saveJobEvaluation(ctx, tx, user, "", JobEvaluation{JobID: job, Evaluation: rules, Shadow: &shadow}); err != nil {
			t.Fatal(err)
		}
		// The API writes neither shape; the summary must still answer.
		change := map[string]string{
			"tie, parts without seniority": `shadow_evaluation #- '{rules_parts,seniority}'`,
			"middle positive, null parts":  `jsonb_set(shadow_evaluation, '{rules_parts}', 'null')`,
		}[tc.name]
		if change != "" {
			if _, err := tx.Exec(ctx, `UPDATE job_matches SET shadow_evaluation=`+change+` WHERE user_id=$1 AND job_id=$2`, user, job); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		description, reason string
		closed              bool
	}{
		{"Go required.", "not_json", false},
		{"Go required.\nRust optional.", "unknown_field", false},
		{"Go required.", "not_json", true},
	} {
		job := insertShadowJob(t, ctx, tx, tc.description, tc.closed)
		storeShadowAnswer(t, ctx, tx, models.JobExtraction{
			JobID: job, Model: model, PromptVersion: prompt, TextHash: extraction.Hash(shadowPostingTitle, tc.description),
			Status: "rejected", Reason: tc.reason,
		})
	}
	insertShadowJob(t, ctx, tx, "Go required.\nRust optional.", false)

	got, err := extractionSummary(ctx, tx, user, model, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if got.OpenJobs < 14 || got.SingleLine.OpenJobs < 9 || got.MultiLine.OpenJobs < 5 ||
		got.OpenJobs != got.SingleLine.OpenJobs+got.MultiLine.OpenJobs {
		t.Fatalf("catalog totals = %+v", got)
	}
	want := models.ExtractionSummary{
		Model: model, PromptVersion: prompt, OpenJobs: got.OpenJobs,
		CurrentExtractions: 11, DroppedSeniority: 1, RejectedAnswers: map[string]int{"not_json": 1, "unknown_field": 1},
		SingleLine: models.ExtractionTexts{OpenJobs: got.SingleLine.OpenJobs, Answered: 9, Rejected: 1},
		MultiLine:  models.ExtractionTexts{OpenJobs: got.MultiLine.OpenJobs, Answered: 4, Rejected: 1},
		Compared:   9, MinScore: 60, WouldReachMinScore: 1, WouldFallBelowMinScore: 1,
		ScoreDifferences: []models.ScoreDifferences{
			{Min: -100, Max: -20, Count: 1}, {Min: -19, Max: -10, Count: 1}, {Min: -9, Max: -1, Count: 1},
			{Min: 0, Max: 0, Count: 1}, {Min: 1, Max: 9, Count: 2}, {Min: 10, Max: 19, Count: 1}, {Min: 20, Max: 100, Count: 2},
		},
		// Not split: the comparison stored before the parts were, and the one whose parts are null.
		// The tie's parts lack the rules' seniority, which then counts as zero.
		DifferenceParts: []models.DifferenceParts{
			{Min: -100, Max: -20, Matches: 1, Skill: -20, Seniority: -5, RulesMatchedSkills: 2, ExtractionMatchedNone: 1},
			{Min: -19, Max: -10, Matches: 1, Skill: -15, Other: -4, RulesMatchedSkills: 1, ExtractionMatchedSkills: 1},
			{Min: -9, Max: -1},
			{Min: 0, Max: 0, Matches: 1, Skill: -5, Seniority: 10, Other: -5},
			{Min: 1, Max: 9, Matches: 2, Skill: 5, Seniority: 9, RulesMatchedSkills: 2, ExtractionMatchedNone: 1},
			{Min: 10, Max: 19},
			{Min: 20, Max: 100, Matches: 2, Skill: 36, Other: 5, RulesMatchedSkills: 2, ExtractionMatchedSkills: 3},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
	if got, err := extractionSummary(ctx, tx, other, model, prompt); err != nil || got.MinScore != 75 || got.Compared != 11 {
		t.Fatalf("second account's summary = %+v, %v", got, err)
	}
	empty, err := extractionSummary(ctx, tx, user, "", prompt)
	if err != nil || empty.RejectedAnswers == nil || len(empty.RejectedAnswers) != 0 || empty.Compared != 0 {
		t.Fatalf("summary without a model = %+v, %v", empty, err)
	}
	for i := range want.ScoreDifferences {
		want.ScoreDifferences[i].Count = 0
	}
	if !slices.Equal(empty.ScoreDifferences, want.ScoreDifferences) {
		t.Fatalf("empty ranges = %+v, want %+v", empty.ScoreDifferences, want.ScoreDifferences)
	}
	for i, part := range want.DifferenceParts {
		want.DifferenceParts[i] = models.DifferenceParts{Min: part.Min, Max: part.Max}
	}
	if !slices.Equal(empty.DifferenceParts, want.DifferenceParts) {
		t.Fatalf("empty parts = %+v, want %+v", empty.DifferenceParts, want.DifferenceParts)
	}
}

func TestExtractionSummaryExcludesReplacedAnswerAgainstPostgres(t *testing.T) {
	ctx, tx := shadowStoreTx(t)
	user := insertShadowUser(t, ctx, tx)
	const description = "Go required."
	job := insertShadowJob(t, ctx, tx, description, false)
	x := storeShadowAnswer(t, ctx, tx, models.JobExtraction{
		JobID: job, Model: "replace-" + user, PromptVersion: extraction.PromptVersion,
		TextHash: extraction.Hash(shadowPostingTitle, description), Status: "ok", Extraction: &models.Extraction{},
	})
	// Seed an older answer time because now() is constant throughout the test transaction.
	if err := tx.QueryRow(ctx, `UPDATE job_extractions SET created_at=now()-interval '1 hour'
		WHERE id=$1 RETURNING created_at`, x.ID).Scan(&x.CreatedAt); err != nil {
		t.Fatal(err)
	}
	shadow := &models.ShadowEvaluation{
		ExtractionID: x.ID, TextHash: x.TextHash, AnsweredAt: x.CreatedAt.UTC(), RulesScore: 70, Evaluation: models.Evaluation{Score: 80},
	}
	if _, err := saveJobEvaluation(ctx, tx, user, "", JobEvaluation{JobID: job, Shadow: shadow}); err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		got, err := extractionSummary(ctx, tx, user, x.Model, x.PromptVersion)
		if err != nil || got.CurrentExtractions != 1 || got.Compared != want || got.WouldReachMinScore != want {
			t.Fatalf("summary = %+v, %v, want one answer and %d comparisons crossing the threshold", got, err, want)
		}
	}
	check(1)
	replaced := storeShadowAnswer(t, ctx, tx, x)
	if replaced.ID != x.ID || replaced.TextHash != x.TextHash || !replaced.CreatedAt.After(x.CreatedAt) {
		t.Fatalf("replacement = %+v, original %+v", replaced, x)
	}
	check(0)
	shadow.AnsweredAt = replaced.CreatedAt.UTC()
	if err := saveShadowEvaluation(ctx, tx, user, job, shadow); err != nil {
		t.Fatal(err)
	}
	check(1)
}
