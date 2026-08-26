package answers

import (
	"strings"
)

// Simple facts are derived once and reused, never re-derived by a model.
//
// A real application form asks for country of residence as a select with two
// hundred options, and for a dozen yes/no questions. Every one of those is a
// lookup against something already stored, so none of them may cost a model
// call. This file is the lookup half: splitting a stored location into its
// parts, and matching a known answer onto a form's own option list.

// countryAliases normalise the spellings a form or a CV might use onto the name
// most option lists carry. Only genuinely ambiguous cases are listed; an exact
// match needs no entry here.
var countryAliases = map[string]string{
	"usa": "United States", "u.s.": "United States", "u.s.a.": "United States",
	"us": "United States", "united states of america": "United States", "america": "United States",
	"uk": "United Kingdom", "u.k.": "United Kingdom", "great britain": "United Kingdom",
	"britain": "United Kingdom", "england": "United Kingdom", "scotland": "United Kingdom",
	"wales": "United Kingdom", "northern ireland": "United Kingdom",
	"deutschland": "Germany", "nederland": "Netherlands", "holland": "Netherlands",
	"the netherlands": "Netherlands", "españa": "Spain", "espana": "Spain",
	"italia": "Italy", "österreich": "Austria", "osterreich": "Austria",
	"sverige": "Sweden", "danmark": "Denmark", "norge": "Norway", "suomi": "Finland",
	"polska": "Poland", "românia": "Romania", "romania": "Romania", "česko": "Czechia",
	"czech republic": "Czechia", "schweiz": "Switzerland", "suisse": "Switzerland",
	"belgië": "Belgium", "belgique": "Belgium", "portugal": "Portugal",
	"éire": "Ireland", "eire": "Ireland", "republic of ireland": "Ireland",
	"uae": "United Arab Emirates", "korea": "South Korea",
}

// SplitLocation separates a stored location into city and country.
//
// "City, Country" is what essentially every CV and profile uses, so the last
// comma-separated part is the country. A location with no comma yields no
// country rather than a guess, because guessing here answers an eligibility
// question wrongly.
func SplitLocation(location string) (city, country string) {
	parts := strings.Split(location, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	var kept []string
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	switch len(kept) {
	case 0:
		return "", ""
	case 1:
		// A single token is a city unless it is a country name we recognise.
		if canonical, ok := countryAliases[strings.ToLower(kept[0])]; ok {
			return "", canonical
		}
		return kept[0], ""
	}
	city = strings.Join(kept[:len(kept)-1], ", ")
	country = kept[len(kept)-1]
	if canonical, ok := countryAliases[strings.ToLower(country)]; ok {
		country = canonical
	}
	return city, country
}

// Country returns just the country part of a stored location.
func Country(location string) string {
	_, country := SplitLocation(location)
	return country
}

// City returns just the city part of a stored location.
func City(location string) string {
	city, _ := SplitLocation(location)
	return city
}

// affirmative and negative cover the wordings option lists actually use.
var affirmative = []string{"yes", "true", "y"}
var negative = []string{"no", "false", "n", "none"}

// MatchOption maps a known answer onto a form's own option list, returning the
// option verbatim so it can be submitted as-is.
//
// Matching is deliberately conservative and ordered from most to least certain.
// A near-miss returns false, which sends the question to review rather than
// submitting a wrong eligibility answer to an employer.
func MatchOption(value string, options []string) (string, bool) {
	needle := normalizeOption(value)
	if needle == "" || len(options) == 0 {
		return "", false
	}

	// 1. Exact, ignoring case, spacing and punctuation.
	for _, option := range options {
		if normalizeOption(option) == needle {
			return option, true
		}
	}

	// 2. The same country under a different spelling.
	if canonical, ok := countryAliases[needle]; ok {
		canonicalNeedle := normalizeOption(canonical)
		for _, option := range options {
			if normalizeOption(option) == canonicalNeedle {
				return option, true
			}
		}
	}
	for _, option := range options {
		if canonical, ok := countryAliases[normalizeOption(option)]; ok && normalizeOption(canonical) == needle {
			return option, true
		}
	}

	// 3. Yes/no phrasing. An option list of exactly two is unambiguous; a longer
	// list (for example the visa question, whose options are "No" plus several
	// "Yes, <scheme>" entries) only resolves for a negative, because choosing
	// which "Yes" applies is not something to infer.
	if group, ok := booleanGroup(needle); ok {
		var matches []string
		for _, option := range options {
			if _, isBool := booleanGroupExact(normalizeOption(option)); isBool && optionGroup(option) == group {
				matches = append(matches, option)
			}
		}
		if len(matches) == 1 {
			return matches[0], true
		}
	}

	// 4. Exactly one option contains the answer as a whole phrase. More than one
	// candidate means the answer is ambiguous, so nothing is returned.
	var contained []string
	for _, option := range options {
		if strings.Contains(normalizeOption(option), needle) {
			contained = append(contained, option)
		}
	}
	if len(contained) == 1 {
		return contained[0], true
	}
	return "", false
}

// MatchBoolean picks the option matching a known yes/no fact.
func MatchBoolean(yes bool, options []string) (string, bool) {
	if yes {
		return MatchOption("yes", options)
	}
	return MatchOption("no", options)
}

func normalizeOption(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = nonWord.ReplaceAllString(v, " ")
	v = spaces.ReplaceAllString(v, " ")
	return strings.TrimSpace(strings.Trim(v, ". "))
}

func booleanGroup(normalized string) (string, bool) {
	return booleanGroupExact(normalized)
}

func booleanGroupExact(normalized string) (string, bool) {
	for _, word := range affirmative {
		if normalized == word {
			return "yes", true
		}
	}
	for _, word := range negative {
		if normalized == word {
			return "no", true
		}
	}
	return "", false
}

func optionGroup(option string) string {
	group, _ := booleanGroupExact(normalizeOption(option))
	return group
}
