package matching

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"jobpilot/core/api/internal/company"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/technology"
)

// splitWords returns the runs of [a-z0-9+#.] in s, the way the regexp of that
// class did, without trailing dots. Every evaluation tokenises the whole job
// description, and the regexp engine made that most of what re-scoring a
// user's matches cost.
// Callers lowercase first; bytes of multi-byte UTF-8 runes are all 0x80 or
// above, so they separate words exactly as the regexp's character class did.
func splitWords(s string) []string {
	var out []string
	start := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '#' || c == '.' {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = appendWord(out, s[start:i])
			start = -1
		}
	}
	if start >= 0 {
		out = appendWord(out, s[start:])
	}
	return out
}

// appendWord drops the dots a word ends with, which are a sentence's full stop
// or an ellipsis: "aws." at the end of a sentence is the word "aws". Dots
// inside a word stay, as in "node.js", and so do leading ones, because ".NET"
// is a name and "net" is a word postings use for pay.
func appendWord(out []string, word string) []string {
	if word = strings.TrimRight(word, "."); word != "" {
		out = append(out, word)
	}
	return out
}

func Evaluate(profile models.Profile, blacklist []models.BlacklistEntry, filter *models.FilterPreset, job models.JobInput, now time.Time) models.Evaluation {
	e := models.Evaluation{Breakdown: map[string]int{}, RejectionReasons: []string{}, Warnings: []string{}, Reasons: []string{}, MatchedSkills: []string{}, MissingSkills: []string{}, RedFlags: []string{}}
	checkCompany(&e, profile, blacklist, job)
	if filter != nil {
		applyHardFilters(&e, *filter, job, now)
	}
	checkPostingGates(&e, profile, filter, job)

	title := scoreTitle(profile.DesiredTitles, job.Title)
	e.Breakdown["title_match"] = title
	skill, matched, missing := scoreSkills(profile.Skills, requiredSkills(filter), job.Title, job.Description)
	e.Breakdown["skill_match"], e.MatchedSkills, e.MissingSkills = skill, matched, missing
	wantedSeniorities := profile.Seniorities
	if len(wantedSeniorities) == 0 && profile.Seniority != "" {
		wantedSeniorities = []string{profile.Seniority}
	}
	e.Breakdown["seniority_match"] = scoreSeniority(wantedSeniorities, job.Title+" "+job.Description)
	e.Breakdown["location_remote_match"] = scoreLocation(profile, job)
	e.Breakdown["salary_match"] = scoreSalary(profile, job)
	e.Breakdown["domain_industry_match"] = scoreIndustry(profile, job)
	e.Breakdown["freshness"] = scoreFreshness(job.PostedAt, now)
	for _, v := range e.Breakdown {
		e.Score += v
	}
	if e.Score > 100 {
		e.Score = 100
	}
	if title >= 18 {
		e.Reasons = append(e.Reasons, "The job title aligns with a desired role.")
	}
	if len(matched) > 0 {
		e.Reasons = append(e.Reasons, fmt.Sprintf("Matched %d profile skill(s): %s.", len(matched), strings.Join(matched, ", ")))
	}
	if e.Breakdown["location_remote_match"] >= 8 {
		e.Reasons = append(e.Reasons, "The location and work arrangement fit your preferences.")
	}
	if e.Breakdown["salary_match"] == 10 && job.SalaryMax > 0 {
		e.Reasons = append(e.Reasons, "The stated compensation meets your expectation.")
	}
	if len(e.Reasons) == 0 {
		e.Reasons = append(e.Reasons, "The role has limited explicit overlap with the profile.")
	}
	if len(e.MissingSkills) > 0 {
		// Missing skills are whatever the job mentions that the profile lacks,
		// which a job may only mention in passing, so this says no more.
		e.RedFlags = append(e.RedFlags, "The job mentions skills your profile does not list: "+strings.Join(e.MissingSkills, ", "))
	}
	if job.SalaryMin == 0 && job.SalaryMax == 0 {
		e.RedFlags = append(e.RedFlags, "No salary or rate was provided.")
	}
	if job.PostedAt == nil {
		e.RedFlags = append(e.RedFlags, "The posting date is unknown.")
	}
	e.Rejected = len(e.RejectionReasons) > 0
	return e
}

