package company

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{" Acme, Inc. ": "acme", "FOO S.R.L.": "foo", "Example GmbH": "example", "A  &  B LLC": "a b"}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestDomainAndSimilarity(t *testing.T) {
	if got := NormalizeDomain("https://www.Example.com/jobs"); got != "example.com" {
		t.Fatalf("domain=%q", got)
	}
	if Similarity("Acme Inc", "Acne") < .7 {
		t.Fatal("expected similar names")
	}
}

func TestAliases(t *testing.T) {
	got := Aliases("New Brand Ltd formerly Old Brand S.R.L.")
	if len(got) != 2 || got[0] != "new brand" || got[1] != "old brand" {
		t.Fatalf("aliases=%v", got)
	}
}
