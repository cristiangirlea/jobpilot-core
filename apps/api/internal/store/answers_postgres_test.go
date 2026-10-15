package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"jobpilot/core/api/internal/answers"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

// answerScopeFixture is two invented users, Ana with two backend jobs and a
// frontend one, and Bob with a backend job of his own.
type answerScopeFixture struct {
	st                        *Store
	ana, bob                  models.User
	backendA, backendB, front models.Job
	bobsJob                   models.Job
	run                       string
}

func newAnswerScopeFixture(t *testing.T) (context.Context, *pgxpool.Pool, answerScopeFixture) {
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
	f := answerScopeFixture{st: New(pool), run: time.Now().Format("20060102150405.000000")}
	user := func(name string) models.User {
		u, err := f.st.CreateUser(ctx, "scope-"+name+"-"+f.run+"@example.test", "not a real hash")
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	f.ana, f.bob = user("ana"), user("bob")
	job := func(owner models.User, name, title string) models.Job {
		in := models.JobInput{Title: title, Company: "Scope Test " + f.run, Description: "Build things.", ApplyURL: "https://example.test/scope-" + name + "-" + f.run}
		j, err := f.st.CreateDiscoveredJob(ctx, owner.ID, "", "", "", "", "scope-"+name+"-"+f.run, in)
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	f.backendA = job(f.ana, "a", "Backend Engineer")
	f.backendB = job(f.ana, "b", "Backend Engineer")
	f.front = job(f.ana, "front", "Frontend Developer")
	f.bobsJob = job(f.bob, "bob", "Backend Engineer")
	return ctx, pool, f
}

func (f answerScopeFixture) save(t *testing.T, ctx context.Context, user models.User, entry models.AnswerEntry) models.AnswerEntry {
	t.Helper()
	saved, err := f.st.SaveAnswer(ctx, user.ID, entry)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

// The same question holds one answer per scope, the narrowest scope that
// matches a job is found first, and nothing crosses from one user to another.
func TestAnswerScopesPostgres(t *testing.T) {
	ctx, _, f := newAnswerScopeFixture(t)
	const q = "which of the following best describes you"
	entry := func(answer, family, scope, jobID string) models.AnswerEntry {
		return models.AnswerEntry{QuestionKey: "", QuestionText: "Which of the following best describes you?", QuestionNormalized: q,
			Answer: answer, RoleFamily: family, Scope: scope, JobID: jobID, Source: "human", Approved: true}
	}

	if _, err := f.st.SaveAnswer(ctx, f.ana.ID, entry("x", "backend", models.AnswerScopeJob, "")); !errors.Is(err, ErrAnswerScopeNeedsJob) {
		t.Fatalf("a job-only answer without a job must be refused, got %v", err)
	}

	family := f.save(t, ctx, f.ana, entry("family", "backend", "", ""))
	if family.Scope != models.AnswerScopeRoleFamily {
		t.Fatalf("an answer saved without a scope is per role family, as before, got %q", family.Scope)
	}
	global := f.save(t, ctx, f.ana, entry("global", "backend", models.AnswerScopeGlobal, ""))
	only := f.save(t, ctx, f.ana, entry("job a", "backend", models.AnswerScopeJob, f.backendA.ID))
	if family.ID == global.ID || global.ID == only.ID || only.JobID != f.backendA.ID {
		t.Fatalf("each scope holds its own answer: %+v %+v %+v", family, global, only)
	}
	// Saving again into a scope replaces that scope's answer only.
	if again := f.save(t, ctx, f.ana, entry("global again", "frontend", models.AnswerScopeGlobal, "")); again.ID != global.ID || again.Answer != "global again" {
		t.Fatalf("a global answer is one per question, got %+v", again)
	}

	find := func(user models.User, family string, job models.Job) string {
		t.Helper()
		got, found, err := f.st.FindAnswer(ctx, user.ID, q, family, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			return ""
		}
		return got.Answer
	}
	if got := find(f.ana, "backend", f.backendA); got != "job a" {
		t.Fatalf("job A's own answer comes first, got %q", got)
	}
	if got := find(f.ana, "backend", f.backendB); got != "family" {
		t.Fatalf("another backend job gets the role family's answer, got %q", got)
	}
	if got := find(f.ana, "frontend", f.front); got != "global again" {
		t.Fatalf("a job of another family gets the answer for every job, got %q", got)
	}
	if got := find(f.bob, "backend", f.bobsJob); got != "" {
		t.Fatalf("Bob must not find Ana's answers, got %q", got)
	}
	// A role-family answer filed under "general" has always served every
	// family, and it still comes before an answer for every job.
	f.save(t, ctx, f.ana, entry("general family", "general", "", ""))
	if got := find(f.ana, "frontend", f.front); got != "general family" {
		t.Fatalf("a general role-family answer keeps today's behaviour, got %q", got)
	}
	// A draft never hides an approved answer, however narrow the draft.
	draft := entry("job b draft", "backend", models.AnswerScopeJob, f.backendB.ID)
	draft.Approved = false
	f.save(t, ctx, f.ana, draft)
	if got := find(f.ana, "backend", f.backendB); got != "family" {
		t.Fatalf("an unapproved answer for job B must not hide the approved one, got %q", got)
	}

	// Similar reuse offers a job-only answer to its own job alone.
	keyed := func(answer, normalized, scope, jobID string) models.AnswerEntry {
		return models.AnswerEntry{QuestionKey: "why_company", QuestionText: "Why us?", QuestionNormalized: normalized,
			Answer: answer, RoleFamily: "backend", Scope: scope, JobID: jobID, Source: "human", Approved: true}
	}
	f.save(t, ctx, f.ana, keyed("only for a", "why us a", models.AnswerScopeJob, f.backendA.ID))
	f.save(t, ctx, f.ana, keyed("everywhere", "why us", models.AnswerScopeGlobal, ""))
	candidates := func(job models.Job) []string {
		t.Helper()
		list, err := f.st.CandidateAnswers(ctx, f.ana.ID, "why_company", "backend", job.ID, 25)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range list {
			out = append(out, c.Answer)
		}
		slices.Sort(out)
		return out
	}
	if got := candidates(f.backendA); !slices.Equal(got, []string{"everywhere", "only for a"}) {
		t.Fatalf("job A's candidates, got %v", got)
	}
	if got := candidates(f.backendB); !slices.Equal(got, []string{"everywhere"}) {
		t.Fatalf("job B must not be offered job A's answer, got %v", got)
	}
	if list, err := f.st.CandidateAnswers(ctx, f.bob.ID, "why_company", "backend", f.bobsJob.ID, 25); err != nil || len(list) != 0 {
		t.Fatalf("Bob must not be offered Ana's answers, got %v, %v", list, err)
	}
	// Many much-used answers to the same question cannot crowd out this job's
	// or the role family's before the limit.
	for i := range 5 {
		saved := f.save(t, ctx, f.ana, models.AnswerEntry{QuestionKey: "why_company", QuestionText: "Why us?", QuestionNormalized: fmt.Sprintf("why us other %d", i),
			Answer: "other", RoleFamily: "data", Source: "human", Approved: true})
		for range 3 {
			if err := f.st.TouchAnswer(ctx, f.ana.ID, saved.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.save(t, ctx, f.ana, keyed("backend family", "why us family", models.AnswerScopeRoleFamily, ""))
	list, err := f.st.CandidateAnswers(ctx, f.ana.ID, "why_company", "backend", f.backendA.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range list {
		got[c.Scope] = c.Answer
	}
	if len(list) != 3 || got[models.AnswerScopeJob] != "only for a" || got[models.AnswerScopeRoleFamily] != "backend family" || got[models.AnswerScopeGlobal] != "everywhere" {
		t.Fatalf("the limit applies per scope, the family's own answer first, got %+v", list)
	}
}

// Approving an answer can choose its scope: a draft narrows to the job it was
// written for, a scope already holding an answer refuses, and another user's
// answer cannot be touched.
func TestUpdateAnswerScopePostgres(t *testing.T) {
	ctx, _, f := newAnswerScopeFixture(t)
	draft := func(normalized, jobID string) models.AnswerEntry {
		return f.save(t, ctx, f.ana, models.AnswerEntry{QuestionText: normalized, QuestionNormalized: normalized,
			Answer: "draft", RoleFamily: "backend", JobID: jobID, Source: "ai"})
	}

	written := draft("drafted for a", f.backendA.ID)
	if written.Scope != models.AnswerScopeRoleFamily || written.JobID != f.backendA.ID {
		t.Fatalf("a draft keeps the job it was written for, got %+v", written)
	}
	narrowed, err := f.st.UpdateAnswer(ctx, f.ana.ID, written.ID, "", true, models.AnswerScopeJob, "")
	if err != nil || narrowed.Scope != models.AnswerScopeJob || narrowed.JobID != f.backendA.ID || !narrowed.Approved {
		t.Fatalf("narrowing a draft to its own job, got %+v, %v", narrowed, err)
	}
	if moved, err := f.st.UpdateAnswer(ctx, f.ana.ID, written.ID, "", true, models.AnswerScopeJob, f.backendB.ID); err != nil || moved.JobID != f.backendB.ID {
		t.Fatalf("a job-only answer can be moved to another job, got %+v, %v", moved, err)
	}
	if widened, err := f.st.UpdateAnswer(ctx, f.ana.ID, written.ID, "", true, models.AnswerScopeGlobal, ""); err != nil || widened.Scope != models.AnswerScopeGlobal {
		t.Fatalf("widening to every job, got %+v, %v", widened, err)
	}
	if kept, err := f.st.UpdateAnswer(ctx, f.ana.ID, written.ID, "edited", true, "", ""); err != nil || kept.Scope != models.AnswerScopeGlobal || kept.Answer != "edited" {
		t.Fatalf("an edit without a scope keeps the scope, got %+v, %v", kept, err)
	}

	orphan := draft("written by hand", "")
	if _, err := f.st.UpdateAnswer(ctx, f.ana.ID, orphan.ID, "", true, models.AnswerScopeJob, ""); !errors.Is(err, ErrAnswerScopeNeedsJob) {
		t.Fatalf("narrowing to a job nobody named must be refused, got %v", err)
	}
	if after, _, _ := f.st.FindAnswer(ctx, f.ana.ID, "written by hand", "backend", ""); after.Approved || after.Scope != models.AnswerScopeRoleFamily {
		t.Fatalf("a refused update must change nothing, got %+v", after)
	}

	// Two answers to one question: moving one into the other's scope collides.
	f.save(t, ctx, f.ana, models.AnswerEntry{QuestionText: "q", QuestionNormalized: "collide", Answer: "global", Scope: models.AnswerScopeGlobal, Source: "human", Approved: true})
	other := f.save(t, ctx, f.ana, models.AnswerEntry{QuestionText: "q", QuestionNormalized: "collide", Answer: "family", RoleFamily: "backend", Source: "human", Approved: true})
	if _, err := f.st.UpdateAnswer(ctx, f.ana.ID, other.ID, "", true, models.AnswerScopeGlobal, ""); !errors.Is(err, ErrAnswerScopeTaken) {
		t.Fatalf("a scope already holding an answer must refuse, got %v", err)
	}

	if _, err := f.st.UpdateAnswer(ctx, f.bob.ID, other.ID, "", true, models.AnswerScopeGlobal, ""); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("Bob must not update Ana's answer, got %v", err)
	}
	if _, err := f.st.UpdateAnswer(ctx, f.bob.ID, orphan.ID, "", true, models.AnswerScopeJob, ""); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("Bob must not learn that Ana's answer exists, got %v", err)
	}
}

// The resolver serves the narrowest approved scope for each job, over the real
// queries and with no provider configured.
func TestAnswerScopeResolutionPostgres(t *testing.T) {
	ctx, _, f := newAnswerScopeFixture(t)
	service := &answers.Service{Bank: f.st, LLM: llm.New(nil, nil, 0, 0)}
	question := answers.Question{Text: "Which of the following best describes you?"}
	normalized := answers.Normalize(question.Text)
	resolve := func(user models.User, job models.Job) answers.Resolution {
		t.Helper()
		got, err := service.Resolve(ctx, user.ID, question, answers.Subject{Job: job})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	save := func(answer, family, scope, jobID string) {
		f.save(t, ctx, f.ana, models.AnswerEntry{QuestionKey: answers.Classify(question.Text), QuestionText: question.Text,
			QuestionNormalized: normalized, Answer: answer, RoleFamily: family, Scope: scope, JobID: jobID, Source: "human", Approved: true})
	}

	// Role family: a backend answer serves backend jobs, and no others.
	save("an engineer", "backend", models.AnswerScopeRoleFamily, "")
	if got := resolve(f.ana, f.backendB); got.Answer != "an engineer" || got.Source != answers.SourceBank {
		t.Fatalf("role family scope on a backend job, got %+v", got)
	}
	if got := resolve(f.ana, f.front); got.Source != answers.SourceUnanswered {
		t.Fatalf("role family scope must not reach a frontend job, got %+v", got)
	}

	// Every job: reaches the frontend job, while backend jobs keep the narrower answer.
	save("a person", "frontend", models.AnswerScopeGlobal, "")
	if got := resolve(f.ana, f.front); got.Answer != "a person" {
		t.Fatalf("global scope on a frontend job, got %+v", got)
	}
	if got := resolve(f.ana, f.backendB); got.Answer != "an engineer" {
		t.Fatalf("the role family's answer is narrower than every job's, got %+v", got)
	}

	// This job only: job A gets its own answer; job B keeps the family one.
	save("a backend engineer at A", "backend", models.AnswerScopeJob, f.backendA.ID)
	if got := resolve(f.ana, f.backendA); got.Answer != "a backend engineer at A" {
		t.Fatalf("job scope on its own job, got %+v", got)
	}
	if got := resolve(f.ana, f.backendB); got.Answer != "an engineer" {
		t.Fatalf("job A's answer must not reach job B, got %+v", got)
	}

	if got := resolve(f.bob, f.bobsJob); got.Source != answers.SourceUnanswered {
		t.Fatalf("Bob must not be served Ana's answers, got %+v", got)
	}
}

// The migration's backfill, replayed on a copy of answer_bank as migration 008
// left it: existing answers become role-family answers and keep their one
// answer per (question, role family), and running it twice changes nothing.
func TestAnswerScopeBackfillPostgres(t *testing.T) {
	ctx, pool, f := newAnswerScopeFixture(t)
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile("../../../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	before, after := read("008_answer_bank.sql"), read("019_answer_scope.sql")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) // drops the scratch schema with everything in it
	schema := "answer_backfill_" + strings.NewReplacer(".", "_").Replace(f.run)
	for _, statement := range []string{"CREATE SCHEMA " + schema, "SET LOCAL search_path TO " + schema + ", public", before} {
		if _, err := tx.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO answer_bank (user_id,question_text,question_normalized,answer,role_family,approved)
		VALUES ($1,'Q','q','backend answer','backend',true), ($1,'Q','q','general answer','general',true)`, f.ana.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := tx.Exec(ctx, after); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := tx.Query(ctx, `SELECT role_family, scope, scope_key, job_id IS NULL FROM answer_bank ORDER BY role_family`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var family, scope, key string
		var noJob bool
		if err := rows.Scan(&family, &scope, &key, &noJob); err != nil {
			t.Fatal(err)
		}
		if !noJob {
			t.Errorf("a backfilled answer has no job, got one for %s", family)
		}
		got = append(got, family+"/"+scope+"/"+key)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"backend/role_family/family:backend", "general/role_family/family:general"}; !slices.Equal(got, want) {
		t.Fatalf("backfilled rows, got %v want %v", got, want)
	}
	var indexes []string
	rows, err = tx.Query(ctx, `SELECT indexname FROM pg_indexes WHERE schemaname=$1 AND tablename='answer_bank' ORDER BY indexname`, schema)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		indexes = append(indexes, name)
	}
	if !slices.Contains(indexes, "answer_bank_scope_idx") || slices.Contains(indexes, "answer_bank_question_idx") {
		t.Fatalf("the scope index replaces the (question, role family) one, got %v", indexes)
	}
	// The new key still allows one answer per (question, role family) only.
	if _, err := tx.Exec(ctx, `INSERT INTO answer_bank (user_id,question_text,question_normalized,answer,role_family) VALUES ($1,'Q','q','again','backend')`, f.ana.ID); !isUniqueViolation(err) {
		t.Fatalf("a second backend answer to the same question must collide, got %v", err)
	}
}
