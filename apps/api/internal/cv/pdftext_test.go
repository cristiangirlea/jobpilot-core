package cv

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/ledongthuc/pdf"
)

// The fixtures below are small PDFs written by hand, each copying the operators
// a real generator emits (checked against an invented document exported by
// Microsoft Word and by ReportLab). People and companies are invented.

// fixtureWidth is the advance of a glyph in FixtureSans, in thousandths of an
// em. Any table works, as long as the content streams are laid out with it.
func fixtureWidth(r rune) int {
	switch {
	case r == ' ':
		return 250
	case r == '-' || r == '.' || r == ',' || r == '|' || r == ':':
		return 300
	case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '@':
		return 650
	}
	return 500
}

func measure(s string, size float64) float64 {
	total := 0
	for _, r := range s {
		total += fixtureWidth(r)
	}
	return float64(total) / 1000 * size
}

// fixtureFonts: F1 is Helvetica, one of the standard fonts a PDF names without
// widths; F2 is a TrueType font with a /Widths array, as Word and ReportLab
// embed theirs; F3 is a composite font with two-byte codes and a /W array, as
// Word uses for characters outside WinAnsi; F4 is a Type3 font.
func fixtureFonts() string {
	var widths strings.Builder
	for r := rune(32); r <= 126; r++ {
		fmt.Fprintf(&widths, "%d ", fixtureWidth(r))
	}
	return "/F1 << /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >> " +
		"/F2 << /Type /Font /Subtype /TrueType /BaseFont /FixtureSans /Encoding /WinAnsiEncoding /FirstChar 32 /LastChar 126 /Widths [" + widths.String() + "] >> " +
		"/F3 << /Type /Font /Subtype /Type0 /BaseFont /FixtureCID /Encoding /Identity-H /DescendantFonts [<< /Type /Font /Subtype /CIDFontType2 /BaseFont /FixtureCID /DW 300 /W [1 [400 600] 10 20 700 30 25 900 40] >>] >> " +
		"/F4 << /Type /Font /Subtype /Type3 /FontMatrix [0.001 0 0 0.001 0 0] /FirstChar 97 /LastChar 98 /Widths [500 500] >>"
}

// buildPDF writes a one-page PDF around a content stream, with a correct
// cross-reference table so the reader takes the normal path.
func buildPDF(content string) []byte {
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << " + fixtureFonts() + " >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content)+1, content),
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}

func pdfString(s string) string {
	return "(" + strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(s) + ")"
}

// wordLine lays out one line the way Word's PDF export does: every run is its
// own text object at an absolute position, and Word makes every hyphen a run
// of its own.
func wordLine(b *strings.Builder, x, y float64, runs ...string) {
	for _, run := range runs {
		fmt.Fprintf(b, "q BT /F2 11 Tf 1 0 0 1 %.3f %.3f Tm [%s] TJ ET Q\n", x, y, pdfString(run))
		x += measure(run, 11)
	}
}

func extractFixture(t *testing.T, content string) string {
	t.Helper()
	text, format, err := ExtractText("fixture.pdf", buildPDF(content))
	if err != nil || format != FormatPDF {
		t.Fatalf("ExtractText = %q, %v", format, err)
	}
	return text
}

