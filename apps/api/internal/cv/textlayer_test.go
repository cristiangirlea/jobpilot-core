package cv

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ledongthuc/pdf"
	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

func TestPDFTextLayerLongIdentity(t *testing.T) {
	for _, field := range []string{"name", "role", "contact", "wrapped contact"} {
		t.Run(field, func(t *testing.T) {
			profile, job, tailored := textLayerFixture()
			switch field {
			case "name":
				profile.Name = "María Fernanda de los Ángeles García-Rodríguez"
				tailored.Name = profile.Name
			case "role":
				tailored.Experiences[0].Title = "Lead Backend Engineer, Payments Platform"
				tailored.Experiences[0].Company = "JPMorgan Chase & Co., Global Payments Technology"
			case "contact":
				profile.Name = "Ana S\u0326tefa\u0306nescu"
				profile.Email, profile.Phone = "ana+jobs@example.test", "(+40) 712-345-678"
				tailored.Name, tailored.Contact = profile.Name, []string{profile.Email, profile.Phone}
			case "wrapped contact":
				profile.Email = strings.Repeat("a", 64) + "@engineering.example.test"
				tailored.Contact = []string{profile.Email, profile.Phone, "Lisbon, Portugal", "https://example.test/ana", "https://github.com/ana-example"}
			}
			data, err := RenderPDF(tailored)
			if err != nil {
				t.Fatal(err)
			}
			text, review := ReviewPDF(data, tailored, profile, "", job)
			if field == "wrapped contact" {
				start, end := strings.Index(text, profile.Email), strings.Index(text, "SUMMARY")
				if start < 0 || end < start || strings.Count(strings.TrimSpace(text[start:end]), "\n") < 1 {
					t.Fatal("fixture must wrap the contact block")
				}
				for _, entry := range tailored.Contact {
					if !intactContact(text, entry) {
						t.Errorf("wrapped contact lost %q", entry)
					}
				}
			}
			if len(review.Blockers) != 0 {
				t.Fatalf("identity blocked: %v\n%s", review.Blockers, text)
			}
		})
	}
}

func TestPDFTextLayerFactSources(t *testing.T) {
	for _, source := range []string{"certification", "education", "unsupported summary"} {
		t.Run(source, func(t *testing.T) {
			profile, _, tailored := textLayerFixture()
			base := ""
			job := models.Job{JobInput: models.JobInput{Title: "Kubernetes Engineer"}}
			want := "covered"
			switch source {
			case "certification":
				profile.Certifications = []string{"Certified Kubernetes Administrator"}
				tailored.Certifications = profile.Certifications
			case "education":
				base = "EDUCATION\nKubernetes Engineering, Example University\n"
				tailored.Education = []string{"Kubernetes Engineering, Example University"}
			case "unsupported summary":
				tailored.Summary = "I build Kubernetes services."
				want = "unsupported_present"
			}
			data, err := RenderPDF(tailored)
			if err != nil {
				t.Fatal(err)
			}
			_, review := ReviewPDF(data, tailored, profile, base, job)
			if len(review.Blockers) != 0 || len(review.Keywords) != 1 || review.Keywords[0] != (KeywordFinding{Technology: "kubernetes", Status: want, Present: true}) {
				t.Fatalf("fact source %s: %+v", source, review)
			}
			warnings := strings.Join(review.Warnings(), " ")
			if want == "unsupported_present" && (!strings.Contains(warnings, "appears in the PDF but no stored fact supports it; review the summary") || strings.Contains(warnings, "must not be added")) {
				t.Fatalf("misleading warning: %s", warnings)
			}
		})
	}
}

func TestTextLayerWarningsExcludeCoveredKeywords(t *testing.T) {
	review := TextLayerReview{Keywords: []KeywordFinding{{Technology: "go", Status: "covered", Present: true}}}
	if warnings := review.Warnings(); len(warnings) != 0 {
		t.Fatalf("good news in warnings: %v", warnings)
	}
}

func TestPDFTextLayerMultiPage(t *testing.T) {
	profile, job, tailored := textLayerFixture()
	tailored.Experiences = nil
	for i := 0; i < 14; i++ {
		tailored.Experiences = append(tailored.Experiences, TailoredRole{
			Title: "Engineer", Company: fmt.Sprintf("Example %d", i), Period: fmt.Sprintf("Jan %d - Dec %d", 2022-i, 2022-i),
			Bullets: []string{"Built Go services.", "Shipped reliable systems.", "Improved service quality."},
		})
	}
	data, err := RenderPDF(tailored)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if reader.NumPage() < 2 {
		t.Fatal("fixture must span multiple pages")
	}
	text, review := ReviewPDF(data, tailored, profile, "", job)
	if len(review.Blockers) != 0 {
		t.Fatalf("multi-page render blocked: %v\n%s", review.Blockers, text)
	}
}

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
