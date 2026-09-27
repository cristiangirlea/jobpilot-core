package discovery

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
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

// olderCopies maps each job that is an older copy of another in jobs to the
// copy that replaces it: the newest posting of the same opening, by posting
// date, then by when it was stored. An undated copy counts as the oldest.
// Jobs are one provider's jobs at one company.
func olderCopies(jobs []models.Job) map[string]string {
	sorted := append([]models.Job(nil), jobs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].PostedAt, sorted[j].PostedAt
		if (a == nil) != (b == nil) {
			return a != nil
		}
		if a != nil && !a.Equal(*b) {
			return a.After(*b)
		}
		return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
	})
	copies := map[string]string{}
	var newest []models.Job
	for _, job := range sorted {
		if survivor, ok := repostOf(job.JobInput, newest); ok {
			copies[job.ID] = survivor.ID
			continue
		}
		newest = append(newest, job)
	}
	return copies
}

// CloseStoredReposts closes the older copies of openings stored once per
// posting before reposts were recognised, keeping the newest posting of
// each, and reports how many it closed. A copy someone has an application or
// outbox entry for stays open. Later runs find nothing to do.
func CloseStoredReposts(ctx context.Context, st *store.Store) (int64, error) {
	groups, err := st.RepeatGroups(ctx)
	if err != nil {
		return 0, err
	}
	var closed int64
	for _, group := range groups {
		jobs, err := st.RepostCandidates(ctx, group[0], group[1])
		if err != nil {
			return closed, err
		}
		open := jobs[:0]
		for _, job := range jobs {
			if job.ClosedAt == nil {
				open = append(open, job)
			}
		}
		ids := make([]string, 0)
		for id := range olderCopies(open) {
			ids = append(ids, id)
		}
		n, err := st.CloseReposts(ctx, ids, ReasonReposted)
		closed += n
		if err != nil {
			return closed, err
		}
	}
	return closed, nil
}
