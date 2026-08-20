package cv

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

const sampleCV = `OCTAVIAN EXAMPLE
Senior Backend Engineer - Contractor

Contact
+40 700 000 000
someone@example.com
Bucharest, Romania
in/ someone-example
Github.com/ someoneexample

ABOUT ME
I am a Senior Backend Engineer with 12+ years of experience building distributed
systems in fully remote environments.

Languages
Golang 5-6 years
Python 7 years
SQL 12 years

WORK EXPERIENCE
Senior Platform Engineer Dec 2022 - Present
Distributed Indexing Systems | Brightloom
- Developed and maintained distributed ingestion pipelines for large-scale work.
Skills: Node.js, Golang, TypeScript

Senior Engineer Jan 2022 - Jul 2022
Stock Planning Platform | Retail Company | NORDVIK DATA
- Modernized backend systems for inventory forecasts and warehouse simulations.

EDUCATION
University of Lowmoor
M.A. Computer engineering
2009 - 2012
`

func TestExtractTextReadsPlainText(t *testing.T) {
	text, format, err := ExtractText("cv.txt", []byte(sampleCV))
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatText {
		t.Fatalf("format = %q, want %q", format, FormatText)
	}
	if !strings.Contains(text, "Senior Platform Engineer") {
		t.Fatal("extraction lost content")
	}
}

func TestExtractTextReadsDOCX(t *testing.T) {
	document := `<?xml version="1.0"?><w:document xmlns:w="x"><w:body>` +
		`<w:p><w:r><w:t>Jane Example</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>Senior Backend</w:t></w:r><w:tab/><w:r><w:t>Engineer</w:t></w:r></w:p>` +
		`</w:body></w:document>`
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	writer, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte(document)); err != nil {
		t.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}

	text, format, err := ExtractText("cv.docx", buffer.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatDOCX {
		t.Fatalf("format = %q, want %q", format, FormatDOCX)
	}
	if !strings.Contains(text, "Jane Example") || !strings.Contains(text, "Senior Backend Engineer") {
		t.Fatalf("unexpected DOCX text: %q", text)
	}
}

func TestDetectFormatPrefersContentOverExtension(t *testing.T) {
	// A PDF saved with the wrong extension must still be read as a PDF.
	if got := DetectFormat("cv.docx", []byte("%PDF-1.7\n...")); got != FormatPDF {
		t.Fatalf("DetectFormat = %q, want %q", got, FormatPDF)
	}
	if got := DetectFormat("cv.bin", []byte{0x00, 0x01, 0x02, 0xff}); got != "" {
		t.Fatalf("DetectFormat = %q, want an unsupported result", got)
	}
}

func TestExtractTextRejectsEmptyAndUnknown(t *testing.T) {
	if _, _, err := ExtractText("cv.pdf", nil); err != ErrEmptyFile {
		t.Fatalf("err = %v, want ErrEmptyFile", err)
	}
	if _, _, err := ExtractText("cv.bin", []byte{0x00, 0x01, 0x02, 0xff}); err != ErrUnknownFormat {
		t.Fatalf("err = %v, want ErrUnknownFormat", err)
	}
}

func TestParseReadsContactDetails(t *testing.T) {
	parsed := Parse(sampleCV)
	cases := map[string]string{
		"name":     "Octavian Example",
		"email":    "someone@example.com",
		"phone":    "+40 700 000 000",
		"location": "Bucharest, Romania",
		"linkedin": "https://www.linkedin.com/in/someone-example",
		"github":   "https://github.com/someoneexample",
		"title":    "Senior Backend Engineer - Contractor",
	}
	got := map[string]string{
		"name": parsed.Name, "email": parsed.Email, "phone": parsed.Phone,
		"location": parsed.Location, "linkedin": parsed.LinkedIn,
		"github": parsed.GitHub, "title": parsed.CurrentTitle,
	}
	for field, want := range cases {
		if got[field] != want {
			t.Errorf("%s = %q, want %q", field, got[field], want)
		}
	}
	if parsed.YearsExperience != 12 {
		t.Errorf("years = %v, want 12", parsed.YearsExperience)
	}
}

