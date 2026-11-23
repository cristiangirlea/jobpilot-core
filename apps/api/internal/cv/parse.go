package cv

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"jobpilot/core/api/internal/models"
)

// Parsed is everything a CV yielded, plus an honest account of what it did not.
//
// Warnings matter as much as the fields: a CV that parses badly must say so
// rather than hand back a confident-looking half-profile. Nothing here is
// written to the profile without the user confirming it.
type Parsed struct {
	Name            string                  `json:"name"`
	Email           string                  `json:"email"`
	Phone           string                  `json:"phone"`
	Location        string                  `json:"location"`
	LinkedIn        string                  `json:"linkedin"`
	GitHub          string                  `json:"github"`
	Portfolio       string                  `json:"portfolio"`
	CurrentTitle    string                  `json:"current_title"`
	Summary         string                  `json:"summary"`
	YearsExperience float64                 `json:"years_experience"`
	Skills          []models.Skill          `json:"skills"`
	Experiences     []models.WorkExperience `json:"experiences"`
	Education       []string                `json:"education"`
	Languages       []string                `json:"languages"`
	Certifications  []string                `json:"certifications"`
	Warnings        []string                `json:"warnings"`
	Text            string                  `json:"text"`
}

var (
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	phonePattern = regexp.MustCompile(`\+?\d[\d\s().\-]{7,}\d`)
	// The \s* after the slash matters: reconstructed PDF text frequently leaves
	// a space there, and without it the handle is never captured.
	linkedInPattern = regexp.MustCompile(`(?i)(?:linkedin\.com/in/|\bin/)\s*([A-Za-z0-9\-_%.]+)`)
	gitHubPattern   = regexp.MustCompile(`(?i)github\.com/\s*([A-Za-z0-9\-_.]+)`)
	sitePattern     = regexp.MustCompile(`(?i)\bhttps?://[A-Za-z0-9.\-]+\.[A-Za-z]{2,}(?:/\S*)?`)
	yearsPattern    = regexp.MustCompile(`(?i)(\d{1,2})\s*\+?\s*years?\s+of\s+experience`)
	// "Golang 5-6 years", "SQL 12 years", "CI/CD 8 years"
	skillYearsPattern = regexp.MustCompile(`(?i)^(.{2,40}?)\s*(\d{1,2})\s*(?:-\s*(\d{1,2}))?\s*years?$`)
	locationPattern   = regexp.MustCompile(`^[\p{L}][\p{L} .'\-]{1,40},\s*[\p{L}][\p{L} .'\-]{1,40}$`)

	monthNames   = `Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec`
	datePeriod   = regexp.MustCompile(`(?i)((?:` + monthNames + `)[a-z]*\.?\s*\d{4}|\d{4})\s*[-–—]\s*(present|current|now|(?:` + monthNames + `)[a-z]*\.?\s*\d{4}|\d{4})`)
	skillsPrefix = regexp.MustCompile(`(?i)^(?:skills|tech(?:nologies)?|stack|tools)\s*[:\-]\s*`)
	// "Backend & APIs: FastAPI, Flask" starts a categorised skills entry.
	skillCategory = regexp.MustCompile(`^([A-Z][^:]{1,40}):\s*(.*)$`)
	// "13+ years building ..." in a summary, where "of experience" is implied.
	summaryYears = regexp.MustCompile(`(?i)\b(\d{1,2})\s*\+?\s*years?\b`)
	// PDF text puts spaces round the hyphen in "Full-Stack". A real dash, as in
	// "Engineer - Contractor", must survive, so only known compounds are joined.
	spacedCompound = regexp.MustCompile(`(?i)\b(full|front|back)\s+-\s+(stack|end)\b`)
)

// sectionHeadings map a CV's own headings onto the sections this parser knows
// how to read. Anything unrecognised simply stays in the section it follows.
var sectionHeadings = map[string]string{
	"about me": "summary", "about": "summary", "summary": "summary", "profile": "summary",
	"professional summary": "summary", "personal statement": "summary", "objective": "summary",
	"work experience": "experience", "experience": "experience", "employment": "experience",
	"employment history": "experience", "professional experience": "experience",
	"career history": "experience", "work history": "experience",
	"education": "education", "academic background": "education",
	"skills": "skills", "technical skills": "skills", "core skills": "skills", "expertise": "skills",
	"languages": "languages", "certifications": "certifications", "certificates": "certifications",
	"contact": "contact", "contact details": "contact",
	"projects": "projects", "personal projects": "projects",
	"independent engineering & open source": "projects",
	"selected projects":                     "projects", "key projects": "projects", "open source": "projects", "publications": "projects",
}

