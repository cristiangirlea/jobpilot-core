package generator

import (
	"context"
	"fmt"
	"strings"

	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

// Sources a cover letter can come from.
const (
	LetterSourceAI       = "ai"
	LetterSourceTemplate = "template"
)

const letterSystemPrompt = `You are writing a short cover letter for a job application on behalf of a candidate.

Hard rules:
- Use ONLY the candidate facts supplied below. Never invent or imply employers, titles, dates, metrics, achievements, qualifications, or skills.
- Never claim experience with a technology that is not in the candidate's skills or history. If the role asks for something the candidate lacks, simply do not mention it. Do not apologise for it and do not claim to be "eager to learn" it as though it were experience.
- Do not flatter the company or speculate about its mission, products, or culture beyond what the posting states.
- Three or four short paragraphs. First person. Plain prose, no markdown, no bullet points.
- Open with the role being applied for. Close with a plain sign-off using the candidate's name.
- Return only the letter text.`

// CoverLetter writes the letter that accompanies an application.
//
// With no provider configured it returns the deterministic letter, which is why
// the product still works with AI switched off. The factual contract is the
// same either way: only stored candidate facts may appear.
func CoverLetter(ctx context.Context, client *llm.Client, profile models.Profile, job models.Job, match models.Evaluation) (string, string) {
	if client.Enabled() {
		prompt := fmt.Sprintf(`CANDIDATE FACTS
%s

ROLE
Title: %s
Company: %s
Location: %s
Remote policy: %s

POSTING
%s

THE CANDIDATE'S OWN SKILLS THAT THIS ROLE ASKS FOR
%s

Write the cover letter.`,
			letterFacts(profile),
			clean(job.Title, "the role"),
			clean(job.Company, "your company"),
			clean(job.Location, "unspecified"),
			clean(job.RemoteType, "unspecified"),
			trimTo(stripMarkup(job.Description), 2500),
			joinOrNone(match.MatchedSkills),
		)
		result, err := client.Complete(ctx, llm.Request{
			Task: "cover_letter", System: letterSystemPrompt, Prompt: prompt,
			MaxTokens: 900, Temperature: 0.3,
		})
		if err == nil && strings.TrimSpace(result.Text) != "" {
			return strings.TrimSpace(result.Text), LetterSourceAI
		}
	}
	return Generate(profile, job, match).CoverLetter, LetterSourceTemplate
}

// letterFacts renders the only facts a provider may draw on. It mirrors the
// contract used elsewhere: one definition of what a model is allowed to see.
func letterFacts(p models.Profile) string {
	var b strings.Builder
	line := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			fmt.Fprintf(&b, "%s: %s\n", label, value)
		}
	}
	line("Name", p.Name)
	line("Current title", p.CurrentTitle)
	if p.YearsExperience > 0 {
		fmt.Fprintf(&b, "Years of experience: %.0f\n", p.YearsExperience)
	}
	line("Location", p.Location)
	line("Summary", p.Summary)
	line("Work authorization", p.WorkAuthorization)
	line("Notice period", p.NoticePeriod)

	names := make([]string, 0, len(p.Skills))
	for _, skill := range p.Skills {
		if skill.Years > 0 {
			names = append(names, fmt.Sprintf("%s (%.0fy)", skill.Name, skill.Years))
			continue
		}
		names = append(names, skill.Name)
	}
	line("Skills", strings.Join(names, ", "))

	for index, x := range p.Experiences {
		if index >= 6 {
			break
		}
		if x.Title == "" && x.Company == "" {
			continue
		}
		fmt.Fprintf(&b, "Experience: %s at %s (%s - %s). %s\n",
			x.Title, x.Company, clean(x.StartDate, "?"), clean(x.EndDate, "present"), trimTo(x.Description, 400))
	}
	for index, project := range p.Projects {
		if index >= 3 || project.Name == "" {
			continue
		}
		fmt.Fprintf(&b, "Project: %s. %s\n", project.Name, trimTo(project.Description, 200))
	}
	if b.Len() == 0 {
		return "(no candidate facts recorded)"
	}
	return strings.TrimRight(b.String(), "\n")
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "(none recorded)"
	}
	return strings.Join(items, ", ")
}

func trimTo(v string, limit int) string {
	v = strings.TrimSpace(v)
	if len(v) <= limit {
		return v
	}
	return strings.TrimSpace(v[:limit]) + "…"
}

// stripMarkup flattens the HTML most job boards return, so a posting reaches a
// provider as prose rather than tags.
func stripMarkup(v string) string {
	var b strings.Builder
	depth := 0
	for _, r := range v {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
				b.WriteByte(' ')
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
