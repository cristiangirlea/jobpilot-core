package cv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

// A profile filled by hand is often thin while the uploaded CV is complete.
// Tailoring then has to draw on the CV, or it produces a page with a name, a
// sentence and two skills.
func TestTailorFillsFromTheCVWhenTheProfileIsThin(t *testing.T) {
	thin := models.Profile{Name: "Jane Doe", Skills: []models.Skill{{Name: "Go", Years: 6}, {Name: "Python", Years: 5}}}
	tailored := Tailor(context.Background(), llm.New(nil, nil, 0, 0), thin, testJob(), models.Evaluation{}, pipeLayoutCV)

	if tailored.Headline != "Lead Full-Stack & AI Engineer" {
		t.Errorf("headline = %q, want the CV's own title", tailored.Headline)
	}
	if !contains(tailored.Contact, "jane.doe@example.com") {
		t.Errorf("contact = %v, want the CV's email", tailored.Contact)
	}
	if len(tailored.Experiences) != 4 || tailored.Experiences[0].Company != "Acme Security" {
		t.Fatalf("roles = %+v, want the CV's four roles", tailored.Experiences)
	}
	if len(tailored.Experiences[0].Bullets) == 0 {
		t.Error("the CV's bullets should carry into the tailored role")
	}
	if !contains(tailored.Education, "M.Sc. Computer Engineering, Example University | 2009 - 2011") {
		t.Errorf("education = %v, want the CV's degree", tailored.Education)
	}
	// The profile's own skills win over the CV's where the profile has some.
	if contains(tailored.Skills, "Kubernetes") {
		t.Errorf("skills = %v: the profile's skills should be used, not the CV's", tailored.Skills)
	}
	if !strings.HasPrefix(tailored.Summary, "Full-stack engineer with 11+ years") {
		t.Errorf("summary = %q, want the CV's own summary", tailored.Summary)
	}
}

func TestTailorTakesSkillsFromTheCVWhenTheProfileHasNone(t *testing.T) {
	tailored := Tailor(context.Background(), llm.New(nil, nil, 0, 0), models.Profile{Name: "Jane Doe"}, testJob(), models.Evaluation{}, pipeLayoutCV)
	if !contains(tailored.Skills, "Kubernetes") || !contains(tailored.Skills, "Go") {
		t.Errorf("skills = %v, want the CV's", tailored.Skills)
	}
}

// Claiming the job's title as the candidate's own is exactly what tailoring
// must not do.
func TestTailorNeverUsesTheJobTitleAsTheHeadline(t *testing.T) {
	tailored := Tailor(context.Background(), llm.New(nil, nil, 0, 0), models.Profile{Name: "Jane Doe"}, testJob(), models.Evaluation{}, "")
	if tailored.Headline != "" {
		t.Errorf("headline = %q, want none rather than the job's title", tailored.Headline)
	}
}

// With no provider the summary is the candidate's own words, not a list of
// keywords stapled to the front of them.
func TestTheDeterministicSummaryIsTheCandidatesOwn(t *testing.T) {
	tailored := Tailor(context.Background(), llm.New(nil, nil, 0, 0), testProfile(), testJob(), models.Evaluation{}, "")
	if tailored.Summary != testProfile().Summary {
		t.Errorf("summary = %q, want %q", tailored.Summary, testProfile().Summary)
	}
}

// PDF text wraps a bullet across lines and puts the role's one-line context
// above its bullets; both must come through as the candidate wrote them.
func TestTailorRejoinsWrappedBulletsAndKeepsTheRoleContext(t *testing.T) {
	profile := models.Profile{Name: "Jane Doe", Experiences: []models.WorkExperience{{
		Title: "Backend Engineer", Company: "Globex", StartDate: "2021", EndDate: "2020",
		Description: "Payments platform for retail banks. Stack: Go, Kafka.\n- Built the settlement service in Go, from the queue consumers\nto the reconciliation reports.\n- Wrote AI - assisted runbooks for the on-call rota.",
	}}}
	role := Tailor(context.Background(), llm.New(nil, nil, 0, 0), profile, testJob(), models.Evaluation{}, "").Experiences[0]
	if role.Context != "Payments platform for retail banks. Stack: Go, Kafka." {
		t.Errorf("context = %q", role.Context)
	}
	want := []string{
		"Built the settlement service in Go, from the queue consumers to the reconciliation reports.",
		"Wrote AI-assisted runbooks for the on-call rota.",
	}
	if strings.Join(role.Bullets, "|") != strings.Join(want, "|") {
		t.Errorf("bullets = %q, want %q", role.Bullets, want)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want || strings.HasPrefix(item, want+" (") {
			return true
		}
	}
	return false
}

