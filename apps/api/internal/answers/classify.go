// Package answers resolves application questions to reusable answers.
//
// Everything in this file is deterministic: normalisation, canonical question
// keys, role families, and technology signatures never call a model. They are
// what makes the answer bank cheap - an unseen question is classified locally,
// and only a genuine miss reaches a provider.
package answers

import (
	"regexp"
	"sort"
	"strings"
)

// Canonical question keys. A key groups differently-worded questions that want
// the same answer, which is what lets one stored answer serve many postings.
const (
	KeyFullName          = "full_name"
	KeyFirstName         = "first_name"
	KeyLastName          = "last_name"
	KeyPreferredName     = "preferred_name"
	KeyWorkedHere        = "worked_here_before"
	KeyLocatedIn         = "located_in"
	KeyCurrentEmployer   = "current_employer"
	KeyCurrentTitle      = "current_title"
	KeyCountryTimezone   = "country_and_timezone"
	KeySpokenLanguages   = "spoken_languages"
	KeyEmail             = "email"
	KeyPhone             = "phone"
	KeyLocation          = "location"
	KeyCountry           = "country"
	KeyCity              = "city"
	KeyLinkedIn          = "linkedin_url"
	KeyGitHub            = "github_url"
	KeyPortfolio         = "portfolio_url"
	KeyAboutYou          = "about_you"
	KeyWhyRole           = "why_role"
	KeyWhyCompany        = "why_company"
	KeyWhyFit            = "why_fit"
	KeySalary            = "salary_expectation"
	KeyNotice            = "notice_period"
	KeyStartDate         = "start_date"
	KeyAuthorization     = "work_authorization"
	KeySponsorship       = "visa_sponsorship"
	KeyRemotePreference  = "remote_preference"
	KeyRelocation        = "relocation"
	KeyYearsExperience   = "years_experience"
	KeyTechExperience    = "technology_experience"
	KeyEnglishLevel      = "english_level"
	KeyReferralSource    = "referral_source"
	KeyConsent           = "data_consent"
	KeyStrengths         = "strengths"
	KeyChallenge         = "challenge_example"
	KeyAdditionalContext = "additional_context"
)

var tagStripper = regexp.MustCompile(`<[^>]*>`)
var nonWord = regexp.MustCompile(`[^a-z0-9+#. ]+`)
var spaces = regexp.MustCompile(`\s+`)
var noise = regexp.MustCompile(`\b(optional|required|please|kindly|briefly|if applicable|if any)\b`)

// Normalize reduces a question to a stable comparison key so that "Why do you
// want to work here?*" and "why do you want to work here (optional)" collapse to
// the same string.
func Normalize(question string) string {
	v := strings.ToLower(tagStripper.ReplaceAllString(question, " "))
	v = strings.NewReplacer("&amp;", "&", "&nbsp;", " ", "’", "'", "‘", "'").Replace(v)
	v = nonWord.ReplaceAllString(v, " ")
	v = noise.ReplaceAllString(v, " ")
	v = spaces.ReplaceAllString(v, " ")
	return strings.TrimSpace(strings.Trim(v, ". "))
}

type rule struct {
	key     string
	pattern *regexp.Regexp
}