func TestParseReadsSkillsWithDurations(t *testing.T) {
	parsed := Parse(sampleCV)
	years := map[string]float64{}
	for _, skill := range parsed.Skills {
		years[skill.Name] = skill.Years
	}
	// "Golang 5-6 years" claims up to six.
	if years["Golang"] != 6 {
		t.Errorf("Golang years = %v, want 6", years["Golang"])
	}
	if years["SQL"] != 12 {
		t.Errorf("SQL years = %v, want 12", years["SQL"])
	}
	// Skills listed on a "Skills:" line have no duration but must still appear.
	if _, ok := years["TypeScript"]; !ok {
		t.Errorf("expected TypeScript from the Skills line, got %v", parsed.Skills)
	}
}

func TestParseReadsRolesAndEmployers(t *testing.T) {
	parsed := Parse(sampleCV)
	if len(parsed.Experiences) != 2 {
		t.Fatalf("expected 2 roles, got %d: %+v", len(parsed.Experiences), parsed.Experiences)
	}
	first := parsed.Experiences[0]
	if first.Title != "Senior Platform Engineer" || first.Company != "Brightloom" {
		t.Errorf("first role = %q at %q", first.Title, first.Company)
	}
	if !first.Current || first.EndDate != "" {
		t.Errorf("a role ending in Present must be current, got %+v", first)
	}
	// "Project | Client | Employer" names the employer last, and the employer is
	// what gets excluded from future matches.
	if parsed.Experiences[1].Company != "NORDVIK DATA" {
		t.Errorf("second employer = %q, want NORDVIK DATA", parsed.Experiences[1].Company)
	}
}

func TestParseDoesNotTreatEducationDatesAsRoles(t *testing.T) {
	parsed := Parse(sampleCV)
	for _, experience := range parsed.Experiences {
		if strings.Contains(strings.ToLower(experience.Title), "university") {
			t.Fatalf("an education entry became a role: %+v", experience)
		}
	}
	if len(parsed.Education) == 0 || !strings.Contains(parsed.Education[0], "Lowmoor") {
		t.Fatalf("education = %v", parsed.Education)
	}
}

func TestParseWarnsAboutMissingFacts(t *testing.T) {
	parsed := Parse("Just some prose with no structure at all.")
	if len(parsed.Warnings) == 0 {
		t.Fatal("a CV this poor must produce warnings rather than a confident empty profile")
	}
	joined := strings.Join(parsed.Warnings, " ")
	for _, want := range []string{"name", "email", "experience", "skills"} {
		if !strings.Contains(strings.ToLower(joined), want) {
			t.Errorf("warnings should mention %q: %v", want, parsed.Warnings)
		}
	}
}

func TestParseInventsNothing(t *testing.T) {
	parsed := Parse("OCTAVIAN EXAMPLE\nSenior Backend Engineer\n")
	if parsed.Email != "" || parsed.Phone != "" || len(parsed.Experiences) != 0 || parsed.YearsExperience != 0 {
		t.Fatalf("absent facts must stay absent, got %+v", parsed)
	}
}

func TestCleanNormalisesLigaturesAndBlankRuns(t *testing.T) {
	got := Clean("o\ufb03ce\r\n\n\n\nwork\u200b\ufeff")
	if !strings.Contains(got, "office") {
		t.Errorf("ligature not expanded: %q", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("blank runs not collapsed: %q", got)
	}
	if strings.ContainsRune(got, '\u200b') || strings.ContainsRune(got, '\ufeff') {
		t.Errorf("zero-width characters survived: %q", got)
	}
}

func TestTidySpacingRepairsSplitsWithoutBreakingWords(t *testing.T) {
	cases := map[string]string{
		"Dec 202 2 - Present": "Dec 2022 - Present",
		"workflows .":         "workflows.",
		"cloud - native":      "cloud-native",
		"Node .js":            "Node.js",
		"S enior Platform":    "Senior Platform",
		// Single-letter English words must survive.
		"I focus on building": "I focus on building",
		"A quick summary":     "A quick summary",
	}
	for input, want := range cases {
		if got := tidySpacing(input); got != want {
			t.Errorf("tidySpacing(%q) = %q, want %q", input, got, want)
		}
	}
}
