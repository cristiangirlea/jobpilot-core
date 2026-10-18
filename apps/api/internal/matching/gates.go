package matching

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"jobpilot/core/api/internal/models"
)

var eligibilityRules = []struct {
	required *regexp.Regexp
	negated  *regexp.Regexp
}{
	{regexp.MustCompile(`(?i)\b(?:(?:must|shall|need to|are required to) (?:(?:be|become) (?:a |an )?(?:[a-z.-]+ ){0,3}citizens?(?: of\b|[.!?,;]|$)|(?:have|hold|possess) (?:[a-z.-]+ ){0,3}citizenship\b)|citizenship (?:is )?(?:required|mandatory|essential)|(?:requires?|restricted to) [a-z .-]{0,30}(?:citizenship|citizens)\b|citizens only\b|only [a-z .-]{0,30}citizens (?:may|can|are eligible))`), regexp.MustCompile(`(?i)\b(?:no citizenship|citizenship (?:is )?not (?:required|necessary|mandatory)|citizenship (?:is )?optional)\b`)},
	{regexp.MustCompile(`(?i)\b(?:(?:must|shall|need to|are required to) (?:be|have|hold) [a-z .-]{0,30}permanent residen(?:t|cy|ce)\b|permanent (?:residen(?:cy|ce)|resident status) (?:is )?(?:required|mandatory|essential)|permanent residents only|(?:requires?|restricted to) [a-z .-]{0,20}permanent residen(?:ts?|cy|ce)\b)`), regexp.MustCompile(`(?i)\b(?:no permanent residen(?:cy|ce)|permanent residen(?:cy|ce) (?:is )?not (?:required|necessary|mandatory))\b`)},
	{regexp.MustCompile(`(?i)\b(?:(?:must|shall|need to|requires?|are required to) [a-z /-]{0,65}clearance\b|(?:active |current )?(?:(?:security|secret|top secret|confidential|ts/sci|sc|dv|baseline|nv1|nv2|public trust) )?clearance (?:is )?(?:required|mandatory|essential)|(?:active|current) (?:security|secret|top secret|ts/sci|sc|dv) clearance\b)`), regexp.MustCompile(`(?i)\b(?:no (?:[a-z/]+ ){0,2}clearance|clearance (?:is )?not (?:required|necessary|mandatory)|clearance (?:is )?optional|must not [a-z /-]{0,65}clearance)\b`)},
}

var sponsorshipSignal = regexp.MustCompile(`(?i)\b(?:no (?:visa )?sponsorship|(?:cannot|can't|do not|does not|will not|unable to) (?:provide|offer|support|sponsor) (?:(?:visa )?sponsorship|visas?)|(?:visa )?sponsorship (?:is )?(?:not available|unavailable|available|provided|offered)|international applicants (?:are )?welcome|(?:must|need to|required to) (?:already )?(?:have|hold) (?:the )?(?:(?:legal )?right to work|work authori[sz]ation)|must (?:already )?be (?:legally )?authori[sz]ed to work)\b`)

var languageNames = []string{
	"English", "German", "French", "Spanish", "Portuguese", "Italian", "Dutch", "Romanian", "Polish", "Czech", "Slovak", "Hungarian", "Bulgarian", "Greek", "Swedish", "Norwegian", "Danish", "Finnish", "Icelandic", "Estonian", "Latvian", "Lithuanian", "Ukrainian", "Russian", "Turkish", "Arabic", "Hebrew", "Hindi", "Urdu", "Bengali", "Mandarin", "Cantonese", "Chinese", "Japanese", "Korean", "Vietnamese", "Thai", "Indonesian", "Malay", "Tagalog", "Serbian", "Croatian", "Slovenian", "Albanian", "Catalan", "Persian", "Swahili", "Afrikaans",
}

const languageBar = `(?:[abc][12]|native|fluent|fluency|business[- ]level|professional|advanced|intermediate|basic|beginner)`

type languagePattern struct {
	name                     string
	word, required, optional *regexp.Regexp
}

var languagePatterns = func() []languagePattern {
	var out []languagePattern
	for _, name := range languageNames {
		n := strings.ToLower(name)
		out = append(out, languagePattern{
			name,
			regexp.MustCompile(`(?i)\b` + n + `\b`),
			regexp.MustCompile(`(?i)(?:\b` + n + ` (?:language |at )?(?:\(?` + languageBar + `\)? )?(?:level |skills |proficiency |fluency )?(?:is |are )?(?:required|mandatory|essential)\b|\b(?:must|need to|required to) (?:be )?(?:speak|write|read|communicate in|fluent in|proficient in) (?:and (?:write|speak) )?` + n + `\b|\b(?:require|requires|need) (?:` + languageBar + ` (?:proficiency in )?)?` + n + `\b|(?:^|: )` + languageBar + ` (?:proficiency in |in )?` + n + `\b)`),
			regexp.MustCompile(`(?i)(?:\b(?:no|without) (?:` + languageBar + ` )?` + n + `\b|\b` + n + ` (?:is )?(?:(?:preferred|optional|a (?:plus|nice to have))|(?:[a-z0-9-]+ ){0,3}not (?:required|necessary|mandatory))\b)`),
		})
	}
	return out
}()

