package discovery

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/matching"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

func TestShadowScoringOffNeedsNoStore(t *testing.T) {
	ctx := context.Background()
	for _, s := range []*Service{nil, {}} {
		if answers, err := s.CurrentExtractions(ctx, "example-id"); answers != nil || err != nil {
			t.Fatalf("CurrentExtractions() = %v, %v", answers, err)
		}
		if err := s.RescoreShadow(ctx, "example-id"); err != nil {
			t.Fatal(err)
		}
	}
}

func shadowFixtureStore(t *testing.T) (context.Context, *store.Store) {
	t.Helper()
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated database to run this")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, store.New(pool)
}

func shadowFixtureUser(t *testing.T, ctx context.Context, st *store.Store, name string) string {
	t.Helper()
	u, err := st.CreateUser(ctx, name+"-"+time.Now().Format("20060102150405.000000000")+"@example.test", "unused")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := st.DB.Exec(ctx, `DELETE FROM users WHERE id=$1`, u.ID); err != nil {
			t.Error(err)
		}
	})
	p := models.Profile{Name: "Alex Example", DesiredTitles: []string{"Backend Engineer"}}
	if err := st.SaveProfile(ctx, u.ID, p); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddSkill(ctx, u.ID, models.Skill{Name: "Go"}); err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func shadowFixtureJob(t *testing.T, ctx context.Context, st *store.Store, title string, closed bool) models.Job {
	t.Helper()
	job := models.Job{JobInput: models.JobInput{
		Title: title, Company: "Example GmbH", Description: "Go or Rust required.",
	}}
	err := st.DB.QueryRow(ctx, `INSERT INTO jobs (title, company, company_normalized, description, discovered_at, posted_at, closed_at)
		VALUES ($1,$2,'example gmbh',$3,now(),'2001-01-01T00:00:00Z',CASE WHEN $4 THEN now() END) RETURNING id`,
		job.Title, job.Company, job.Description, closed).Scan(&job.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := st.DB.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, job.ID); err != nil {
			t.Error(err)
		}
	})
	return job
}

func shadowFixtureAnswer(job models.Job, model string) models.JobExtraction {
	return models.JobExtraction{
		JobID: job.ID, Model: model, PromptVersion: extraction.PromptVersion,
		TextHash: extraction.Hash(job.Title, job.Description), Status: "ok",
		Extraction: &models.Extraction{
			Required:  []models.ExtractedSkills{{Skills: []string{"Go", "Rust"}, Quote: job.Description}},
			Seniority: models.ExtractedSeniority{Level: "unspecified"},
		},
	}
}

func saveFixtureAnswer(t *testing.T, ctx context.Context, st *store.Store, answer models.JobExtraction) {
	t.Helper()
	if saved, err := st.SaveExtraction(ctx, answer); err != nil || !saved {
		t.Fatalf("SaveExtraction() = %v, %v", saved, err)
	}
}

type shadowMatchSnapshot struct {
	match  string
	shadow string
}

func readShadowMatch(ctx context.Context, st *store.Store, userID, jobID string) (shadowMatchSnapshot, error) {
	var row shadowMatchSnapshot
	err := st.DB.QueryRow(ctx, `SELECT (to_jsonb(m)-'shadow_evaluation')::text, COALESCE(m.shadow_evaluation::text, '')
		FROM job_matches m WHERE user_id=$1 AND job_id=$2`, userID, jobID).Scan(&row.match, &row.shadow)
	return row, err
}

func TestSaveRunMatchShadowAgainstPostgres(t *testing.T) {
	ctx, st := shadowFixtureStore(t)
	job := shadowFixtureJob(t, ctx, st, "Backend Engineer", true)
	model := "run-match-shadow-" + job.ID
	answer := shadowFixtureAnswer(job, model)
	saveFixtureAnswer(t, ctx, st, answer)
	for _, tc := range []struct {
		name                       string
		duplicate, on, changedText bool
		wantShadow                 bool
	}{
		{name: "known", duplicate: true, on: true, wantShadow: true},
		{name: "changed text", duplicate: true, on: true, changedText: true},
		{name: "new", on: true},
		{name: "off", duplicate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID := shadowFixtureUser(t, ctx, st, "run-match")
			profile, err := st.Profile(ctx, userID)
			if err != nil {
				t.Fatal(err)
			}
			input := job
			if tc.changedText {
				input.Description = "Go and SQL required."
			}
			svc := &Service{Store: st, ExtractionModel: model, ShadowScoring: tc.on}
			got, err := svc.saveRunMatch(ctx, userID, "", profile, nil, nil, input, tc.duplicate)
			want := matching.Evaluate(profile, nil, nil, input.JobInput, time.Now())
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("saveRunMatch() = %+v, %v, want rules evaluation %+v", got, err, want)
			}
			row, err := readShadowMatch(ctx, st, userID, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.wantShadow {
				if row.shadow != "" {
					t.Fatalf("stored shadow = %s, want SQL NULL", row.shadow)
				}
				return
			}
			var shadow models.ShadowEvaluation
			if err := json.Unmarshal([]byte(row.shadow), &shadow); err != nil {
				t.Fatal(err)
			}
			if shadow.ExtractionID == "" || shadow.TextHash != answer.TextHash || shadow.RulesScore != got.Score || shadow.RulesRejected != got.Rejected {
				t.Fatalf("stored shadow = %+v, want comparison with the current answer and rules evaluation", shadow)
			}
		})
	}
}