func checkCompany(e *models.Evaluation, p models.Profile, list []models.BlacklistEntry, job models.JobInput) {
	targetAliases, domain := company.Aliases(job.Company), company.NormalizeDomain(job.CompanyDomain)
	type known struct{ name, domain, reason string }
	var entries []known
	for _, x := range p.Experiences {
		reason := "previous employer"
		if x.Current {
			reason = "current employer"
		}
		entries = append(entries, known{x.Company, x.CompanyDomain, reason})
	}
	for _, x := range list {
		entries = append(entries, known{x.CompanyName, x.CompanyDomain, "company blacklist"})
	}
	for _, x := range entries {
		knownAliases := company.Aliases(x.name)
		d := company.NormalizeDomain(x.domain)
		exact := false
		for _, n := range knownAliases {
			for _, target := range targetAliases {
				if n == target {
					exact = true
				}
			}
		}
		if exact {
			e.RejectionReasons = appendUnique(e.RejectionReasons, "Company matches "+x.reason+": "+x.name)
			continue
		}
		if domain != "" && d != "" && domain == d {
			e.RejectionReasons = appendUnique(e.RejectionReasons, "Company domain matches "+x.reason+": "+d)
			continue
		}
		for _, n := range knownAliases {
			for _, target := range targetAliases {
				if len(n) >= 4 && company.Similarity(n, target) >= .88 {
					e.Warnings = appendUnique(e.Warnings, "Company name is very similar to "+x.reason+": "+x.name)
				}
			}
		}
	}
}

func applyHardFilters(e *models.Evaluation, f models.FilterPreset, j models.JobInput, now time.Time) {
	title := strings.ToLower(j.Title)
	for _, x := range f.ExcludedTitleWords {
		if containsPhrase(title, x) {
			e.RejectionReasons = appendUnique(e.RejectionReasons, "Job title contains excluded words: "+x)
		}
	}
	if len(f.IncludedTitles) > 0 && !anyContains(title, f.IncludedTitles) && scoreTitle(f.IncludedTitles, j.Title) < 12 {
		e.RejectionReasons = appendUnique(e.RejectionReasons, "Job title does not match included titles")
	}
	if len(f.Locations) > 0 && !anyContains(strings.ToLower(j.Location), f.Locations) {
		if strings.TrimSpace(j.Location) == "" {
			e.Warnings = appendUnique(e.Warnings, "Location is not stated; verify it manually")
		} else if strings.EqualFold(strings.TrimSpace(j.RemoteType), "remote") && equalFoldAny("remote", f.RemoteTypes) {
			e.Warnings = appendUnique(e.Warnings, "The role is remote but its geographic eligibility does not match the allowed locations; verify it manually")
		} else {
			e.RejectionReasons = appendUnique(e.RejectionReasons, "Location is incompatible with the active filter")
		}
	}
	if len(f.RemoteTypes) > 0 && !equalFoldAny(j.RemoteType, f.RemoteTypes) {
		if strings.TrimSpace(j.RemoteType) == "" {
			e.Warnings = appendUnique(e.Warnings, "Remote policy is not stated; verify it manually")
		} else {
			e.RejectionReasons = appendUnique(e.RejectionReasons, "Remote policy is incompatible with the active filter")
		}
	}
	knownPay := j.SalaryMax
	if knownPay == 0 {
		knownPay = j.SalaryMin
	}
	if f.MinSalary > 0 && knownPay > 0 {
		if f.Currency != "" && j.Currency != "" && !strings.EqualFold(f.Currency, j.Currency) {
			e.Warnings = appendUnique(e.Warnings, "Compensation currency differs from the active filter; compare it manually")
		} else if knownPay < f.MinSalary {
			e.RejectionReasons = appendUnique(e.RejectionReasons, "Known salary is below the minimum")
		}
	}
	if len(f.EmploymentTypes) > 0 && !equalFoldAny(j.EmploymentType, f.EmploymentTypes) {
		if strings.TrimSpace(j.EmploymentType) == "" {
			e.Warnings = appendUnique(e.Warnings, "Employment type is not stated; verify it manually")
		} else {
			e.RejectionReasons = appendUnique(e.RejectionReasons, "Employment type is incompatible")
		}
	}
	if equalFoldAny(j.Industry, f.ExcludedIndustries) {
		e.RejectionReasons = appendUnique(e.RejectionReasons, "Industry is excluded")
	}
	jobText := j.Title + " " + j.Description
	if len(f.IncludedTechnologies) > 0 && !anyContains(jobText, f.IncludedTechnologies) {
		e.RejectionReasons = appendUnique(e.RejectionReasons, "Job does not mention any required technology: "+strings.Join(f.IncludedTechnologies, ", "))
	}
	for _, technology := range f.ExcludedTechnologies {
		if containsPhrase(jobText, technology) {
			e.RejectionReasons = appendUnique(e.RejectionReasons, "Job mentions an excluded technology: "+technology)
		}
	}
	if f.MaxAgeDays > 0 && j.PostedAt != nil && now.Sub(*j.PostedAt) > time.Duration(f.MaxAgeDays)*24*time.Hour {
		e.RejectionReasons = appendUnique(e.RejectionReasons, "Job is older than the allowed posting age")
	}
}

