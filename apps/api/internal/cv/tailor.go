package cv

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

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
	// Certifications and Languages come from the profile, or the CV when the
	// profile has none.
	Certifications []string `json:"certifications"`
	Languages      []string `json:"languages"`
	RoleFamily     string   `json:"role_family"`
	Technologies   []string `json:"technologies"`
	Alignment      float64  `json:"alignment"`
	Highlighted    []string `json:"highlighted"`
	Missing        []string `json:"missing"`
}

// TailoredRole is one role with its bullets selected and ordered for this job.
type TailoredRole struct {
	Title   string `json:"title"`
	Company string `json:"company"`
	Period  string `json:"period"`
	// Context is the role's opening line when it has one, such as "Payments
	// platform for retail banks. Stack: Go, Kafka.", kept whole above the
	// bullets rather than competing with them.
	Context string   `json:"context,omitempty"`
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

// Tailor builds a CV aimed at one posting from the candidate's profile and
// their own uploaded CV (baseText), which fills whatever the profile leaves
// empty. It works with no AI configured: the summary is then the candidate's
// own.
func Tailor(ctx context.Context, client *llm.Client, profile models.Profile, job models.Job, match models.Evaluation, baseText string) Tailored {
	posting := answers.TechnologySignature(20, job.Title, job.Description)
	postingSet := map[string]bool{}
	for _, tech := range posting {
		postingSet[tech] = true
	}
	facts, education := withCV(profile, baseText)

	tailored := Tailored{
		Name:           strings.TrimSpace(facts.Name),
		Headline:       strings.TrimSpace(facts.CurrentTitle),
		Contact:        nonNil(contactLines(facts)),
		Education:      nonNil(education),
		Certifications: nonNil(facts.Certifications),
		Languages:      nonNil(facts.Languages),
		RoleFamily:     answers.RoleFamily(job.Title),
		Technologies:   nonNil(posting),
		Alignment:      Alignment(profile, baseText, job),
	}
	// Lists are never nil: a nil slice is stored as JSON null, and readers of the
	// stored CV should not have to guard every one of them.
	skills, highlighted, missing := orderSkills(facts, job, postingSet)
	tailored.Skills, tailored.Highlighted, tailored.Missing = nonNil(skills), nonNil(highlighted), nonNil(missing)
	tailored.Experiences = orderExperiences(facts.Experiences, postingSet)
	tailored.Summary, tailored.SummarySource = summarise(ctx, client, facts, job, match, tailored.Highlighted)
	return tailored
}

// withCV fills the profile's gaps from the candidate's uploaded CV, read with
// the current parser. The profile wins wherever it has a value, because the
// candidate edits it; the CV supplies the rest. Roles from both are kept (see
// mergeRoles), since a hand-entered profile often lists titles without the
// bullets the CV has, and the CV may lag a new job. The headline is only ever the
// candidate's own title: using the posting's would claim a role they never held.
func withCV(profile models.Profile, baseText string) (models.Profile, []string) {
	if strings.TrimSpace(baseText) == "" {
		return profile, nil
	}
	fromCV := Parse(baseText)
	merged := profile
	for _, field := range []struct {
		into *string
		from string
	}{
		{&merged.Name, fromCV.Name}, {&merged.Email, fromCV.Email}, {&merged.Phone, fromCV.Phone},
		{&merged.Location, fromCV.Location}, {&merged.LinkedIn, fromCV.LinkedIn}, {&merged.GitHub, fromCV.GitHub},
		{&merged.Portfolio, fromCV.Portfolio}, {&merged.CurrentTitle, fromCV.CurrentTitle}, {&merged.Summary, fromCV.Summary},
	} {
		if strings.TrimSpace(*field.into) == "" {
			*field.into = field.from
		}
	}
	if merged.YearsExperience == 0 {
		merged.YearsExperience = fromCV.YearsExperience
	}
	if len(merged.Skills) == 0 {
		merged.Skills = fromCV.Skills
	}
	merged.Experiences = mergeRoles(profile.Experiences, fromCV.Experiences)
	if len(merged.Certifications) == 0 {
		merged.Certifications = fromCV.Certifications
	}
	if len(merged.Languages) == 0 {
		merged.Languages = fromCV.Languages
	}
	return merged, fromCV.Education
}

// mergeRoles keeps every role from both sources, since tailoring never drops
// one. The profile's roles come first, in its order, and a profile role with
// no description takes the CV's bullets for the same employer; the CV's other
// roles follow in the CV's order.
func mergeRoles(profile, fromCV []models.WorkExperience) []models.WorkExperience {
	if len(profile) == 0 {
		return fromCV
	}
	byCompany := map[string]models.WorkExperience{}
	for _, role := range fromCV {
		key := strings.ToLower(strings.TrimSpace(role.Company))
		if _, seen := byCompany[key]; key != "" && !seen {
			byCompany[key] = role
		}
	}
	merged := make([]models.WorkExperience, 0, len(profile)+len(fromCV))
	used := map[string]bool{}
	for _, role := range profile {
		key := strings.ToLower(strings.TrimSpace(role.Company))
		if match, ok := byCompany[key]; ok && strings.TrimSpace(role.Description) == "" {
			role.Description = match.Description
		}
		used[key] = true
		merged = append(merged, role)
	}
	for _, role := range fromCV {
		if key := strings.ToLower(strings.TrimSpace(role.Company)); !used[key] || key == "" {
			merged = append(merged, role)
		}
	}
	return merged
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
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

// orderSkills puts first the skills the posting asks for, whether the
// technology vocabulary knows them or the posting simply names them, then the
// longest-held, then those the candidate's own experience mentions most.
// Nothing is added: a technology the posting wants and the candidate lacks is
// reported as missing, never listed as a skill.
func orderSkills(profile models.Profile, job models.Job, postingSet map[string]bool) (ordered, highlighted, missing []string) {
	type entry struct {
		name     string
		years    float64
		wants    bool
		mentions int
	}
	postingText := strings.ToLower(job.Title + " " + job.Description)
	var history strings.Builder
	for _, experience := range profile.Experiences {
		history.WriteString(strings.ToLower(experience.Title + " " + experience.Description + " "))
	}
	var entries []entry
	held := map[string]bool{}
	for _, skill := range profile.Skills {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		normalized := answers.TechnologySignature(1, name)
		inVocabulary := len(normalized) == 1 && postingSet[normalized[0]]
		if inVocabulary {
			held[normalized[0]] = true
		}
		wants := inVocabulary || countWord(postingText, strings.ToLower(name)) > 0
		// An all-capitals name such as REST or AWS must appear as written,
		// whichever way it matched: in lower case it is often just a word, as
		// in "the rest of the team".
		if name == strings.ToUpper(name) {
			wants = countWord(job.Title+" "+job.Description, name) > 0
		}
		entries = append(entries, entry{name: name, years: skill.Years, wants: wants, mentions: countWord(history.String(), strings.ToLower(name))})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].wants != entries[j].wants {
			return entries[i].wants
		}
		if entries[i].years != entries[j].years {
			return entries[i].years > entries[j].years
		}
		if entries[i].mentions != entries[j].mentions {
			return entries[i].mentions > entries[j].mentions
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
		context, bullets := splitRole(experience.Description)
		out = append(out, TailoredRole{
			Title:   strings.TrimSpace(experience.Title),
			Company: strings.TrimSpace(experience.Company),
			Period:  period(experience),
			Context: context,
			Bullets: selectBullets(bullets, postingSet, limit),
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

// bulletMarkers start a bullet in a role description.
var bulletMarkers = []string{"- ", "• ", "* ", "– "}

// splitRole separates a role description into its opening context and its
// bullets. PDF text wraps a bullet across lines, so a line that does not start
// a bullet continues the one before; lines before the first bullet are the
// role's context. A description without any bullet markers, as a hand-typed
// one often is, has one bullet per line.
func splitRole(description string) (string, []string) {
	lines := strings.Split(description, "\n")
	marked := false
	for _, line := range lines {
		if bulletMarker(strings.TrimSpace(line)) != "" {
			marked = true
			break
		}
	}
	var context, bullets []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch marker := bulletMarker(line); {
		case line == "":
		case !marked:
			bullets = append(bullets, line)
		case marker != "":
			bullets = append(bullets, strings.TrimSpace(strings.TrimPrefix(line, strings.TrimSpace(marker))))
		case len(bullets) > 0:
			bullets[len(bullets)-1] += " " + line
		default:
			context = append(context, line)
		}
	}
	for i := range bullets {
		bullets[i] = closeHyphens(bullets[i])
	}
	return closeHyphens(strings.Join(context, " ")), bullets
}

func bulletMarker(line string) string {
	for _, marker := range bulletMarkers {
		if strings.HasPrefix(line, marker) || line == strings.TrimSpace(marker) {
			return marker
		}
	}
	return ""
}

// selectBullets ranks a role's existing bullets by how many of the posting's
// technologies they mention, keeping the original wording and, among equals,
// the original order.
func selectBullets(bullets []string, postingSet map[string]bool, limit int) []string {
	type scored struct {
		text  string
		score int
		order int
	}
	var candidates []scored
	for index, text := range bullets {
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
- Return only the summary text.` + "\n- " + llm.FenceRule

// summarise writes the one part of a tailored CV that is newly worded. With no
// provider configured it falls back to a deterministic sentence, which is why
// the product still works with AI switched off.
func summarise(ctx context.Context, client *llm.Client, profile models.Profile, job models.Job, match models.Evaluation, highlighted []string) (string, string) {
	if client.Enabled() {
		role := fmt.Sprintf("Title: %s\nCompany: %s", fallbackText(job.Title, "unspecified"), fallbackText(job.Company, "unspecified"))
		prompt := fmt.Sprintf(`CANDIDATE FACTS
%s

TARGET ROLE
%s

THE CANDIDATE'S OWN SKILLS THAT THIS ROLE ASKS FOR
%s

Write the CV summary.`, factSheetFor(profile), llm.Fence(role), listOrNoneText(highlighted))

		result, err := client.Complete(ctx, llm.Request{
			Task: "tailor_cv_summary", System: summarySystemPrompt, Prompt: prompt,
			MaxTokens: 400, Temperature: 0.2,
		})
		if summary := strings.TrimSpace(result.Text); err == nil && summary != "" {
			if result.Backup {
				return summary, answers.SourceAIBackup
			}
			return summary, answers.SourceAI
		}
	}
	return deterministicSummary(profile), "deterministic"
}

// maxSummary keeps the deterministic summary to a CV-sized paragraph.
const maxSummary = 700

// deterministicSummary is what a CV carries when no provider is configured:
// the candidate's own summary, cut at a sentence end if it runs long. The
// posting's skills already lead the skills section, so the summary is not
// prefixed with them. With no summary at all, it states the title and years.
func deterministicSummary(profile models.Profile) string {
	if summary := strings.TrimSpace(profile.Summary); summary != "" {
		if len(summary) <= maxSummary {
			return summary
		}
		cut := strings.LastIndex(summary[:maxSummary], ". ")
		if cut < maxSummary/2 {
			// No sentence end near the limit: cut on a character boundary so
			// the result stays valid UTF-8.
			end := maxSummary
			for end > 0 && !utf8.RuneStart(summary[end]) {
				end--
			}
			return strings.TrimSpace(summary[:end])
		}
		return summary[:cut+1]
	}
	title := fallbackText(profile.CurrentTitle, "Engineer")
	if profile.YearsExperience > 0 {
		return fmt.Sprintf("%s with %.0f years of experience.", title, profile.YearsExperience)
	}
	return title + "."
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

// countWord counts whole-word occurrences of term in text, compared as given.
// A boundary is anything but a letter or digit, so "go" does not count inside
// "going" while "node.js" counts after "(" and "kafka" before "’s".
func countWord(text, term string) int {
	if term == "" {
		return 0
	}
	count := 0
	for offset := 0; ; {
		index := strings.Index(text[offset:], term)
		if index < 0 {
			return count
		}
		start, end := offset+index, offset+index+len(term)
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if (start == 0 || !isWordRune(before)) && (end == len(text) || !isWordRune(after)) {
			count++
		}
		offset = start + 1
	}
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