// Order matters: the most specific reading of a question wins. "why do you want
// to work at acme" is a company question, not a generic role question.
var rules = []rule{
	{KeyLocatedIn, regexp.MustCompile(`(are|do) you\s+(currently\s+)?(located|based|living|residing|reside|live|work(ing)?)\s*(in|from|out of)\s`)},
	{KeyCountryTimezone, regexp.MustCompile(`country and time ?zone|time ?zone and country|what country and`)},
	{KeyCurrentEmployer, regexp.MustCompile(`^current employer|who is your current employer|name of your current employer|present employer`)},
	{KeyCurrentTitle, regexp.MustCompile(`^current (job )?title|current role|your present (job )?title`)},
	{KeySpokenLanguages, regexp.MustCompile(`languages you speak|which languages|languages? (do you|you) speak|select all the languages`)},
	{KeyWorkedHere, regexp.MustCompile(`previously worked (at|for)|worked at or consulted for|(been|were you) employed (at|by)|former (employee|contractor) of|worked (here|with us) before`)},
	{KeyWhyCompany, regexp.MustCompile(`why (do you want to |would you like to )?(work|join|apply).*(at|for|with) (us|our|this comp|the comp)|why (us|our company|this company)|what (interests|excites|attracts) you about (us|our|this comp)`)},
	{KeyWhyRole, regexp.MustCompile(`why (are you |do you )?(interested|excited|applying).*(role|position|job|opportunity)|what (interests|attracts|excites) you about (this|the) (role|position)|why (this|the) (role|position)`)},
	{KeyWhyFit, regexp.MustCompile(`why (are you |do you think you are |would you be )?(a )?(good |great |strong |the right )?(fit|match|candidate)|what makes you (a )?(good|great|strong|the right)|why should we (hire|consider)`)},
	{KeyAboutYou, regexp.MustCompile(`tell (us|me) (a bit |something )?about (yourself|you)|introduce yourself|who are you|describe yourself|your background`)},
	{KeySalary, regexp.MustCompile(`salary|compensation|remuneration|day rate|hourly rate|rate expectation|expected pay`)},
	{KeyNotice, regexp.MustCompile(`notice period|how much notice|availability to start|earliest start`)},
	{KeyStartDate, regexp.MustCompile(`start date|available from|when would you be available|when can you start`)},
	{KeySponsorship, regexp.MustCompile(`sponsor|sponsorship|require a visa|need a visa|visa support`)},
	{KeyAuthorization, regexp.MustCompile(`(legally )?(authori[sz]ed|eligible|permitted|right) to work|work permit|work authori[sz]ation|citizenship status`)},
	{KeyRelocation, regexp.MustCompile(`relocat|willing to move|open to moving`)},
	{KeyRemotePreference, regexp.MustCompile(`remote|hybrid|onsite|on site|in office|work from home|working arrangement|time ?zone`)},
	{KeyYearsExperience, regexp.MustCompile(`how many years|years of (professional )?experience|total experience`)},
	{KeyTechExperience, regexp.MustCompile(`experience (with|in|using)|describe your experience|rate your (skill|proficiency|experience)|(strong |solid |practical |hands.on )?(proficiency|fluency|expertise|familiarity) (with|in|across)|have you (used|worked with)|familiar with|comfortable with|do you know`)},
	{KeyEnglishLevel, regexp.MustCompile(`english (level|proficiency|fluency)|level of english|do you speak english|language proficiency`)},
	{KeyReferralSource, regexp.MustCompile(`how did you (hear|find out|learn) about|where did you (hear|find)|referred by|referral source`)},
	{KeyConsent, regexp.MustCompile(`consent|gdpr|privacy policy|store your data|process your (personal )?data|agree to the terms`)},
	{KeyStrengths, regexp.MustCompile(`(greatest |biggest |key )?strength|what are you (best|good) at|proudest (achievement|accomplishment)|biggest achievement`)},
	{KeyChallenge, regexp.MustCompile(`describe a (time|situation|project)|tell (us|me) about a (time|challenge|project)|most challenging|difficult (problem|situation)|example of`)},
	{KeyLinkedIn, regexp.MustCompile(`linkedin\s*(profile|url|link|handle|username)|^(your )?linkedin\b`)},
	{KeyGitHub, regexp.MustCompile(`(github|gitlab|bitbucket)\s*(profile|url|link|handle|username|repo)|^(your )?(github|gitlab|bitbucket)\b`)},
	{KeyPortfolio, regexp.MustCompile(`portfolio|personal (website|site)|your website|^website$|^website url|personal url`)},
	{KeyPreferredName, regexp.MustCompile(`preferred (first |given )?name|name you.{0,20}prefer|prefer (us|we) to (use|call)|what should we call you|goes by`)},
	{KeyFirstName, regexp.MustCompile(`^first name|^given name|^forename|^legal first name`)},
	{KeyLastName, regexp.MustCompile(`^last name|^surname|^family name|^legal last name`)},
	{KeyFullName, regexp.MustCompile(`^(your |full |first and last )?name$|^full name|^legal name`)},
	{KeyEmail, regexp.MustCompile(`^(your )?e ?mail|email address`)},
	{KeyPhone, regexp.MustCompile(`phone|mobile number|telephone`)},
	{KeyCountry, regexp.MustCompile(`country of (residence|origin|employment)|which country|what country|country (in which|where) you|country you (are |re )?(currently )?(located|based|living|residing)`)},
	{KeyCity, regexp.MustCompile(`which city|what city|city of residence|city you (are |re )?(currently )?(located|based|living)`)},
	{KeyLocation, regexp.MustCompile(`^(your )?(current )?(location|city|country)|where (are|do) you (based|live|located)`)},
	{KeyAdditionalContext, regexp.MustCompile(`anything else|additional (information|context|comments)|further comments|cover letter|message to (the )?(hiring|recruiter|team)`)},
}

