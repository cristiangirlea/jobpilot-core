// Package technology is the closed vocabulary of technologies JobPilot
// recognises in postings, CVs, profiles and questions. It is deliberately a
// closed list: free-text extraction would make signatures unstable, and every
// part of the product that compares technologies must agree on the words.
package technology

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// terms is the vocabulary, aliases included.
var terms = []string{
	"go", "golang", "java", "kotlin", "scala", "python", "ruby", "php", "rust", "c#", "c++", "elixir", "erlang", "clojure", "swift", "dart",
	"javascript", "typescript", "node.js", "node", "nodejs", "deno", "bun",
	"react", "next.js", "angular", "vue", "svelte", "remix", "redux", "tailwind",
	"spring", "spring boot", "quarkus", "micronaut", ".net", "asp.net", "django", "flask", "fastapi", "rails", "laravel", "symfony", "express", "nestjs", "gin", "echo", "fiber",
	"postgresql", "postgres", "mysql", "mariadb", "mongodb", "redis", "cassandra", "dynamodb", "elasticsearch", "opensearch", "clickhouse", "snowflake", "bigquery", "sqlite", "oracle", "sql server",
	"kafka", "rabbitmq", "nats", "sqs", "pubsub", "grpc", "graphql", "rest", "openapi", "protobuf", "websocket",
	"docker", "kubernetes", "helm", "terraform", "pulumi", "ansible", "argocd", "jenkins", "github actions", "gitlab ci", "circleci",
	"aws", "azure", "gcp", "google cloud", "digitalocean", "cloudflare", "vercel", "heroku",
	"prometheus", "grafana", "datadog", "opentelemetry", "sentry", "splunk",
	"spark", "airflow", "dbt", "hadoop", "flink", "pandas", "pytorch", "tensorflow", "langchain",
	"linux", "bash", "git", "microservices", "serverless", "ci/cd", "tdd", "agile", "scrum",
}

// aliases collapse spellings that mean the same technology, so a signature
// built from one posting matches a signature built from another.
var aliases = map[string]string{
	"golang": "go", "node": "node.js", "nodejs": "node.js", "postgres": "postgresql", "google cloud": "gcp",
}

// names is how each term is written as a skill. Every name lowercases back to
// its term, so a skill picked from a suggestion is matched against postings
// exactly as the term itself would be.
var names = map[string]string{
	"go": "Go", "java": "Java", "kotlin": "Kotlin", "scala": "Scala", "python": "Python", "ruby": "Ruby", "php": "PHP", "rust": "Rust",
	"c#": "C#", "c++": "C++", "elixir": "Elixir", "erlang": "Erlang", "clojure": "Clojure", "swift": "Swift", "dart": "Dart",
	"javascript": "JavaScript", "typescript": "TypeScript", "node.js": "Node.js", "deno": "Deno", "bun": "Bun",
	"react": "React", "next.js": "Next.js", "angular": "Angular", "vue": "Vue", "svelte": "Svelte", "remix": "Remix", "redux": "Redux", "tailwind": "Tailwind",
	"spring": "Spring", "spring boot": "Spring Boot", "quarkus": "Quarkus", "micronaut": "Micronaut", ".net": ".NET", "asp.net": "ASP.NET",
	"django": "Django", "flask": "Flask", "fastapi": "FastAPI", "rails": "Rails", "laravel": "Laravel", "symfony": "Symfony",
	"express": "Express", "nestjs": "NestJS", "gin": "Gin", "echo": "Echo", "fiber": "Fiber",
	"postgresql": "PostgreSQL", "mysql": "MySQL", "mariadb": "MariaDB", "mongodb": "MongoDB", "redis": "Redis", "cassandra": "Cassandra",
	"dynamodb": "DynamoDB", "elasticsearch": "Elasticsearch", "opensearch": "OpenSearch", "clickhouse": "ClickHouse", "snowflake": "Snowflake",
	"bigquery": "BigQuery", "sqlite": "SQLite", "oracle": "Oracle", "sql server": "SQL Server",
	"kafka": "Kafka", "rabbitmq": "RabbitMQ", "nats": "NATS", "sqs": "SQS", "pubsub": "PubSub", "grpc": "gRPC", "graphql": "GraphQL",
	"rest": "REST", "openapi": "OpenAPI", "protobuf": "Protobuf", "websocket": "WebSocket",
	"docker": "Docker", "kubernetes": "Kubernetes", "helm": "Helm", "terraform": "Terraform", "pulumi": "Pulumi", "ansible": "Ansible",
	"argocd": "ArgoCD", "jenkins": "Jenkins", "github actions": "GitHub Actions", "gitlab ci": "GitLab CI", "circleci": "CircleCI",
	"aws": "AWS", "azure": "Azure", "gcp": "GCP", "digitalocean": "DigitalOcean", "cloudflare": "Cloudflare", "vercel": "Vercel", "heroku": "Heroku",
	"prometheus": "Prometheus", "grafana": "Grafana", "datadog": "Datadog", "opentelemetry": "OpenTelemetry", "sentry": "Sentry", "splunk": "Splunk",
	"spark": "Spark", "airflow": "Airflow", "dbt": "dbt", "hadoop": "Hadoop", "flink": "Flink", "pandas": "pandas", "pytorch": "PyTorch",
	"tensorflow": "TensorFlow", "langchain": "LangChain",
	"linux": "Linux", "bash": "Bash", "git": "Git", "microservices": "Microservices", "serverless": "Serverless", "ci/cd": "CI/CD",
	"tdd": "TDD", "agile": "Agile", "scrum": "Scrum",
}

