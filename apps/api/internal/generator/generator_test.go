package generator

import (
	"jobpilot/core/api/internal/models"
	"strings"
	"testing"
)

func TestTemplatesUseProfileData(t *testing.T) {
	p := models.Profile{Name: "Ada", CurrentTitle: "Engineer", YearsExperience: 5, Skills: []models.Skill{{Name: "Go"}}, NoticePeriod: "Two weeks"}
	j := models.Job{JobInput: models.JobInput{Title: "Go Engineer", Company: "Example"}}
	d := Generate(p, j, models.Evaluation{MatchedSkills: []string{"Go"}})
	if !strings.Contains(d.CoverLetter, "Ada") || !strings.Contains(d.RecruiterMessage, "Go") {
		t.Fatal(d)
	}
	if d.Answers["Notice period."] != "Two weeks" {
		t.Fatal("notice")
	}
	if strings.Contains(d.CoverLetter, "Kubernetes") {
		t.Fatal("invented data")
	}
}
