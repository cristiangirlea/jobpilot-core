package boards

import "testing"

func TestExtractBoardRoot(t *testing.T) {
	cases := []struct {
		name       string
		rawURL     string
		provider   string
		identifier string
	}{
		{"greenhouse", "https://job-boards.greenhouse.io/grafanalabs", "greenhouse", "grafanalabs"},
		{"lever", "https://jobs.lever.co/gitlab", "lever", "gitlab"},
		{"ashby", "https://jobs.ashbyhq.com/linear", "ashby", "linear"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Extract(tc.rawURL)
			if !ok {
				t.Fatalf("Extract(%q) returned no match", tc.rawURL)
			}
			if got.Provider != tc.provider || got.Identifier != tc.identifier || !got.Supported {
				t.Fatalf("Extract(%q) = %+v, want provider %q identifier %q and supported", tc.rawURL, got, tc.provider, tc.identifier)
			}
		})
	}
}

func TestExtractPostingURL(t *testing.T) {
	got, ok := Extract("https://boards.greenhouse.io/example/jobs/123")
	if !ok {
		t.Fatal("expected posting URL to be detected")
	}
	if got.Provider != "greenhouse" || got.Identifier != "example" || got.JobID != "123" || !got.Supported {
		t.Fatalf("unexpected detection: %+v", got)
	}
}

func TestExtractUnsupportedURL(t *testing.T) {
	if _, ok := Extract("https://example.com/careers"); ok {
		t.Fatal("generic careers URL should not be detected")
	}
}
