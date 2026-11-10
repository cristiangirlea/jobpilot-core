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

// Only ASCII counted as part of a word, so the "go" of "goût" was a whole
// word and a French posting asked for Go.
func TestMentionsDoNotReadInsideAnAccentedWord(t *testing.T) {
	for _, text := range []string{
		"Le goût du challenge.",
		"Le go\U00000302ut du challenge.", // the accent as a combining mark
		"Un protégo, une Ærust.",
		"Un prote\U00000301go.",
	} {
		if got := Mentions("Développeur", text); len(got) != 0 {
			t.Errorf("Mentions(%q) = %v, want nothing", text, got)
		}
		if n := Count(" "+strings.ToLower(text)+" ", "go") + Count(" "+strings.ToLower(text)+" ", "rust"); n != 0 {
			t.Errorf("Count finds %d whole words in %q, want none", n, text)
		}
	}
	// What is not a Latin letter still ends a word: punctuation, a dash, an
	// emoji with its variation selector, a script written without spaces (also
	// one whose last letter carries a combining mark).
	for _, text := range []string{"Go, Rust.", "(Go) and Rust\U00002014fast", "\U00002714\U0000fe0fGo \U00002714\U0000fe0fRust", "熟悉go和rust", "\U00000e17\U00000e35\U00000e48go \U00000e17\U00000e35\U00000e48rust"} {
		if got := Mentions(text); got["go"] != 1 || got["rust"] != 1 {
			t.Errorf("Mentions(%q) = %v, want go and rust once each", text, got)
		}
	}
}

// The texts were joined by a space before counting, so a term of two words
// could start at the end of the title and finish in the description.
func TestMentionsDoNotReadATermAcrossTexts(t *testing.T) {
	if got := Mentions("Engineer using GitHub", "Actions improve delivery."); got["github actions"] != 0 {
		t.Errorf("a term is not read across two texts, got %v", got)
	}
	if got := Signature(0, "Engineer for SQL", "Server rooms are cold."); len(got) != 0 {
		t.Errorf("a signature holds no term read across two texts, got %v", got)
	}
	got := Mentions("GitHub Actions engineer", "You maintain GitHub Actions and Go.", "Go")
	if got["github actions"] != 2 || got["go"] != 2 {
		t.Errorf("mentions inside each text add up, got %v", got)
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

// The boundary rule is about letters: punctuated terms, aliases and the
// signature of a plain ASCII posting are what they were.
func TestPunctuatedTermsAndPlainSignaturesAreUnchanged(t *testing.T) {
	got := Mentions("C++ and C# engineer (.NET)", "ASP.NET, Node.js and Next.js; CI/CD with C++. Not c, not net.")
	// Node.js counts as itself and as its alias node, as it did.
	want := map[string]int{"c++": 2, "c#": 1, ".net": 1, "asp.net": 1, "node.js": 2, "next.js": 1, "ci/cd": 1}
	if len(got) != len(want) {
		t.Errorf("Mentions = %v, want %v", got, want)
	}
	for term, n := range want {
		if got[term] != n {
			t.Errorf("%s counted %d times, want %d: %v", term, got[term], n, got)
		}
	}
	// Ranked by mentions, cut to the limit, then sorted; golang folds into go.
	signature := Signature(3, "Senior Golang Engineer", "Go on Kubernetes with PostgreSQL. Kubernetes operators, Docker, PostgreSQL and Go.")
	if strings.Join(signature, ",") != "go,kubernetes,postgresql" {
		t.Errorf("Signature = %v, want go, kubernetes and postgresql", signature)
	}
	if Count(" go", "go") != 1 || Count(" c++", "c++") != 1 {
		t.Error("a term that ends the text is a whole word")
	}
}
