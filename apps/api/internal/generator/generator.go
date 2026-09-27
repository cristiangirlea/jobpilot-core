package generator

import (
	"fmt"
	"jobpilot/core/api/internal/models"
	"sort"
	"strings"
)

func Generate(p models.Profile, job models.Job, match models.Evaluation) models.Documents {
	name := clean(p.Name, "Candidate")
	title := clean(p.CurrentTitle, "professional")
	company := clean(job.Company, "your company")
	role := clean(job.Title, "the role")
	notice, _ := p.NoticeText()
	skills := allowedSkills(p, match.MatchedSkills)
	skillText := "my relevant background"
	if len(skills) > 0 {
		skillText = strings.Join(skills, ", ")
	}
	experience := experienceLine(p)
	project := projectLine(p)
	recruiter := fmt.Sprintf("Hi, I’m %s, a %s with %.0f years of experience. I’m interested in the %s role at %s. My background in %s appears well aligned. I’d be glad to share more context—thank you for your time.", name, title, p.YearsExperience, role, company, skillText)
	cover := fmt.Sprintf("Dear Hiring Team,\n\nI’m writing to apply for the %s position at %s. I am a %s with %.0f years of experience, with relevant skills including %s.%s%s\n\nThe role stands out because it aligns with my experience and stated career direction. I would welcome the opportunity to discuss how my documented background could contribute to your team.\n\nSincerely,\n%s", role, company, title, p.YearsExperience, skillText, experience, project, name)
	summary := fmt.Sprintf("%s is a %s with %.0f years of experience. Key relevant skills include %s.%s", name, title, p.YearsExperience, skillText, experience)
	answers := map[string]string{
		"Tell us about yourself.":              fmt.Sprintf("I’m a %s with %.0f years of experience. My background includes %s.%s", title, p.YearsExperience, skillText, experience),
		"Why are you interested in this role?": fmt.Sprintf("I’m interested in the %s role because it aligns with my desired direction and my documented experience in %s.", role, skillText),
		"Why are you a good fit?":              fmt.Sprintf("My profile shows relevant experience in %s%s. I would bring that experience to the responsibilities described for this role.", skillText, experience),
		"Salary expectation.":                  expectation(p),
		"Notice period.":                       fallbackSentence(notice, "My notice period is best discussed during the interview process."),
		"Work authorization.":                  fallbackSentence(p.WorkAuthorization, "I’m happy to clarify my work authorization for this location."),
		"Remote work preference.":              preference(p),
	}
	return models.Documents{RecruiterMessage: recruiter, CoverLetter: cover, CandidateSummary: summary, Answers: answers}
}

func allowedSkills(p models.Profile, matched []string) []string {
	have := map[string]string{}
	for _, s := range p.Skills {
		have[strings.ToLower(s.Name)] = s.Name
	}
	var out []string
	for _, s := range matched {
		if x, ok := have[strings.ToLower(s)]; ok {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		for _, s := range p.Skills {
			out = append(out, s.Name)
			if len(out) == 5 {
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
func experienceLine(p models.Profile) string {
	if len(p.Experiences) == 0 {
		return ""
	}
	x := p.Experiences[0]
	if x.Title == "" || x.Company == "" {
		return ""
	}
	return fmt.Sprintf(" My documented experience includes working as %s at %s.", x.Title, x.Company)
}
func projectLine(p models.Profile) string {
	if len(p.Projects) == 0 {
		return ""
	}
	x := p.Projects[0]
	if x.Name == "" {
		return ""
	}
	d := strings.TrimSpace(x.Description)
	if d != "" {
		return fmt.Sprintf(" A relevant project is %s: %s.", x.Name, d)
	}
	return " A relevant project is " + x.Name + "."
}
func expectation(p models.Profile) string {
	if p.SalaryExpectation > 0 {
		return fmt.Sprintf("My salary expectation is %s, subject to the role’s full scope and benefits.", p.SalaryText())
	}
	if p.RateExpectation > 0 {
		return fmt.Sprintf("My rate expectation is %s, subject to contract details.", p.SalaryText())
	}
	return "I’m open to discussing compensation based on the role’s scope and total package."
}
func preference(p models.Profile) string {
	if len(p.WorkPreference) == 0 {
		return "I’m open to discussing the team’s working arrangement."
	}
	return "My preferred working arrangement is " + strings.Join(p.WorkPreference, " or ") + "."
}
func fallbackSentence(v, f string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return f
	}
	return v
}
func clean(v, f string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return f
	}
	return v
}
