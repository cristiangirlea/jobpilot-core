package slug

import (
	"strings"
	"testing"
)

func TestJobUsesTheEndOfTheLinkAndWhereItCameFrom(t *testing.T) {
	cases := []struct {
		name                             string
		applyURL, source, company, title string
		want                             string
	}{
		{"aggregator page with a readable end", "https://www.arbeitnow.com/jobs/companies/larchmont-gmbh/remote-python-backend-engineer-berlin-100001", "arbeitnow", "Larchmont GmbH", "Python Backend Engineer (m/f/x)",
			"arbeitnow-remote-python-backend-engineer-berlin-100001"},
		{"query strings and trailing slashes are ignored", "https://remotive.com/remote-jobs/software-development/head-of-engineering-2090983/?utm_source=x", "remotive", "Lemon.io", "Head of Engineering",
			"remotive-head-of-engineering-2090983"},
		{"a numeric id is kept but named by company and title", "https://job-boards.greenhouse.io/acme/jobs/4567890", "greenhouse", "Acme", "Senior Backend Engineer",
			"greenhouse-acme-senior-backend-engineer-4567890"},
		{"a uuid is shortened, and a trailing /apply is not the job", "https://jobs.lever.co/odaseva/af389cc6-0bf8-4ee5-85da-eddb7ddbd14c/apply", "lever", "Odaseva", "Big Data Engineer",
			"lever-odaseva-big-data-engineer-af389cc6"},
		{"no provider: named after the site", "https://careers.recaresolutions.com/o/senior-backend-engineer-go-mwd", "", "Recare", "Senior Backend Engineer (Go)",
			"recaresolutions-senior-backend-engineer-go-mwd"},
		{"accents are spelled out, not dropped", "https://www.arbeitnow.com/jobs/companies/x/entwickler-für-künstliche-intelligenz-münchen-12", "arbeitnow", "X", "Entwickler",
			"arbeitnow-entwickler-fuer-kuenstliche-intelligenz-muenchen-12"},
		{"no link at all", "", "", "Fresh Systems GmbH", "Senior Backend Engineer",
			"job-fresh-systems-gmbh-senior-backend-engineer"},
	}
	for _, c := range cases {
		if got := Job(c.applyURL, c.source, c.company, c.title); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}

func TestJobSlugsAreShortAndURLSafe(t *testing.T) {
	long := "https://example.com/jobs/" + strings.Repeat("very-long-title-", 20) + "99"
	got := Job(long, "example", "Co", "T")
	if len(got) > maxLength {
		t.Errorf("slug is %d bytes, want at most %d", len(got), maxLength)
	}
	if strings.HasSuffix(got, "-") || strings.Contains(got, "--") {
		t.Errorf("slug %q has stray hyphens", got)
	}
	for _, r := range got {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			t.Fatalf("slug %q contains %q", got, r)
		}
	}
}
