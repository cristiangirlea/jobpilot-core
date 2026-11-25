package matching

import (
	"encoding/json"
	"jobpilot/core/api/internal/models"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The verb "go" asked for Go, so a posting naming no technology scored a Go
// profile's skills as a full match.
func TestScoreSkillsSkipTheVerbGo(t *testing.T) {
	profile := models.Profile{Skills: []models.Skill{{Name: "Go"}}}
	verbOnly := Evaluate(profile, nil, nil, models.JobInput{Title: "Account Executive", Description: "Our releases go through review. Go further together."}, time.Now())
	if verbOnly.Breakdown["skill_match"] != 15 || len(verbOnly.MatchedSkills) != 0 || len(verbOnly.MissingSkills) != 0 {
		t.Errorf("a posting naming no technology scores a neutral 15 and claims nothing, got %d, %v, %v", verbOnly.Breakdown["skill_match"], verbOnly.MatchedSkills, verbOnly.MissingSkills)
	}
	mixed := Evaluate(profile, nil, nil, models.JobInput{Title: "Backend Engineer", Description: "Go further. We use Go and Rust."}, time.Now())
	if mixed.Breakdown["skill_match"] != 15 || strings.Join(mixed.MatchedSkills, ",") != "Go" || strings.Join(mixed.MissingSkills, ",") != "Rust" {
		t.Errorf("Go and Rust weigh the same, got %d, %v, %v", mixed.Breakdown["skill_match"], mixed.MatchedSkills, mixed.MissingSkills)
	}
}

func sampleProfile() models.Profile {
	return models.Profile{DesiredTitles: []string{"Backend Engineer"}, Seniorities: []string{"senior"}, WorkPreference: []string{"remote"}, SalaryExpectation: 90000, Experiences: []models.WorkExperience{{Company: "Acme Inc.", Current: true}}, Skills: []models.Skill{{Name: "Go"}, {Name: "PostgreSQL"}}}
}

func TestHardRejectEmployer(t *testing.T) {
	e := Evaluate(sampleProfile(), nil, nil, models.JobInput{Title: "Backend Engineer", Company: "ACME LLC"}, time.Now())
	if !e.Rejected {
		t.Fatal("expected current employer rejection")
	}
}
func TestFilterRejections(t *testing.T) {
	f := models.FilterPreset{ExcludedTitleWords: []string{"unpaid"}, RemoteTypes: []string{"remote"}, MinSalary: 80000}
	e := Evaluate(sampleProfile(), nil, &f, models.JobInput{Title: "Unpaid Backend Engineer", Company: "Other", RemoteType: "onsite", SalaryMax: 50000}, time.Now())
	if len(e.RejectionReasons) != 3 {
		t.Fatalf("reasons=%v", e.RejectionReasons)
	}
}
func TestScoreBreakdown(t *testing.T) {
	e := Evaluate(sampleProfile(), nil, nil, models.JobInput{Title: "Backend Engineer", Company: "Other", RemoteType: "remote", SalaryMax: 100000, Description: "Go and PostgreSQL", Industry: "Software"}, time.Now())
	if e.Score < 70 || e.Score > 100 {
		t.Fatalf("unexpected score %d", e.Score)
	}
	if e.Breakdown["title_match"] != 25 {
		t.Fatal(e.Breakdown)
	}
}

func TestTechnologyFilters(t *testing.T) {
	f := models.FilterPreset{IncludedTechnologies: []string{"Go", "Rust"}, ExcludedTechnologies: []string{"PHP"}}
	accepted := Evaluate(sampleProfile(), nil, &f, models.JobInput{Title: "Backend Engineer", Company: "Other", Description: "Build services with Go and PostgreSQL."}, time.Now())
	if accepted.Rejected {
		t.Fatalf("expected technology match, got %v", accepted.RejectionReasons)
	}
	rejected := Evaluate(sampleProfile(), nil, &f, models.JobInput{Title: "Backend Engineer", Company: "Other", Description: "Maintain a PHP application."}, time.Now())
	if len(rejected.RejectionReasons) != 2 {
		t.Fatalf("expected missing-required and excluded-technology reasons, got %v", rejected.RejectionReasons)
	}
}

func TestRemoteRoleWithDifferentLocationWarns(t *testing.T) {
	f := models.FilterPreset{Locations: []string{"Romania"}, RemoteTypes: []string{"remote"}}
	e := Evaluate(sampleProfile(), nil, &f, models.JobInput{Title: "Backend Engineer", Company: "Other", Location: "Europe", RemoteType: "remote", Description: "Go services"}, time.Now())
	if e.Rejected || len(e.Warnings) != 1 {
		t.Fatalf("expected warning without rejection, rejected=%v warnings=%v", e.Rejected, e.Warnings)
	}
}

func TestIncludedTitleAllowsStrongTokenOverlap(t *testing.T) {
	f := models.FilterPreset{IncludedTitles: []string{"Backend Engineer"}}
	e := Evaluate(sampleProfile(), nil, &f, models.JobInput{Title: "Senior Backend Software Engineer", Company: "Other", Description: "Go services"}, time.Now())
	if e.Rejected {
		t.Fatalf("expected similar title to pass, got %v", e.RejectionReasons)
	}
}

func TestSeniorityUsesBestSelectedLevel(t *testing.T) {
	p := sampleProfile()
	p.Seniorities = []string{"mid", "senior"}
	e := Evaluate(p, nil, nil, models.JobInput{Title: "Mid Backend Engineer", Company: "Other", Description: "Go services"}, time.Now())
	if e.Breakdown["seniority_match"] != 15 {
		t.Fatalf("expected exact match from selected levels, got %v", e.Breakdown)
	}
}

// splitWords replaced a regexp in the hot path of every evaluation, so it has
// to tokenise as a pattern would: a word starts at a-z, a digit, +, #, a dot
// or a Latin letter, and a combining accent continues it. One deliberate
// difference: dots at the end of a word are punctuation and are dropped, so
// that a word ending a sentence is still that word.
func TestSplitWordsMatchesTheWordPattern(t *testing.T) {
	pattern := regexp.MustCompile(`[a-z0-9+#.\p{Latin}](?:[a-z0-9+#.\p{Latin}]|[^\P{Mn}\x{180b}-\x{180d}\x{180f}\x{fe00}-\x{fe0f}\x{e0100}-\x{e01ef}])*`)
	for _, text := range []string{
		"", "   ", "go", "Senior Go/PostgreSQL engineer (m/w/d)",
		"C++, C#, .NET and Node.js; 5+ years", "trailing.dot. and ...dots",
		"Überweisung für Entwickler — naïve café", "ÀÉÎ ßẞ ǅ İstanbul", "tabs\tand\nnewlines\r\n",
		"emoji 🚀 inside, go-live, e-mail", `under_score and back\slash`,
		"Ku\U00000308nstliche \U00000308lone accent, k\U00001ab0unstliche", "1\U0000fe0f\U000020e3 go \U00002714\U0000fe0frust", "熟悉go和python на rust", "go\xffrust\xc3",
	} {
		lower := strings.ToLower(text)
		var want []string
		for _, word := range pattern.FindAllString(lower, -1) {
			if word = strings.TrimRight(word, "."); word != "" {
				want = append(want, word)
			}
		}
		got := splitWords(lower)
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("splitWords(%q) = %q, want %q", lower, got, want)
		}
	}
}

