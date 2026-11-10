package matching

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"jobpilot/core/api/internal/models"
)

const personnelClearance = `(?:security|secret|top secret|confidential|ts/sci|sc|dv|baseline|nv1|nv2|public trust)`
const securityQualifier = `(?:` + personnelClearance + `|government|federal)`
const clearanceModifiers = `(?:(?:a|an|the|active|current|valid|uk|us|eligible) ){0,2}`
const statusModifiers = `(?:(?:a|an|the|dual|full|valid|eu|us|uk|(?-i:[A-Z][A-Za-z.]*)) ){0,2}`

// A citizen with a nationality, then a definition in brackets or the role.
// Without the nationality, "citizens (in the broad sense)" is not a legal status.
const namedCitizen = `(?:be|become) (?:a |an )?(?:(?-i:[A-Z][A-Za-z.-]*) ){1,2}citizens? (?:\(|(?:for|to (?:fill|hold|be considered for)) (?:this|the) (?:role|position|job)\b)`

var eligibilityRules = []struct {
	required *regexp.Regexp
	negated  *regexp.Regexp
}{
	{regexp.MustCompile(`(?i)\b(?:(?:must|shall|need to|are required to) (?:(?:be|become) (?:a |an )?(?:(?-i:[A-Z][A-Za-z.-]*) ){0,2}citizens?(?: of (?-i:\p{Lu})| to apply\b| or (?:a )?(?:green card holder|(?:lawful )?permanent resident)\b|[.!?,;]|$)|` + namedCitizen + `|(?:have|hold|possess) ` + statusModifiers + `citizenship\b)|citizenship (?:is )?(?:required|mandatory|essential)|requires? ` + statusModifiers + `citizenship\b|restricted to ` + statusModifiers + `citizens\b|citizens only\b|only ` + statusModifiers + `citizens (?:may|can|are eligible))`), regexp.MustCompile(`(?i)\b(?:no citizenship|citizenship (?:is )?not (?:required|necessary|mandatory)|citizenship (?:is )?optional)\b`)},
	{regexp.MustCompile(`(?i)\b(?:(?:must|shall|need to|are required to) (?:be|have|hold) ` + statusModifiers + `permanent residen(?:t|cy|ce)\b|permanent (?:residen(?:cy|ce)|resident status) (?:is )?(?:required|mandatory|essential)|permanent residents only|(?:requires?|restricted to) ` + statusModifiers + `permanent residen(?:ts?|cy|ce)\b)`), regexp.MustCompile(`(?i)\b(?:no permanent residen(?:cy|ce)|permanent residen(?:cy|ce) (?:is )?not (?:required|necessary|mandatory))\b`)},
	// Government approval of products is not a personnel clearance.
	{regexp.MustCompile(`(?i)\b(?:(?:must|shall|need to|requires?|are required to) (?:(?:(?:have|hold|possess|obtain)|be eligible to (?:obtain|hold)|be able to obtain(?: \(?and maintain\)?)?|the ability to obtain) ` + clearanceModifiers + securityQualifier + `|` + clearanceModifiers + personnelClearance + `) clearance\b|(?:active |current )?` + personnelClearance + ` clearance (?:is )?(?:required|mandatory|essential)|(?:active|current) ` + securityQualifier + ` clearance\b)`), regexp.MustCompile(`(?i)\b(?:no (?:[a-z/]+ ){0,2}clearance|clearance (?:(?:is )?not|isn['?]t) (?:required|necessary|mandatory|needed|essential)|clearance (?:is )?optional|must not (?:[a-z /-]+ )?clearance)\b`)},
}

var civicCitizenship = regexp.MustCompile(`(?i)\b(?:(?:corporate|digital|global|good|strong|social|responsible|engineering|team) ){1,3}citizen(?:s|ship)?\b`)
var citizenOfCountry = regexp.MustCompile(`^ of \p{Lu}`)
var statusObject = regexp.MustCompile(`(?i)^\s+(?:applications|cases|process|services|law|records)\b`)
var negatedRequirement = regexp.MustCompile(`(?i)(?:\b(?:not|never|no|nor|none|neither|without)|n['’]t)(?:\s+[\pL]+){0,3}\s*$`)
var optionalRequirement = regexp.MustCompile(`(?i)\b(?:plus|bonus|nice[- ]to[- ]have|preferred|preferably|desired|desirable|advantage|advantageous|asset|beneficial|priority|encouraged|welcome|helpful|ideally|optional)\b`)
var eligibilityWords = regexp.MustCompile(`(?i)\b(?:citizens?(?:ship)?|permanent residen\w*|clearance)\b`)
var requirementBoundary = regexp.MustCompile(`(?i)[,;]|\band\s+`)

