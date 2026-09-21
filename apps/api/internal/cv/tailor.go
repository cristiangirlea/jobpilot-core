package cv

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"jobpilot/core/api/internal/answers"
	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

// Tailoring re-emphasises what the candidate already has. It never adds, and it
// never removes a role.
//
// The rules, in order of importance:
//
//   - Every role, employer, title and date is carried through unchanged. A CV
//     with a gap invented away, or an employer dropped for relevance, is a lie
//     told on the candidate's behalf.
//   - Bullets are selected and reordered from the ones already written. No bullet
//     is rewritten, so no achievement can drift.
//   - Skills are reordered so the ones the posting names come first.
//   - Only the summary is newly written, and only from stored facts.

// AlignmentThreshold is the technology overlap below which a base CV is judged
// not to speak to a posting, and a tailored variant is offered.
const AlignmentThreshold = 0.45

// maxBulletsPerRole keeps a tailored CV readable. Older roles get fewer, which
// is how a human would prioritise the same material.
const maxBulletsPerRole = 6

// Tailored is a CV rendered for one posting. Every field traces back to the
// candidate's own stored data.
type Tailored struct {
	Name          string         `json:"name"`
	Headline      string         `json:"headline"`
	Contact       []string       `json:"contact"`
	Summary       string         `json:"summary"`
	SummarySource string         `json:"summary_source"`
	Skills        []string       `json:"skills"`
	Experiences   []TailoredRole `json:"experiences"`
	Education     []string       `json:"education"`
	RoleFamily    string         `json:"role_family"`
	Technologies  []string       `json:"technologies"`
	Alignment     float64        `json:"alignment"`
	Highlighted   []string       `json:"highlighted"`
	Missing       []string       `json:"missing"`
}

// TailoredRole is one role with its bullets selected and ordered for this job.
type TailoredRole struct {
	Title   string   `json:"title"`
	Company string   `json:"company"`
	Period  string   `json:"period"`
	Bullets []string `json:"bullets"`
}

// Alignment scores how well the candidate's own technologies overlap the
// posting's. It is the signal for whether a tailored CV is worth making.
func Alignment(profile models.Profile, extraText string, job models.Job) float64 {
	candidate := candidateSignature(profile, extraText)
	posting := answers.TechnologySignature(20, job.Title, job.Description)
	return answers.Overlap(posting, candidate)
}

func candidateSignature(profile models.Profile, extraText string) []string {
	parts := []string{profile.CurrentTitle, profile.Summary, extraText}
	for _, skill := range profile.Skills {
		parts = append(parts, skill.Name)
	}
	for _, experience := range profile.Experiences {
		parts = append(parts, experience.Title, experience.Description)
	}
	return answers.TechnologySignature(40, parts...)
}

// Tailor builds a CV aimed at one posting. It works with no AI configured: the
// summary then falls back to deterministic wording built from stored facts.
func Tailor(ctx context.Context, client *llm.Client, profile models.Profile, job models.Job, match models.Evaluation, baseText string) Tailored {
	posting := answers.TechnologySignature(20, job.Title, job.Description)
	postingSet := map[string]bool{}
	for _, tech := range posting {
		postingSet[tech] = true
	}

	tailored := Tailored{
		Name:         strings.TrimSpace(profile.Name),
		Headline:     headline(profile, job),
		Contact:      contactLines(profile),
		Education:    []string{},
		RoleFamily:   answers.RoleFamily(job.Title),
		Technologies: posting,
		Alignment:    Alignment(profile, baseText, job),
	}
	tailored.Skills, tailored.Highlighted, tailored.Missing = orderSkills(profile, job, postingSet)
	tailored.Experiences = orderExperiences(profile.Experiences, postingSet)
	tailored.Summary, tailored.SummarySource = summarise(ctx, client, profile, job, match, tailored.Highlighted)
	return tailored
}