// Trap 1 (#144): Word draws "app", "-" and "store" as three text objects. The
// hyphen touches the glyph before it, so no space may be read in front of it.
func TestExtractPDFKeepsWordHyphensAttached(t *testing.T) {
	var b strings.Builder
	wordLine(&b, 72, 700, "Own billing, ledger, app", "-", "store and refund processing pipelines end", "-", "to", "-", "end.")
	wordLine(&b, 72, 686, "Skills: Vault", "-", "managed secrets, Go")
	// A dash between spaces is punctuation and keeps them.
	wordLine(&b, 72, 672, "Platform Engineer ", "-", " Contractor")
	text := extractFixture(t, b.String())
	for _, want := range []string{"app-store", "end-to-end.", "Vault-managed secrets", "Platform Engineer - Contractor"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

// reportLabParagraph copies ReportLab's paragraph engine (platypus): the
// paragraph is placed with cm, its first line with Tm, and every further line
// with T* after setting the leading with TL.
func reportLabParagraph(b *strings.Builder, x, y, size float64, lines ...string) {
	fmt.Fprintf(b, "q 1 0 0 1 %.4f %.4f cm q BT 1 0 0 1 0 %.0f Tm %.0f TL /F2 %.0f Tf", x, y, size*float64(len(lines)), size*1.3, size)
	for _, line := range lines {
		fmt.Fprintf(b, " %s Tj T*", pdfString(line))
	}
	b.WriteString(" ET Q Q\n")
}

// Trap 2 (#144): every line of a ReportLab paragraph must stay its own line,
// and a list bullet drawn in Helvetica as WinAnsi code 127 is a bullet.
func TestExtractPDFReadsReportLabParagraphs(t *testing.T) {
	var b strings.Builder
	reportLabParagraph(&b, 72, 760, 22, "JANE EXAMPLE", "Platform Engineer")
	reportLabParagraph(&b, 72, 720, 10, "Lisbon, Portugal | +351 912 000 111 | jane@example.com", "example.dev | github.com/janeexample")
	reportLabParagraph(&b, 72, 690, 14, "EXPERIENCE")
	reportLabParagraph(&b, 72, 670, 10, "Globex | Platform Engineer | Jan 2019 - Present")
	// platypus draws bulletText in its own text object, in Helvetica.
	b.WriteString("q 1 0 0 1 72 640 cm q BT 1 0 0 1 2 16 Tm /F1 10 Tf 12 TL (\\177) Tj ET Q Q\n")
	reportLabParagraph(&b, 86, 640, 10, "Built the ingestion pipeline and its autoscaling across three regions,", "with staged rollouts and an audit trail for every change.")
	reportLabParagraph(&b, 72, 600, 14, "SKILLS")
	reportLabParagraph(&b, 72, 580, 10, "Languages: Go, Python, TypeScript")
	text := extractFixture(t, b.String())

	for _, want := range []string{"JANE EXAMPLE\nPlatform Engineer\n", "jane@example.com\nexample.dev", "- Built the ingestion pipeline"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	p := Parse(text)
	if p.Name != "Jane Example" || p.CurrentTitle != "Platform Engineer" || p.Email != "jane@example.com" || p.Location != "Lisbon, Portugal" {
		t.Errorf("identity = %q, %q, %q, %q", p.Name, p.CurrentTitle, p.Email, p.Location)
	}
	if len(p.Experiences) != 1 || p.Experiences[0].Company != "Globex" || !strings.Contains(p.Experiences[0].Description, "audit trail") {
		t.Errorf("roles = %+v", p.Experiences)
	}
	if len(p.Skills) != 3 {
		t.Errorf("skills = %+v, want Go, Python and TypeScript", p.Skills)
	}
}

// Trap 3 (#144): role headers at the margin with their bullets indented a
// little is one column. Without widths, only where each line starts is known,
// and the band between the two left edges looked like a gutter.
func TestExtractPDFKeepsIndentedBulletsWithTheirRole(t *testing.T) {
	for _, font := range []string{"F2", "F1"} { // with widths, and Helvetica without
		t.Run(font, func(t *testing.T) {
			var b strings.Builder
			line := func(x, y float64, s string) {
				fmt.Fprintf(&b, "BT /%s 10 Tf 1 0 0 1 %.1f %.1f Tm %s Tj ET\n", font, x, y, pdfString(s))
			}
			y := 800.0
			line(50, y, "EXPERIENCE")
			companies := []string{"Globex", "Initech", "Umbrella", "Hooli", "Vandelay", "Stark", "Wayne", "Acme", "Tyrell", "Cyberdyne"}
			for i, company := range companies {
				y -= 18
				line(50, y, fmt.Sprintf("%s | Backend Engineer | %d - %d", company, 2000+2*i, 2001+2*i))
				for j := 1; j <= 3; j++ {
					y -= 13
					line(64, y, fmt.Sprintf("- Shipped part %d of the %s billing platform and its reconciliation jobs.", j, company))
				}
			}
			p := Parse(extractFixture(t, b.String()))
			if len(p.Experiences) != len(companies) {
				t.Fatalf("got %d roles, want %d: %+v", len(p.Experiences), len(companies), p.Experiences)
			}
			for i, e := range p.Experiences {
				if e.Company != companies[i] || strings.Count(e.Description, "- Shipped part") != 3 || !strings.Contains(e.Description, companies[i]+" billing") {
					t.Errorf("role %d = %q with %q, want %s and its three bullets", i, e.Company, e.Description, companies[i])
				}
			}
		})
	}
}

// A two-column page whose name and contact line run across the gutter is still
// two columns, and a sparse page (a CV's second page) is split too: otherwise
// the sidebar is read into the middle of the roles.
func TestExtractPDFSplitsColumnsUnderAFullWidthHeader(t *testing.T) {
	var b strings.Builder
	line := func(x, y, size float64, s string) {
		fmt.Fprintf(&b, "BT /F2 %.0f Tf 1 0 0 1 %.1f %.1f Tm %s Tj ET\n", size, x, y, pdfString(s))
	}
	line(50, 800, 24, "JANE EXAMPLE")
	line(50, 770, 10, "Lisbon, Portugal | +351 912 000 111 | jane@example.com | github.com/janeexample")
	sidebar := []string{"SKILLS", "Cloud: AWS, GCP", "Data: Postgres", "Queues: Kafka", "Tools: Terraform", "Go, Python"}
	main := []string{"EXPERIENCE"}
	for _, company := range []string{"Globex", "Initech", "Umbrella", "Hooli"} {
		main = append(main, company+" | Backend Engineer | 2020 - 2019",
			"- Rebuilt the "+company+" billing service and its jobs.", "- Moved the "+company+" queues onto a managed cluster.")
	}
	for i, s := range main {
		y := 740 - 14*float64(i)
		line(230, y, 10, s)
		if i < len(sidebar) {
			line(50, y, 10, sidebar[i])
		}
	}
	p := Parse(extractFixture(t, b.String()))
	if len(p.Experiences) != 4 {
		t.Fatalf("roles = %+v, want four", p.Experiences)
	}
	for _, e := range p.Experiences {
		if strings.Count(e.Description, "- ") != 2 || strings.ContainsAny(e.Description, ":,") {
			t.Errorf("%s has %q, want its two bullets and nothing from the sidebar", e.Company, e.Description)
		}
	}
	if p.Name != "Jane Example" || p.Location != "Lisbon, Portugal" || len(p.Skills) < 6 {
		t.Errorf("name %q, location %q, skills %+v", p.Name, p.Location, p.Skills)
	}
}

// Writers that place each word on its own leave no space glyph; where widths
// are known, the blank between words is a word break and a narrower one, as
// kerning leaves, is not.
func TestExtractPDFBreaksWordsPlacedApart(t *testing.T) {
	var b strings.Builder
	x := 72.0
	for _, word := range []string{"Built", "the", "ingestion", "pipeline"} {
		fmt.Fprintf(&b, "BT /F2 11 Tf 1 0 0 1 %.3f 700 Tm %s Tj ET\n", x, pdfString(word))
		x += measure(word, 11) + 0.25*11
	}
	fmt.Fprintf(&b, "BT /F2 11 Tf 1 0 0 1 72 680 Tm (Glo) Tj ET BT /F2 11 Tf 1 0 0 1 %.3f 680 Tm (bex) Tj ET\n", 72+measure("Glo", 11)+0.12*11)
	// A heading tracked a glyph at a time, as designer templates do, is one word.
	b.WriteString("BT /F2 12 Tf 1 0 0 1 72 650 Tm [(S) -200 (K) -200 (I) -200 (L) -200 (L) -200 (S)] TJ ET\n")
	b.WriteString("BT /F2 11 Tf 1 0 0 1 72 635 Tm (Languages: Go, Python) Tj ET\n")
	text := extractFixture(t, b.String())
	if !strings.Contains(text, "Built the ingestion pipeline\nGlobex\nSKILLS\n") {
		t.Errorf("text = %q", text)
	}
	if p := Parse(text); len(p.Skills) != 2 {
		t.Errorf("skills = %+v, want Go and Python under the tracked heading", p.Skills)
	}
}

func walkFixture(t *testing.T, content string) []chunk {
	t.Helper()
	data := buildPDF(content)
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	runs, ok := walkText(reader.Page(1))
	if !ok {
		t.Fatal("the content stream could not be walked")
	}
	return runs
}

// The walker keeps the text state the way a renderer does.
func TestWalkTextFollowsTheTextState(t *testing.T) {
	cases := []struct {
		name, content string
		want          []chunk // compared on text, position, width and size
	}{
		{"q and Q restore the font and spacing", "BT /F2 10 Tf 2 Tc ET q BT /F2 20 Tf 0 Tc ET Q BT 1 0 0 1 0 0 Tm (ab) Tj ET",
			[]chunk{{s: "ab", w: 14, size: 10}}},
		{"TD sets the leading T* uses", "BT /F2 10 Tf 1 0 0 1 0 700 Tm 0 -12 TD (a) Tj T* (b) Tj ET",
			[]chunk{{s: "a", y: 688, w: 5, size: 10}, {s: "b", y: 676, w: 5, size: 10}}},
		{"quote operators move to the next line", "BT /F2 10 Tf 12 TL 1 0 0 1 0 700 Tm (a) Tj (b) ' 3 1 (a b) \" ET",
			[]chunk{{s: "a", y: 700, w: 5, size: 10}, {s: "b", y: 688, w: 5, size: 10}, {s: "a b", y: 676, w: 18.5, size: 10}}},
		{"Tz scales the width", "BT /F2 10 Tf 50 Tz (ab) Tj ET", []chunk{{s: "ab", w: 5, size: 10}}},
		{"Ts raises the text", "BT /F2 10 Tf 3 Ts 1 0 0 1 0 700 Tm (a) Tj ET", []chunk{{s: "a", y: 703, w: 5, size: 10}}},
		{"cm scales position, width and size", "2 0 0 2 0 0 cm BT /F2 10 Tf 1 0 0 1 5 5 Tm (a) Tj ET", []chunk{{s: "a", x: 10, y: 10, w: 10, size: 20}}},
		{"TJ offsets move the next string", "BT /F2 10 Tf [(a) -1000 (b)] TJ ET",
			[]chunk{{s: "a", w: 5, size: 10}, {s: "b", x: 15, w: 5, size: 10}}},
		{"a y-flipped page keeps page coordinates", "1 0 0 -1 0 842 cm BT /F2 10 Tf 1 0 0 -1 72 100 Tm (a) Tj 1 0 0 -1 72 200 Tm (b) Tj ET",
			[]chunk{{s: "a", x: 72, y: 742, w: 5, size: 10}, {s: "b", x: 72, y: 642, w: 5, size: 10}}},
		{"composite fonts read two-byte codes and their W array", "BT /F3 10 Tf <0001000200630010> Tj ET",
			[]chunk{{w: 20, size: 10}}}, // 400 + 600 + DW 300 + 700
		{"Type3 widths stay unknown", "BT /F4 10 Tf (ab) Tj ET", []chunk{{s: "ab", size: 10}}},
		{"an unused WinAnsi code is a bullet", "BT /F1 10 Tf (\\177) Tj ET", []chunk{{s: "•", size: 10}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := walkFixture(t, c.content)
			if len(got) != len(c.want) {
				t.Fatalf("runs = %+v, want %+v", got, c.want)
			}
			for i, w := range c.want {
				g := got[i]
				if (w.s != "" && g.s != w.s) || absolute(g.x-w.x) > 0.01 || absolute(g.y-w.y) > 0.01 || absolute(g.w-w.w) > 0.01 || absolute(g.size-w.size) > 0.01 {
					t.Errorf("run %d = %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

// Malformed and hostile content costs at most the text it garbles.
func TestExtractPDFSurvivesHostileContent(t *testing.T) {
	huge := "1000000000000000000000000000000.0 0 0 1000000000000000000000000000000.0 0 0 cm "
	content := "Q Q Tj Tf TJ T* BT /F9 10 Tf ET\n" +
		strings.Repeat("q 2 0 0 2 0 0 cm ", 300) + strings.Repeat("Q ", 300) + "\n" +
		"q " + strings.Repeat(huge, 11) + "BT /F2 10 Tf (lost) Tj ET Q\n" +
		"BT /F2 10 Tf 1 0 0 1 72 700 Tm (kept) Tj ET\n"
	runs := walkFixture(t, content)
	if last := runs[len(runs)-1]; last.s != "kept" || last.x != 72 || last.size != 10 {
		t.Errorf("after 300 balanced saves the state is not restored: %+v", last)
	}
	if text := extractFixture(t, content); text != "kept" {
		t.Errorf("text = %q, want only the placeable run", text)
	}
}
