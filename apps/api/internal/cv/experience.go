package cv

import (
	"fmt"
	"regexp"
	"strings"

	"jobpilot/core/api/internal/models"
)

// Work experience is read from the whole document rather than from the
// "experience" section alone.
//
// A two-column CV reaches this parser one column at a time, so a sidebar
// heading on page two (say EDUCATION) lands in the middle of the role list and
// would otherwise swallow every role after it. Scanning everything and deciding
// per line is robust to that; the filters below are what keep sidebar chatter
// out of the role descriptions.

// parseExperience splits the document on date ranges. A dated line that also
// carries a title starts a role, and qualifying lines beneath it describe it.
func (p *Parsed) parseExperience(lines []string) {
	type block struct {
		title, company, start, end string
		body                       []string
	}
	var blocks []block
	previous := ""
	// A heading for another section (EDUCATION, PROJECTS) ends the role above
	// it, so a projects list after the last role is not read as its bullets.
	inOtherSection := false
	for _, line := range unwrapRoleHeaders(lines) {
		if title, company, start, end, ok := roleHeading(line); ok {
			blocks = append(blocks, block{title: title, company: company, start: start, end: end})
			previous, inOtherSection = line, false
			continue
		}
		if section, ok := headingFor(line); ok {
			inOtherSection = section != "experience"
			previous = line
			continue
		}
		// Plenty of layouts put the dates on their own line under the job title.
		// The title is then the line just above, provided that line is not an
		// academic entry: "M.A. Computer Engineering" over "2009 - 2012" is a
		// degree, not a job, and turning it into one would both invent a role and
		// exclude a university as though it were an employer.
		if start, end, ok := bareDateRange(line); ok && isRoleTitleLine(previous) {
			blocks = append(blocks, block{title: strings.TrimSpace(previous), start: start, end: end})
			previous = line
			continue
		}
		if len(blocks) > 0 && !inOtherSection && isRoleContent(line) {
			current := &blocks[len(blocks)-1]
			current.body = append(current.body, line)
		}
		previous = line
	}
	for index, b := range blocks {
		experience := models.WorkExperience{
			Title:       b.title,
			Company:     b.company,
			StartDate:   b.start,
			EndDate:     normalizeEnd(b.end),
			Current:     isPresent(b.end),
			Description: strings.Join(compact(b.body, 40), "\n"),
		}
		// A "Project | Client | Employer" line names the employer last, and the
		// employer is what must be excluded from future job matches.
		for _, line := range b.body {
			if experience.Company != "" {
				break
			}
			if strings.Contains(line, "|") {
				parts := strings.Split(line, "|")
				experience.Company = strings.TrimSpace(parts[len(parts)-1])
				break
			}
		}
		if experience.Company == "" && index == 0 {
			// Only the current role is worth warning about; it drives exclusion.
			p.Warnings = append(p.Warnings, fmt.Sprintf("Could not determine the employer for %q. Set it manually so that company is excluded from job matches.", experience.Title))
		}
		p.Experiences = append(p.Experiences, experience)
	}
	if len(p.Experiences) > 30 {
		p.Experiences = p.Experiences[:30]
	}
}

// roleHeading recognises "Senior Platform Engineer Dec 2022 - Present", and the
// piped "Company | Title | Location | Dec 2022 - Present" many exports use.
//
// The title requirement is what separates a role from an education entry: a
// bare "2009 - 2012" under EDUCATION has no title and starts nothing, and a
// piped line whose parts name no role is a degree or a course, not a job.
func roleHeading(line string) (title, company, start, end string, ok bool) {
	match := datePeriod.FindStringSubmatch(line)
	if len(match) != 3 {
		return "", "", "", "", false
	}
	rest := strings.Trim(strings.TrimSpace(strings.Replace(line, match[0], "", 1)), " ,|-–—:")
	start, end = strings.TrimSpace(match[1]), strings.TrimSpace(match[2])
	if strings.Contains(rest, "|") {
		title, company, ok = pipedRole(rest)
		return title, company, start, end, ok
	}
	if len([]rune(rest)) < 3 || len(rest) > 80 || !hasLetter(rest) {
		return "", "", "", "", false
	}
	// "M.Sc. Computer Engineering, Example University 2009 - 2011" is a
	// degree; treating it as a job would exclude the university as an employer.
	if isAcademic(rest) && !roleWords.MatchString(rest) {
		return "", "", "", "", false
	}
	return rest, "", start, end, true
}

