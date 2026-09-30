package technology

import (
	"regexp"
	"strings"
	"testing"
)

// A suggested name is what the user will store as a skill, and matching finds
// a skill in a posting by its lowercased words. A name that differs from its
// vocabulary term by more than case ("Tailwind CSS" for "tailwind") would stop
// matching postings that only say "Tailwind".
func TestEveryTechnologyHasANameThatMatchesLikeItsTerm(t *testing.T) {
	for _, term := range Terms() {
		if IsAlias(term) {
			continue
		}
		name := Name(term)
		if name == "" {
			t.Errorf("%q has no display name", term)
			continue
		}
		if strings.ToLower(name) != term {
			t.Errorf("Name(%q) = %q, which does not lowercase back to the term", term, name)
		}
	}
}

// Mentions counts whole words, folds spellings into one technology, and reads
// titles and markup the way postings arrive.
func TestMentionsCountsWholeWordsUnderOneSpelling(t *testing.T) {
	got := Mentions("Senior Golang Engineer", "<p>We are going to build Go services on Postgres and PostgreSQL.</p>")
	if got["go"] != 2 || got["postgresql"] != 2 {
		t.Fatalf("golang and go, postgres and postgresql should each count as one technology, got %v", got)
	}
	if _, ok := got["golang"]; ok {
		t.Fatalf("an alias should not appear under its own name: %v", got)
	}
}

func TestCanonicalFoldsCaseSpaceAndAliases(t *testing.T) {
	for in, want := range map[string]string{" Golang ": "go", "PostgreSQL": "postgresql", "Node": "node.js", "WordPress": "wordpress"} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPracticesAreNotTechnologies(t *testing.T) {
	if !IsPractice("agile") || !IsPractice("rest") || IsPractice("kafka") {
		t.Fatal("agile and REST are ways of working; Kafka is a technology")
	}
}

// haystackOf replaced two patterns in the hot path of every job evaluation,
// so it has to produce what they did. The one difference is padding: the
// patterns could leave two spaces at an end, haystackOf leaves one, and no
// whole-word count depends on it. Non-ASCII spaces and \v stayed as they were
// under \s, and still do.
func TestHaystackMatchesThePatternsItReplaced(t *testing.T) {
	tags, spaces := regexp.MustCompile(`<[^>]*>`), regexp.MustCompile(`\s+`)
	for _, texts := range [][]string{
		{""}, {"Go"}, {"  Senior  Go\tEngineer\r\n"},
		{"<p>Build <b>Go</b> services</p>", "on AWS."},
		{"a < b and c > d", "unclosed <tag"},
		{"Überweisung\u00a0für\vEntwickler — naïve", "C++, C#, .NET; Node.js"},
	} {
		old := " " + spaces.ReplaceAllString(strings.ToLower(tags.ReplaceAllString(strings.Join(texts, " "), " ")), " ") + " "
		want := " "
		if inner := strings.Trim(old, " "); inner != "" {
			want = " " + inner + " "
		}
		if got := haystackOf(texts); got != want {
			t.Errorf("haystackOf(%q) = %q, want %q", texts, got, want)
		}
	}
}
