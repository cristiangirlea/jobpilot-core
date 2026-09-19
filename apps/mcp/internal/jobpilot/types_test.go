package jobpilot

import "testing"

func TestSummarizeJobSource(t *testing.T) {
	cases := []struct{ name, provider, want string }{
		{"Remotive feed", "remotive", "Remotive feed (remotive)"},
		{"", "remotive", "remotive"},
		{"Manual import", "", "Manual import"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := summarizeJob(Job{SourceName: c.name, SourceProvider: c.provider}).Source; got != c.want {
			t.Errorf("(%q, %q): got %q, want %q", c.name, c.provider, got, c.want)
		}
	}
}

func TestSummarizeMatchBlanksApplyURLForRejectedJobs(t *testing.T) {
	job := Job{ID: "j", ApplyURL: "https://example.test/apply"}
	if got := summarizeMatch(JobWithMatch{Job: job, Match: Evaluation{Rejected: true}}); got.Job.ApplyURL != "" {
		t.Fatalf("rejected job kept its apply URL: %q", got.Job.ApplyURL)
	}
	if got := summarizeMatch(JobWithMatch{Job: job, Match: Evaluation{Score: 80}}); got.Job.ApplyURL != job.ApplyURL {
		t.Fatalf("accepted job lost its apply URL: %q", got.Job.ApplyURL)
	}
}