// A nil slice encodes as JSON null, and clients index these lists directly: the
// job page crashed on missing_skills.length for every job whose skills all
// matched. Both lists must encode as arrays whichever way the match goes.
func TestSkillListsAreNeverNull(t *testing.T) {
	allMatched := Evaluate(sampleProfile(), nil, nil, models.JobInput{Title: "Backend Engineer", Description: "Go and PostgreSQL."}, time.Now())
	noneMatched := Evaluate(sampleProfile(), nil, &models.FilterPreset{RequiredSkills: []string{"Rust"}}, models.JobInput{Title: "Backend Engineer", Description: "Rust."}, time.Now())
	for name, lists := range map[string][2][]string{
		"all matched":  {allMatched.MatchedSkills, allMatched.MissingSkills},
		"none matched": {noneMatched.MatchedSkills, noneMatched.MissingSkills},
	} {
		for _, list := range lists {
			if encoded, _ := json.Marshal(list); string(encoded) == "null" {
				t.Errorf("%s: a skill list encodes as null, want an array", name)
			}
		}
	}
}

// Found in early use: a filter's "skills used for score" were
// counted whether or not the job mentioned them, so every job matched the
// same three skills, got the same skill score, and every cover letter called
// them the relevant skills, including for a Python role that never mentions
// Go. The listed skills now count only where the job mentions them.
func TestScoreSkillsOnlyCountSkillsTheJobMentions(t *testing.T) {
	profile := models.Profile{Skills: []models.Skill{{Name: "Go"}, {Name: "Docker"}, {Name: "AWS"}, {Name: "Python"}, {Name: "PostgreSQL"}}}
	filter := &models.FilterPreset{RequiredSkills: []string{"Go", "Docker", "AWS"}}

	pythonRole := Evaluate(profile, nil, filter, models.JobInput{Title: "Python Backend Engineer", Description: "Build Python services on AWS."}, time.Now())
	if strings.Join(pythonRole.MatchedSkills, ",") != "AWS,Python" {
		t.Fatalf("only skills the job asks for may match, got %v", pythonRole.MatchedSkills)
	}
	if pythonRole.Breakdown["skill_match"] != 30 {
		t.Fatalf("everything the job asks for is held, so the skill score is full, got %d", pythonRole.Breakdown["skill_match"])
	}

	// None of the listed skills appear: score on the profile's skills the job
	// mentions, as without a list, rather than claiming the list.
	elsewhere := Evaluate(profile, nil, filter, models.JobInput{Title: "Backend Engineer", Description: "Python and PostgreSQL."}, time.Now())
	if strings.Join(elsewhere.MatchedSkills, ",") != "PostgreSQL,Python" {
		t.Fatalf("with no listed skill mentioned, the profile's mentioned skills should count, got %v", elsewhere.MatchedSkills)
	}
	for _, skill := range elsewhere.MatchedSkills {
		if skill == "Go" || skill == "Docker" || skill == "AWS" {
			t.Fatalf("%s is not in the job and must not be claimed", skill)
		}
	}

	// A listed skill the job wants and the profile lacks is the useful gap.
	gap := Evaluate(profile, nil, &models.FilterPreset{RequiredSkills: []string{"Kafka", "Go"}}, models.JobInput{Title: "Backend Engineer", Description: "Go services on Kafka."}, time.Now())
	if strings.Join(gap.MatchedSkills, ",") != "Go" || strings.Join(gap.MissingSkills, ",") != "Kafka" {
		t.Fatalf("expected Go matched and Kafka missing, got %v and %v", gap.MatchedSkills, gap.MissingSkills)
	}

	// A listed skill the job asks for and the profile lacks lowers the score,
	// so two jobs no longer score the same whatever they say.
	if pythonRole.Breakdown["skill_match"] == gap.Breakdown["skill_match"] {
		t.Fatalf("a gap in the listed skills should lower the skill score, both got %d", gap.Breakdown["skill_match"])
	}
}