// PDF text spaces hyphens in several ways; words joined by one come back as
// one word wherever the CV's own text is reused.
func TestHyphenatedWordsAreClosedUp(t *testing.T) {
	for in, want := range map[string]string{
		"AI - assisted workflows":             "AI-assisted workflows",
		"a synthetic dataset -generation job": "a synthetic dataset-generation job",
		"LLM- ready context":                  "LLM-ready context",
		"Dec 2022 - Oct 2023":                 "Dec 2022 - Oct 2023",
		"Engineer - Contractor":               "Engineer - Contractor",
	} {
		if got := closeHyphens(in); got != want {
			t.Errorf("closeHyphens(%q) = %q, want %q", in, got, want)
		}
	}
}

// Skills with no stated years are ordered by how much the candidate's own
// experience talks about them, not alphabetically, and a skill the posting
// names in its text leads even when the technology vocabulary lacks it.
func TestSkillsWithoutYearsFollowTheEvidence(t *testing.T) {
	profile := models.Profile{
		Name:   "Jane Doe",
		Skills: []models.Skill{{Name: "ABAC"}, {Name: "Beanstalkd"}, {Name: "Kafka"}, {Name: "Hatchet"}},
		Experiences: []models.WorkExperience{{Title: "Engineer", Company: "Globex", StartDate: "2021", EndDate: "2020",
			Description: "- Ran Kafka consumers for payments.\n- Moved the Kafka topics to a new cluster.\n- Added Beanstalkd for retries."}},
	}
	job := models.Job{JobInput: models.JobInput{Title: "Backend Engineer", Description: "We run Hatchet workers for background jobs."}}
	skills := Tailor(context.Background(), llm.New(nil, nil, 0, 0), profile, job, models.Evaluation{}, "").Skills
	want := []string{"Hatchet", "Kafka", "Beanstalkd", "ABAC"}
	if strings.Join(skills, ",") != strings.Join(want, ",") {
		t.Errorf("skills = %v, want %v: the posting's first, then by mentions in experience", skills, want)
	}
}

// A role only the profile knows, such as a new job the CV does not list yet,
// must survive being merged with a fuller CV.
func TestTailorKeepsProfileRolesTheCVLacks(t *testing.T) {
	profile := models.Profile{Name: "Jane Doe", Experiences: []models.WorkExperience{
		{Title: "Staff Engineer", Company: "Newco", StartDate: "2023", Current: true},
		{Title: "Senior Software Engineer, Payments & Backend", Company: "Acme Security", StartDate: "Dec 2022"},
	}}
	roles := Tailor(context.Background(), llm.New(nil, nil, 0, 0), profile, testJob(), models.Evaluation{}, pipeLayoutCV).Experiences
	companies := make([]string, 0, len(roles))
	for _, role := range roles {
		companies = append(companies, role.Company)
	}
	if strings.Join(companies, ",") != "Newco,Acme Security,Globex,Umbrella Corp,Initech" {
		t.Fatalf("companies = %v: profile roles first, then the CV's others, none lost", companies)
	}
	// The profile's Acme role had no description; the CV's bullets fill it.
	if len(roles[1].Bullets) == 0 {
		t.Error("a profile role with no description should take the CV's bullets for the same employer")
	}
}

// An acronym that is also a word only counts when the posting writes it as
// the acronym.
func TestAcronymSkillsNeedTheAcronym(t *testing.T) {
	profile := models.Profile{Name: "Jane Doe", Skills: []models.Skill{{Name: "REST"}, {Name: "Terraform"}}}
	job := models.Job{JobInput: models.JobInput{Title: "Engineer", Description: "You will join the rest of the team and write terraform modules."}}
	tailored := Tailor(context.Background(), llm.New(nil, nil, 0, 0), profile, job, models.Evaluation{}, "")
	if strings.Join(tailored.Highlighted, ",") != "Terraform" {
		t.Errorf("highlighted = %v, want only Terraform", tailored.Highlighted)
	}
}

func TestALongSummaryIsCutOnACharacterBoundary(t *testing.T) {
	summary := strings.Repeat("€", 300) // 900 bytes of 3-byte runes, no sentence end
	got := deterministicSummary(models.Profile{Summary: summary})
	if !utf8.ValidString(got) || len(got) > maxSummary {
		t.Errorf("cut summary is %d bytes, valid UTF-8 = %v", len(got), utf8.ValidString(got))
	}
}

func TestCountWordTreatsTypographicPunctuationAsABoundary(t *testing.T) {
	if n := countWord("kafka’s topics and “kafka” streams", "kafka"); n != 2 {
		t.Errorf("countWord = %d, want 2", n)
	}
}

// The tailored CV is stored as JSON and read by the web preview, which walks
// every list. An empty profile must give empty lists, never null.
func TestTailorNeverReturnsANilList(t *testing.T) {
	tailored := Tailor(context.Background(), llm.New(nil, nil, 0, 0), models.Profile{Name: "Jane Doe"}, testJob(), models.Evaluation{}, "")
	raw, err := json.Marshal(tailored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "null") {
		t.Errorf("tailored CV has a null list: %s", raw)
	}
}