func TestSaveRunMatchWaitsForMatchingLockAgainstPostgres(t *testing.T) {
	ctx, st := shadowFixtureStore(t)
	user := shadowFixtureUser(t, ctx, st, "run-match-lock")
	job := shadowFixtureJob(t, ctx, st, "Backend Engineer", true)
	model := "run-match-lock-" + job.ID
	svc := &Service{Store: st, ExtractionModel: model, ShadowScoring: true}
	saveFixtureAnswer(t, ctx, st, shadowFixtureAnswer(job, model))

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	unlock := svc.LockMatching(user)
	started, done := make(chan struct{}), make(chan struct{})
	var saveErr error
	go func() {
		close(started)
		_, saveErr = svc.saveRunMatch(ctx, user, "", models.Profile{}, nil, nil, job, true)
		close(done)
	}()
	defer func() {
		if unlock != nil {
			unlock()
		}
		<-done
	}()
	<-started
	assertNoMatch := func() {
		t.Helper()
		var exists bool
		err := st.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM job_matches WHERE user_id=$1 AND job_id=$2)`, user, job.ID).Scan(&exists)
		if err != nil || exists {
			t.Fatalf("while locked, match exists = %t, %v", exists, err)
		}
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
waiting:
	for {
		select {
		case <-done:
			t.Fatalf("saveRunMatch returned while the lock was held: %v", saveErr)
		case <-deadline.C:
			assertNoMatch()
			break waiting
		case <-ticker.C:
			assertNoMatch()
		}
	}
	unlock()
	unlock = nil
	select {
	case <-done:
		if saveErr != nil {
			t.Fatal(saveErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("saveRunMatch still blocked after unlocking")
	}
	if row, err := readShadowMatch(ctx, st, user, job.ID); err != nil || row.shadow == "" {
		t.Fatalf("after unlock, shadow = %q, %v", row.shadow, err)
	}
}

func TestScoreCatalogShadowAgainstPostgres(t *testing.T) {
	ctx, st := shadowFixtureStore(t)
	on := shadowFixtureUser(t, ctx, st, "shadow-on")
	off := shadowFixtureUser(t, ctx, st, "shadow-off")
	marker := "catalog" + strconv.FormatInt(time.Now().UnixNano(), 10)
	answered := shadowFixtureJob(t, ctx, st, "Backend Engineer "+marker, false)
	shadowFixtureJob(t, ctx, st, "Platform Engineer "+marker, false)
	model := "catalog-shadow-" + answered.ID
	saveFixtureAnswer(t, ctx, st, shadowFixtureAnswer(answered, model))

	visible := make([][]models.JobWithMatch, 0, 2)
	for _, tc := range []struct {
		userID string
		on     bool
	}{{on, true}, {off, false}} {
		svc := &Service{Store: st, ExtractionModel: model, ShadowScoring: tc.on}
		if _, err := svc.ScoreCatalog(ctx, tc.userID); err != nil {
			t.Fatal(err)
		}
		jobs, _, err := st.Jobs(ctx, tc.userID, store.JobQuery{Search: marker})
		if err != nil || len(jobs) != 2 {
			t.Fatalf("Jobs() = %d, %v, want two fixture jobs", len(jobs), err)
		}
		for i := range jobs {
			job := &jobs[i]
			row, err := readShadowMatch(ctx, st, tc.userID, job.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.on && job.Job.ID == answered.ID {
				var shadow models.ShadowEvaluation
				if err := json.Unmarshal([]byte(row.shadow), &shadow); err != nil {
					t.Fatal(err)
				}
				if shadow.TextHash != extraction.Hash(answered.Title, answered.Description) || shadow.ExtractionID == "" ||
					shadow.RulesScore != job.Match.Score || shadow.RulesRejected != job.Match.Rejected || shadow.AnsweredAt.IsZero() {
					t.Fatalf("stored shadow = %+v", shadow)
				}
			} else if row.shadow != "" {
				t.Fatalf("unexpected shadow for %s with shadow scoring=%t: %s", job.Job.ID, tc.on, row.shadow)
			}
			job.MatchID = ""
		}
		visible = append(visible, jobs)
	}
	if !reflect.DeepEqual(visible[0], visible[1]) {
		t.Fatalf("Jobs() differs with shadow scoring: %+v vs %+v", visible[0], visible[1])
	}
}

func TestRescoreShadowAgainstPostgres(t *testing.T) {
	ctx, st := shadowFixtureStore(t)
	first := shadowFixtureUser(t, ctx, st, "rescore-first")
	second := shadowFixtureUser(t, ctx, st, "rescore-second")
	a := shadowFixtureJob(t, ctx, st, "Backend Engineer", true)
	b := shadowFixtureJob(t, ctx, st, "Platform Engineer", true)
	model := "rescore-shadow-" + a.ID
	svc := &Service{Store: st, ExtractionModel: model, ShadowScoring: true}
	answer := shadowFixtureAnswer(a, model)
	saveFixtureAnswer(t, ctx, st, answer)
	saveFixtureAnswer(t, ctx, st, shadowFixtureAnswer(b, model))
	if _, err := st.DB.Exec(ctx, `UPDATE job_extractions SET created_at=now()-interval '1 hour' WHERE job_id=$1 AND model=$2`, a.ID, model); err != nil {
		t.Fatal(err)
	}
	original, err := svc.CurrentExtractions(ctx, a.ID, b.ID)
	if err != nil || len(original) != 2 {
		t.Fatalf("current answers = %d, %v, want two", len(original), err)
	}
	old := &models.ShadowEvaluation{
		RulesScore: 12, Evaluation: models.Evaluation{Score: 34},
	}
	match := models.Evaluation{
		Score: 12, Warnings: []string{"Example warning"}, Reasons: []string{"Example reason"},
		MatchedSkills: []string{"Go"}, MissingSkills: []string{"Rust"}, Breakdown: map[string]int{"skills": 10},
	}
	read := func(userID, jobID string) shadowMatchSnapshot {
		t.Helper()
		row, err := readShadowMatch(ctx, st, userID, jobID)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	for _, item := range []struct{ userID, jobID string }{{first, a.ID}, {second, a.ID}, {first, b.ID}} {
		x := original[item.jobID]
		old.ExtractionID, old.TextHash, old.AnsweredAt = x.ID, x.TextHash, x.CreatedAt.UTC()
		if _, err := st.SaveJobEvaluation(ctx, item.userID, "", store.JobEvaluation{
			JobID: item.jobID, Evaluation: match, Shadow: old,
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := map[string]shadowMatchSnapshot{first: read(first, a.ID), second: read(second, a.ID)}
	bBefore := read(first, b.ID)

	if _, err := st.SaveAnswer(ctx, first, models.AnswerEntry{
		QuestionKey: "motivation", QuestionText: "Why this role?", QuestionNormalized: "why this role?",
		Answer: "I build reliable services.", RoleFamily: "backend", Source: "ai",
	}); err != nil {
		t.Fatal(err)
	}
	notifications := func() (string, int) {
		t.Helper()
		var rows string
		var open int
		err := st.DB.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(n) ORDER BY n.id), '[]')::text,
			count(*) FILTER (WHERE resolved_at IS NULL) FROM notifications n WHERE user_id=$1`, first).Scan(&rows, &open)
		if err != nil {
			t.Fatal(err)
		}
		return rows, open
	}
	notificationBefore, open := notifications()
	if open == 0 {
		t.Fatal("the answer fixture did not open a notification")
	}

	saveFixtureAnswer(t, ctx, st, answer)
	answers, err := svc.CurrentExtractions(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, ok := answers[a.ID]
	if !ok || !current.CreatedAt.After(original[a.ID].CreatedAt) {
		t.Fatal("missing replacement answer")
	}
	var held string
	visits := map[string]int{}
	lock := func(userID string) func() {
		if held != "" {
			t.Fatalf("taking %s while %s is locked", userID, held)
		}
		held = userID
		visits[userID]++
		if got := read(userID, a.ID); got != before[userID] {
			t.Fatalf("match changed before the lock: %+v, want %+v", got, before[userID])
		}
		return func() {
			got := read(userID, a.ID)
			var shadow models.ShadowEvaluation
			if err := json.Unmarshal([]byte(got.shadow), &shadow); err != nil {
				t.Fatal(err)
			}
			if got.match != before[userID].match || shadow.ExtractionID != current.ID ||
				shadow.TextHash != current.TextHash || !shadow.AnsweredAt.Equal(current.CreatedAt) {
				t.Fatalf("at unlock, match or shadow = %+v", got)
			}
			held = ""
		}
	}
	if err := svc.rescoreShadow(ctx, a.ID, lock); err != nil {
		t.Fatal(err)
	}
	if held != "" || !reflect.DeepEqual(visits, map[string]int{first: 1, second: 1}) {
		t.Fatalf("lock visits = %v, still held = %q", visits, held)
	}
	if got := read(first, b.ID); got != bBefore {
		t.Fatalf("rescoring A changed B: %+v, want %+v", got, bBefore)
	}
	if got, _ := notifications(); got != notificationBefore {
		t.Fatalf("notifications changed: %s, want %s", got, notificationBefore)
	}
	answer.Status, answer.Reason, answer.Extraction = "rejected", "not_json", nil
	saveFixtureAnswer(t, ctx, st, answer)
	if err := svc.RescoreShadow(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{first, second} {
		if got := read(userID, a.ID); got.match != before[userID].match || got.shadow != "" {
			t.Fatalf("after rejection, match = %+v, want unchanged match and SQL NULL shadow", got)
		}
	}
	if got, _ := notifications(); got != notificationBefore {
		t.Fatalf("notifications after rejection = %s, want %s", got, notificationBefore)
	}
}

func TestRescoreShadowWaitsForMatchingLockAgainstPostgres(t *testing.T) {
	ctx, st := shadowFixtureStore(t)
	user := shadowFixtureUser(t, ctx, st, "shadow-lock")
	job := shadowFixtureJob(t, ctx, st, "Backend Engineer", true)
	model := "lock-shadow-" + job.ID
	svc := &Service{Store: st, ExtractionModel: model, ShadowScoring: true}
	if _, err := st.SaveJobMatch(ctx, user, job.ID, models.Evaluation{Score: 12}, ""); err != nil {
		t.Fatal(err)
	}
	saveFixtureAnswer(t, ctx, st, shadowFixtureAnswer(job, model))

	unlock := svc.LockMatching(user)
	started, done := make(chan struct{}), make(chan struct{})
	var rescoreErr error
	go func() {
		close(started)
		rescoreErr = svc.RescoreShadow(ctx, job.ID)
		close(done)
	}()
	defer func() {
		if unlock != nil {
			unlock()
		}
		<-done
	}()
	<-started
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
waiting:
	for {
		select {
		case <-done:
			t.Fatalf("RescoreShadow returned while the lock was held: %v", rescoreErr)
		case <-deadline.C:
			break waiting
		case <-ticker.C:
			row, err := readShadowMatch(ctx, st, user, job.ID)
			if err != nil || row.shadow != "" {
				t.Fatalf("while locked, shadow = %q, %v", row.shadow, err)
			}
		}
	}
	unlock()
	unlock = nil
	select {
	case <-done:
		if rescoreErr != nil {
			t.Fatal(rescoreErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RescoreShadow still blocked after unlocking")
	}
	if row, err := readShadowMatch(ctx, st, user, job.ID); err != nil || row.shadow == "" {
		t.Fatalf("after unlock, shadow = %q, %v", row.shadow, err)
	}
}

func TestSaveRunMatchTakesNoLockWithoutShadowAgainstPostgres(t *testing.T) {
	ctx, st := shadowFixtureStore(t)
	user := shadowFixtureUser(t, ctx, st, "run-match-no-lock")
	job := shadowFixtureJob(t, ctx, st, "Backend Engineer", true)
	model := "run-match-no-lock-" + job.ID
	saveFixtureAnswer(t, ctx, st, shadowFixtureAnswer(job, model))

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, tc := range []struct {
		name      string
		shadow    bool
		duplicate bool
	}{{"setting off", false, true}, {"new job", true, false}} {
		svc := &Service{Store: st, ExtractionModel: model, ShadowScoring: tc.shadow}
		unlock := svc.LockMatching(user)
		done := make(chan error, 1)
		go func() {
			_, err := svc.saveRunMatch(ctx, user, "", models.Profile{}, nil, nil, job, tc.duplicate)
			done <- err
		}()
		// The lock is still held here, so returning proves the save did not wait for it.
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		case <-time.After(5 * time.Second):
			unlock()
			t.Fatalf("%s: saveRunMatch waited for the matching lock", tc.name)
		}
		unlock()
		row, err := readShadowMatch(ctx, st, user, job.ID)
		if err != nil || row.shadow != "" {
			t.Fatalf("%s: shadow = %q, %v, want none", tc.name, row.shadow, err)
		}
	}
}
