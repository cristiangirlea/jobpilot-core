package matching

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/technology"
)

func scoreExtractedSkills(profile []models.Skill, x models.Extraction) (int, []string, []string) {
	matched, missing := []string{}, []string{}
	heldWeight, totalWeight := 0, 0
	for _, group := range []struct {
		entries  []models.ExtractedSkills
		weight   int
		required bool
	}{
		{x.Required, 2, true},
		{x.NiceToHave, 1, false},
	} {
		for _, entry := range group.entries {
			options := make([]postingOption, 0, len(entry.Skills))
			for _, option := range entry.Skills {
				if containsPhrase(entry.Quote, option) && mentionsTechnology(option, entry.Quote) {
					options = append(options, readOption(option))
				}
			}
			if onlyPractices(options) {
				continue
			}
			held := false
			for _, skill := range profile {
				name := strings.TrimSpace(skill.Name)
				for _, option := range options {
					if option.sameSkill(name) {
						matched = appendUnique(matched, name)
						held = true
						break
					}
				}
			}
			// Like the rules, the share counts technologies and the profile's own
			// skills; any other requirement is listed but cannot lower the score.
			if held || namesTechnology(options) {
				totalWeight += group.weight
			}
			if held {
				heldWeight += group.weight
			} else if group.required {
				names := make([]string, len(options))
				for i, option := range options {
					names[i] = option.skill
				}
				missing = appendUnique(missing, strings.Join(names, " or "))
			}
		}
	}
	if totalWeight == 0 {
		return 15, matched, missing
	}
	sort.Strings(matched)
	sort.Strings(missing)
	return int(math.Round(30 * float64(heldWeight) / float64(totalWeight))), matched, missing
}

// postingOption is one alternative of an extracted entry with what matching
// reads from it. Every profile skill is compared with it, so its technologies
// are read once here, not once per profile skill.
type postingOption struct {
	skill    string
	key      string
	mentions map[string]int
	words    map[string]bool
	text     string
}

func readOption(skill string) postingOption {
	return postingOption{skill, technology.Canonical(skill), technology.Mentions(skill), words(skill), phraseText(skill)}
}

func onlyPractices(options []postingOption) bool {
	for _, option := range options {
		if !technology.IsPractice(option.key) {
			return false
		}
	}
	return true
}

// namesTechnology reports whether an option names a vocabulary technology
// that is not a way of working.
func namesTechnology(options []postingOption) bool {
	for _, option := range options {
		if technology.Name(option.key) != "" && !technology.IsPractice(option.key) {
			return true
		}
		for term := range option.mentions {
			if !technology.IsPractice(term) {
				return true
			}
		}
	}
	return false
}

// mentionsTechnology reports whether texts can name skill: a technology that
// is also an everyday word only where the vocabulary finds it, any other
// skill always. Each text is read on its own.
func mentionsTechnology(skill string, texts ...string) bool {
	key := technology.Canonical(skill)
	if !technology.HasEverydayUses(key) {
		return true
	}
	_, ok := technology.Mentions(texts...)[key]
	return ok
}

func (o postingOption) sameSkill(profileName string) bool {
	if strings.TrimSpace(profileName) == "" {
		return false
	}
	key := technology.Canonical(profileName)
	if technology.IsPractice(key) {
		return false
	}
	if key == o.key {
		return true
	}
	if _, ok := o.mentions[key]; ok {
		return true
	}
	if technology.HasEverydayUses(key) {
		return false
	}
	return namesPhrase(profileName, o.words, o.text)
}

func scoreExtractedSeniority(wants []string, level string) int {
	found, ok := seniority[level]
	return seniorityPoints(wants, found, ok && level != "unspecified")
}

const (
	extractedEligibilityPrefix       = "Posting requires eligibility: "
	extractedWorkAuthorizationPrefix = "Posting states work authorization or sponsorship: "
)

func addExtractedWarnings(e *models.Evaluation, p models.Profile, x models.Extraction) {
	if x.MinYears != nil && p.YearsExperience > 0 && float64(*x.MinYears) > p.YearsExperience {
		unit := "years"
		if *x.MinYears == 1 {
			unit = "year"
		}
		years := strconv.FormatFloat(p.YearsExperience, 'f', -1, 64)
		e.Warnings = appendUnique(e.Warnings, fmt.Sprintf("Posting asks for %d %s of experience; the profile lists %s", *x.MinYears, unit, years))
	}
	if len(x.Constraints) == 0 {
		return
	}
	listed, _ := profileLanguages(p.Languages)
	for _, constraint := range x.Constraints {
		if !constraint.Hard {
			continue
		}
		quoted := `"` + constraint.Quote + `"`
		switch constraint.Kind {
		case "citizenship", "clearance", "work_authorization":
			if hasEligibilityWarning(e.Warnings, constraint.Quote) {
				continue
			}
			prefix := extractedEligibilityPrefix
			if constraint.Kind == "work_authorization" {
				prefix = extractedWorkAuthorizationPrefix
			}
			e.Warnings = appendUnique(e.Warnings, prefix+quoted)
		case "language":
			addExtractedLanguageWarnings(e, listed, constraint, quoted)
		default:
			addExtractedCondition(e, constraint.Kind, quoted)
		}
	}
}

func hasEligibilityWarning(warnings []string, quote string) bool {
	quote = strings.Join(strings.Fields(quote), " ")
	for _, warning := range warnings {
		if !strings.HasPrefix(warning, extractedEligibilityPrefix) && !strings.HasPrefix(warning, extractedWorkAuthorizationPrefix) {
			continue
		}
		first, last := strings.IndexByte(warning, '"'), strings.LastIndexByte(warning, '"')
		if first < 0 || last <= first {
			continue
		}
		existing := strings.Join(strings.Fields(warning[first+1:last]), " ")
		if strings.Contains(existing, quote) {
			return true
		}
	}
	return false
}

func addExtractedLanguageWarnings(e *models.Evaluation, listed map[*languagePattern]profileLanguage, constraint models.ExtractedConstraint, quoted string) {
	hits := findLanguages(constraint.Value, false)
	if len(hits) == 0 {
		addExtractedCondition(e, constraint.Kind, quoted)
		return
	}
	if len(listed) == 0 {
		return
	}
	alternative := requirementAlternative.MatchString(constraint.Value)
	anyListed := false
	for _, hit := range hits {
		anyListed = anyListed || listed[hit.pattern].listed
	}
	seen := make(map[*languagePattern]bool)
	for _, hit := range hits {
		if seen[hit.pattern] {
			continue
		}
		seen[hit.pattern] = true
		language := listed[hit.pattern]
		required := languageLevel(languageContext(constraint.Value, hit.start, hit.end))
		if language.listed && (language.level == 0 || language.level >= required) {
			continue
		}
		if alternative && anyListed {
			continue
		}
		prefix := "Posting requires " + hit.pattern.name + " beyond"
		if !hasWarningPrefix(e.Warnings, prefix) {
			e.Warnings = append(e.Warnings, prefix+" the listed profile languages or level: "+quoted)
		}
	}
}

func hasWarningPrefix(warnings []string, prefix string) bool {
	for _, warning := range warnings {
		if strings.HasPrefix(warning, prefix) {
			return true
		}
	}
	return false
}

func addExtractedCondition(e *models.Evaluation, kind, quoted string) {
	e.Warnings = appendUnique(e.Warnings, "Posting states a condition ("+strings.ReplaceAll(kind, "_", " ")+"): "+quoted)
}
