package discovery

import (
	"strings"
	"unicode"

	"jobpilot/core/api/internal/models"
)

// repostSimilarity is how alike two descriptions must be, by shared words,
// for a posting to be the same opening reposted under a new link. On a real
// catalog, repeats of a company, title and location split cleanly: 249 of 272
// were at least this alike, and the rest were different openings that happen
// to share a title.
const repostSimilarity = 0.8

// repostOf returns the stored job a posting reposts: the same title and
// location, with a description at least repostSimilarity alike. Callers pass
// jobs from the same provider and company, so a job is only ever merged with
// another listing of the same source.
func repostOf(candidate models.JobInput, stored []models.Job) (models.Job, bool) {
	title, location := sameForm(candidate.Title), sameForm(candidate.Location)
	for _, job := range stored {
		if sameForm(job.Title) != title || sameForm(job.Location) != location {
			continue
		}
		if descriptionSimilarity(candidate.Description, job.Description) >= repostSimilarity {
			return job, true
		}
	}
	return models.Job{}, false
}

// sameForm lowercases and collapses whitespace, the way Fingerprint compares
// titles and locations.
func sameForm(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// descriptionSimilarity is the share of distinct words two descriptions have
// in common, ignoring case and punctuation. Two empty descriptions share
// nothing, so they are not evidence of the same opening.
func descriptionSimilarity(a, b string) float64 {
	left, right := wordSet(a), wordSet(b)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	shared := 0
	for word := range left {
		if right[word] {
			shared++
		}
	}
	return float64(shared) / float64(len(left)+len(right)-shared)
}

func wordSet(s string) map[string]bool {
	words := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		words[word] = true
	}
	return words
}