// practices are ways of working rather than technologies. A posting that
// mentions agile or REST says little about the stack a candidate must know.
var practices = map[string]bool{
	"rest": true, "git": true, "linux": true, "bash": true, "microservices": true,
	"serverless": true, "ci/cd": true, "tdd": true, "agile": true, "scrum": true,
}

// everyday lists phrases in which a term is an ordinary word, not the
// technology: a sales posting's "go live" asks for no Go. A phrase is ordinary
// only where what follows agrees (see blankPhrase). A "go-to" compound is
// ordinary too ("go-to-market", "the go-to engineer") unless it joins two
// technologies ("Go-to-Python").
var everyday = map[string][]string{
	"go": {
		"go to market", "go live", "go-live", "go getter", "go-getter", "on the go", "on-the-go",
		"a go to", "the go to", "our go to", "your go to", "my go to", "their go to",
	},
}

// verbWords follow a term where it is a verb ("go through review", "go to the
// client") and seldom where it is the technology; see countOutsideVerbUses.
// "to" counts only before these words, so "Go to Rust" stays two languages.
var verbWords = map[string][]string{
	"go": {
		"through", "from", "further", "into", "beyond", "above", "straight", "deeper", "ahead", "far",
		"to the", "to a", "to an", "to our", "to your", "to their", "to production",
	},
}

// ordinaryNeighbours lists words beside which a term names something else:
// "node" next to a cluster or tree word is a machine or a graph's node, not
// Node.js ("single-node", "worker node", "node failures"). Only the bare
// alias is read this way; "Node.js" always counts.
var ordinaryNeighbours = map[string]struct{ before, after []string }{
	"node": {
		before: []string{
			"single", "multi", "worker", "master", "edge", "compute", "each", "per", "every", "leaf",
			"root", "child", "parent", "graph", "tree", "cluster", "storage", "gpu", "k8s", "kubernetes",
			"plane", "validator", "full", "bootstrap", "head", "data", "control",
		},
		after: []string{
			"failure", "failures", "health", "pool", "pools", "affinity", "selector", "selectors",
			"exporter", "count", "group", "groups", "drain", "crash", "crashes", "pressure", "label",
			"labels", "autoscaler", "autoscaling", "size", "sizes", "type", "types", "taint", "taints",
			"issue", "issues",
		},
	},
}

// neighbourGap is what may stand between a term and an ordinary neighbour.
const neighbourGap = " -_\u2010\u2011\u2013"

// withoutNeighbours blanks each whole-word term that one of its ordinary
// neighbours touches on the same line, across a space, hyphen or underscore.
// A term the technology's own suffix follows ("node.js", "node js") stays.
// breaks holds the spaces that stand for a line break or a block tag.
func withoutNeighbours(haystack, term string, breaks map[int]bool) string {
	words := ordinaryNeighbours[term]
	var out []byte
	for offset := 0; offset < len(haystack); {
		index := strings.Index(haystack[offset:], term)
		if index < 0 {
			break
		}
		start := offset + index
		end := start + len(term)
		offset = start + 1
		if start == 0 || endsInWord(haystack[:start]) || startsInWord(haystack[end:]) {
			continue
		}
		before := strings.TrimRight(haystack[:start], neighbourGap)
		after := strings.TrimLeft(haystack[end:], neighbourGap)
		jsSuffix := strings.HasPrefix(haystack[end:], ".js") && !startsInWord(haystack[end+3:])
		if jsSuffix || followedBy(after, []string{"js"}) && !anyBreak(breaks, end, len(haystack)-len(after)) {
			continue
		}
		beforeNear := endsWithWord(before, words.before) && !anyBreak(breaks, len(before), start)
		afterNear := followedBy(after, words.after) && !anyBreak(breaks, end, len(haystack)-len(after))
		if !beforeNear && !afterNear {
			continue
		}
		if out == nil {
			out = []byte(haystack)
		}
		for i := start; i < end; i++ {
			out[i] = ' '
		}
	}
	if out == nil {
		return haystack
	}
	return string(out)
}

