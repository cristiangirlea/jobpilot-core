package generator

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"jobpilot/core/api/internal/models"
)

func TestPrepareInterview(t *testing.T) {
	cv := "  CV as sent\nGo services\n"
	sent := &InterviewDocuments{OutboxID: "outbox", CVText: &cv, CoverLetter: " Dear team,\n\nGo.  ", Answers: []models.OutboxAnswer{
		{Field: "why", Label: "Why?", Answer: "  My own words.\n", Source: "human"},
	}}
	posting := models.JobWithMatch{Job: models.Job{JobInput: models.JobInput{Title: "Engineer", Company: "Example", Description: "Build Go services with PostgreSQL. Kafka is useful.", RemoteType: "remote"}}, Match: models.Evaluation{
		MatchedSkills: []string{"Go", "PostgreSQL"}, MissingSkills: []string{"Kafka"}, Reasons: []string{"Relevant skills"}, Warnings: []string{"Salary unspecified"},
	}}
	profile := models.Profile{Experiences: []models.WorkExperience{
		{ID: "work", Title: "Engineer", Company: "Past", Description: "Built Go services."},
		{ID: "unrelated", Description: "Going places; trusted colleague."},
	}, Projects: []models.Project{{ID: "project", Name: "Ledger", Description: "A PostgreSQL store", Skills: []string{"PostgreSQL"}}}}
	for _, status := range []string{"interview", "draft", "ready", "applied", "rejected", "offer", "withdrawn", ""} {
		t.Run(status, func(t *testing.T) {
			app := models.Application{ID: "app", Status: status}
			pack, err := PrepareInterview(app, profile, posting, sent)
			if status != "interview" {
				if !errors.Is(err, ErrInterviewStatus) || !strings.Contains(err.Error(), "interview") {
					t.Fatalf("want clear status refusal, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pack.SentDocuments, sent) || !reflect.DeepEqual(pack.Posting, posting) {
				t.Fatalf("stored posting and sent text must survive verbatim: %+v", pack)
			}
			if len(pack.STARPrompts) != 2 || len(pack.GapPrompts) != 1 || pack.GapPrompts[0].Gap != "Kafka" {
				t.Fatalf("want two evidence mappings and one gap: %+v", pack)
			}
			for _, prompt := range pack.STARPrompts {
				if prompt.Situation == "" || prompt.Task == "" || prompt.Action == "" || !strings.Contains(prompt.Result, "real") {
					t.Fatalf("STAR must ask for real results: %+v", prompt)
				}
				if prompt.Experience != nil && !reflect.DeepEqual(*prompt.Experience, profile.Experiences[0]) {
					t.Fatal("invented experience")
				}
				if prompt.Project != nil && !reflect.DeepEqual(*prompt.Project, profile.Projects[0]) {
					t.Fatal("invented project")
				}
			}
			if !strings.Contains(pack.GapPrompts[0].Prompt, "do not claim") {
				t.Fatal("gap must not claim a skill")
			}
			if len(pack.Questions) == 0 || !strings.Contains(strings.Join(pack.Questions, " "), "remote") {
				t.Fatal("questions must use posting facts")
			}
			again, _ := PrepareInterview(app, profile, posting, sent)
			if !reflect.DeepEqual(pack, again) {
				t.Fatal("pack is not deterministic")
			}
		})
	}
}

func TestInterviewEvidenceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, description, experience string
		want                          int
	}{
		{"alias", "Build Go services", "Used Golang", 1},
		{"word boundary", "Use Go and Rust", "Going places with trusted peers", 0},
		{"unrelated", "Use Python", "Used Go", 0},
		{"empty", "", "Used Go", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pack, err := PrepareInterview(models.Application{Status: "interview"}, models.Profile{Experiences: []models.WorkExperience{{Description: tc.experience}}}, models.JobWithMatch{Job: models.Job{JobInput: models.JobInput{Description: tc.description}}}, nil)
			if err != nil || len(pack.STARPrompts) != tc.want || pack.SentDocuments != nil || len(pack.Notices) == 0 {
				t.Fatalf("unexpected pack: %+v, %v", pack, err)
			}
		})
	}
}

func TestInterviewLeavesUnsentAnswersOut(t *testing.T) {
	pack, err := PrepareInterview(models.Application{Status: "interview"}, models.Profile{}, models.JobWithMatch{}, &InterviewDocuments{Answers: []models.OutboxAnswer{
		{Field: "draft", Source: "ai", NeedsReview: true, Answer: "Not sent"},
		{Field: "personal", Source: "personal", Answer: "Not sent"},
		{Field: "declaration", Source: "declaration", Answer: "Not sent"},
		{Field: "upload", Source: "upload", Answer: "Not sent"},
		{Field: "empty", Source: "human", Answer: " "},
		{Field: "human", Source: "human", Answer: "  Exact answer\n"},
	}})
	if err != nil || len(pack.SentDocuments.Answers) != 1 || pack.SentDocuments.Answers[0].Answer != "  Exact answer\n" {
		t.Fatalf("only delivered answers belong: %+v, %v", pack, err)
	}
}