// Parse turns extracted CV text into candidate facts. It is deterministic and
// never invents a value: a field it cannot find stays empty and, where the
// absence is surprising, earns a warning.
func Parse(text string) Parsed {
	parsed := Parsed{Text: text, Skills: []models.Skill{}, Experiences: []models.WorkExperience{}, Warnings: []string{}}
	lines := splitLines(text)
	if len(lines) == 0 {
		parsed.Warnings = append(parsed.Warnings, "The CV contained no readable text.")
		return parsed
	}

	sections := groupSections(lines)
	parsed.parseContacts(text, lines)
	parsed.parseIdentity(lines, sections)
	// The place on a contact line beats a "City, Country" line elsewhere near
	// the top, which can be a wrapped list ("CloudWatch, QuickSight"). After
	// the identity, so a contact line that opens with the name or the title
	// does not report either as the place.
	// A "City, Country" line that spells out the contact line's place ("Lisbon,
	// Portugal" over "Portugal") is the same place, given in full.
	if place := contactLinePlace(lines[:min(len(lines), 25)], parsed.Name); place != "" && !namesPlace(parsed.Location, place) {
		parsed.Location = place
	}
	parsed.parseSummary(sections)
	parsed.parseSkills(lines, sections["skills"])
	parsed.parseExperience(lines)
	parsed.Education = parseEducation(sections["education"])
	parsed.Languages = parseLanguageList(sections["languages"])
	parsed.Certifications = compact(sections["certifications"], 12)

	if matches := yearsPattern.FindStringSubmatch(text); len(matches) == 2 {
		if years, err := strconv.ParseFloat(matches[1], 64); err == nil {
			parsed.YearsExperience = years
		}
	}
	if parsed.YearsExperience == 0 {
		if matches := summaryYears.FindStringSubmatch(parsed.Summary); len(matches) == 2 {
			parsed.YearsExperience, _ = strconv.ParseFloat(matches[1], 64)
		}
	}
	if parsed.YearsExperience == 0 && len(parsed.Skills) > 0 {
		// Fall back to the longest-held skill, which is a lower bound rather
		// than a guess: the CV states it directly.
		for _, skill := range parsed.Skills {
			if skill.Years > parsed.YearsExperience {
				parsed.YearsExperience = skill.Years
			}
		}
	}
	parsed.addWarnings()
	return parsed
}

func splitLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// groupSections buckets lines under the heading that precedes them.
func groupSections(lines []string) map[string][]string {
	sections := map[string][]string{}
	current := "header"
	for _, line := range lines {
		if section, ok := headingFor(line); ok {
			current = section
			continue
		}
		sections[current] = append(sections[current], line)
	}
	return sections
}

// headingFor recognises a section heading. Headings are short and stand alone,
// so a long line that merely contains the word "experience" is not one.
func headingFor(line string) (string, bool) {
	candidate := strings.ToLower(strings.Trim(line, " :-—–"))
	if section, ok := sectionHeadings[candidate]; ok {
		return section, true
	}
	// "SELECTED PROJECTS & OPEN SOURCE (github.com/someone)": a heading set in
	// capitals may run on past its keyword, so its start is matched instead.
	// Capitals are what keep an ordinary sentence starting "Projects" out.
	text, _, _ := strings.Cut(line, "(")
	text = strings.TrimSpace(text)
	if len(text) < 4 || len(text) > 48 || text != strings.ToUpper(text) {
		return "", false
	}
	lower := strings.ToLower(text)
	for heading, section := range sectionHeadings {
		if strings.HasPrefix(lower, heading+" ") {
			return section, true
		}
	}
	return "", false
}

func (p *Parsed) parseContacts(text string, lines []string) {
	if match := emailPattern.FindString(text); match != "" {
		p.Email = match
	}
phone:
	for _, line := range lines {
		// A contact line such as "Country | +40 700 000 000 | me@example.com"
		// is judged one segment at a time.
		for _, segment := range contactSegments(line) {
			if match := phonePattern.FindString(segment); match != "" && countDigits(match) >= 9 && countDigits(match) <= 15 {
				// A date range or a metric can look like a phone number; requiring
				// the segment to be mostly the number itself avoids both.
				if len(strings.TrimSpace(match)) >= len(segment)-2 {
					p.Phone = strings.TrimSpace(match)
					break phone
				}
			}
		}
	}
	if match := linkedInPattern.FindStringSubmatch(text); len(match) == 2 {
		p.LinkedIn = "https://www.linkedin.com/in/" + strings.Trim(match[1], "/.")
	}
	if match := gitHubPattern.FindStringSubmatch(text); len(match) == 2 {
		p.GitHub = "https://github.com/" + strings.Trim(match[1], "/.")
	}
	for _, candidate := range sitePattern.FindAllString(text, -1) {
		lower := strings.ToLower(candidate)
		if strings.Contains(lower, "linkedin.") || strings.Contains(lower, "github.") {
			continue
		}
		p.Portfolio = candidate
		break
	}
	for _, line := range lines[:min(len(lines), 25)] {
		if locationPattern.MatchString(line) && !strings.Contains(line, "@") {
			p.Location = line
			break
		}
	}
}

