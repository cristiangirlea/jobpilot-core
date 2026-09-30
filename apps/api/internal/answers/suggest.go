package answers

import (
	"sort"
	"strings"

	"jobpilot/core/api/internal/technology"
)

// TechnologyName is how a vocabulary term is written as a skill.
func TechnologyName(term string) string { return technology.Name(term) }

// SkillSuggestion is a vocabulary technology the user does not list yet.
type SkillSuggestion struct {
	Name string `json:"name"`
	// InCV says the user's own CV mentions it, which makes it the likeliest
	// thing they simply have not added.
	InCV bool `json:"in_cv"`
	// Jobs counts the postings, among those sampled, that mention it.
	Jobs int `json:"jobs"`
}

// SuggestSkills lists the vocabulary minus what the user already has: first
// what their CV mentions, then what the most postings ask for, then the rest
// alphabetically. held are skill names as the user wrote them, fromCV are
// vocabulary terms, and jobTexts are the postings to count demand in.
func SuggestSkills(held, fromCV, jobTexts []string) []SkillSuggestion {
	has := map[string]bool{}
	for _, name := range held {
		has[technology.Canonical(name)] = true
	}
	inCV := map[string]bool{}
	for _, term := range fromCV {
		inCV[technology.Canonical(term)] = true
	}
	demand := map[string]int{}
	for _, text := range jobTexts {
		for _, term := range TechnologySignature(len(technology.Terms()), text) {
			demand[term]++
		}
	}
	out := []SkillSuggestion{}
	for _, term := range technology.Terms() {
		if technology.IsAlias(term) || has[term] {
			continue
		}
		out = append(out, SkillSuggestion{Name: TechnologyName(term), InCV: inCV[term], Jobs: demand[term]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].InCV != out[j].InCV {
			return out[i].InCV
		}
		if out[i].Jobs != out[j].Jobs {
			return out[i].Jobs > out[j].Jobs
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
