package cv

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

func textLayerFixture() (models.Profile, models.Job, Tailored) {
	profile := models.Profile{
		Name: "Ana Example", Email: "ana@example.test", Phone: "+40 712 345 678",
		Summary: "Build reliable services.", Skills: []models.Skill{{Name: "Golang"}, {Name: "Postgres"}},
		Experiences: []models.WorkExperience{
			{Title: "Platform Engineer", Company: "Example One", StartDate: "Jan 2020", Current: true, Description: "Built Go services."},
			{Title: "Software Engineer", Company: "Example Two", StartDate: "Feb 2020", EndDate: "Dec 2019", Description: "Built PostgreSQL services."},
		},
		Certifications: []string{"Example Certificate"}, Languages: []string{"English"},
	}
	job := models.Job{JobInput: models.JobInput{Title: "Backend Engineer", Description: "Golang, PostgreSQL and Rust."}}
	tailored := Tailor(context.Background(), llm.New(nil, nil, 0, 0), profile, job, models.Evaluation{}, "")
	tailored.Education = []string{"Example University"}
	return profile, job, tailored
}

func TestPDFTextLayer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Tailored)
		text   func(string) string
		want   string
	}{
		{name: "clean render"},
		{name: "email glyph", mutate: func(cv *Tailored) { cv.Contact[0] = "\u2709" }, want: "email"},
		{name: "missing email", mutate: func(cv *Tailored) { cv.Contact[0] = "" }, want: "email"},
		{name: "missing phone", mutate: func(cv *Tailored) { cv.Contact[1] = "" }, want: "phone"},
		{name: "altered email suffix", mutate: func(cv *Tailored) { cv.Contact[0] += ".invalid" }, want: "email"},
		{name: "altered phone suffix", mutate: func(cv *Tailored) { cv.Contact[1] += "9" }, want: "phone"},
		{name: "missing name", mutate: func(cv *Tailored) { cv.Name = "" }, want: "name"},
		{name: "split email", text: func(s string) string { return strings.ReplaceAll(s, "ana@example.test", "ana@\nexample.test") }, want: "email"},
		{name: "split phone", text: func(s string) string { return strings.ReplaceAll(s, "+40 712 345 678", "+40 712\n345 678") }, want: "phone"},
		{name: "interleaved roles", text: func(s string) string {
			return strings.ReplaceAll(s, "Platform Engineer - Example One", "Platform Engineer Software Engineer - Example One")
		}, want: "reading order"},
		{name: "sections out of order", text: func(s string) string {
			return strings.NewReplacer("SKILLS", "EXPERIENCE", "EXPERIENCE", "SKILLS").Replace(s)
		}, want: "reading order"},
		{name: "missing section", text: func(s string) string { return strings.ReplaceAll(s, "EXPERIENCE", "HISTORY") }, want: "EXPERIENCE"},
		{name: "missing company", mutate: func(cv *Tailored) { cv.Experiences[0].Company = "" }, want: "Example One"},
		{name: "missing dates", mutate: func(cv *Tailored) { cv.Experiences[0].Period = "" }, want: "Jan 2020"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile, job, expected := textLayerFixture()
			rendered := expected
			rendered.Contact = append([]string(nil), expected.Contact...)
			rendered.Experiences = append([]TailoredRole(nil), expected.Experiences...)
			if tc.mutate != nil {
				tc.mutate(&rendered)
			}
			data, err := RenderPDF(rendered)
			if err != nil {
				t.Fatal(err)
			}
			text, review := ReviewPDF(data, expected, profile, "", job)
			if tc.text != nil {
				review = CheckTextLayer(tc.text(text), expected, profile, "", job)
			}
			if tc.want == "" {
				if len(review.Blockers) != 0 {
					t.Fatalf("clean render blocked: %v\n%s", review.Blockers, text)
				}
			} else if !strings.Contains(strings.Join(review.Blockers, " "), tc.want) {
				t.Fatalf("want blocker naming %q, got %v\n%s", tc.want, review.Blockers, text)
			}
			want := []KeywordFinding{{Technology: "go", Status: "covered", Present: true}, {Technology: "postgresql", Status: "covered", Present: true}, {Technology: "rust", Status: "gap"}}
			if !reflect.DeepEqual(review.Keywords, want) {
				t.Fatalf("keywords = %+v, want %+v", review.Keywords, want)
			}
			if strings.Contains(strings.ToLower(text), "rust") {
				t.Fatal("a gap was inserted into the CV")
			}
		})
	}
}

func TestTextLayerReportsSupportedKeywordLostInPDF(t *testing.T) {
	profile, job, tailored := textLayerFixture()
	data, err := RenderPDF(tailored)
	if err != nil {
		t.Fatal(err)
	}
	text, _, _ := ExtractText("cv.pdf", data)
	review := CheckTextLayer(strings.NewReplacer("PostgreSQL", "Storage", "Postgres", "Storage").Replace(text), tailored, profile, "", job)
	if review.Keywords[1].Status != "missing" || review.Keywords[1].Present {
		t.Fatalf("lost supported keyword: %+v", review.Keywords)
	}
}

func TestPDFTextLayerExtractionFailureBlocksReview(t *testing.T) {
	profile, job, tailored := textLayerFixture()
	text, review := ReviewPDF([]byte("broken PDF"), tailored, profile, "", job)
	if text != "" || !strings.Contains(strings.Join(review.Blockers, " "), "extract") {
		t.Fatalf("extraction failure: %q %+v", text, review)
	}
}

func TestTextLayerUsesStoredCVFacts(t *testing.T) {
	profile, job, tailored := textLayerFixture()
	base := "Ana Example\nana@example.test\n+40 712 345 678\nSKILLS\nRust\n"
	profile.Phone = ""
	data, err := RenderPDF(tailored)
	if err != nil {
		t.Fatal(err)
	}
	_, review := ReviewPDF(data, tailored, profile, base, job)
	if len(review.Blockers) != 0 || review.Keywords[2].Status != "missing" {
		t.Fatalf("stored CV facts ignored: %+v", review)
	}
}

func TestPDFTextLayerKeepsNormalizedContactPunctuation(t *testing.T) {
	profile, job, tailored := textLayerFixture()
	profile.Name = "Ana O’Neil"
	tailored.Name = profile.Name
	data, err := RenderPDF(tailored)
	if err != nil {
		t.Fatal(err)
	}
	_, review := ReviewPDF(data, tailored, profile, "", job)
	if len(review.Blockers) != 0 {
		t.Fatalf("normal text cleanup must not block a contact: %v", review.Blockers)
	}
}