// The word pattern keeps dots so that "node.js" survives, which also glued a
// sentence's full stop onto its last word: "Build on AWS." never matched the
// skill AWS, and "Maintain a PHP app." dodged an excluded-technology rule only
// when PHP ended the sentence. Dots that end a word are now dropped.
func TestSentenceEndingWordsAreStillWords(t *testing.T) {
	got := strings.Join(splitWords(strings.ToLower("Build on AWS. We use Node.js, Next.js and ASP.NET... Ship it.")), " ")
	want := "build on aws we use node.js next.js and asp.net ship it"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	f := models.FilterPreset{ExcludedTechnologies: []string{"PHP"}}
	e := Evaluate(sampleProfile(), nil, &f, models.JobInput{Title: "Backend Engineer", Company: "Other", Description: "You will maintain our legacy PHP."}, time.Now())
	if !e.Rejected {
		t.Fatal("an excluded technology at the end of a sentence must still reject")
	}

	// A leading dot is part of the name: ".NET" is not the word "net", which
	// postings use for pay ("net salary").
	dotnet := models.FilterPreset{ExcludedTechnologies: []string{".NET"}}
	if e := Evaluate(sampleProfile(), nil, &dotnet, models.JobInput{Title: "Backend Engineer", Company: "Other", Description: "Go services. Competitive net salary."}, time.Now()); e.Rejected {
		t.Fatalf("excluding .NET must not reject a job that only mentions net pay, got %v", e.RejectionReasons)
	}
	if e := Evaluate(sampleProfile(), nil, &dotnet, models.JobInput{Title: "Backend Engineer", Company: "Other", Description: "Our services run on .NET."}, time.Now()); !e.Rejected {
		t.Fatal("excluding .NET must reject a job built on .NET, even at the end of a sentence")
	}
}