func anyBreak(breaks map[int]bool, from, to int) bool {
	for i := from; i < to; i++ {
		if breaks[i] {
			return true
		}
	}
	return false
}

func endsWithWord(s string, words []string) bool {
	for _, word := range words {
		if strings.HasSuffix(s, word) && !endsInWord(s[:len(s)-len(word)]) {
			return true
		}
	}
	return false
}

// HasEverydayUses reports whether term is also an ordinary word in some
// phrases, so only Mentions can say whether a text names it.
func HasEverydayUses(term string) bool { return everyday[term] != nil || HasNeighbours(term) }

// HasNeighbours reports whether term, or the alias it stands for, has
// ordinary neighbours, so a word written like it may be something else.
func HasNeighbours(term string) bool {
	for t := range ordinaryNeighbours {
		if t == term || Canonical(t) == term {
			return true
		}
	}
	return false
}

// hyphens writes a non-breaking hyphen or an en dash as a hyphen.
var hyphens = strings.NewReplacer("\u2010", "-", "\u2011", "-", "\u2013", "-")

// withoutEveryday blanks term where it is an ordinary word in haystack, so
// Count no longer finds it there.
func withoutEveryday(haystack, term string) string {
	if everyday[term] == nil {
		return haystack
	}
	text := haystack
	if strings.ContainsAny(text, "\u2010\u2011\u2013") {
		text = hyphens.Replace(text)
	}
	text = blankToCompounds(text, term)
	for _, phrase := range everyday[term] {
		text = blankPhrase(text, phrase, term)
	}
	return text
}

// blankPhrase blanks each whole-word occurrence of an everyday phrase unless
// what follows says the term is the technology after all: after a phrase
// ending in "to", a technology ("a Go to Python transpiler"); after "go live",
// a hyphen joining on ("Go live-streaming"); after a phrase ending in the
// term, more words ("on the Go runtime" against "on the go.").
func blankPhrase(haystack, phrase, term string) string {
	var out []byte
	endsInTo := strings.HasSuffix(phrase, " to")
	endsInLive := strings.HasSuffix(phrase, " live")
	endsInTerm := strings.HasSuffix(phrase, " "+term)
	for offset := 0; offset < len(haystack); {
		index := strings.Index(haystack[offset:], phrase)
		if index < 0 {
			break
		}
		start := offset + index
		end := start + len(phrase)
		offset = start + 1
		if start == 0 || endsInWord(haystack[:start]) || startsInWord(haystack[end:]) {
			continue
		}
		rest := haystack[end:]
		next := strings.TrimLeft(rest, " ")
		if endsInLive && strings.HasPrefix(rest, "-") || endsInTo && startsWithTerm(next) || endsInTerm && (strings.HasPrefix(rest, "-") || next != "" && isWordByte(next[0])) {
			continue
		}
		if out == nil {
			out = []byte(haystack)
		}
		for i := start; i < end; i++ {
			out[i] = ' '
		}
	}
	if out == nil {
		return haystack
	}
	return string(out)
}

// blankToCompounds blanks term in each "term-to" compound whose next part,
// if hyphenated on, is not a technology.
func blankToCompounds(haystack, term string) string {
	var out []byte
	for offset := 0; offset < len(haystack); {
		index := strings.Index(haystack[offset:], term)
		if index < 0 {
			break
		}
		start := offset + index
		end := start + len(term)
		offset = start + 1
		if start == 0 || endsInWord(haystack[:start]) {
			continue
		}
		rest, ok := afterHyphen(haystack[end:])
		if !ok || !strings.HasPrefix(rest, "to") || startsInWord(rest[2:]) {
			continue
		}
		if next, ok := afterHyphen(rest[2:]); ok && startsWithTerm(next) {
			continue
		}
		if out == nil {
			out = []byte(haystack)
		}
		for i := start; i < end; i++ {
			out[i] = ' '
		}
	}
	if out == nil {
		return haystack
	}
	return string(out)
}

