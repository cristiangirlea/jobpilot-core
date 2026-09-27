package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

// Found in early use: listing rejected or all jobs downloaded 14.4 MB,
// 45% of the MCP client's response cap, and matches grew by about 160 a day.
// The list now filters, pages and leaves descriptions out on the server. This
// runs the query on a migrated database named by JOBPILOT_TEST_DATABASE_URL,
// and is skipped without one; CI's integration job provides it.
func TestJobsQueryAgainstPostgres(t *testing.T) {
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated database to run this")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := New(pool)

	run := time.Now().Format("20060102150405.000000")
	user, err := st.CreateUser(ctx, "jobs-query-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	add := func(name string, score int, rejected, closed bool) {
		in := models.JobInput{Title: name, Company: "Query Test " + run, Description: "The description of " + name + "."}
		job, err := st.CreateDiscoveredJob(ctx, user.ID, "", "", "", "", "query-"+run+"-"+name, in)
		if err != nil {
			t.Fatal(err)
		}
		evaluation := models.Evaluation{Score: score, Rejected: rejected, Breakdown: map[string]int{}, RejectionReasons: []string{}, Warnings: []string{}, Reasons: []string{}, MatchedSkills: []string{}, MissingSkills: []string{}, RedFlags: []string{}}
		if _, err = st.SaveJobMatch(ctx, user.ID, job.ID, evaluation, ""); err != nil {
			t.Fatal(err)
		}
		if closed {
			if _, err = st.DB.Exec(ctx, `UPDATE jobs SET closed_at=now(), closed_reason='gone' WHERE id=$1`, job.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("open90", 90, false, false)
	add("closed80", 80, false, true)
	add("open60", 60, false, false)
	add("rejected95", 95, true, false)

	for _, c := range []struct {
		name  string
		query JobQuery
		want  string
		total int
	}{
		// Closed postings sink to the bottom, then rejections, then by score.
		{"everything", JobQuery{}, "open90 open60 rejected95 closed80", 4},
		{"accepted", JobQuery{State: "accepted"}, "open90 open60 closed80", 3},
		{"rejected", JobQuery{State: "rejected"}, "rejected95", 1},
		{"minimum score", JobQuery{MinScore: 70}, "open90 rejected95 closed80", 3},
		{"open only", JobQuery{OpenOnly: true}, "open90 open60 rejected95", 3},
		{"a page", JobQuery{Limit: 2, Offset: 1}, "open60 rejected95", 4},
		{"past the end", JobQuery{Limit: 2, Offset: 10}, "", 4},
	} {
		jobs, total, err := st.Jobs(ctx, user.ID, c.query)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var names []string
		for _, job := range jobs {
			names = append(names, job.Job.Title)
			if job.Job.Description == "" {
				t.Errorf("%s: %s lost its description without summary", c.name, job.Job.Title)
			}
		}
		if got := strings.Join(names, " "); got != c.want || total != c.total {
			t.Errorf("%s: got %q of %d, want %q of %d", c.name, got, total, c.want, c.total)
		}
	}

	jobs, _, err := st.Jobs(ctx, user.ID, JobQuery{Summary: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Job.Description != "" || jobs[0].Job.Title == "" {
		t.Fatalf("summary should keep the job but leave its description out, got %s", fmt.Sprint(jobs))
	}
}