// Found in early use: the skill score only counted skills the
// profile or the filter named, so a technology the candidate lacks was never
// seen, and 2,159 of 2,189 jobs scored the full 30. The job's own
// technologies now count, each weighted by how often the job mentions it, so
// a framework it keeps naming matters and a tool it names once barely does.
// Ways of working such as agile are not technologies.
func TestSkillScoreWeighsWhatTheJobAsksFor(t *testing.T) {
	profile := models.Profile{Skills: []models.Skill{{Name: "Golang"}, {Name: "PostgreSQL"}, {Name: "Temporal"}}}

	django := Evaluate(profile, nil, nil, models.JobInput{Title: "Backend Engineer", Description: "Build Go services with Django and PostgreSQL. Django experience is key. Agile team."}, time.Now())
	if got := django.Breakdown["skill_match"]; got != 15 {
		t.Fatalf("go and postgresql held, django named twice and not: 2 of 4 mentions is 15, got %d", got)
	}
	if strings.Join(django.MatchedSkills, ",") != "Golang,PostgreSQL" || strings.Join(django.MissingSkills, ",") != "Django" {
		t.Fatalf("matched skills keep the profile's spelling and agile is not missing: got %v and %v", django.MatchedSkills, django.MissingSkills)
	}
	// A skill the job mentions is not one it requires, so the flag says what
	// is known and no more.
	if strings.Join(django.RedFlags, " ") == "" || !strings.Contains(strings.Join(django.RedFlags, " "), "mentions skills your profile does not list: Django") {
		t.Fatalf("the red flag should name the skill without calling it required: %v", django.RedFlags)
	}

	tool := Evaluate(profile, nil, nil, models.JobInput{Title: "Go Engineer", Description: "Write Go on PostgreSQL, in Go, deployed with CircleCI."}, time.Now())
	if got := tool.Breakdown["skill_match"]; got != 24 {
		t.Fatalf("go named three times and postgresql once, held; circleci once, not: 4 of 5 mentions is 24, got %d", got)
	}

	// A skill outside the vocabulary still counts where the job names it.
	workflow := Evaluate(profile, nil, nil, models.JobInput{Title: "Backend Engineer", Description: "Orchestrate workflows with Temporal."}, time.Now())
	if strings.Join(workflow.MatchedSkills, ",") != "Temporal" || workflow.Breakdown["skill_match"] != 30 {
		t.Fatalf("a profile skill the job names should count, got %v and %d", workflow.MatchedSkills, workflow.Breakdown["skill_match"])
	}
}

// A skill of several words matched whenever each word was somewhere in the
// posting, so "customer success" matched "customer feedback" in one paragraph
// and "our success" in another. Its words must be next to each other.
func TestMultiWordSkillsNeedTheirWordsTogether(t *testing.T) {
	// "..." has no words, so it is never named, whatever joins title and description.
	profile := models.Profile{Skills: []models.Skill{{Name: "Customer success"}, {Name: "Go test"}, {Name: "End-to-end testing"}, {Name: "..."}}}
	matched := func(filter *models.FilterPreset, title, description string) string {
		return strings.Join(Evaluate(profile, nil, filter, models.JobInput{Title: title, Description: description}, time.Now()).MatchedSkills, ",")
	}
	for name, job := range map[string][2]string{
		"words apart":            {"Backend Engineer", "You act on customer feedback and write Go.\n\nOur success rests on test generation, from one end of the system to the other end, and on testing."},
		"words in another order": {"Backend Engineer", "Success with every customer. We test Go services."},
		"half in the title":      {"Engineer, Customer", "Success is shared. Go is our language."},
	} {
		if got := matched(nil, job[0], job[1]); got != "" {
			t.Errorf("%s: no skill is named as a phrase, got %s", name, got)
		}
	}
	for name, job := range map[string][2]string{
		"in the description": {"Backend Engineer", "You work with customer success, run go test in CI and own end-to-end testing."},
		"in the title":       {"Customer Success Engineer", "Run `go test`; end to end testing is yours."},
	} {
		if got := matched(nil, job[0], job[1]); got != "Customer success,End-to-end testing,Go test" {
			t.Errorf("%s: all three skills are named, got %s", name, got)
		}
	}

	// The filter's skills are read the same way.
	filter := &models.FilterPreset{RequiredSkills: []string{"Ledger reconciliation"}}
	apart := Evaluate(profile, nil, filter, models.JobInput{Title: "Backend Engineer", Description: "Reconciliation runs nightly against the ledger."}, time.Now())
	if len(apart.MissingSkills) != 0 {
		t.Errorf("a listed skill whose words are apart is not asked for, got %v", apart.MissingSkills)
	}
	named := Evaluate(profile, nil, filter, models.JobInput{Title: "Backend Engineer", Description: "You own ledger reconciliation."}, time.Now())
	if strings.Join(named.MissingSkills, ",") != "Ledger reconciliation" {
		t.Errorf("a listed skill the job names is the gap, got %v", named.MissingSkills)
	}
	// The score follows: nothing asked is the neutral 15, a named skill that is
	// held is the full 30, and a named skill that is not held is 0.
	for description, want := range map[string]int{
		"You act on customer feedback. Our success is shared.": 15,
		"You work with customer success.":                      30,
	} {
		if got := Evaluate(profile, nil, nil, models.JobInput{Title: "Engineer", Description: description}, time.Now()).Breakdown["skill_match"]; got != want {
			t.Errorf("%q: skill score %d, want %d", description, got, want)
		}
	}
	if got := named.Breakdown["skill_match"]; got != 0 {
		t.Errorf("the only skill asked for is not held, so the score is 0, got %d", got)
	}
}