func afterHyphen(s string) (string, bool) {
	r, size := utf8.DecodeRuneInString(s)
	switch r {
	case '-', '\u2010', '\u2011', '\u2013':
		return s[size:], true
	}
	return s, false
}

// startsWithTerm reports whether s starts with a whole vocabulary term,
// reading hyphens as spaces ("sql-server").
func startsWithTerm(s string) bool {
	if len(s) > 32 {
		s = s[:32]
	}
	s = strings.ReplaceAll(s, "-", " ")
	for _, term := range terms {
		if strings.HasPrefix(s, term) && !startsInWord(s[len(term):]) {
			return true
		}
	}
	return false
}

// Terms returns the vocabulary, aliases included, in its fixed order.
func Terms() []string { return append([]string(nil), terms...) }

// IsAlias reports whether term is another spelling of a vocabulary term.
func IsAlias(term string) bool { _, ok := aliases[term]; return ok }

// IsPractice reports whether term is a way of working rather than a
// technology.
func IsPractice(term string) bool { return practices[term] }

// Name is how a term is written as a skill, or "" for a word outside the
// vocabulary.
func Name(term string) string { return names[term] }

// Canonical folds a skill or term as written to the term it stands for,
// ignoring case and surrounding space and resolving aliases, so "Golang" and
// "go" are the same technology. A word outside the vocabulary is returned
// lowercased.
func Canonical(name string) string {
	term := strings.ToLower(strings.TrimSpace(name))
	if alias, ok := aliases[term]; ok {
		return alias
	}
	return term
}

// haystackOf lowercases texts into one string, turns markup tags into a
// space, collapses runs of ASCII whitespace, and pads both ends with a space,
// as the <[^>]*> and \s+ patterns it replaced did, in one pass: it runs on
// every posting each time a job is scored.
func haystackOf(texts []string) string {
	text, _ := scanHaystack(strings.ToLower(strings.Join(texts, " ")), false)
	return text
}

// keptHaystack is haystackOf for one text with its case kept, and with the
// positions of the spaces that stand for a line break or a block tag, so a
// line's first word can be told from one inside a sentence.
func keptHaystack(text string) (string, map[int]bool) {
	return scanHaystack(text, true)
}

func scanHaystack(text string, markBreaks bool) (string, map[int]bool) {
	var b strings.Builder
	b.Grow(len(text) + 2)
	b.WriteByte(' ')
	space, lineBreak := true, false
	var breaks map[int]bool
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '<' {
			if end := strings.IndexByte(text[i:], '>'); end >= 0 {
				if markBreaks && blockTag(text[i+1:i+end]) {
					lineBreak = true
				}
				i += end
				c = ' '
			}
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' {
			if markBreaks && (c == '\n' || c == '\r') {
				lineBreak = true
			}
			if !space {
				b.WriteByte(' ')
				space = true
			}
			if lineBreak {
				// The space written for this run of whitespace stands for the break.
				if breaks == nil {
					breaks = map[int]bool{}
				}
				breaks[b.Len()-1] = true
				lineBreak = false
			}
			continue
		}
		b.WriteByte(c)
		space = false
	}
	if !space {
		b.WriteByte(' ')
	}
	return b.String(), breaks
}

// blockTag reports whether a tag's contents name an element that starts a new
// line, such as p, li or br.
func blockTag(tag string) bool {
	// The name ends at a space or "/", so a custom element such as "p-chip" is not "p".
	name := strings.ToLower(strings.TrimLeft(tag, "/ "))
	if end := strings.IndexAny(name, " \t\r\n\f/"); end >= 0 {
		name = name[:end]
	}
	switch name {
	case "p", "br", "li", "div", "ul", "ol", "tr", "td", "th", "h1", "h2", "h3", "h4", "h5", "h6", "dt", "dd", "hr", "section", "blockquote", "pre":
		return true
	}
	return false
}

// verbSpan is more than the space and the longest verb word take.
const verbSpan = 32

// MayMention reports whether text names term as Mentions read it before verb
// words were left out: a caller to whom a missed mention costs more than a
// stray verb, such as a question about years with one skill, asks this too.
func MayMention(text, term string) bool {
	haystack := haystackOf([]string{text})
	if HasEverydayUses(term) {
		haystack = withoutEveryday(haystack, term)
	}
	return Count(haystack, term) > 0
}