// roleWords mark the part of a piped header that is the job title.
var roleWords = regexp.MustCompile(`(?i)\b(engineer|developer|architect|lead|manager|consultant|analyst|designer|scientist|intern|head|director|officer|cto|founder|specialist|administrator|programmer|devops|sre|tester|owner|researcher)\b`)

// pipedRole picks the title and employer out of "Company | Title | Location".
// The title is the part that names a role; the employer is the first other
// part, which is where every layout puts it.
func pipedRole(rest string) (title, company string, ok bool) {
	var parts []string
	for _, part := range strings.Split(rest, "|") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	at := -1
	for i, part := range parts {
		if roleWords.MatchString(part) {
			at = i
			break
		}
	}
	if at < 0 {
		return "", "", false
	}
	// "Senior Engineer & Architect, Retail - Ordering and Billing Platforms"
	// is too long for a title field; the role is what comes before the comma.
	if len(parts[at]) > 80 {
		head, _, _ := strings.Cut(parts[at], ",")
		if len(head) > 80 || !roleWords.MatchString(head) {
			return "", "", false
		}
		parts[at] = strings.TrimSpace(head)
	}
	for i, part := range parts {
		if i != at {
			company = part
			break
		}
	}
	return parts[at], company, true
}

var (
	// "... | Dec 2022 - Oct" whose end year wrapped onto the next line.
	danglingRange = regexp.MustCompile(`(?i)(?:(?:` + monthNames + `)[a-z]*\.?\s*)?\d{4}\s*[-–—]\s*(?:(?:` + monthNames + `)[a-z]*\.?)?\s*$`)
	leadingYear   = regexp.MustCompile(`(?i)^(?:\d{4}|present|current|now)\b`)
)

// unwrapRoleHeaders rejoins piped role headers that PDF text wrapped: the end
// year pushed onto the next line ("... Dec 2022 - Oct" / "2023"), or the header
// split in the middle so that the dates arrive on a second piped line
// ("Company | Title | City" / "(remote) | Aug 2022 - Jan 2023").
func unwrapRoleHeaders(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if i+1 < len(lines) && strings.Contains(line, "|") && !strings.HasPrefix(line, "-") && !datePeriod.MatchString(line) {
			next := lines[i+1]
			if (danglingRange.MatchString(line) && leadingYear.MatchString(next)) ||
				(strings.Contains(next, "|") && datePeriod.MatchString(next)) {
				line += " " + next
				i++
			}
		}
		out = append(out, line)
	}
	return out
}

// bareDateRange matches a line that is nothing but a period, such as
// "Dec 2022 - Present".
func bareDateRange(line string) (start, end string, ok bool) {
	match := datePeriod.FindStringSubmatch(line)
	if len(match) != 3 {
		return "", "", false
	}
	if strings.TrimSpace(strings.Replace(line, match[0], "", 1)) != "" {
		return "", "", false
	}
	return strings.TrimSpace(match[1]), strings.TrimSpace(match[2]), true
}

// academicTerms mark a line as a qualification rather than a job title.
var academicTerms = []string{"university", "universitatea", "college", "faculty", "school",
	"bachelor", "master", "m.a.", "m.sc", "b.sc", "b.a.", "mba", "phd", "doctorate", "diploma", "certificate"}

// isRoleTitleLine reports whether a line could be the job title sitting above a
// date range.
func isRoleTitleLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "•") {
		return false
	}
	if len(line) > 80 || !hasLetter(line) || len(strings.Fields(line)) > 12 {
		return false
	}
	if _, _, ok := bareDateRange(line); ok {
		return false
	}
	return !isAcademic(line)
}

// isAcademic reports whether a line names a qualification.
func isAcademic(line string) bool {
	lower := strings.ToLower(line)
	for _, term := range academicTerms {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

// isRoleContent keeps a role's own text and rejects the one- and two-word
// entries that make up a skills sidebar.
func isRoleContent(line string) bool {
	switch {
	case strings.HasPrefix(line, "-"):
		return true
	case strings.Contains(line, "|"):
		return true
	case skillsPrefix.MatchString(line):
		return true
	}
	return len(strings.Fields(line)) >= 6
}

// parseEducation reads the education section up to the first dated role, which
// is where the main column resumes in an interleaved two-column layout.
func parseEducation(lines []string) []string {
	out := []string{}
	for _, line := range lines {
		if _, _, _, _, ok := roleHeading(line); ok {
			break
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func hasLetter(v string) bool {
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}