// headline names the target role without claiming to hold it. "Senior Backend
// Engineer" as a headline over a CV applying for that role is normal practice;
// inventing a title the candidate never had is not, so the candidate's own
// current title is what is stated.
func headline(profile models.Profile, job models.Job) string {
	current := strings.TrimSpace(profile.CurrentTitle)
	if current == "" {
		return strings.TrimSpace(job.Title)
	}
	return current
}

func contactLines(profile models.Profile) []string {
	var out []string
	for _, value := range []string{profile.Email, profile.Phone, profile.Location, profile.LinkedIn, profile.GitHub, profile.Portfolio} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// orderSkills puts the posting's technologies first, then the candidate's
// longest-held skills. Nothing is added: a technology the posting wants and the
// candidate lacks is reported as missing, never listed as a skill.
func orderSkills(profile models.Profile, job models.Job, postingSet map[string]bool) (ordered, highlighted, missing []string) {
	type entry struct {
		name  string
		years float64
		wants bool
	}
	var entries []entry
	held := map[string]bool{}
	for _, skill := range profile.Skills {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		normalized := answers.TechnologySignature(1, name)
		wants := false
		if len(normalized) == 1 && postingSet[normalized[0]] {
			wants = true
			held[normalized[0]] = true
		}
		entries = append(entries, entry{name: name, years: skill.Years, wants: wants})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].wants != entries[j].wants {
			return entries[i].wants
		}
		if entries[i].years != entries[j].years {
			return entries[i].years > entries[j].years
		}
		return strings.ToLower(entries[i].name) < strings.ToLower(entries[j].name)
	})
	for _, e := range entries {
		label := e.name
		if e.years > 0 {
			label = fmt.Sprintf("%s (%.0fy)", e.name, e.years)
		}
		ordered = append(ordered, label)
		if e.wants {
			highlighted = append(highlighted, e.name)
		}
	}
	if len(ordered) > 28 {
		ordered = ordered[:28]
	}
	for tech := range postingSet {
		if !held[tech] {
			missing = append(missing, tech)
		}
	}
	sort.Strings(missing)
	return ordered, highlighted, missing
}

// orderExperiences keeps every role, in the order the candidate holds them, and
// only chooses which of each role's existing bullets to surface.
func orderExperiences(experiences []models.WorkExperience, postingSet map[string]bool) []TailoredRole {
	out := make([]TailoredRole, 0, len(experiences))
	for index, experience := range experiences {
		limit := maxBulletsPerRole
		// Recent roles carry the argument; older ones are context.
		if index >= 3 {
			limit = 3
		}
		if index >= 6 {
			limit = 2
		}
		out = append(out, TailoredRole{
			Title:   strings.TrimSpace(experience.Title),
			Company: strings.TrimSpace(experience.Company),
			Period:  period(experience),
			Bullets: selectBullets(experience.Description, postingSet, limit),
		})
	}
	return out
}

func period(experience models.WorkExperience) string {
	start := strings.TrimSpace(experience.StartDate)
	end := strings.TrimSpace(experience.EndDate)
	if experience.Current {
		end = "Present"
	}
	switch {
	case start == "" && end == "":
		return ""
	case start == "":
		return end
	case end == "":
		return start
	}
	return start + " - " + end
}

// selectBullets ranks a role's existing bullets by how many of the posting's
// technologies they mention, keeping the original wording and, among equals,
// the original order.
func selectBullets(description string, postingSet map[string]bool, limit int) []string {
	type scored struct {
		text  string
		score int
		order int
	}
	var candidates []scored
	for index, line := range strings.Split(description, "\n") {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		text = strings.TrimSpace(text)
		if len(text) < 12 || strings.Contains(text, "|") {
			continue
		}
		score := 0
		for _, tech := range answers.TechnologySignature(20, text) {
			if postingSet[tech] {
				score++
			}
		}
		if skillsPrefix.MatchString(text) {
			// A "Skills: ..." line is a stack listing, not an achievement; it is
			// useful only when it speaks to this posting.
			if score == 0 {
				continue
			}
			score++
		}
		candidates = append(candidates, scored{text: text, score: score, order: index})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].order < candidates[j].order
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	// Restore the candidate's own ordering among those kept, so the role still
	// reads as they wrote it.
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].order < candidates[j].order })
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.text)
	}
	return out
}