// countOutsideVerbUses is Count for a term with verbWords: it leaves out each
// occurrence that a verb word follows, unless the text writes the term as a
// name ("Go") inside a sentence. A name opening a sentence or line ("Go
// further together") and the term in capitals ("GO THROUGH") are the verb.
func countOutsideVerbUses(text, term string) int {
	kept, breaks := keptHaystack(text)
	lower, at := lowerWithOffsets(kept)
	if HasEverydayUses(term) {
		lower = withoutEveryday(lower, term)
	}
	n := 0
	for offset := 0; offset < len(lower); {
		index := strings.Index(lower[offset:], term)
		if index < 0 {
			break
		}
		start := offset + index
		end := start + len(term)
		offset = start + 1
		if start == 0 || endsInWord(lower[:start]) || startsInWord(lower[end:]) {
			continue
		}
		// A verb word on the next line is not this word's; no verb word is
		// longer than verbSpan, so only that much is searched for a break.
		rest := lower[end:min(len(lower), end+verbSpan)]
		for i := end; i < end+len(rest); i++ {
			if breaks[offsetAt(at, i)] {
				rest = lower[end:i]
				break
			}
		}
		k := offsetAt(at, start)
		named := kept[k] >= 'A' && kept[k] <= 'Z' && kept[k+1] >= 'a' && kept[k+1] <= 'z'
		// A no-break space separates words here as a space does.
		rest = strings.ReplaceAll(rest, "\u00a0", " ")
		if followedBy(strings.TrimLeft(rest, " "), verbWords[term]) && !(named && !opensSentence(kept, breaks, k)) {
			continue
		}
		n++
	}
	return n
}

func followedBy(s string, words []string) bool {
	for _, word := range words {
		if strings.HasPrefix(s, word) && !startsInWord(s[len(word):]) {
			return true
		}
	}
	return false
}

// opensSentence reports whether the word at start begins the text, a line, a
// list item or a sentence. Opening quotes, emphasis and "-" or "*" before it
// are passed over, so "**Go to production**" inside a sentence opens nothing
// while a quoted "Go further" at a line's start does.
func opensSentence(kept string, breaks map[int]bool, start int) bool {
	i := start
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(kept[:i])
		// A bullet or a dash starts an item or a clause wherever it stands.
		if strings.ContainsRune("\u2022\u00b7\u2013\u2014", r) {
			return true
		}
		// A numbered item's marker ("1)") is passed over like a bullet.
		if r == ')' && i > 1 && kept[i-2] >= '0' && kept[i-2] <= '9' {
			j := i - 1
			for j > 0 && kept[j-1] >= '0' && kept[j-1] <= '9' {
				j--
			}
			// Only a number standing on its own, not "v1.24)".
			if j == 0 || kept[j-1] == ' ' {
				i = j
				continue
			}
		}
		if r != ' ' && !strings.ContainsRune("\"'(*_\u201c\u2018-", r) {
			break
		}
		i -= size
		if r == ' ' && breaks[i] {
			return true
		}
	}
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(kept[:i])
	if r == '.' && abbreviation(kept[:i]) {
		return false
	}
	return strings.ContainsRune(".!?;", r)
}

// abbreviation reports a full stop that ends "e.g.", "i.e." or "vs.", which a
// sentence does not ("etc." often does).
func abbreviation(before string) bool {
	before = strings.ToLower(before[max(0, len(before)-6):])
	for _, short := range []string{" e.g.", " i.e.", " vs.", "(e.g.", "(i.e."} {
		if strings.HasSuffix(before, short) {
			return true
		}
	}
	return false
}

// lowerWithOffsets lowercases s as strings.ToLower does, writes the hyphens
// withoutEveryday reads as one as "-", and gives, for each byte of the result,
// the offset in s of the letter it came from: a letter's lower case can be
// shorter or longer ("İ"). For ASCII text the offsets are the same and no
// table is made (see offsetAt).
func lowerWithOffsets(s string) (string, []int) {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		b := []byte(s)
		for i, c := range b {
			if c >= 'A' && c <= 'Z' {
				b[i] = c + 'a' - 'A'
			}
		}
		return string(b), nil
	}
	var b strings.Builder
	b.Grow(len(s))
	at := make([]int, 0, len(s)+1)
	for i, r := range s {
		n := b.Len()
		switch r {
		case '\u2010', '\u2011', '\u2013':
			b.WriteByte('-')
		default:
			b.WriteRune(unicode.ToLower(r))
		}
		for range b.Len() - n {
			at = append(at, i)
		}
	}
	return b.String(), append(at, len(s))
}