// contactSegments splits a line on the separators contact lines use.
func contactSegments(line string) []string {
	parts := strings.FieldsFunc(line, func(r rune) bool { return r == '|' || r == '•' || r == '·' })
	out := parts[:0]
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// contactLinePlace reads the place from a separated contact line, such as
// "Portugal" in "Portugal | Remote (EU) | +351 ... | me@example.com": the first
// segment that reads as a place rather than a way to reach the candidate.
func contactLinePlace(lines []string, name string) string {
	for _, line := range lines {
		segments := contactSegments(line)
		if len(segments) < 2 || !(strings.Contains(line, "@") || phonePattern.MatchString(line)) {
			continue
		}
		for _, segment := range segments {
			if looksLikePlace(segment) && !strings.EqualFold(segment, name) && !roleWords.MatchString(segment) {
				return segment
			}
		}
	}
	return ""
}

func looksLikePlace(segment string) bool {
	if segment == "" || !isUpperRune([]rune(segment)[0]) || strings.ContainsAny(segment, "@/.:0123456789") {
		return false
	}
	return !notAPlace.MatchString(segment) && len(strings.Fields(segment)) <= 4
}

// notAPlace matches the other capitalised words a contact line carries: link
// labels standing in for a URL, and how or where the candidate will work.
var notAPlace = regexp.MustCompile(`(?i)^(remote|hybrid|on-?site|open to|willing to|available|full-?time|part-?time|contract|freelance|linkedin|github|gitlab|portfolio|website|web|blog|twitter|x|email|e-mail|phone|mobile|cv|resume)\b`)

// namesPlace reports whether a "City, Country" line names the given place as
// one of its parts.
func namesPlace(line, place string) bool {
	for _, part := range strings.Split(line, ",") {
		if strings.EqualFold(strings.TrimSpace(part), place) {
			return true
		}
	}
	return false
}

// parseIdentity reads the name and headline from the top of the CV, which is
// where essentially every template puts them.
func (p *Parsed) parseIdentity(lines []string, sections map[string][]string) {
	// A two-column CV reaches this parser sidebar-first, so the name is not
	// necessarily near the top of the text. Candidates are collected across a
	// wider window and the one styled as a name wins: templates set the name in
	// full capitals far more often than they do any other short line.
	candidates := lines
	if len(candidates) > 80 {
		candidates = candidates[:80]
	}
	best, bestIndex := "", -1
	for i, line := range candidates {
		if !looksLikeName(line) {
			continue
		}
		if bestIndex < 0 {
			best, bestIndex = line, i
		}
		if line == strings.ToUpper(line) {
			best, bestIndex = line, i
			break
		}
	}
	if bestIndex < 0 {
		return
	}
	p.Name = titleCaseName(best)
	// The headline is normally the line immediately below the name.
	for _, next := range lines[bestIndex+1:] {
		// "Lead Engineer | Python | Go | ..." puts the title first and the stack
		// after it; the stack is not part of the title.
		candidate := next
		if strings.Contains(next, "|") {
			candidate = strings.TrimSpace(strings.Split(next, "|")[0])
		}
		if looksLikeTitle(candidate) {
			p.CurrentTitle = spacedCompound.ReplaceAllString(strings.TrimSpace(candidate), "$1-$2")
			break
		}
	}
}

func looksLikeName(line string) bool {
	// Commas and slashes rule out the two things that most resemble a name in a
	// CV sidebar: a "City, Country" line and a profile handle.
	if strings.ContainsAny(line, "@0123456789|:/,()") || len(line) > 48 {
		return false
	}
	words := strings.Fields(line)
	if len(words) < 2 || len(words) > 4 {
		return false
	}
	for _, word := range words {
		runes := []rune(word)
		if len(runes) < 2 || !isUpperRune(runes[0]) {
			return false
		}
	}
	return true
}

// looksLikeTitle accepts a headline such as "Senior Payments & Backend Engineer
// - Contractor" while rejecting contact lines and bullets.
func looksLikeTitle(line string) bool {
	if strings.ContainsAny(line, "@/") || strings.HasPrefix(line, "-") {
		return false
	}
	words := strings.Fields(line)
	return len(words) >= 2 && len(words) <= 14 && len(line) <= 90
}

func (p *Parsed) parseSummary(sections map[string][]string) {
	body := sections["summary"]
	if len(body) == 0 {
		return
	}
	joined := strings.Join(body, " ")
	joined = closeHyphens(strings.Join(strings.Fields(joined), " "))
	if len(joined) > 1200 {
		joined = strings.TrimSpace(joined[:1200])
	}
	p.Summary = joined
}

// parseSkills reads the "<skill> <n> years" entries CV sidebars use, the
// "Skills: a, b" lines, and the "Category: a, b, c" entries of a skills section.
func (p *Parsed) parseSkills(lines, section []string) {
	seen := map[string]models.Skill{}
	for _, line := range lines {
		match := skillYearsPattern.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		name := strings.TrimSpace(strings.Trim(match[1], ":-–—,"))
		if name == "" || len(name) > 40 || looksLikeSentence(name) {
			continue
		}
		years, _ := strconv.ParseFloat(match[2], 64)
		if match[3] != "" {
			// "5-6 years": take the upper bound the candidate claimed.
			if upper, err := strconv.ParseFloat(match[3], 64); err == nil && upper > years {
				years = upper
			}
		}
		key := strings.ToLower(name)
		if existing, ok := seen[key]; !ok || years > existing.Years {
			seen[key] = models.Skill{Name: name, Years: years}
		}
	}
	// Skills named on "Skills: ..." lines, which carry no duration.
	for _, line := range lines {
		if !skillsPrefix.MatchString(line) {
			continue
		}
		for _, item := range strings.Split(skillsPrefix.ReplaceAllString(line, ""), ",") {
			name := strings.TrimSpace(strings.Trim(item, ".;"))
			if name == "" || len(name) > 40 || looksLikeSentence(name) {
				continue
			}
			key := strings.ToLower(name)
			if _, ok := seen[key]; !ok {
				seen[key] = models.Skill{Name: name}
			}
		}
	}
	for _, name := range categorisedSkills(section) {
		key := strings.ToLower(name)
		if _, ok := seen[key]; !ok {
			seen[key] = models.Skill{Name: name}
		}
	}
	for _, skill := range seen {
		p.Skills = append(p.Skills, skill)
	}
	sort.SliceStable(p.Skills, func(i, j int) bool {
		if p.Skills[i].Years != p.Skills[j].Years {
			return p.Skills[i].Years > p.Skills[j].Years
		}
		return strings.ToLower(p.Skills[i].Name) < strings.ToLower(p.Skills[j].Name)
	})
	if len(p.Skills) > 120 {
		p.Skills = p.Skills[:120]
	}
}

// categorisedSkills reads "Category: a, b (c, d), e / f" entries. PDF text
// wraps them, so a line that does not start a new category continues the one
// before. A note in brackets is dropped unless it is itself a list, "a / b"
// names two skills, and anything longer than a few words is prose, not a
// skill.
func categorisedSkills(section []string) []string {
	var entries []string
	for _, line := range section {
		if match := skillCategory.FindStringSubmatch(line); match != nil && !strings.Contains(match[1], ",") {
			entries = append(entries, match[2])
		} else if len(entries) > 0 {
			entries[len(entries)-1] += " " + line
		}
	}
	var out []string
	for _, entry := range entries {
		for _, item := range splitOutsideBrackets(entry) {
			for _, alternative := range strings.Split(item, " / ") {
				head, inner := alternative, ""
				if open := strings.Index(alternative, "("); open >= 0 {
					head = alternative[:open]
					inner = strings.TrimSuffix(strings.TrimSpace(alternative[open+1:]), ")")
				}
				// "RBAC and ABAC" names two skills.
				if pair := skillPair.FindStringSubmatch(strings.TrimSpace(head)); pair != nil {
					out = appendSkillName(appendSkillName(out, pair[1]), pair[2])
				} else {
					out = appendSkillName(out, head)
				}
				if parts := strings.Split(inner, ","); len(parts) >= 2 {
					for _, part := range parts {
						out = appendSkillName(out, part)
					}
				}
			}
		}
	}
	return out
}

// skillQualifiers are words a CV puts before a skill that are not its name.
var skillQualifiers = []string{"legacy ", "basic ", "some "}

var (
	skillPair = regexp.MustCompile(`^([A-Z][\w.#+]*) and ([A-Z][\w.#+]*)$`)
	// PDF text spaces the hyphen in "fault-injection" and "JSON-RPC"; inside a
	// skill name a hyphen never stands apart.
	spacedHyphen = regexp.MustCompile(`(\w)\s*-\s*(\w)`)
)

func appendSkillName(out []string, name string) []string {
	name = strings.Join(strings.Fields(strings.Trim(name, " .;:-")), " ")
	name = spacedHyphen.ReplaceAllString(name, "$1-$2")
	for _, qualifier := range skillQualifiers {
		name = strings.TrimPrefix(name, qualifier)
	}
	if name == "" || len(name) > 40 || looksLikeSentence(name) || strings.ContainsAny(name, ":()") {
		return out
	}
	return append(out, name)
}

// splitOutsideBrackets splits on commas and semicolons outside brackets, so
// "AWS (EKS, EC2), Docker" is two items.
func splitOutsideBrackets(v string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range v {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',', ';':
			if depth == 0 {
				out = append(out, v[start:i])
				start = i + 1
			}
		}
	}
	return append(out, v[start:])
}

