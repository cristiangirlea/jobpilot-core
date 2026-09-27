package answers

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"jobpilot/core/api/internal/company"
	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

// Source values describe where a resolved answer came from. They are part of
// the API surface: the review UI and the apply engine both branch on them.
const (
	SourceProfile     = "profile"      // derived from stored profile facts
	SourceBank        = "bank"         // an exact previously-answered question
	SourceBankSimilar = "bank_similar" // reused from a similar role or stack
	SourceAI          = "ai"           // newly generated, never auto-trusted
	SourceTemplate    = "template"     // deterministic fallback wording
	SourceUnanswered  = "unanswered"   // nothing safe to say without a human
)

// similarityFloor is the minimum technology-signature overlap before an answer
// written for another role may be reused. Below this the roles are different
// enough that reuse would put stale specifics in front of an employer.
const similarityFloor = 0.34

// Bank is the persistence the resolver needs. Implemented by the store.
type Bank interface {
	FindAnswer(ctx context.Context, userID, normalized, roleFamily string) (models.AnswerEntry, bool, error)
	CandidateAnswers(ctx context.Context, userID, questionKey string, limit int) ([]models.AnswerEntry, error)
	SaveAnswer(ctx context.Context, userID string, entry models.AnswerEntry) (models.AnswerEntry, error)
	TouchAnswer(ctx context.Context, userID, id string) error
}

// Service resolves questions deterministically first and only then reaches for
// a provider. It is safe to use with a nil or disabled LLM client.
type Service struct {
	Bank Bank
	LLM  *llm.Client
}

// Question is one field on an application form.
type Question struct {
	Text      string   `json:"text"`
	Kind      string   `json:"kind,omitempty"`
	Options   []string `json:"options,omitempty"`
	MaxLength int      `json:"max_length,omitempty"`
}

// Subject is everything the resolver may draw facts from. Nothing outside this
// struct may appear in an answer.
type Subject struct {
	Profile   models.Profile
	Job       models.Job
	Match     models.Evaluation
	Documents models.Documents
	// Question is the wording currently being resolved. A few deterministic
	// answers depend on it - a location gate names the places it is asking
	// about - so it travels with the facts rather than being passed separately.
	Question string
}

// Resolution is one answered question plus its provenance.
type Resolution struct {
	Question     string   `json:"question"`
	QuestionKey  string   `json:"question_key"`
	Answer       string   `json:"answer"`
	Source       string   `json:"source"`
	Approved     bool     `json:"approved"`
	NeedsReview  bool     `json:"needs_review"`
	EntryID      string   `json:"entry_id,omitempty"`
	Reused       bool     `json:"reused"`
	Technologies []string `json:"technologies,omitempty"`
}

