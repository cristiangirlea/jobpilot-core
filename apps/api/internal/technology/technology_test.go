package technology

import (
	"reflect"
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

// A hyphen ends a word, so a sales posting's "go-to-market" asked for Go.
func TestMentionsSkipGoInEverydayPhrases(t *testing.T) {
	for _, text := range []string{
		"Own the go-to-market strategy.",
		"Shape our Go-To-Market plan and go to market fast.",
		"You are the go-to person for releases before go-live.",
		"We go live in May; a go-getter who works on the go.",
		"Own the go\u2011to\u2011market strategy.", // non-breaking hyphens
		"Own the go\u2010to\u2013market strategy.",
		"Our go-to market motion, on-the-go.",
		"Be the go-to engineer for escalations.",
		"You will be our go to person for escalations.",
		"Our go-to tools, go-to-guide.",
		"Be the go\u2013to person.",
		"Be the go-to.",
		"Work on the go.",
		"Work on the go",
		"On-the-go access for field teams.",
		"Looking for a real go getter.",
		"We go live spring 2027.",
		"We go to market spring 2027.",
		"We go to market-entry next quarter.",
	} {
		if got := Mentions(text); got["go"] != 0 {
			t.Errorf("Mentions(%q) = %v, want no go", text, got)
		}
	}
	for text, want := range map[string]int{
		"Go":                                                             1,
		"Experience with Go and Rust.":                                   1,
		"Go in an agile team":                                            1,
		"Golang services, go-to-market too":                              1,
		"We build go-toolchain plugins in Go":                            2,
		"Own the go-to-market of our Go SDK.":                            1,
		"Maintain a Go-to-Python transpiler.":                            1,
		"Port Go-to-Node.js bridges.":                                    1,
		"Maintain a Go-to-SQL Server connector.":                         1,
		"Transpile Go to source code in the target language.":            1,
		"Ship Go to production twice a day.":                             1,
		"A Go-to-Python transpiler for our go\u2011to\u2011market team.": 1,
		"Maintain a Go to Python transpiler.":                            1,
		"Lead the Go to Rust migration.":                                 1,
		"Services built on the Go runtime.":                              1,
		"Build Go live-streaming services.":                              1,
		"Maintain a Go-to-SQL-Server connector.":                         1,
		"We write Go.":                                                   1,
		"Work on the go and stay connected.":                             1, // more words follow: read as Go, as before
		"Services built on the Go-based platform.":                       1,
	} {
		if got := Mentions(text)["go"]; got != want {
			t.Errorf("Mentions(%q)[go] = %d, want %d", text, got, want)
		}
	}
	if got := Signature(0, "Account Executive", "Own the go-to-market strategy in Salesforce."); len(got) != 0 {
		t.Errorf("signature of a posting with only go-to-market = %v, want none", got)
	}
	if got := Signature(0, "Engineer", "Kafka for our go-to-market team."); !reflect.DeepEqual(got, []string{"kafka"}) {
		t.Errorf("signature = %v, want [kafka]", got)
	}
	if got := Signature(0, "Engineer", "Go and Kafka for our go-to-market team."); !reflect.DeepEqual(got, []string{"go", "kafka"}) {
		t.Errorf("signature = %v, want [go kafka]", got)
	}
	if !HasEverydayUses("go") || HasEverydayUses("rust") {
		t.Error("only go has everyday uses")
	}
}

// Outside the listed phrases the verb "go" counted as Go: "our releases go
// through review". Before a word the verb takes, go is the verb, unless the
// text writes "Go" inside a sentence.
func TestMentionsSkipTheVerbGoBeforeItsUsualWords(t *testing.T) {
	for _, texts := range [][]string{
		{"Engineer", "Our releases go through code review."},
		{"Backend Engineer", "As we go from one team to three, you own the platform."},
		{"Engineer", "Nodes come and go from the cluster."},
		{"Engineer", "Your best hours go to the client."},
		{"Engineer", "Ready to go into the code when needed."},
		{"Engineer", "Questions now go straight to the AI tools."},
		{"Engineer", "Ship it. Go further together: we value ownership."},
		{"Engineer", "Our values\nGo beyond the brief"},
		{"Engineer", "<ul><li>Go deeper than the ticket</li></ul>"},
		{"Engineer", "Customer\u2011first values\nGo further together"},
		{"Engineer", "Go to production twice a day."},
		{"Engineer", "OUR RELEASES GO THROUGH REVIEW."},
		{"Engineer", "We go above and beyond; go ahead and ship."},
	} {
		if got := Mentions(texts...); got["go"] != 0 {
			t.Errorf("Mentions(%q) = %v, want no go", texts, got)
		}
		if got := Signature(0, texts...); len(got) != 0 {
			t.Errorf("Signature(%q) = %v, want none", texts, got)
		}
	}
	for _, c := range []struct {
		texts []string
		want  int
	}{
		// Written inside a sentence, Go is the language whatever follows.
		{[]string{"Engineer", "Services written in Go from day one."}, 1},
		{[]string{"Engineer", "We moved from Python to Go through 2025."}, 1},
		{[]string{"Engineer", "Transpile Go to the target language."}, 1},
		// Before a technology, "to" joins two languages.
		{[]string{"Engineer", "Go to Rust migration."}, 1},
		// Everything master counted outside the verb phrases still counts.
		{[]string{"Go"}, 1},
		{[]string{"go"}, 1},
		{[]string{"SENIOR GO DEVELOPER"}, 1},
		{[]string{"Go Back-End Engineer"}, 1},
		{[]string{"Engineer", "How many years have you used go?"}, 1},
		{[]string{"Engineer", "Languages: Python, Rust, and go."}, 1},
		{[]string{"Engineer", "Go is a plus. Go services on Kafka."}, 2},
		{[]string{"Engineer", "Work on the go and stay connected."}, 1},
		{[]string{"Engineer", "Our releases go through review. We write Go."}, 1},
		{[]string{"Engineer", "熟悉go和rust"}, 1},
		{[]string{"Engineer", "Nous développons nos services en Go."}, 1},
		{[]string{"Engineer", "Wir migrieren von Java zu Go."}, 1},
		// A verb word on the next line or in the next block is not this Go's.
		{[]string{"Engineer", "Required languages\nGo\nFurther information below"}, 1},
		{[]string{"Engineer", "<li>Go</li><li>Further languages welcome</li>"}, 1},
		// Emphasis inside a sentence opens nothing; a quote at a line's start does not hide the verb.
		{[]string{"Engineer", "Experience deploying **Go to production** is required."}, 1},
		{[]string{"Engineer", "\"Go further together.\" is our motto."}, 0},
		{[]string{"Engineer", "- Go further together"}, 0},
		// A letter whose lower case is longer still lets "to" join two languages.
		{[]string{"Engineer", "GO-TO-SW\u0130FT bridges."}, 1},
		// A letter whose lower case is shorter before Go keeps the offsets right.
		{[]string{"Engineer", "\u0130stanbul office\nGo further together"}, 0},
		{[]string{"Engineer", "\u0130stanbul: we use Go from day one."}, 1},
		// A bullet starts an item wherever it stands.
		{[]string{"Engineer", "Our values \u2022 Go further together"}, 0},
		{[]string{"Engineer", "Our values \u00b7 Go further together"}, 0},
		{[]string{"Engineer", "Our values \u2013 Go further together"}, 0},
		{[]string{"Engineer", "Our values \u2014 Go further together"}, 0},
		{[]string{"Engineer", "Our values - Go further together"}, 1},
		{[]string{"Engineer", "Our values\n1) Go further together."}, 0},
		{[]string{"Engineer", "We deploy compiled languages, e.g. Go to production."}, 1},
		{[]string{"Engineer", "Our releases go\u00a0through review."}, 0},
		{[]string{"Engineer", "We use <p-chip>Go from day one</p-chip>."}, 1},
		{[]string{"Engineer", "We cover pay, leave, etc. Go further together."}, 0},
		{[]string{"Engineer", "We build a pinned (v1.24) Go from source."}, 1},
		{[]string{"Engineer", "Our values<p\fclass=x>Go further together.</p>"}, 0},
		{[]string{"Engineer", "Check your dashboard on the go\u00a0with our mobile app."}, 0},
		{[]string{"Engineer", "Port Go\u2013to\u2013Rust bridges."}, 1},
		{[]string{"Engineer", "\u0130stanbul needs go\nFurther information below"}, 1},
	} {
		if got := Mentions(c.texts...)["go"]; got != c.want {
			t.Errorf("Mentions(%q)[go] = %d, want %d", c.texts, got, c.want)
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