// PDF text breaks "AI-assisted" into "AI - assisted", "dataset -generation" or
// "LLM- ready". A dash in prose has spaces on both sides and, as in "Engineer -
// Contractor", usually a capital after it, so only these shapes are closed up.
var (
	hyphenSpacedBoth  = regexp.MustCompile(`(\p{L}) - (\p{Ll})`)
	hyphenSpacedLeft  = regexp.MustCompile(`(\p{L}) -(\p{L})`)
	hyphenSpacedRight = regexp.MustCompile(`(\p{L})- (\p{Ll})`)
)

// closeHyphens rejoins words that PDF text split around a hyphen.
func closeHyphens(s string) string {
	for _, pattern := range []*regexp.Regexp{hyphenSpacedBoth, hyphenSpacedLeft, hyphenSpacedRight} {
		s = pattern.ReplaceAllString(s, "$1-$2")
	}
	return s
}

func looksLikeSentence(v string) bool {
	return len(strings.Fields(v)) > 5
}

func normalizeEnd(v string) string {
	if isPresent(v) {
		return ""
	}
	return v
}

func isPresent(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "present", "current", "now":
		return true
	}
	return false
}

func parseLanguageList(lines []string) []string {
	var out []string
	for _, line := range lines {
		for _, item := range strings.Split(line, ",") {
			name := strings.TrimSpace(item)
			if name != "" && len(name) <= 40 && !looksLikeSentence(name) {
				out = append(out, name)
			}
		}
	}
	return compact(out, 12)
}