// Resolve answers one question. The order is deliberate and is the whole cost
// model: stored facts, then an exact reuse, then a similar reuse, then a
// provider, then deterministic wording. Only step four can cost anything.
func (s *Service) Resolve(ctx context.Context, userID string, question Question, subject Subject) (Resolution, error) {
	text := strings.TrimSpace(question.Text)
	if text == "" {
		return Resolution{Source: SourceUnanswered}, nil
	}
	key := Classify(text)
	normalized := Normalize(text)
	subject.Question = text
	family := RoleFamily(subject.Job.Title)
	signature := TechnologySignature(12, subject.Job.Title, subject.Job.Description)

	resolution := Resolution{Question: text, QuestionKey: key, Technologies: signature}

	// 1. Stored profile facts. These are verifiable, so they need no review.
	//
	// A question with a fixed option list must be answered with one of its own
	// options. Matching a stored fact onto that list is a lookup, so a country
	// select with two hundred entries and every yes/no question resolve here,
	// at no cost. A fact that does not match cleanly falls through rather than
	// submitting free text into a select.
	if answer, ok := subjectAnswer(key, subject); ok {
		if fitted, usable := fitToQuestion(answer, question.Options, question.Kind); usable {
			resolution.Answer, resolution.Source, resolution.Approved = fitted, SourceProfile, true
			return resolution, nil
		}
	}
	if answer, ok := profileAnswer(key, subject.Profile); ok {
		if len(question.Options) == 0 {
			resolution.Answer, resolution.Source, resolution.Approved = answer, SourceProfile, true
			return resolution, nil
		}
		if option, matched := fitToQuestion(answer, question.Options, question.Kind); matched {
			resolution.Answer, resolution.Source, resolution.Approved = option, SourceProfile, true
			return resolution, nil
		}
	}

	// 2. This exact question, answered before for this role family.
	if s.Bank != nil {
		if entry, found, err := s.Bank.FindAnswer(ctx, userID, normalized, family); err == nil && found && entry.Approved {
			if answer, usable := fitToQuestion(entry.Answer, question.Options, question.Kind); usable {
				_ = s.Bank.TouchAnswer(ctx, userID, entry.ID)
				resolution.Answer, resolution.Source = answer, SourceBank
				resolution.Approved, resolution.EntryID, resolution.Reused = true, entry.ID, true
				return resolution, nil
			}
		}
	}

	// 3. The same kind of question, answered for a sufficiently similar stack.
	if s.Bank != nil && key != "" {
		if entry, ok := s.bestSimilar(ctx, userID, key, family, signature); ok {
			if answer, usable := fitToQuestion(entry.Answer, question.Options, question.Kind); usable {
				_ = s.Bank.TouchAnswer(ctx, userID, entry.ID)
				resolution.Answer, resolution.Source = answer, SourceBankSimilar
				resolution.Approved, resolution.EntryID, resolution.Reused = true, entry.ID, true
				return resolution, nil
			}
		}
	}

	// 4. Generate. Stored unapproved so it cannot be reused until reviewed.
	if s.LLM.Enabled() {
		answer, err := s.generate(ctx, question, subject, key)
		if fitted, usable := fitToQuestion(answer, question.Options, question.Kind); err == nil && answer != "" && usable {
			answer = fitted
			resolution.Answer, resolution.Source, resolution.NeedsReview = answer, SourceAI, true
			if s.Bank != nil {
				entry := models.AnswerEntry{
					QuestionKey: key, QuestionText: text, QuestionNormalized: normalized,
					Answer: answer, RoleFamily: family, Technologies: signature,
					Source: SourceAI, Approved: false,
				}
				if saved, saveErr := s.Bank.SaveAnswer(ctx, userID, entry); saveErr == nil {
					resolution.EntryID = saved.ID
				}
			}
			return resolution, nil
		}
	}

	// 5. Deterministic wording, when the generator already covers this question.
	// Fitted like every other source: template prose must never land in a
	// select, and one that cannot be matched to an option stays unanswered.
	if answer, ok := templateAnswer(key, subject.Documents); ok {
		if fitted, usable := fitToQuestion(answer, question.Options, question.Kind); usable {
			resolution.Answer, resolution.Source, resolution.NeedsReview = fitted, SourceTemplate, true
			return resolution, nil
		}
	}

	resolution.Source, resolution.NeedsReview = SourceUnanswered, true
	return resolution, nil
}

// ResolveAll answers a whole form. It never aborts on one bad question, because
// a single unanswerable field should not discard the rest of the work.
func (s *Service) ResolveAll(ctx context.Context, userID string, questions []Question, subject Subject) []Resolution {
	out := make([]Resolution, 0, len(questions))
	for _, question := range questions {
		resolution, err := s.Resolve(ctx, userID, question, subject)
		if err != nil {
			resolution = Resolution{Question: question.Text, Source: SourceUnanswered, NeedsReview: true}
		}
		out = append(out, resolution)
	}
	return out
}