// A neighbouring requirement owns its own optional/negative wording. Keep a
// trailing relative clause (", which is a plus") when there is no next hit.
func requirementRange(text string, start, end int, words [][]int) (int, int) {
	left, right := 0, len(text)
	for _, word := range words {
		if word[1] <= start {
			left = word[1]
		} else if word[0] >= end {
			right = word[0]
			break
		}
	}
	return left, right
}

func requirementWindow(text string, start, end int, words [][]int) (int, int) {
	left, right := requirementRange(text, start, end, words)
	if boundaries := requirementBoundary.FindAllStringIndex(text[left:start], -1); left > 0 && len(boundaries) > 0 {
		left += boundaries[len(boundaries)-1][1]
	}
	// Some broad patterns consume their terminating comma.
	if end > start && text[end-1] == ',' && right < len(text) {
		right = end
	} else if boundary := requirementBoundary.FindStringIndex(text[end:right]); boundary != nil && right < len(text) {
		right = end + boundary[0]
	}
	return left, right
}

// A comma list may contain non-eligibility items before its final "or".
// Clauses already stop at semicolons; read the whole remaining list.
func eligibilityAlternativeEnd(clause string, end int, words [][]int) int {
	if strings.HasPrefix(strings.TrimSpace(clause[end:]), ",") {
		return len(clause)
	}
	for _, word := range words {
		if word[0] >= end {
			return word[0]
		}
	}
	return len(clause)
}

// Warning guards are local; rejection also checks the entire sentence.
func mandatoryRequirement(clause string, start int) bool {
	return !negatedRequirement.MatchString(clause[:start]) && !optionalRequirement.MatchString(clause)
}

// Rejection has a deliberately smaller grammar than warnings. In particular,
// title-case adjectives are not evidence of a legal nationality.
const strictNationality = `(?:u\.?s\.?|u\.?k\.?|eu|american|british|canadian|australian|new zealand|irish|german|french|dutch|romanian|indian|singaporean)`