// Only a-z counted as letters, so "Künstliche" was read as "k" and "nstliche"
// and a one-letter skill matched a German posting with a full skill score.
func TestAccentedLettersStayInsideTheirWord(t *testing.T) {
	for text, want := range map[string]string{
		"Künstliche Intelligenz für große Projekte": "künstliche intelligenz für große projekte",
		"naïve café in Łódź":                        "naïve café in łódź",
		// A letter and a combining accent are one letter.
		"Ku\U00000308nstliche, k\U00001ab0unstliche": "ku\U00000308nstliche k\U00001ab0unstliche",
		// Everything else separates words as before: a non-breaking space, a
		// dash, a curly apostrophe, an emoji with its variation selector, a
		// script written without spaces, a byte that is not UTF-8.
		"senior\U000000a0engineer, go\U00002014rust, team\U00002019s":      "senior engineer go rust team s",
		"\U00002714\U0000fe0fgo 1\U0000fe0f\U000020e3 熟悉go和python на rust": "go 1 go python rust",
		"go\xffrust": "go rust",
	} {
		if got := strings.Join(splitWords(strings.ToLower(text)), " "); got != want {
			t.Errorf("splitWords(%q) = %q, want %q", text, got, want)
		}
	}

	profile := models.Profile{Skills: []models.Skill{{Name: "K"}, {Name: "Qualität"}, {Name: "Qualitätssicherung"}}}
	for _, description := range []string{
		"Erfahrung in Künstlicher Intelligenz und Qualitätssicherung.",
		"Erfahrung in Ku\U00000308nstlicher Intelligenz, k\U00001ab0unstlich und Qualitätssicherung.",
	} {
		german := Evaluate(profile, nil, nil, models.JobInput{Title: "Entwickler", Description: description}, time.Now())
		if got := strings.Join(german.MatchedSkills, ","); got != "Qualitätssicherung" {
			t.Errorf("%q: only the skill the posting names as a whole word matches, got %s", description, got)
		}
	}

	// The filters read words the same way: "für" is not the technology R.
	filter := &models.FilterPreset{ExcludedTechnologies: []string{"R"}}
	if e := Evaluate(profile, nil, filter, models.JobInput{Title: "Entwickler", Company: "Other", Description: "Wir suchen für unser Team."}, time.Now()); e.Rejected {
		t.Errorf("an excluded technology is not a fragment of an accented word: %v", e.RejectionReasons)
	}
	if e := Evaluate(profile, nil, filter, models.JobInput{Title: "Entwickler", Company: "Other", Description: "Statistik in R für unser Team."}, time.Now()); !e.Rejected {
		t.Error("an excluded technology the posting names still rejects")
	}
}

// A hyphen ends a word, so "go-to-market" asked a sales posting for Go: a
// profile without Go lost points and one with Go matched it.
func TestGoToMarketIsNotGo(t *testing.T) {
	job := models.JobInput{Title: "Account Executive", Company: "Brambleglass Labs", Description: "Own the go-to-market strategy and go live with new regions."}
	for _, skills := range [][]models.Skill{{{Name: "Python"}}, {{Name: "Go"}, {Name: "golang"}}} {
		e := Evaluate(models.Profile{Skills: skills}, nil, nil, job, time.Now())
		if e.Breakdown["skill_match"] != 15 || len(e.MatchedSkills) != 0 || len(e.MissingSkills) != 0 {
			t.Errorf("skills %v: got skill_match %d, matched %v, missing %v; want the neutral 15 and no skills", skills, e.Breakdown["skill_match"], e.MatchedSkills, e.MissingSkills)
		}
	}
	job.Description = "Own the go-to-market of our Go SDK."
	e := Evaluate(models.Profile{Skills: []models.Skill{{Name: "Go"}}}, nil, nil, job, time.Now())
	if e.Breakdown["skill_match"] != 30 || !reflect.DeepEqual(e.MatchedSkills, []string{"Go"}) {
		t.Errorf("a real Go mention beside go-to-market: got skill_match %d, matched %v", e.Breakdown["skill_match"], e.MatchedSkills)
	}
}