var requiredLanguageList = regexp.MustCompile(`(?i)\b(?:required|mandatory|essential) languages?:`)
var cefrLevel = regexp.MustCompile(`(?i)\b([abc][12])\b`)
var languageSeparator = regexp.MustCompile(`(?i)[,;]|\band\b|\bor\b`)

// A level next to another language must not become this language's level.
func languageContext(text string, word *regexp.Regexp) string {
	span := word.FindStringIndex(text)
	if span == nil {
		return ""
	}
	start, end := 0, len(text)
	for _, separator := range languageSeparator.FindAllStringIndex(text[:span[0]], -1) {
		start = separator[1]
	}
	if separator := languageSeparator.FindStringIndex(text[span[1]:]); separator != nil {
		end = span[1] + separator[0]
	}
	return text[start:end]
}

func startsWithSpace(text string) bool {
	r, _ := utf8.DecodeRuneInString(text)
	return unicode.IsSpace(r)
}

func languageLevel(text string) int {
	if level := cefrLevel.FindString(text); level != "" {
		return strings.Index("a1a2b1b2c1c2", strings.ToLower(level))/2 + 1
	}
	for _, level := range []struct {
		words []string
		rank  int
	}{
		{[]string{"native"}, 6}, {[]string{"fluent", "fluency", "advanced"}, 5},
		{[]string{"business-level", "business level", "professional"}, 4},
		{[]string{"intermediate"}, 3}, {[]string{"basic", "beginner"}, 1},
	} {
		for _, word := range level.words {
			if containsPhrase(text, word) {
				return level.rank
			}
		}
	}
	return 0
}

func checkPostingGates(e *models.Evaluation, p models.Profile, f *models.FilterPreset, j models.JobInput) {
	for _, text := range []string{j.Title, j.Description} {
		for _, sentence := range postingSentences(text) {
			quote := []rune(strings.TrimSpace(sentence))
			if len(quote) > 200 {
				quote = quote[:200]
			}
			quoted := `"` + strings.TrimSpace(string(quote)) + `"`
			for _, clause := range strings.Split(sentence, ";") {
				clause = strings.TrimSpace(clause)
				for _, rule := range eligibilityRules {
					if rule.required.MatchString(clause) && !rule.negated.MatchString(clause) {
						e.Warnings = appendUnique(e.Warnings, "Posting requires eligibility: "+quoted)
						if f != nil && f.RejectEligibilityRequirements {
							e.RejectionReasons = appendUnique(e.RejectionReasons, "Active filter rejects eligibility requirements: "+quoted)
						}
					}
				}
				if sponsorshipSignal.MatchString(clause) {
					e.Warnings = appendUnique(e.Warnings, "Posting states work authorization or sponsorship: "+quoted)
				}
				for _, language := range languagePatterns {
					if !language.word.MatchString(clause) || language.optional.MatchString(clause) || !(language.required.MatchString(clause) || requiredLanguageList.MatchString(clause)) {
						continue
					}
					listed, level := false, 0
					for _, entry := range p.Languages {
						if language.word.MatchString(entry) {
							listed = true
							if rank := languageLevel(languageContext(entry, language.word)); rank > level {
								level = rank
							}
						}
					}
					if !listed || level > 0 && level < languageLevel(languageContext(clause, language.word)) {
						e.Warnings = appendUnique(e.Warnings, "Posting requires "+language.name+" beyond the listed profile languages or level: "+quoted)
					}
					if !listed && f != nil && f.RejectUnlistedLanguages {
						e.RejectionReasons = appendUnique(e.RejectionReasons, "Active filter rejects unlisted language "+language.name+": "+quoted)
					}
				}
			}
		}
	}
}

func postingSentences(text string) []string {
	var out []string
	start := 0
	for i, r := range text {
		end := i + len(string(r))
		boundary := r == '\n' || r == '\r' || (r == '.' || r == '!' || r == '?') && (end == len(text) || startsWithSpace(text[end:]))
		if r == '.' {
			prefix := strings.ToLower(text[start:end])
			for _, abbreviation := range []string{"u.s.", "u.k.", "e.g.", "i.e."} {
				if strings.HasSuffix(prefix, abbreviation) {
					boundary = false
				}
			}
		}
		if boundary {
			if sentence := strings.TrimSpace(text[start:end]); sentence != "" {
				out = append(out, sentence)
			}
			start = end
		}
	}
	if sentence := strings.TrimSpace(text[start:]); sentence != "" {
		out = append(out, sentence)
	}
	return out
}
