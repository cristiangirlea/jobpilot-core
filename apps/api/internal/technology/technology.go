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
	"javascript", "typescript", "node.js", "node", "deno", "bun",
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
	"golang": "go", "node": "node.js", "postgres": "postgresql", "google cloud": "gcp",
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
	text := strings.ToLower(strings.Join(texts, " "))
	var b strings.Builder
	b.Grow(len(text) + 2)
	b.WriteByte(' ')
	space := true
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '<' {
			if end := strings.IndexByte(text[i:], '>'); end >= 0 {
				i += end
				c = ' '
			}
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		b.WriteByte(c)
		space = false
	}
	if !space {
		b.WriteByte(' ')
	}
	return b.String()
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
			if n := Count(haystack, term); n > 0 {
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