// A profile skill of several words still matches a hyphenated spelling.
func TestHyphenatedSkillStillMatches(t *testing.T) {
	job := models.JobInput{Title: "Backend Engineer", Company: "Brambleglass Labs", Description: "Services in Spring-Boot."}
	profile := models.Profile{Skills: []models.Skill{{Name: "Spring Boot"}}}
	if e := Evaluate(profile, nil, nil, job, time.Now()); !reflect.DeepEqual(e.MatchedSkills, []string{"Spring Boot"}) {
		t.Errorf("rules matched %v, want [Spring Boot]", e.MatchedSkills)
	}
	x := models.Extraction{Required: []models.ExtractedSkills{{Skills: []string{"Spring-Boot"}, Quote: "Services in Spring-Boot."}}}
	if e := EvaluateWith(profile, nil, nil, job, &x, time.Now()); e.Breakdown["skill_match"] != 30 || !reflect.DeepEqual(e.MatchedSkills, []string{"Spring Boot"}) {
		t.Errorf("extraction scored %d and matched %v, want 30 and [Spring Boot]", e.Breakdown["skill_match"], e.MatchedSkills)
	}
}

// Technology filters read go-to-market as no mention of Go, and a filter's
// required skill Go is not found in it either.
func TestTechnologyFiltersSkipGoInEverydayPhrases(t *testing.T) {
	sales := models.JobInput{Title: "Account Executive", Company: "Brambleglass Labs", Description: "Own the go-to-market strategy."}
	engineering := models.JobInput{Title: "Engineer", Company: "Brambleglass Labs", Description: "Own the go-to-market of our Go SDK."}
	include := &models.FilterPreset{IncludedTechnologies: []string{"Go"}}
	exclude := &models.FilterPreset{ExcludedTechnologies: []string{"Go"}}
	if e := Evaluate(models.Profile{}, nil, include, sales, time.Now()); !e.Rejected {
		t.Error("an include-Go filter accepts a posting that only says go-to-market")
	}
	if e := Evaluate(models.Profile{}, nil, exclude, sales, time.Now()); e.Rejected {
		t.Errorf("an exclude-Go filter rejects a posting that only says go-to-market: %v", e.RejectionReasons)
	}
	if e := Evaluate(models.Profile{}, nil, include, engineering, time.Now()); e.Rejected {
		t.Errorf("an include-Go filter rejects a posting that names Go: %v", e.RejectionReasons)
	}
	if e := Evaluate(models.Profile{}, nil, exclude, engineering, time.Now()); !e.Rejected {
		t.Error("an exclude-Go filter accepts a posting that names Go")
	}
	required := &models.FilterPreset{RequiredSkills: []string{"Go"}}
	if e := Evaluate(models.Profile{}, nil, required, sales, time.Now()); e.Breakdown["skill_match"] != 15 || len(e.MissingSkills) != 0 {
		t.Errorf("a required filter skill Go was read in go-to-market: skill_match %d, missing %v", e.Breakdown["skill_match"], e.MissingSkills)
	}
}

// The filters read the title and the description apart, so a title ending in
// Go and a description starting with "Live" do not make "go live".
func TestTechnologyFiltersReadTitleAndDescriptionApart(t *testing.T) {
	job := models.JobInput{Title: "Backend Engineer Go", Company: "Brambleglass Labs", Description: "Live services at scale."}
	if e := Evaluate(models.Profile{}, nil, &models.FilterPreset{IncludedTechnologies: []string{"Go"}}, job, time.Now()); e.Rejected {
		t.Errorf("an include-Go filter rejects a posting whose title names Go: %v", e.RejectionReasons)
	}
	if e := Evaluate(models.Profile{}, nil, &models.FilterPreset{ExcludedTechnologies: []string{"Go"}}, job, time.Now()); !e.Rejected {
		t.Error("an exclude-Go filter accepts a posting whose title names Go")
	}
}