func scoreTitle(desired []string, title string) int {
	if len(desired) == 0 {
		return 12
	}
	best := 0.0
	for _, d := range desired {
		n, t := strings.ToLower(strings.TrimSpace(d)), strings.ToLower(title)
		if n == t {
			best = 1
			break
		}
		if containsPhrase(t, n) || containsPhrase(n, t) {
			best = math.Max(best, .85)
			continue
		}
		best = math.Max(best, jaccard(words(n), words(t)))
	}
	return int(math.Round(25 * best))
}

// scoreSkills scores, out of 30, how much of what a job asks for the profile
// holds. What it asks for is the vocabulary technologies it mentions, each
// weighted by how often, with any skill the filter lists or the profile holds
// that it names; ways of working such as agile are not counted. Only skills
// the profile or filter named used to count, so a technology the candidate
// lacks was never seen and nearly every job scored the full 30. A job that
// names nothing to go on scores a neutral 15. Matched skills keep the
// profile's own spelling, since generated text quotes them.
func scoreSkills(profile []models.Skill, required []string, title, description string) (int, []string, []string) {
	jobWords := words(strings.ToLower(title + " " + description))
	held := map[string]string{}
	for _, s := range profile {
		if name := strings.TrimSpace(s.Name); name != "" {
			held[technology.Canonical(name)] = name
		}
	}
	type ask struct {
		name   string
		weight int
	}
	asked := map[string]ask{}
	for term, mentions := range technology.Mentions(title, description) {
		if !technology.IsPractice(term) {
			asked[term] = ask{technology.Name(term), mentions}
		}
	}
	// Skills outside the vocabulary count where the job names them: the
	// filter's, which say what matters to the user, and the profile's own.
	named := append([]string(nil), required...)
	for _, name := range held {
		named = append(named, name)
	}
	for _, name := range named {
		name = strings.TrimSpace(name)
		key := technology.Canonical(name)
		if _, ok := asked[key]; ok || name == "" || technology.IsPractice(key) || !phraseInWords(strings.ToLower(name), jobWords) {
			continue
		}
		asked[key] = ask{name, 1}
	}
	if len(asked) == 0 {
		return 15, []string{}, []string{}
	}
	// Empty rather than nil: a nil slice is stored and served as JSON null.
	matched, missing := []string{}, []string{}
	heldWeight, totalWeight := 0, 0
	for key, a := range asked {
		totalWeight += a.weight
		if own, ok := held[key]; ok {
			heldWeight += a.weight
			matched = append(matched, own)
		} else if a.name != "" {
			missing = append(missing, a.name)
		} else {
			missing = append(missing, key)
		}
	}
	sort.Strings(matched)
	sort.Strings(missing)
	return int(math.Round(30 * float64(heldWeight) / float64(totalWeight))), matched, missing
}

var seniority = map[string]int{"intern": 0, "junior": 1, "entry": 1, "mid": 2, "middle": 2, "senior": 3, "lead": 4, "staff": 4, "principal": 5, "manager": 4, "director": 5}