// bestSimilar picks the highest-scoring approved answer for the same canonical
// question. Same role family is worth more than stack overlap alone, and ties
// break towards the answer that has already served well.
func (s *Service) bestSimilar(ctx context.Context, userID, key, family string, signature []string) (models.AnswerEntry, bool) {
	candidates, err := s.Bank.CandidateAnswers(ctx, userID, key, 25)
	if err != nil || len(candidates) == 0 {
		return models.AnswerEntry{}, false
	}
	type scored struct {
		entry models.AnswerEntry
		score float64
	}
	var ranked []scored
	for _, entry := range candidates {
		if !entry.Approved || strings.TrimSpace(entry.Answer) == "" {
			continue
		}
		overlap := Overlap(signature, entry.Technologies)
		score := overlap
		if entry.RoleFamily == family {
			score += 0.5
		}
		if overlap < similarityFloor && entry.RoleFamily != family {
			continue
		}
		score += float64(entry.UsageCount) / 1000
		ranked = append(ranked, scored{entry, score})
	}
	if len(ranked) == 0 {
		return models.AnswerEntry{}, false
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	return ranked[0].entry, true
}

const answerSystemPrompt = `You are drafting one answer on a job application on behalf of a candidate.

Hard rules:
- Use ONLY the candidate facts supplied below. Never invent employers, dates, titles, skills, achievements, metrics, or qualifications.
- If the facts do not support an answer, reply with exactly: INSUFFICIENT_FACTS
- Write in the first person, as the candidate.
- Plain prose. No markdown, no headings, no bullet points, no preamble, no sign-off.
- Return only the answer text itself.`

// generate asks a provider for one answer under a strict factual contract. An
// INSUFFICIENT_FACTS reply is treated as no answer, which sends the caller to
// the deterministic fallback rather than shipping a guess.
func (s *Service) generate(ctx context.Context, question Question, subject Subject, key string) (string, error) {
	limit := question.MaxLength
	if limit <= 0 {
		limit = 900
	}
	prompt := fmt.Sprintf(`CANDIDATE FACTS
%s

ROLE APPLIED FOR
Title: %s
Company: %s
Location: %s
Remote policy: %s

RELEVANT MATCHED SKILLS
%s

QUESTION
%s

Answer in at most %d characters.`,
		factSheet(subject.Profile),
		fallback(subject.Job.Title, "unspecified"),
		fallback(subject.Job.Company, "unspecified"),
		fallback(subject.Job.Location, "unspecified"),
		fallback(subject.Job.RemoteType, "unspecified"),
		listOrNone(subject.Match.MatchedSkills),
		strings.TrimSpace(question.Text),
		limit,
	)
	if len(question.Options) > 0 {
		prompt += "\n\nChoose exactly one of these options and reply with it verbatim:\n- " + strings.Join(question.Options, "\n- ")
	}
	result, err := s.LLM.Complete(ctx, llm.Request{
		Task:        "answer_question:" + fallback(key, "unclassified"),
		System:      answerSystemPrompt,
		Prompt:      prompt,
		MaxTokens:   600,
		Temperature: 0.2,
	})
	if err != nil {
		return "", err
	}
	answer := strings.TrimSpace(result.Text)
	if answer == "" || strings.Contains(answer, "INSUFFICIENT_FACTS") {
		return "", nil
	}
	return answer, nil
}

// factSheet renders the only facts a provider is allowed to draw on.
func factSheet(p models.Profile) string {
	var b strings.Builder
	line := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			fmt.Fprintf(&b, "%s: %s\n", label, value)
		}
	}
	line("Name", p.Name)
	line("Current title", p.CurrentTitle)
	if p.YearsExperience > 0 {
		fmt.Fprintf(&b, "Years of experience: %.0f\n", p.YearsExperience)
	}
	line("Location", p.Location)
	line("Seniority", strings.Join(p.Seniorities, ", "))
	line("Summary", p.Summary)
	line("Work authorization", p.WorkAuthorization)
	line("Notice period", p.NoticePeriod)
	line("Work preference", strings.Join(p.WorkPreference, ", "))
	line("Languages", strings.Join(p.Languages, ", "))
	line("Certifications", strings.Join(p.Certifications, ", "))

	if len(p.Skills) > 0 {
		names := make([]string, 0, len(p.Skills))
		for _, skill := range p.Skills {
			if skill.Years > 0 {
				names = append(names, fmt.Sprintf("%s (%.0fy)", skill.Name, skill.Years))
				continue
			}
			names = append(names, skill.Name)
		}
		line("Skills", strings.Join(names, ", "))
	}
	for _, x := range p.Experiences {
		if x.Title == "" && x.Company == "" {
			continue
		}
		fmt.Fprintf(&b, "Experience: %s at %s (%s - %s). %s\n", x.Title, x.Company, fallback(x.StartDate, "?"), fallback(x.EndDate, "present"), x.Description)
	}
	for _, project := range p.Projects {
		if project.Name == "" {
			continue
		}
		fmt.Fprintf(&b, "Project: %s. %s\n", project.Name, project.Description)
	}
	if b.Len() == 0 {
		return "(no candidate facts recorded)"
	}
	return strings.TrimRight(b.String(), "\n")
}