// offsetAt is the offset in the original text of byte i of the lowered one.
func offsetAt(at []int, i int) int {
	if at == nil {
		return i
	}
	return at[i]
}

// leads holds each term's first run of letters and digits: ".net" leads
// with "net", "ci/cd" with "ci". A term cannot appear in a text that lacks
// that word, which spares most of the vocabulary a scan of every posting.
var leads = func() []string {
	out := make([]string, len(terms))
	for i, term := range terms {
		out[i] = strings.FieldsFunc(term, notAlphanumeric)[0]
	}
	return out
}()

func notAlphanumeric(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') }

// Mentions counts how often texts mention each vocabulary technology, as a
// whole word, keyed by canonical term. Every evaluation of a job runs it, so
// only terms whose leading word the text contains are counted. Each text is
// counted on its own, so no term is read across a title and a description.
func Mentions(texts ...string) map[string]int {
	counts := map[string]int{}
	for t := range texts {
		haystack := haystackOf(texts[t : t+1])
		present := map[string]bool{}
		for _, word := range strings.FieldsFunc(haystack, notAlphanumeric) {
			present[word] = true
		}
		for i, term := range terms {
			if !present[leads[i]] {
				continue
			}
			var n int
			if verbWords[term] != nil {
				n = countOutsideVerbUses(texts[t], term)
			} else {
				text := haystack
				if HasEverydayUses(term) {
					text = withoutEveryday(haystack, term)
				}
				if ordinaryNeighbours[term].before != nil {
					// Line breaks are only needed here, so only read here.
					_, breaks := scanHaystack(strings.ToLower(texts[t]), true)
					text = withoutNeighbours(text, term, breaks)
				}
				n = Count(text, term)
			}
			if n > 0 {
				counts[Canonical(term)] += n
			}
		}
	}
	return counts
}

// Signature extracts a sorted, de-duplicated technology list from texts,
// capped at limit (12 when limit is not positive). Sorting is what makes it
// comparable across postings.
func Signature(limit int, texts ...string) []string {
	if limit <= 0 {
		limit = 12
	}
	counts := Mentions(texts...)
	out := make([]string, 0, len(counts))
	for term := range counts {
		out = append(out, term)
	}
	// Rank by how often a technology is mentioned before truncating. Cutting an
	// alphabetically sorted list instead would keep "angular" and drop
	// "typescript" purely because of the letter it starts with.
	sort.SliceStable(out, func(i, j int) bool {
		if counts[out[i]] != counts[out[j]] {
			return counts[out[i]] > counts[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > limit {
		out = out[:limit]
	}
	// Sorted output keeps the stored signature stable regardless of how the
	// source text happened to be ordered.
	sort.Strings(out)
	return out
}

// Count counts whole-word occurrences of term in haystack, which callers
// lowercase and pad with spaces. The boundary check is what stops "go"
// matching "going" and "rust" matching "trusted".
func Count(haystack, term string) int {
	count, offset := 0, 0
	for offset < len(haystack) {
		index := strings.Index(haystack[offset:], term)
		if index < 0 {
			return count
		}
		start := offset + index
		end := start + len(term)
		if start > 0 && !endsInWord(haystack[:start]) && !startsInWord(haystack[end:]) {
			count++
		}
		offset = start + 1
	}
	return count
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '+' || b == '#'
}

// endsInWord and startsInWord say whether the text on either side of a term
// continues its word: a word byte, an accented Latin letter, or an accent
// combining with one. Other scripts end a word, so a name inside text written
// without spaces is still found.
func endsInWord(s string) bool {
	for s != "" {
		r, size := utf8.DecodeLastRuneInString(s)
		if r < utf8.RuneSelf {
			return isWordByte(byte(r))
		}
		if !combining(r) {
			return unicode.Is(unicode.Latin, r)
		}
		// The letter the accent sits on decides.
		s = s[:len(s)-size]
	}
	return false
}

func startsInWord(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	if r < utf8.RuneSelf {
		return isWordByte(byte(r))
	}
	return unicode.Is(unicode.Latin, r) || combining(r)
}

// combining reports a combining accent; a variation selector belongs to an emoji.
func combining(r rune) bool {
	return unicode.Is(unicode.Mn, r) && !unicode.Is(unicode.Variation_Selector, r)
}
