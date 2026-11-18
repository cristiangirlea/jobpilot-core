package extraction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"jobpilot/core/api/internal/models"
)

const (
	MaxAnswerBytes = 64 << 10
	MaxEntries     = 100
	MaxConstraints = 50
	MaxSkills      = 12
	MaxQuoteRunes  = 600
	MaxSkillRunes  = 120
	MaxValueRunes  = 300
)

// Error says why an answer was refused. Reason is a stable code and Where the
// place in the answer; neither ever holds posting or answer text.
type Error struct{ Reason, Where string }

func (e *Error) Error() string {
	message := "extraction refused: " + e.Reason
	if e.Where != "" {
		message += " at " + e.Where
	}
	return message
}

// Verify checks an answer's shape and verifies its evidence against the posting.
func Verify(title, description string, raw []byte) (models.Extraction, error) {
	var zero models.Extraction
	if len(raw) > MaxAnswerBytes {
		return zero, &Error{"too_large", "answer"}
	}
	if !utf8.Valid(raw) {
		return zero, &Error{"not_json", "answer"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var answer map[string]json.RawMessage
	if err := decoder.Decode(&answer); err != nil || answer == nil {
		return zero, &Error{"not_json", "answer"}
	}
	if len(bytes.Trim(raw[decoder.InputOffset():], " \t\r\n")) != 0 {
		return zero, &Error{"trailing_data", "answer"}
	}
	if err := checkFields(answer, "answer", "required", "nice_to_have", "stack", "seniority", "min_years", "constraints"); err != nil {
		return zero, err
	}

	posting := Text(title, description)
	required, err := verifySkills(answer["required"], posting, "required")
	if err != nil {
		return zero, err
	}
	niceToHave, err := verifySkills(answer["nice_to_have"], posting, "nice_to_have")
	if err != nil {
		return zero, err
	}
	stack, err := verifySkills(answer["stack"], posting, "stack")
	if err != nil {
		return zero, err
	}
	seniority, err := verifySeniority(answer["seniority"], posting)
	if err != nil {
		return zero, err
	}
	minYears, err := verifyYears(answer["min_years"], posting)
	if err != nil {
		return zero, err
	}
	constraints, err := verifyConstraints(answer["constraints"], posting)
	if err != nil {
		return zero, err
	}
	return models.Extraction{
		Required:    required,
		NiceToHave:  niceToHave,
		Stack:       stack,
		Seniority:   seniority,
		MinYears:    minYears,
		Constraints: constraints,
	}, nil
}

func checkFields(object map[string]json.RawMessage, where string, fields ...string) *Error {
	for key := range object {
		if !slices.Contains(fields, key) {
			return &Error{"unknown_field", where}
		}
	}
	for _, key := range fields {
		if _, ok := object[key]; !ok {
			if where == "answer" {
				where = key
			}
			return &Error{"missing_field", where}
		}
	}
	return nil
}

func readObject(raw json.RawMessage, where string, fields ...string) (map[string]json.RawMessage, *Error) {
	var object map[string]json.RawMessage
	if !decodeNonNull(raw, &object) {
		return nil, &Error{"wrong_type", where}
	}
	return object, checkFields(object, where, fields...)
}

func decodeNonNull(raw json.RawMessage, value any) bool {
	return !isNull(raw) && json.Unmarshal(raw, value) == nil
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(raw, []byte("null"))
}

func readList(raw json.RawMessage, where string, limit int) ([]json.RawMessage, *Error) {
	var items []json.RawMessage
	if !decodeNonNull(raw, &items) {
		return nil, &Error{"wrong_type", where}
	}
	if len(items) > limit {
		return nil, &Error{"too_many", where}
	}
	return items, nil
}

func verifySkills(raw json.RawMessage, posting, name string) ([]models.ExtractedSkills, *Error) {
	items, err := readList(raw, name, MaxEntries)
	if err != nil {
		return nil, err
	}
	result := make([]models.ExtractedSkills, len(items))
	for i, item := range items {
		where := fmt.Sprintf("%s[%d]", name, i)
		object, err := readObject(item, where, "skills", "quote")
		if err != nil {
			return nil, err
		}
		var skills []json.RawMessage
		entry := &result[i]
		if !decodeNonNull(object["skills"], &skills) || !decodeNonNull(object["quote"], &entry.Quote) {
			return nil, &Error{"wrong_type", where}
		}
		entry.Skills = make([]string, len(skills))
		for j, skill := range skills {
			if !decodeNonNull(skill, &entry.Skills[j]) {
				return nil, &Error{"wrong_type", where}
			}
		}
		if err := verifyQuote(entry.Quote, posting, where); err != nil {
			return nil, err
		}
		if len(skills) == 0 {
			return nil, &Error{"no_skills", where}
		}
		if len(skills) > MaxSkills {
			return nil, &Error{"too_many", where}
		}
		quoteLower := strings.ToLower(entry.Quote)
		for j, skill := range entry.Skills {
			var reason string
			switch {
			case strings.TrimSpace(skill) == "":
				reason = "empty_skill"
			case utf8.RuneCountInString(skill) > MaxSkillRunes:
				reason = "too_long"
			case !strings.Contains(quoteLower, strings.ToLower(skill)):
				reason = "skill_not_in_quote"
			}
			if reason != "" {
				return nil, &Error{reason, fmt.Sprintf("%s.skills[%d]", where, j)}
			}
		}
	}
	return result, nil
}

func verifyQuote(quote, posting, where string) *Error {
	switch {
	case strings.TrimSpace(quote) == "":
		return &Error{"empty_quote", where}
	case utf8.RuneCountInString(quote) > MaxQuoteRunes:
		return &Error{"too_long", where}
	case strings.ContainsAny(quote, "\n\r"):
		return &Error{"quote_spans_lines", where}
	case !strings.Contains(posting, quote):
		return &Error{"quote_not_in_posting", where}
	}
	return nil
}

func verifySeniority(raw json.RawMessage, posting string) (models.ExtractedSeniority, *Error) {
	const where = "seniority"
	var result models.ExtractedSeniority
	object, err := readObject(raw, where, "level", "quote")
	if err != nil {
		return result, err
	}
	if !decodeNonNull(object["level"], &result.Level) || json.Unmarshal(object["quote"], &result.Quote) != nil {
		return result, &Error{"wrong_type", where}
	}
	if !slices.Contains(levels, result.Level) {
		return result, &Error{"unknown_level", where}
	}
	if (result.Level == "unspecified") != (result.Quote == nil) {
		return result, &Error{"seniority_quote", where}
	}
	if result.Quote != nil {
		return result, verifyQuote(*result.Quote, posting, where)
	}
	return result, nil
}

func verifyYears(raw json.RawMessage, posting string) (*int, *Error) {
	const where = "min_years"
	if isNull(raw) {
		return nil, nil
	}
	var years int
	if json.Unmarshal(raw, &years) != nil {
		return nil, &Error{"wrong_type", where}
	}
	if years < 0 {
		return nil, &Error{"bad_years", where}
	}
	if containsStandaloneNumber(posting, strconv.Itoa(years)) {
		return &years, nil
	}
	return nil, nil
}

func containsStandaloneNumber(text, digits string) bool {
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], digits)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(digits)
		if (start == 0 || !isASCIIDigit(text[start-1])) && (end == len(text) || !isASCIIDigit(text[end])) {
			return true
		}
		offset = end
	}
	return false
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func verifyConstraints(raw json.RawMessage, posting string) ([]models.ExtractedConstraint, *Error) {
	const name = "constraints"
	items, err := readList(raw, name, MaxConstraints)
	if err != nil {
		return nil, err
	}
	result := make([]models.ExtractedConstraint, len(items))
	for i, item := range items {
		where := fmt.Sprintf("%s[%d]", name, i)
		object, err := readObject(item, where, "kind", "value", "hard", "quote")
		if err != nil {
			return nil, err
		}
		entry := &result[i]
		if !decodeNonNull(object["kind"], &entry.Kind) ||
			!decodeNonNull(object["value"], &entry.Value) ||
			!decodeNonNull(object["hard"], &entry.Hard) ||
			!decodeNonNull(object["quote"], &entry.Quote) {
			return nil, &Error{"wrong_type", where}
		}
		switch {
		case !slices.Contains(kinds, entry.Kind):
			return nil, &Error{"unknown_kind", where}
		case strings.TrimSpace(entry.Value) == "":
			return nil, &Error{"no_value", where}
		case utf8.RuneCountInString(entry.Value) > MaxValueRunes:
			return nil, &Error{"too_long", where}
		}
		if err := verifyQuote(entry.Quote, posting, where); err != nil {
			return nil, err
		}
	}
	return result, nil
}