// profileAnswer returns closed-form answers that are pure lookups. These must
// never reach a provider: they are facts, not writing.
func profileAnswer(key string, p models.Profile) (string, bool) {
	switch key {
	case KeyFullName:
		return nonEmpty(p.Name)
	case KeyFirstName, KeyPreferredName:
		return nonEmpty(firstName(p.Name))
	case KeyLastName:
		return nonEmpty(lastName(p.Name))
	case KeySponsorship:
		// Only an explicit statement counts. Inferring "no sponsorship needed"
		// from a citizenship alone would answer for the wrong jurisdiction: an
		// EU citizen still needs sponsorship for a role based in the US.
		authorization := strings.ToLower(p.WorkAuthorization)
		for _, phrase := range []string{"no sponsorship", "without sponsorship", "not require sponsorship", "no visa required", "no work permit required"} {
			if strings.Contains(authorization, phrase) {
				return "No", true
			}
		}
	case KeyEmail:
		return nonEmpty(p.Email)
	case KeyPhone:
		return nonEmpty(p.Phone)
	case KeyLocation:
		return nonEmpty(p.Location)
	case KeyCountry:
		return nonEmpty(Country(p.Location))
	case KeyCity:
		return nonEmpty(City(p.Location))
	case KeyCurrentTitle:
		return nonEmpty(p.CurrentTitle)
	case KeyCountryTimezone:
		// One field asking for both. The stored location answers it directly,
		// with the timezone appended only when the profile states one.
		country := Country(p.Location)
		if country == "" {
			return "", false
		}
		if len(p.PreferredTimezones) > 0 {
			return country + " (" + p.PreferredTimezones[0] + ")", true
		}
		return country, true
	case KeySpokenLanguages:
		if len(p.Languages) > 0 {
			return strings.Join(p.Languages, ", "), true
		}
	case KeyAuthorization:
		// A yes/no eligibility question wants a decision, not the sentence
		// stored on the profile; the caller maps this onto the form's options.
		// Only an explicit statement counts, for the same reason sponsorship
		// does: a citizenship says nothing about the role's jurisdiction.
		authorization := strings.ToLower(p.WorkAuthorization)
		for _, phrase := range []string{"authorized to work", "authorised to work", "eligible to work",
			"right to work", "no sponsorship", "no visa required", "no work permit required"} {
			if strings.Contains(authorization, phrase) {
				return "Yes", true
			}
		}
		return nonEmpty(p.WorkAuthorization)
	case KeyLinkedIn:
		return nonEmpty(p.LinkedIn)
	case KeyGitHub:
		return nonEmpty(p.GitHub)
	case KeyPortfolio:
		return nonEmpty(p.Portfolio)
	case KeyYearsExperience:
		if p.YearsExperience > 0 {
			return fmt.Sprintf("%.0f", p.YearsExperience), true
		}
	case KeyNotice:
		return nonEmpty(p.NoticePeriod)
	case KeySalary:
		if p.SalaryExpectation > 0 {
			return strings.TrimSpace(fmt.Sprintf("%.0f %s", p.SalaryExpectation, p.Currency)), true
		}
		if p.RateExpectation > 0 {
			return strings.TrimSpace(fmt.Sprintf("%.0f %s per day", p.RateExpectation, p.Currency)), true
		}
	case KeyRemotePreference:
		if len(p.WorkPreference) > 0 {
			return strings.Join(p.WorkPreference, ", "), true
		}
	}
	return "", false
}

// templateAnswer maps a canonical key onto the deterministic generator output,
// which is what keeps the product usable with no provider configured at all.
func templateAnswer(key string, docs models.Documents) (string, bool) {
	switch key {
	case KeyAboutYou:
		return nonEmpty(docs.Answers["Tell us about yourself."])
	case KeyWhyRole, KeyWhyCompany:
		return nonEmpty(docs.Answers["Why are you interested in this role?"])
	case KeyWhyFit:
		return nonEmpty(docs.Answers["Why are you a good fit?"])
	case KeySalary:
		return nonEmpty(docs.Answers["Salary expectation."])
	case KeyNotice:
		return nonEmpty(docs.Answers["Notice period."])
	case KeyAuthorization:
		return nonEmpty(docs.Answers["Work authorization."])
	case KeyRemotePreference:
		return nonEmpty(docs.Answers["Remote work preference."])
	case KeyAdditionalContext:
		return nonEmpty(docs.CoverLetter)
	}
	return "", false
}