func compact(lines []string, limit int) []string {
	out := []string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// addWarnings names what a review should look at. Silence here would be worse
// than a long list: an unnoticed empty field becomes a wrong application.
func (p *Parsed) addWarnings() {
	if p.Name == "" {
		p.Warnings = append(p.Warnings, "No name was found. Enter it manually.")
	}
	if p.Email == "" {
		p.Warnings = append(p.Warnings, "No email address was found. Applications need one.")
	}
	if p.Phone == "" {
		p.Warnings = append(p.Warnings, "No phone number was found.")
	}
	if p.Location == "" {
		p.Warnings = append(p.Warnings, "No location was found. Location affects matching and eligibility answers.")
	}
	if len(p.Experiences) == 0 {
		p.Warnings = append(p.Warnings, "No dated work experience was found. Add roles manually so current and previous employers are excluded from matches.")
	}
	if len(p.Skills) == 0 {
		p.Warnings = append(p.Warnings, "No skills were found. Skills drive most of the match score.")
	}
	if p.Summary == "" {
		p.Warnings = append(p.Warnings, "No summary section was found.")
	}
}

func countDigits(v string) int {
	count := 0
	for _, r := range v {
		if r >= '0' && r <= '9' {
			count++
		}
	}
	return count
}

func isUpperRune(r rune) bool {
	return strings.ToUpper(string(r)) == string(r) && strings.ToLower(string(r)) != string(r)
}

// titleCaseName normalises an all-caps heading such as "OCTAVIAN PIRVAN" while
// leaving a normally-cased name untouched.
func titleCaseName(line string) string {
	if line != strings.ToUpper(line) {
		return strings.TrimSpace(line)
	}
	words := strings.Fields(strings.ToLower(line))
	for i, word := range words {
		runes := []rune(word)
		words[i] = strings.ToUpper(string(runes[0])) + string(runes[1:])
	}
	return strings.Join(words, " ")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