var strictEligibility = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:must be an? ` + strictNationality + ` citizen|` + strictNationality + ` citizenship is required|only ` + strictNationality + ` citizens)\b`),
	regexp.MustCompile(`(?i)\b(?:must be a permanent resident|permanent residency is required)\b`),
	regexp.MustCompile(`(?i)\b(?:` + personnelClearance + ` clearance is required|must (?:hold|have) an active ` + securityQualifier + ` clearance|must be able to obtain an? ` + securityQualifier + ` clearance)\b`),
}
var sentenceNegation = regexp.MustCompile(`(?i)\b(?:no|not|nor|none|never|neither|without)\b|n['’]t\b`)
var requirementAlternative = regexp.MustCompile(`(?i)\b(?:or|either|alternatively)\b`)
var uncertainRequirement = regexp.MustCompile(`(?i)\b(?:if|where|when|who|whether|unless|except|apart from)\b`)
var scopedRequirement = regexp.MustCompile(`(?i)\b(?:only|except|unless|apart from)\b`)
var negativeAnswer = regexp.MustCompile(`(?i)(?:^|[:–-])\s*(?:no|none|n/a|not required)\b`)
var strictTail = regexp.MustCompile(`(?i)^\s*(?:$|[.,;!)]|(?:to apply|(?:may|can) apply|are eligible|of|for (?:this|the) (?:role|position|job)|before|and)\b)`)
var strictListHeading = regexp.MustCompile(`(?i)\brequired languages:\s*`)
var strictListTail = regexp.MustCompile(`^\s*[.!]?\s*$`)

func cleanRejectionSentence(sentence string) bool {
	return !strings.Contains(sentence, "?") && !sentenceNegation.MatchString(sentence) && !negativeAnswer.MatchString(sentence)
}

func cleanRejectionWindow(clause string, left, right, end int) bool {
	window := clause[left:right]
	// Conditions before a neighbouring requirement still qualify this one.
	// Scope limits follow the template; its leading "Only ... citizens" is valid.
	return !optionalRequirement.MatchString(window) && !requirementAlternative.MatchString(window) &&
		!uncertainRequirement.MatchString(clause) && !scopedRequirement.MatchString(clause[end:])
}

func checkStrictEligibility(e *models.Evaluation, sentence, clause string, offset int, reject bool, warn func(int, int, string)) {
	words := eligibilityWords.FindAllStringIndex(clause, -1)
	for _, pattern := range strictEligibility {
		for _, span := range pattern.FindAllStringIndex(clause, -1) {
			left, right := requirementWindow(clause, span[0], span[1], words)
			window := clause[left:right]
			if !mandatoryRequirement(window, span[0]-left) {
				continue
			}
			quoted := findingQuote(sentence, offset+span[0], offset+span[1])
			warn(span[0], span[1], quoted)
			outerRight := eligibilityAlternativeEnd(clause, span[1], words)
			if reject && strictTail.MatchString(clause[span[1]:]) && cleanRejectionWindow(clause, left, right, span[1]) && !requirementAlternative.MatchString(clause[left:outerRight]) {
				e.RejectionReasons = appendUnique(e.RejectionReasons, "Active filter rejects eligibility requirements: "+quoted)
			}
		}
	}
}

// A required-language list contains only languages, commas and "and".
// A market/localisation noun after the list makes it warning-only.
func strictLanguageList(clause string, hits []languageHit) bool {
	heading := strictListHeading.FindStringIndex(clause)
	if heading == nil || len(hits) == 0 || heading[1] > hits[0].start || strings.TrimSpace(clause[heading[1]:hits[0].start]) != "" {
		return false
	}
	for i := 1; i < len(hits); i++ {
		joiner := clause[hits[i-1].end:hits[i].start]
		if !languageComma.MatchString(joiner) && !languageConjunction.MatchString(joiner) {
			return false
		}
	}
	return strictListTail.MatchString(clause[hits[len(hits)-1].end:])
}

func languageRequirementSpan(pattern *regexp.Regexp, clause string, hit languageHit) []int {
	for _, span := range pattern.FindAllStringIndex(clause, -1) {
		if span[0] <= hit.start && span[1] >= hit.end {
			return span
		}
	}
	return nil
}

func isCivicCitizenship(clause string, start, end int) bool {
	for _, span := range civicCitizenship.FindAllStringIndex(clause[:end], -1) {
		// "Good Citizens of Canada" names a country; "good citizenship" never does.
		noun := !strings.HasSuffix(strings.ToLower(clause[span[0]:span[1]]), "ship")
		if span[1] > start && !(noun && citizenOfCountry.MatchString(clause[span[1]:])) {
			return true
		}
	}
	return false
}

var sponsorshipSignal = regexp.MustCompile(`(?i)\b(?:no (?:visa )?sponsorship|(?:cannot|can't|do not|does not|will not|unable to) (?:provide|offer|support|sponsor) (?:(?:visa )?sponsorship|visas?)|(?:visa )?sponsorship (?:is )?(?:not available|unavailable|available|provided|offered)|international applicants (?:are )?welcome|(?:must|need to|required to) (?:already )?(?:have|hold) (?:the )?(?:(?:legal )?right to work|work authori[sz]ation)|(?:must|need to|required to) (?:have|hold) (?:a )?(?:valid )?work permit|eligible to work in (?:the )?[a-z]+|must (?:already )?be (?:legally )?authori[sz]ed to work)\b`)

var languageNames = []string{
	"English", "German", "French", "Spanish", "Portuguese", "Italian", "Dutch", "Romanian", "Polish", "Czech", "Slovak", "Hungarian", "Bulgarian", "Greek", "Swedish", "Norwegian", "Danish", "Finnish", "Icelandic", "Estonian", "Latvian", "Lithuanian", "Ukrainian", "Russian", "Turkish", "Arabic", "Hebrew", "Hindi", "Urdu", "Bengali", "Cantonese", "Chinese", "Japanese", "Korean", "Vietnamese", "Thai", "Indonesian", "Malay", "Tagalog", "Serbian", "Croatian", "Slovenian", "Albanian", "Catalan", "Persian", "Swahili", "Afrikaans",
}

const languageBar = `(?:[abc][12]|native|fluent|fluency|business[- ]level|professional|advanced|intermediate|basic|beginner)`

type languagePattern struct {
	name          string
	required      *regexp.Regexp
	commaRequired *regexp.Regexp
	strict        *regexp.Regexp
}

var languageAliases = map[string]string{
	"german": "deutsch", "french": "français", "spanish": "español", "italian": "italiano",
	"dutch": "nederlands", "romanian": "română", "polish": "polski", "portuguese": "português",
	"swedish": "svenska", "norwegian": "norsk", "danish": "dansk", "finnish": "suomi",
	"hungarian": "magyar", "czech": "čeština", "greek": "ελληνικά", "chinese": "mandarin",
}

var languagePatterns, languageWords = func() (map[string]*languagePattern, *regexp.Regexp) {
	out := map[string]*languagePattern{}
	var words []string
	for _, name := range languageNames {
		n := strings.ToLower(name)
		aliases := []string{n}
		if alias := languageAliases[n]; alias != "" {
			aliases = append(aliases, alias)
		}
		word := `(?:` + strings.Join(aliases, `|`) + `)`
		wordEnd := word + `(?:$|[^\pL\pN_])`
		// A language adjective alone does not make localisation or market experience a language requirement.
		requiredEnd := `(?:$|[.;:!?]|\s*\(` + languageBar + `\)| (?:language|skills|proficiency|fluency)\b)`
		pattern := &languagePattern{name: name, required: regexp.MustCompile(`(?i)(?:(?:^|[^\pL\pN_])` + word + ` (?:language |at )?(?:\(?` + languageBar + `\)? )?(?:level |skills |proficiency |fluency )?(?:is |are )?(?:required|mandatory|essential|a must)\b|\b(?:must|need to|required to) (?:be )?(?:speak|write|read|communicate in|fluent in|proficient in) (?:and (?:write|speak) )?` + wordEnd + `|\brequires? (?:` + languageBar + ` (?:proficiency in )?)?` + word + requiredEnd + `|\b(?:languages?|required):\s*` + languageBar + ` (?:proficiency in |in )?` + wordEnd + `)`)}
		pattern.commaRequired = regexp.MustCompile(`(?i)\brequires? (?:` + languageBar + ` (?:proficiency in )?)?` + word + `,`)
		pattern.strict = regexp.MustCompile(`(?i)(?:(?:^|[^\pL\pN_])` + word + `(?: \(` + languageBar + `\))? is required\b|\bmust (?:speak|be fluent in) ` + word + `(?:$|[^\pL\pN_]))`)
		for _, alias := range aliases {
			out[alias] = pattern
			words = append(words, alias)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(words)))
	return out, regexp.MustCompile(`(?i)(?:^|[^\pL\pN_])(` + strings.Join(words, `|`) + `)`)
}()

var requiredLanguageList = regexp.MustCompile(`(?i)\b(?:required|mandatory|essential) languages?:`)
var requiredLanguageEnd = regexp.MustCompile(`(?i)^(?:$|[.;:!?]|\s*\(` + languageBar + `\)| (?:language|skills|proficiency|fluency)\b)`)
var optionalLanguage = regexp.MustCompile(`(?i)\b(?:preferred|optional|nice to have|an? (?:plus|advantage)|helpful|not (?:required|necessary|mandatory)|no|without)\b`)
var cefrLevel = regexp.MustCompile(`(?i)\b([abc][12])\b`)
var languageSeparator = regexp.MustCompile(`(?i)[,;]|\band/or\b|\band\b|\bor\b`)
var languageAlternative = regexp.MustCompile(`(?i)^\s*(?:\(?` + languageBar + `\)?\s*)?,?\s*(?:or|and/or)\s*(?:` + languageBar + `\s*(?:in\s*)?)?$`)
var languageConjunction = regexp.MustCompile(`(?i)^\s*(?:\(?` + languageBar + `\)?\s*)?,?\s*and\s*(?:` + languageBar + `\s*(?:in\s*)?)?$`)
var languageComma = regexp.MustCompile(`(?i)^\s*(?:\(?` + languageBar + `\)?\s*)?,\s*(?:` + languageBar + `\s*(?:in\s*)?)?$`)

type languageHit struct {
	start, end int
	pattern    *languagePattern
}

func findLanguages(text string, posting bool) []languageHit {
	var hits []languageHit
	for _, match := range languageWords.FindAllStringSubmatchIndex(text, -1) {
		span := match[2:4]
		first, _ := utf8.DecodeRuneInString(text[span[0]:span[1]])
		// Posting language names are capitalised; profile entries may use any case.
		if posting && !unicode.IsUpper(first) {
			continue
		}
		before, _ := utf8.DecodeLastRuneInString(text[:span[0]])
		after, _ := utf8.DecodeRuneInString(text[span[1]:])
		// Unicode boundaries keep native names whole without matching parts of other words.
		if unicode.IsLetter(before) || unicode.IsDigit(before) || unicode.IsLetter(after) || unicode.IsDigit(after) {
			continue
		}
		hits = append(hits, languageHit{span[0], span[1], languagePatterns[strings.ToLower(text[span[0]:span[1]])]})
	}
	return hits
}

// A level next to another language must not become this language's level.
func languageContext(text string, start, end int) string {
	left, right := 0, len(text)
	for _, separator := range languageSeparator.FindAllStringIndex(text[:start], -1) {
		left = separator[1]
	}
	if separator := languageSeparator.FindStringIndex(text[end:]); separator != nil {
		right = end + separator[0]
	}
	return text[left:right]
}

func startsWithSpace(text string) bool {
	r, _ := utf8.DecodeRuneInString(text)
	return unicode.IsSpace(r)
}

func languageLevel(text string) int {
	if level := cefrLevel.FindString(text); level != "" {
		return strings.Index("a1a2b1b2c1c2", strings.ToLower(level))/2 + 1
	}
	for _, level := range []struct {
		words []string
		rank  int
	}{
		{[]string{"native"}, 6}, {[]string{"fluent", "fluency", "advanced"}, 5},
		{[]string{"business-level", "business level", "professional"}, 4},
		{[]string{"intermediate"}, 3}, {[]string{"basic", "beginner"}, 1},
	} {
		for _, word := range level.words {
			if containsPhrase(text, word) {
				return level.rank
			}
		}
	}
	return 0
}

func eligibilityCandidate(lower string) bool {
	for _, word := range []string{"citizen", "residen", "clearance", "sponsor", "visa", "authori", "right to work", "international", "work permit", "eligible to work"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func findingQuote(sentence string, start, end int) string {
	quote := []rune(sentence)
	if len(quote) > 200 {
		matchStart := utf8.RuneCountInString(sentence[:start])
		matchLength := utf8.RuneCountInString(sentence[start:end])
		left := max(0, matchStart-max(0, 200-matchLength)/2)
		left = min(left, len(quote)-200)
		quote = quote[left : left+200]
	}
	return `"` + strings.TrimSpace(string(quote)) + `"`
}

// Joining changes whitespace only, so a requirement's position among non-space
// runes identifies the same occurrence in both readings, even with indentation.
type postingWarningKey struct {
	kind                string
	textIndex, position int
}

func checkPostingGates(e *models.Evaluation, p models.Profile, f *models.FilterPreset, j models.JobInput) {
	joined := j
	joined.Title = joinPostingLines(j.Title)
	joined.Description = joinPostingLines(j.Description)
	if joined.Title == j.Title && joined.Description == j.Description {
		checkPostingGateReading(e, p, f, j, nil, false)
		return
	}
	seen := map[postingWarningKey]bool{}
	checkPostingGateReading(e, p, f, joined, seen, false)
	// Raw lines can warn, but only the joined reading may reject. Keep its quote
	// when it already warned for this occurrence of the same requirement.
	checkPostingGateReading(e, p, nil, j, seen, true)
}

func checkPostingGateReading(e *models.Evaluation, p models.Profile, f *models.FilterPreset, j models.JobInput, seen map[postingWarningKey]bool, fallback bool) {
	type profileLanguage struct {
		listed bool
		level  int
	}
	listed := map[*languagePattern]profileLanguage{}
	allRecognised := true
	for _, entry := range p.Languages {
		hits := findLanguages(entry, false)
		allRecognised = allRecognised && len(hits) > 0
		for _, hit := range hits {
			previous := listed[hit.pattern]
			listed[hit.pattern] = profileLanguage{true, max(previous.level, languageLevel(languageContext(entry, hit.start, hit.end)))}
		}
	}
	for textIndex, text := range []string{j.Title, j.Description} {
		warningKey := func(kind string, start, end int) postingWarningKey {
			// Broad and strict eligibility patterns may span different words,
			// but they refer to the same citizenship/residency/clearance term.
			if kind == "Posting requires eligibility: " {
				if word := eligibilityWords.FindStringIndex(text[start:end]); word != nil {
					start += word[0]
				}
			}
			position := 0
			for _, r := range text[:start] {
				if !unicode.IsSpace(r) {
					position++
				}
			}
			return postingWarningKey{kind, textIndex, position}
		}
		warn := func(kind string, start, end int, quoted string) {
			if seen != nil {
				key := warningKey(kind, start, end)
				if fallback && seen[key] {
					return
				}
				seen[key] = true
			}
			e.Warnings = appendUnique(e.Warnings, kind+quoted)
		}
		var hits []languageHit
		if len(listed) > 0 {
			hits = findLanguages(text, true)
		}
		cursor := 0
		for _, sentence := range postingSentences(text) {
			sentenceStart := cursor + strings.Index(text[cursor:], sentence)
			cursor = sentenceStart + len(sentence)
			clauseStart := sentenceStart
			for _, clause := range strings.Split(sentence, ";") {
				lower := strings.ToLower(clause)
				if eligibilityCandidate(lower) {
					words := eligibilityWords.FindAllStringIndex(clause, -1)
					for ruleIndex, rule := range eligibilityRules {
						for _, span := range rule.required.FindAllStringIndex(clause, -1) {
							left, right := requirementWindow(clause, span[0], span[1], words)
							window := clause[left:right]
							if rule.negated.MatchString(window) || !mandatoryRequirement(window, span[0]-left) || ruleIndex < 2 && statusObject.MatchString(clause[span[1]:]) {
								continue
							}
							// Civic citizenship describes values, not a legal status.
							if ruleIndex == 0 && isCivicCitizenship(clause, span[0], span[1]) {
								continue
							}
							quoted := findingQuote(sentence, clauseStart-sentenceStart+span[0], clauseStart-sentenceStart+span[1])
							warn("Posting requires eligibility: ", clauseStart+span[0], clauseStart+span[1], quoted)
						}
					}
					checkStrictEligibility(e, sentence, clause, clauseStart-sentenceStart, f != nil && f.RejectEligibilityRequirements && cleanRejectionSentence(sentence), func(start, end int, quoted string) {
						warn("Posting requires eligibility: ", clauseStart+start, clauseStart+end, quoted)
					})
					for index, span := range sponsorshipSignal.FindAllStringIndex(clause, -1) {
						if index == 0 {
							warn("Posting states work authorization or sponsorship: ", clauseStart+span[0], clauseStart+span[1], findingQuote(sentence, clauseStart-sentenceStart+span[0], clauseStart-sentenceStart+span[1]))
						} else if seen != nil && !fallback {
							seen[warningKey("Posting states work authorization or sponsorship: ", clauseStart+span[0], clauseStart+span[1])] = true
						}
					}
				}
				var local []languageHit
				var languageSpans [][]int
				for _, hit := range hits {
					if hit.start >= clauseStart && hit.end <= clauseStart+len(clause) {
						local = append(local, languageHit{hit.start - clauseStart, hit.end - clauseStart, hit.pattern})
						languageSpans = append(languageSpans, []int{hit.start - clauseStart, hit.end - clauseStart})
					}
				}
				strictList := strictLanguageList(clause, local)
				for first := 0; first < len(local); {
					last := first + 1
					alternative := false
					// Only "or" makes a language list alternatives; "and" requires each.
					for next := last; next < len(local); next++ {
						joiner := clause[local[next-1].end:local[next].start]
						if languageAlternative.MatchString(joiner) {
							alternative = true
							last = next + 1
						} else if languageComma.MatchString(joiner) || languageConjunction.MatchString(joiner) {
							last = next + 1
						} else {
							break
						}
					}
					required, anyListed := false, false
					var requiredSpan []int
					for index, hit := range local[first:last] {
						context := languageContext(clause, hit.start, hit.end)
						left, right := requirementWindow(clause, hit.start, hit.end, languageSpans)
						window := clause[left:right]
						span := languageRequirementSpan(hit.pattern.required, clause, hit)
						// A comma ends a requirement only when another language follows.
						if span == nil && first+index+1 < last && requiredLanguageEnd.MatchString(clause[local[last-1].end:]) {
							span = languageRequirementSpan(hit.pattern.commaRequired, clause, hit)
						}
						if span == nil {
							span = requiredLanguageList.FindStringIndex(clause)
						}
						if span != nil && mandatoryRequirement(window, max(0, span[0]-left)) && !optionalLanguage.MatchString(context) {
							required = true
							if span != nil {
								requiredSpan = span
							}
						}
						anyListed = anyListed || listed[hit.pattern].listed
					}
					for _, hit := range local[first:last] {
						context := languageContext(clause, hit.start, hit.end)
						left, right := requirementWindow(clause, hit.start, hit.end, languageSpans)
						window := clause[left:right]
						if !required || optionalLanguage.MatchString(context) || !mandatoryRequirement(window, hit.start-left) {
							continue
						}
						profile := listed[hit.pattern]
						if alternative && anyListed {
							if seen != nil && !fallback {
								seen[warningKey("Posting requires "+hit.pattern.name+" beyond the listed profile languages or level: ", clauseStart+hit.start, clauseStart+hit.end)] = true
							}
							continue
						}
						if !profile.listed || profile.level > 0 && profile.level < languageLevel(context) {
							start, end := hit.start, hit.end
							if requiredSpan != nil {
								start, end = requiredSpan[0], requiredSpan[1]
							}
							quoted := findingQuote(sentence, clauseStart-sentenceStart+start, clauseStart-sentenceStart+end)
							warn("Posting requires "+hit.pattern.name+" beyond the listed profile languages or level: ", clauseStart+hit.start, clauseStart+hit.end, quoted)
							// Unrecognised profile entries may contain the missing language.
							strict := strictList
							for _, span := range hit.pattern.strict.FindAllStringIndex(window, -1) {
								strict = strict || span[0] <= hit.start-left && span[1] >= hit.end-left && strictTail.MatchString(window[span[1]:])
							}
							_, outerRight := requirementRange(clause, hit.start, hit.end, languageSpans)
							if !profile.listed && !alternative && allRecognised && f != nil && f.RejectUnlistedLanguages && strict && cleanRejectionSentence(sentence) && cleanRejectionWindow(clause, left, right, hit.end) && !requirementAlternative.MatchString(clause[left:outerRight]) {
								e.RejectionReasons = appendUnique(e.RejectionReasons, "Active filter rejects unlisted language "+hit.pattern.name+": "+quoted)
							}
						}
					}
					first = last
				}
				clauseStart += len(clause) + 1
			}
		}
	}
}

// Hard wraps and HTML <br> continuations belong to one sentence. Preserve
// paragraph breaks, bullets and other capitalised lines as separate requirements.
func joinPostingLines(text string) string {
	if !strings.ContainsAny(text, "\r\n") {
		return text
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	var out strings.Builder
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if i > 0 {
			first, _ := utf8.DecodeRuneInString(line)
			word, _, _ := strings.Cut(strings.ToLower(line), " ")
			word = strings.TrimRightFunc(word, unicode.IsPunct)
			continuation := false
			switch word {
			case "and", "but", "nor", "and/or", "or", "either", "alternatively", "if", "where", "when", "who", "whether", "unless", "except", "apart":
				continuation = true
			}
			bullet := strings.HasPrefix(strings.ToLower(line), "o ")
			if strings.TrimSpace(lines[i-1]) != "" && !bullet && (unicode.IsLower(first) || first == '(' || continuation) {
				out.WriteByte(' ')
			} else {
				out.WriteByte('\n')
			}
		}
		out.WriteString(line)
	}
	return out.String()
}

func postingSentences(text string) []string {
	var out []string
	start := 0
	for i, r := range text {
		end := i + len(string(r))
		boundary := r == '\n' || r == '\r' || (r == '.' || r == '!' || r == '?') && (end == len(text) || startsWithSpace(text[end:]))
		if r == '.' {
			for _, abbreviation := range []string{"u.s.", "u.s.c.", "u.k.", "e.g.", "i.e."} {
				if end-start >= len(abbreviation) && strings.EqualFold(text[end-len(abbreviation):end], abbreviation) {
					boundary = false
				}
			}
		}
		if boundary {
			if sentence := strings.TrimSpace(text[start:end]); sentence != "" {
				out = append(out, sentence)
			}
			start = end
		}
	}
	if sentence := strings.TrimSpace(text[start:]); sentence != "" {
		out = append(out, sentence)
	}
	return out
}
