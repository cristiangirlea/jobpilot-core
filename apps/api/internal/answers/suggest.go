package answers

import (
	"sort"
	"strings"
)

// technologyNames is how each vocabulary term is written as a skill. Every
// name lowercases back to its term, so a skill picked from a suggestion is
// matched against postings exactly as the term itself would be.
var technologyNames = map[string]string{
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

// TechnologyName is how a vocabulary term is written as a skill.
func TechnologyName(term string) string { return technologyNames[term] }

// SkillSuggestion is a vocabulary technology the user does not list yet.
type SkillSuggestion struct {
	Name string `json:"name"`
	// InCV says the user's own CV mentions it, which makes it the likeliest
	// thing they simply have not added.
	InCV bool `json:"in_cv"`
	// Jobs counts the postings, among those sampled, that mention it.
	Jobs int `json:"jobs"`
}

// SuggestSkills lists the vocabulary minus what the user already has: first
// what their CV mentions, then what the most postings ask for, then the rest
// alphabetically. held are skill names as the user wrote them, fromCV are
// vocabulary terms, and jobTexts are the postings to count demand in.
func SuggestSkills(held, fromCV, jobTexts []string) []SkillSuggestion {
	has := map[string]bool{}
	for _, name := range held {
		has[canonicalTerm(name)] = true
	}
	inCV := map[string]bool{}
	for _, term := range fromCV {
		inCV[canonicalTerm(term)] = true
	}
	demand := map[string]int{}
	for _, text := range jobTexts {
		for _, term := range TechnologySignature(len(knownTechnologies), text) {
			demand[term]++
		}
	}
	out := []SkillSuggestion{}
	for _, term := range knownTechnologies {
		if _, alias := technologyAliases[term]; alias || has[term] {
			continue
		}
		out = append(out, SkillSuggestion{Name: TechnologyName(term), InCV: inCV[term], Jobs: demand[term]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].InCV != out[j].InCV {
			return out[i].InCV
		}
		if out[i].Jobs != out[j].Jobs {
			return out[i].Jobs > out[j].Jobs
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// canonicalTerm maps a skill as written to the vocabulary term it stands for,
// folding case and aliases, so "Golang" and "go" count as the same skill.
func canonicalTerm(name string) string {
	term := strings.ToLower(strings.TrimSpace(name))
	if alias, ok := technologyAliases[term]; ok {
		return alias
	}
	return term
}
