package cv

import (
	"fmt"
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
		title, start, end string
		body              []string
	}
	var blocks []block
	for _, line := range lines {
		if title, start, end, ok := roleHeading(line); ok {
			blocks = append(blocks, block{title: title, start: start, end: end})
			continue
		}
		if len(blocks) > 0 && isRoleContent(line) {
			current := &blocks[len(blocks)-1]
			current.body = append(current.body, line)
		}
	}
	for index, b := range blocks {
		experience := models.WorkExperience{
			Title:       b.title,
			StartDate:   b.start,
			EndDate:     normalizeEnd(b.end),
			Current:     isPresent(b.end),
			Description: strings.Join(compact(b.body, 40), "\n"),
		}
		// A "Project | Client | Employer" line names the employer last, and the
		// employer is what must be excluded from future job matches.
		for _, line := range b.body {
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

// roleHeading recognises "Senior Platform Engineer Dec 2022 - Present".
//
// The title requirement is what separates a role from an education entry: a
// bare "2009 - 2012" under EDUCATION has no title and starts nothing.
func roleHeading(line string) (title, start, end string, ok bool) {
	match := datePeriod.FindStringSubmatch(line)
	if len(match) != 3 {
		return "", "", "", false
	}
	title = strings.Trim(strings.TrimSpace(strings.Replace(line, match[0], "", 1)), " ,|-–—:")
	if len([]rune(title)) < 3 || len(title) > 80 || !hasLetter(title) {
		return "", "", "", false
	}
	return title, strings.TrimSpace(match[1]), strings.TrimSpace(match[2]), true
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
		if _, _, _, ok := roleHeading(line); ok {
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
