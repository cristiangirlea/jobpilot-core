package discovery

import (
	"fmt"
	"strings"
	"testing"

	"jobpilot/core/api/internal/models"
)

func postings(source string, n int) []models.DiscoveredJob {
	out := make([]models.DiscoveredJob, n)
	for i := range out {
		out[i] = models.DiscoveredJob{ExternalID: fmt.Sprintf("%s%d", source, i)}
	}
	return out
}

// Found in early use: the run budget was spent source by source, so
// the first sources used all 100 every run and the seventh source was never
// read. It is now shared: one posting from each source in turn.
func TestShareBudgetReachesEverySource(t *testing.T) {
	sources := []models.DiscoverySource{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	fetched := [][]models.DiscoveredJob{postings("a", 10), postings("b", 1), postings("c", 10)}

	var order []string
	for _, next := range shareBudget(sources, fetched, 7) {
		order = append(order, next.candidate.ExternalID)
		if !strings.HasPrefix(next.candidate.ExternalID, next.source.Name) {
			t.Fatalf("%s was paired with source %s", next.candidate.ExternalID, next.source.Name)
		}
	}
	// b has one posting, so its unused share goes to a and c, and each
	// source's own order is kept.
	if got, want := strings.Join(order, " "), "a0 b0 c0 a1 c1 a2 c2"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}

	if got := len(shareBudget(sources, fetched, 500)); got != 21 {
		t.Fatalf("a budget above what was fetched should take everything, got %d of 21", got)
	}
	// A source whose fetch failed has no postings and takes no share.
	failed := [][]models.DiscoveredJob{nil, postings("b", 3), postings("c", 3)}
	if got := len(shareBudget(sources, failed, 4)); got != 4 {
		t.Fatalf("the failed source's share should go to the others, got %d of 4", got)
	}
}

// A source's share is small once it is shared, so it must go to postings the
// catalog does not hold yet. A board that lists its known postings first
// would otherwise spend every run re-scoring them and never reach a new one.
func TestNewPostingsComeFirstWithinASource(t *testing.T) {
	jobs := []models.DiscoveredJob{
		{ExternalID: "known1", Input: models.JobInput{Title: "A", Company: "C"}},
		{ExternalID: "new1", Input: models.JobInput{Title: "B", Company: "C"}},
		{ExternalID: "known2", Input: models.JobInput{Title: "D", Company: "C"}},
		{ExternalID: "new2", Input: models.JobInput{Title: "E", Company: "C"}},
	}
	known := map[string]bool{Fingerprint(jobs[0].Input): true, Fingerprint(jobs[2].Input): true}
	var order []string
	for _, job := range newFirst(jobs, known) {
		order = append(order, job.ExternalID)
	}
	if got, want := strings.Join(order, " "), "new1 new2 known1 known2"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Found in early use: an employer board lists every open role, so
// Grafana's brought all 148 postings, sales included, into a run whose budget
// is small once shared. A board source's own terms now keep only the titles
// wanted. Feeds that search whole postings already apply their terms.
func TestBoardSourcesKeepOnlyWantedTitles(t *testing.T) {
	listing := []models.DiscoveredJob{
		{ExternalID: "1", Input: models.JobInput{Title: "Senior Backend Engineer - Databases"}},
		{ExternalID: "2", Input: models.JobInput{Title: "Enterprise Account Executive"}},
		{ExternalID: "3", Input: models.JobInput{Title: "Engineering Manager, Mimir"}},
		{ExternalID: "4", Input: models.JobInput{Title: "Go Developer"}},
	}
	ids := func(jobs []models.DiscoveredJob) string {
		var out []string
		for _, job := range jobs {
			out = append(out, job.ExternalID)
		}
		return strings.Join(out, " ")
	}
	board := models.DiscoverySource{Provider: "greenhouse", SearchQuery: "engineer, developer", ExcludedSearchQuery: "manager"}
	if got := ids(keepWanted(board, listing)); got != "1 4" {
		t.Fatalf("board terms should keep engineering titles and drop managers, got %s", got)
	}
	if got := ids(keepWanted(models.DiscoverySource{Provider: "lever"}, listing)); got != "1 2 3 4" {
		t.Fatalf("a board source without terms keeps every posting, got %s", got)
	}
	feed := models.DiscoverySource{Provider: "arbeitnow", SearchQuery: "nothing here"}
	if got := ids(keepWanted(feed, listing)); got != "1 2 3 4" {
		t.Fatalf("a feed has already applied its terms to the whole posting, got %s", got)
	}
}