const summarySystemPrompt = `You are rewriting the summary paragraph at the top of a candidate's CV so it speaks to one specific role.

Hard rules:
- Use ONLY the candidate facts supplied. Never invent or imply employers, titles, dates, metrics, achievements, or skills.
- Never claim experience with a technology that is not in the candidate's skills or history.
- Two or three sentences, first person, plain prose. No markdown, no headings, no preamble.
- Return only the summary text.`

// summarise writes the one part of a tailored CV that is newly worded. With no
// provider configured it falls back to a deterministic sentence, which is why
// the product still works with AI switched off.
func summarise(ctx context.Context, client *llm.Client, profile models.Profile, job models.Job, match models.Evaluation, highlighted []string) (string, string) {
	if client.Enabled() {
		prompt := fmt.Sprintf(`CANDIDATE FACTS
%s

TARGET ROLE
Title: %s
Company: %s

THE CANDIDATE'S OWN SKILLS THAT THIS ROLE ASKS FOR
%s

Write the CV summary.`, factSheetFor(profile), fallbackText(job.Title, "unspecified"), fallbackText(job.Company, "unspecified"), listOrNoneText(highlighted))

		result, err := client.Complete(ctx, llm.Request{
			Task: "tailor_cv_summary", System: summarySystemPrompt, Prompt: prompt,
			MaxTokens: 400, Temperature: 0.2,
		})
		if err == nil && strings.TrimSpace(result.Text) != "" {
			return strings.TrimSpace(result.Text), "ai"
		}
	}
	return deterministicSummary(profile, highlighted), "deterministic"
}

// deterministicSummary is what a CV carries when no provider is configured. It
// leads with the candidate's own summary and prefixes the relevance line only
// when this posting actually asks for skills they hold; restating the current
// title on top of a summary that already opens with it just reads as a stutter.
func deterministicSummary(profile models.Profile, highlighted []string) string {
	var parts []string
	if len(highlighted) > 0 {
		limit := highlighted
		if len(limit) > 6 {
			limit = limit[:6]
		}
		parts = append(parts, fmt.Sprintf("Relevant to this role: %s.", strings.Join(limit, ", ")))
	}
	if summary := strings.TrimSpace(profile.Summary); summary != "" {
		if len(summary) > 500 {
			summary = strings.TrimSpace(summary[:500])
		}
		parts = append(parts, summary)
		return strings.Join(parts, " ")
	}
	title := fallbackText(profile.CurrentTitle, "Engineer")
	if profile.YearsExperience > 0 {
		parts = append(parts, fmt.Sprintf("%s with %.0f years of experience.", title, profile.YearsExperience))
	} else {
		parts = append(parts, title+".")
	}
	return strings.Join(parts, " ")
}

// factSheetFor renders the facts a provider may draw on. It mirrors the answer
// bank's contract deliberately: one place decides what a model may see.
func factSheetFor(p models.Profile) string {
	var b strings.Builder
	line := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			fmt.Fprintf(&b, "%s: %s\n", label, value)
		}
	}
	line("Current title", p.CurrentTitle)
	if p.YearsExperience > 0 {
		fmt.Fprintf(&b, "Years of experience: %.0f\n", p.YearsExperience)
	}
	line("Existing summary", p.Summary)
	names := make([]string, 0, len(p.Skills))
	for _, skill := range p.Skills {
		names = append(names, skill.Name)
	}
	line("Skills", strings.Join(names, ", "))
	for index, x := range p.Experiences {
		if index >= 6 {
			break
		}
		fmt.Fprintf(&b, "Experience: %s at %s\n", x.Title, x.Company)
	}
	if b.Len() == 0 {
		return "(no candidate facts recorded)"
	}
	return strings.TrimRight(b.String(), "\n")
}

func fallbackText(v, f string) string {
	if strings.TrimSpace(v) == "" {
		return f
	}
	return strings.TrimSpace(v)
}

func listOrNoneText(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ", ")
}