func scoreSeniority(wants []string, text string) int {
	// Tokenising a whole description is most of what an evaluation costs, so it
	// happens once here rather than once per seniority word.
	normalized := phraseText(text)
	found, ok := 0, false
	for token, level := range seniority {
		if strings.Contains(normalized, " "+token+" ") && (!ok || level > found) {
			found = level
			ok = true
		}
	}
	if !ok || len(wants) == 0 {
		return 10
	}
	best := 2
	for _, want := range wants {
		level, known := seniority[strings.ToLower(strings.TrimSpace(want))]
		if !known {
			continue
		}
		d := abs(level - found)
		if d == 0 {
			return 15
		}
		if d == 1 && best < 9 {
			best = 9
		}
	}
	return best
}
func scoreLocation(p models.Profile, j models.JobInput) int {
	if len(p.WorkPreference) == 0 {
		return 6
	}
	if equalFoldAny(j.RemoteType, p.WorkPreference) {
		return 10
	}
	if anyContains(strings.ToLower(j.Location), p.PreferredCountries) {
		return 7
	}
	return 1
}
func scoreSalary(p models.Profile, j models.JobInput) int {
	expected := p.SalaryExpectation
	if expected == 0 {
		expected = p.RateExpectation
	}
	if expected == 0 || (j.SalaryMin == 0 && j.SalaryMax == 0) {
		return 5
	}
	if p.Currency != "" && j.Currency != "" && !strings.EqualFold(p.Currency, j.Currency) {
		return 3
	}
	max := j.SalaryMax
	if max == 0 {
		max = j.SalaryMin
	}
	if max >= expected {
		return 10
	}
	if max >= expected*.9 {
		return 6
	}
	return 1
}
func scoreIndustry(p models.Profile, j models.JobInput) int {
	if len(p.IndustryPreferences) == 0 || j.Industry == "" {
		return 3
	}
	if equalFoldAny(j.Industry, p.IndustryPreferences) {
		return 5
	}
	return 0
}
func scoreFreshness(posted *time.Time, now time.Time) int {
	if posted == nil {
		return 2
	}
	days := now.Sub(*posted).Hours() / 24
	if days <= 7 {
		return 5
	}
	if days <= 30 {
		return 3
	}
	if days <= 60 {
		return 1
	}
	return 0
}
func requiredSkills(f *models.FilterPreset) []string {
	if f == nil {
		return nil
	}
	return f.RequiredSkills
}
func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, x := range splitWords(strings.ToLower(s)) {
		out[x] = true
	}
	return out
}
func phraseInWords(s string, w map[string]bool) bool {
	parts := splitWords(strings.ToLower(s))
	if len(parts) == 0 {
		return false
	}
	for _, x := range parts {
		if !w[x] {
			return false
		}
	}
	return true
}
func jaccard(a, b map[string]bool) float64 {
	inter := 0
	union := map[string]bool{}
	for x := range a {
		union[x] = true
		if b[x] {
			inter++
		}
	}
	for x := range b {
		union[x] = true
	}
	if len(union) == 0 {
		return 0
	}
	return float64(inter) / float64(len(union))
}

// phraseText lowercases text and joins its words with single spaces, padded at
// both ends, so a whole phrase can be found with one strings.Contains.
func phraseText(text string) string {
	return " " + strings.Join(splitWords(strings.ToLower(text)), " ") + " "
}
func containsPhrase(text, phrase string) bool {
	phrase = strings.Join(splitWords(strings.ToLower(phrase)), " ")
	return phrase != "" && strings.Contains(phraseText(text), " "+phrase+" ")
}
func anyContains(text string, list []string) bool {
	for _, x := range list {
		if containsPhrase(text, x) || strings.Contains(strings.ToLower(text), strings.ToLower(x)) {
			return true
		}
	}
	return false
}
func equalFoldAny(value string, list []string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(x)) {
			return true
		}
	}
	return false
}
func appendUnique(in []string, v string) []string {
	for _, x := range in {
		if x == v {
			return in
		}
	}
	return append(in, v)
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