func nonEmpty(v string) (string, bool) {
	v = strings.TrimSpace(v)
	return v, v != ""
}

func fallback(v, f string) string {
	if strings.TrimSpace(v) == "" {
		return f
	}
	return v
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "(none recorded)"
	}
	return strings.Join(items, ", ")
}

// fitToOptions constrains an answer to a question's own option list. A question
// with no options accepts the answer as written; a select accepts it only if it
// maps cleanly onto one of its choices, so a stale or free-text answer is sent
// to review instead of into a dropdown it does not belong in.
func fitToOptions(answer string, options []string) (string, bool) {
	return fitToQuestion(answer, options, "")
}

// fitToQuestion constrains an answer to a question's own options. A
// "select all that apply" question accepts several, so a list of languages can
// resolve there while a single-choice question still takes exactly one.
func fitToQuestion(answer string, options []string, kind string) (string, bool) {
	if strings.TrimSpace(answer) == "" {
		return "", false
	}
	if len(options) == 0 {
		return answer, true
	}
	if kind == "multiselect" {
		if matched, ok := MatchAll(answer, options); ok {
			return strings.Join(matched, ", "), true
		}
		return "", false
	}
	return MatchOption(answer, options)
}

// subjectAnswer covers deterministic answers that need the job as well as the
// profile. These are lookups against facts already stored, so they must never
// reach a provider.
func subjectAnswer(key string, subject Subject) (string, bool) {
	switch key {
	case KeyCurrentEmployer:
		// The current role is the one the CV marks as ongoing.
		for _, experience := range subject.Profile.Experiences {
			if experience.Current && strings.TrimSpace(experience.Company) != "" {
				return strings.TrimSpace(experience.Company), true
			}
		}
		if len(subject.Profile.Experiences) > 0 {
			return nonEmpty(subject.Profile.Experiences[0].Company)
		}
		return "", false
	case KeyLocatedIn:
		if yes, ok := LocationGate(subject.Question, subject.Profile.Location); ok {
			if yes {
				return "Yes", true
			}
			return "No", true
		}
		return "", false
	case KeyWorkedHere:
		// The candidate's own employment history already answers this, and it
		// is the same list that drives company exclusion.
		//
		// The company named in the question wins over the one on the posting:
		// an ATS board and the hiring entity are not always the same name, and
		// the question says plainly which employer it means.
		name := employerFromQuestion(subject.Question)
		if name == "" {
			name = subject.Job.Company
		}
		if strings.TrimSpace(name) == "" {
			return "", false
		}
		employer := company.Normalize(name)
		if employer == "" {
			return "", false
		}
		for _, experience := range subject.Profile.Experiences {
			if company.Normalize(experience.Company) == employer {
				return "Yes", true
			}
		}
		return "No", true
	}
	return "", false
}

// firstName and lastName split a stored full name. A single-token name yields
// that token as the first name and no surname, rather than a guess.
func firstName(name string) string {
	parts := strings.Fields(strings.TrimSpace(name))
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func lastName(name string) string {
	parts := strings.Fields(strings.TrimSpace(name))
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}

// employerNamePattern pulls the company out of "Have you previously worked at
// or consulted for Acme?".
var employerNamePattern = regexp.MustCompile(`(?i)(?:worked (?:at|for)|consulted for|employed (?:at|by)|employee of|contractor of)\s+([^?.,]+)`)

// employerFromQuestion returns the company a previous-employment question names,
// or "" when it names none.
func employerFromQuestion(question string) string {
	match := employerNamePattern.FindStringSubmatch(question)
	if len(match) != 2 {
		return ""
	}
	// "worked at or consulted for Acme" leaves the alternative inside the
	// capture, because the first branch consumes the rest of the clause. Peeling
	// repeatedly reaches the company itself.
	name := strings.TrimSpace(match[1])
	for range 3 {
		inner := employerNamePattern.FindStringSubmatch(name)
		if len(inner) != 2 {
			break
		}
		name = strings.TrimSpace(inner[1])
	}
	name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(name), "before"))
	if len(name) > 60 {
		return ""
	}
	return name
}