// Classify maps a question to a canonical key, or "" when nothing matches. An
// unclassified question is not a failure: it still goes through the bank on its
// normalized text before any provider is consulted.
func Classify(question string) string {
	normalized := Normalize(question)
	if normalized == "" {
		return ""
	}
	for _, r := range rules {
		if r.pattern.MatchString(normalized) {
			return r.key
		}
	}
	return ""
}

// roleFamilies are ordered so a compound title resolves to its dominant family;
// "senior devops engineer" is devops, not a generic engineering role.
var roleFamilies = []struct {
	family string
	terms  []string
}{
	{"devops", []string{"devops", "sre", "site reliability", "platform engineer", "infrastructure", "cloud engineer", "kubernetes"}},
	{"data", []string{"data engineer", "data scientist", "machine learning", "ml engineer", "analytics", "data analyst", "ai engineer"}},
	{"mobile", []string{"android", "ios", "mobile", "react native", "flutter"}},
	{"qa", []string{"qa", "quality assurance", "test engineer", "sdet", "automation tester"}},
	{"security", []string{"security", "appsec", "penetration", "infosec"}},
	{"management", []string{"engineering manager", "team lead", "tech lead", "head of", "director", "cto", "vp of"}},
	{"fullstack", []string{"full stack", "fullstack", "full-stack"}},
	{"frontend", []string{"frontend", "front end", "front-end", "ui engineer", "react developer", "angular", "vue"}},
	{"backend", []string{"backend", "back end", "back-end", "server side", "api engineer"}},
	{"engineering", []string{"software engineer", "developer", "programmer", "architect"}},
	{"product", []string{"product manager", "product owner", "business analyst"}},
	{"design", []string{"designer", "ux", "ui designer"}},
}

// RoleFamily buckets a job title so an answer written for one backend role can
// be reused for the next one. Returns "general" when nothing matches.
func RoleFamily(title string) string {
	v := strings.ToLower(strings.TrimSpace(title))
	if v == "" {
		return "general"
	}
	// Word boundaries are required, not optional: plain substring matching reads
	// "Contractor" as "cto" and files a backend engineer under management.
	haystack := " " + spaces.ReplaceAllString(v, " ") + " "
	for _, candidate := range roleFamilies {
		for _, term := range candidate.terms {
			if containsTerm(haystack, term) {
				return candidate.family
			}
		}
	}
	return "general"
}

// knownTechnologies is the vocabulary used to build a comparable technology
// signature. It is deliberately a closed list: free-text extraction would make
// signatures unstable and break reuse.
var knownTechnologies = []string{
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

// technologyAliases collapse spellings that mean the same stack, so a signature
// built from one posting matches a signature built from another.
var technologyAliases = map[string]string{
	"golang": "go", "node": "node.js", "postgres": "postgresql", "google cloud": "gcp",
}

// TechnologySignature extracts a sorted, de-duplicated technology list from job
// text, capped at limit. Sorting is what makes it comparable across postings.
func TechnologySignature(limit int, texts ...string) []string {
	if limit <= 0 {
		limit = 12
	}
	haystack := " " + spaces.ReplaceAllString(strings.ToLower(tagStripper.ReplaceAllString(strings.Join(texts, " "), " ")), " ") + " "
	counts := map[string]int{}
	for _, tech := range knownTechnologies {
		mentions := countTerm(haystack, tech)
		if mentions == 0 {
			continue
		}
		if alias, ok := technologyAliases[tech]; ok {
			tech = alias
		}
		counts[tech] += mentions
	}
	out := make([]string, 0, len(counts))
	for tech := range counts {
		out = append(out, tech)
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

// containsTerm reports whether a term appears as a whole word.
func containsTerm(haystack, term string) bool { return countTerm(haystack, term) > 0 }

// countTerm counts whole-word occurrences. The boundary check is what stops
// "go" matching "going" and "rust" matching "trusted".
func countTerm(haystack, term string) int {
	count, offset := 0, 0
	for offset < len(haystack) {
		index := strings.Index(haystack[offset:], term)
		if index < 0 {
			return count
		}
		start := offset + index
		end := start + len(term)
		if start > 0 && !isWordByte(haystack[start-1]) && (end >= len(haystack) || !isWordByte(haystack[end])) {
			count++
		}
		offset = start + 1
	}
	return count
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '+' || b == '#'
}

// Overlap scores how similar two technology signatures are, from 0 to 1. It is
// the ranking signal for reusing an answer written for a "similar" role.
func Overlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]bool, len(a))
	for _, v := range a {
		set[v] = true
	}
	shared := 0
	for _, v := range b {
		if set[v] {
			shared++
		}
	}
	smaller := len(a)
	if len(b) < smaller {
		smaller = len(b)
	}
	return float64(shared) / float64(smaller)
}
