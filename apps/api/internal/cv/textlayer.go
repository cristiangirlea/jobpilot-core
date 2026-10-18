package cv

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/technology"
)

type TextLayerReview struct {
	Blockers []string         `json:"blockers"`
	Keywords []KeywordFinding `json:"keywords"`
}

type KeywordFinding struct {
	Technology string `json:"technology"`
	Status     string `json:"status"`
	Present    bool   `json:"present"`
}

func ReviewPDF(data []byte, tailored Tailored, profile models.Profile, baseText string, job models.Job) (string, TextLayerReview) {
	text, _, err := ExtractText("tailored.pdf", data)
	review := CheckTextLayer(text, tailored, profile, baseText, job)
	if err != nil {
		review.Blockers = append([]string{"PDF text layer: could not extract readable text: " + err.Error()}, review.Blockers...)
	}
	return text, review
}

func CheckTextLayer(text string, tailored Tailored, profile models.Profile, baseText string, job models.Job) TextLayerReview {
	review := TextLayerReview{Blockers: []string{}, Keywords: []KeywordFinding{}}
	facts, _ := withCV(profile, baseText)
	for _, contact := range []struct{ label, value string }{
		{"name", facts.Name}, {"email", facts.Email}, {"phone", facts.Phone},
	} {
		if value := strings.TrimSpace(contact.value); value != "" && !intactContact(text, value) {
			review.Blockers = append(review.Blockers, "PDF text layer: "+contact.label+" is missing, altered, or split across lines.")
		}
	}

	sections := []struct {
		heading string
		present bool
	}{
		{"SUMMARY", strings.TrimSpace(tailored.Summary) != ""},
		{"SKILLS", len(tailored.Skills) > 0},
		{"EXPERIENCE", len(tailored.Experiences) > 0},
		{"EDUCATION", len(tailored.Education) > 0},
		{"CERTIFICATIONS", len(tailored.Certifications) > 0},
		{"LANGUAGES", len(tailored.Languages) > 0},
	}
	lines := "\n" + text + "\n"
	cursor := 0
	experienceStart, experienceEnd := -1, len(lines)
	for _, section := range sections {
		if !section.present {
			continue
		}
		needle := "\n" + section.heading + "\n"
		pos := strings.Index(lines[cursor:], needle)
		if pos < 0 {
			review.Blockers = append(review.Blockers, "PDF reading order: "+section.heading+" heading is missing or out of order.")
			continue
		}
		pos += cursor
		if experienceStart >= 0 && experienceEnd == len(lines) {
			experienceEnd = pos
		}
		cursor = pos + len(needle) - 1
		if section.heading == "EXPERIENCE" {
			experienceStart = cursor
		}
	}
	if experienceStart >= 0 {
		roles := strings.Join(strings.Fields(Clean(lines[experienceStart:experienceEnd])), " ")
		cursor = 0
		for _, role := range tailored.Experiences {
			heading := strings.TrimSpace(role.Title)
			if role.Company != "" {
				heading = strings.TrimSpace(heading + " — " + role.Company)
			}
			needle := strings.Join(strings.Fields(Clean(heading+" "+role.Period)), " ")
			if needle == "" {
				continue
			}
			pos := strings.Index(roles[cursor:], needle)
			if pos < 0 {
				review.Blockers = append(review.Blockers, fmt.Sprintf("PDF reading order: title, company, or dates are missing, altered, or interleaved for %q at %q (%s).", role.Title, role.Company, role.Period))
				continue
			}
			cursor += pos + len(needle)
		}
	}

	parts := []string{facts.CurrentTitle, facts.Summary, baseText}
	for _, skill := range facts.Skills {
		parts = append(parts, skill.Name)
	}
	for _, role := range facts.Experiences {
		parts = append(parts, role.Title, role.Description)
	}
	supported, extracted := technology.Mentions(parts...), technology.Mentions(text)
	for _, term := range technology.Signature(len(technology.Terms()), job.Title, job.Description) {
		finding := KeywordFinding{Technology: term, Present: extracted[term] > 0, Status: "covered"}
		switch {
		case supported[term] == 0:
			finding.Status = "gap"
		case !finding.Present:
			finding.Status = "missing"
		}
		review.Keywords = append(review.Keywords, finding)
	}
	return review
}

func intactContact(text, value string) bool {
	value = Clean(value)
	if value == "" {
		return false
	}
	joined := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("@._+", r) }
	for _, line := range strings.Split(text, "\n") {
		for offset := 0; offset < len(line); {
			pos := strings.Index(line[offset:], value)
			if pos < 0 {
				break
			}
			pos += offset
			end := pos + len(value)
			before, _ := utf8.DecodeLastRuneInString(line[:pos])
			after, _ := utf8.DecodeRuneInString(line[end:])
			if (pos == 0 || !joined(before)) && (end == len(line) || !joined(after)) {
				return true
			}
			offset = end
		}
	}
	return false
}

func (r TextLayerReview) Warnings() []string {
	warnings := append([]string{}, r.Blockers...)
	for _, keyword := range r.Keywords {
		switch keyword.Status {
		case "gap":
			warnings = append(warnings, "Keyword gap: "+technology.Name(keyword.Technology)+" is not supported by the stored profile or CV; it must not be added.")
		case "missing":
			warnings = append(warnings, "PDF keyword missing: "+technology.Name(keyword.Technology)+" is supported by stored facts but absent from the extracted text.")
		case "covered":
			warnings = append(warnings, "PDF keyword present: "+technology.Name(keyword.Technology)+".")
		}
	}
	return warnings
}
