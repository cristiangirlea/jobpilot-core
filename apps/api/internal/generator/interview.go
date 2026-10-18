package generator

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/technology"
)

var ErrInterviewStatus = errors.New("Interview preparation requires an application in interview status.")

type InterviewDocuments struct {
	OutboxID    string                `json:"outbox_id"`
	SentAt      *time.Time            `json:"sent_at,omitempty"`
	CVText      *string               `json:"cv_text"`
	CoverLetter string                `json:"cover_letter"`
	Answers     []models.OutboxAnswer `json:"answers"`
}

type STARPrompt struct {
	Requirement string                 `json:"requirement"`
	Experience  *models.WorkExperience `json:"experience,omitempty"`
	Project     *models.Project        `json:"project,omitempty"`
	Situation   string                 `json:"situation"`
	Task        string                 `json:"task"`
	Action      string                 `json:"action"`
	Result      string                 `json:"result"`
}

type GapPrompt struct {
	Gap    string `json:"gap"`
	Prompt string `json:"prompt"`
}

type InterviewPack struct {
	ApplicationID string              `json:"application_id"`
	Posting       models.JobWithMatch `json:"posting"`
	Requirements  []string            `json:"requirements"`
	SentDocuments *InterviewDocuments `json:"sent_documents"`
	STARPrompts   []STARPrompt        `json:"star_prompts"`
	GapPrompts    []GapPrompt         `json:"gap_prompts"`
	Questions     []string            `json:"questions"`
	Notices       []string            `json:"notices"`
}

func PrepareInterview(app models.Application, profile models.Profile, posting models.JobWithMatch, sent *InterviewDocuments) (InterviewPack, error) {
	if app.Status != "interview" {
		return InterviewPack{}, ErrInterviewStatus
	}
	pack := InterviewPack{
		ApplicationID: app.ID, Posting: posting, SentDocuments: sent,
		Requirements: interviewRequirements(posting), STARPrompts: []STARPrompt{},
		GapPrompts: []GapPrompt{}, Questions: []string{},
		Notices: []string{"The posting and match are the currently stored versions; no application-time snapshot is available. Evidence comes from the current profile. Fill in prompts with real examples and results only."},
	}
	if sent == nil {
		pack.Notices = append(pack.Notices, "No confirmed sent outbox entry is linked to this application. Documents sent outside JobPilot are unavailable.")
	} else {
		docs := *sent
		docs.Answers = []models.OutboxAnswer{}
		for _, answer := range sent.Answers {
			switch answer.Source {
			case "cv", "cover_letter", "declaration", "personal", "upload":
				continue
			case "ai", "template":
				if answer.NeedsReview && !answer.Required {
					continue
				}
			}
			if strings.TrimSpace(answer.Answer) != "" {
				docs.Answers = append(docs.Answers, answer)
			}
		}
		pack.SentDocuments = &docs
		if docs.CVText == nil {
			pack.Notices = append(pack.Notices, "The sent CV text is unavailable: the referenced CV is missing, was changed after sending, or has no extracted text.")
		}
	}
	for _, requirement := range pack.Requirements {
		prompt := STARPrompt{
			Requirement: requirement,
			Situation:   "What was the real context of this example?",
			Task:        "What responsibility or goal did you personally have?",
			Action:      "What did you personally do? Use only actions you actually took.",
			Result:      "What was the real outcome? Supply only results you can substantiate; leave unknown results blank.",
		}
		for _, experience := range profile.Experiences {
			if interviewMentions(experience.Title+" "+experience.Description, requirement) {
				p := prompt
				p.Experience = &experience
				pack.STARPrompts = append(pack.STARPrompts, p)
			}
		}
		for _, project := range profile.Projects {
			if interviewMentions(project.Name+" "+project.Description+" "+strings.Join(project.Skills, " "), requirement) {
				p := prompt
				p.Project = &project
				pack.STARPrompts = append(pack.STARPrompts, p)
			}
		}
		pack.Questions = append(pack.Questions, fmt.Sprintf("The posting mentions %q. What would you expect the person in this role to deliver in this area?", requirement))
	}
	for _, gap := range posting.Match.MissingSkills {
		pack.GapPrompts = append(pack.GapPrompts, GapPrompt{Gap: gap, Prompt: fmt.Sprintf("For the recorded gap %q, state your actual level honestly; do not claim this skill. Is there a real adjacent example you can explain, and what would you need to learn?", gap)})
	}
	if posting.Job.RemoteType != "" {
		pack.Questions = append(pack.Questions, fmt.Sprintf("The posting lists %q working. How does that work for this role?", posting.Job.RemoteType))
	}
	if posting.Job.Location != "" {
		pack.Questions = append(pack.Questions, fmt.Sprintf("The posting gives the location as %q. What attendance is expected?", posting.Job.Location))
	}
	if len(pack.Questions) == 0 && strings.TrimSpace(posting.Job.Description) != "" {
		pack.Questions = append(pack.Questions, fmt.Sprintf("Which responsibilities in this posting would come first, and how would you assess success?\n\n%s", posting.Job.Description))
	}
	return pack, nil
}

func interviewRequirements(posting models.JobWithMatch) []string {
	text := stripMarkup(posting.Job.Description)
	terms := technology.Signature(len(technology.Terms()), text)
	for _, skills := range [][]string{posting.Match.MatchedSkills, posting.Match.MissingSkills} {
		for _, skill := range skills {
			if strings.TrimSpace(skill) == "" || !interviewMentions(text, skill) {
				continue
			}
			found := false
			for _, term := range terms {
				if technology.Canonical(term) == technology.Canonical(skill) {
					found = true
					break
				}
			}
			if !found {
				terms = append(terms, skill)
			}
		}
	}
	sort.Strings(terms)
	return terms
}

func interviewMentions(text, requirement string) bool {
	if technology.Count(strings.ToLower(text), strings.ToLower(requirement)) > 0 {
		return true
	}
	_, ok := technology.Mentions(text)[technology.Canonical(requirement)]
	return ok
}
