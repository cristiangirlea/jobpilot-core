package cv

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"

	"github.com/signintech/gopdf"
)

// Tailored CVs render as a single column.
//
// This is a deliberate choice, not a shortcut. A two-column CV interleaves its
// sidebar with the main text when a machine reads it - the same failure this
// project's own parser had to be taught around - and the machine reading a
// submitted CV is usually the employer's ATS. A single column parses correctly
// everywhere, which matters more on a CV that is sent automatically than any
// visual flourish would.
//
// The font is embedded so output never depends on what is installed on the
// host, and so that text outside Latin-1 survives. That last point is not
// theoretical: the first renderer written here emitted an identity ToUnicode
// map, which silently turned "București" into "Bucure\x19ti" for every reader
// including an employer's ATS. Any replacement for this renderer must be
// checked by reading its own output back, which TestRenderedPDFKeepsDiacritics
// does.

//go:embed fonts/DejaVuSans.ttf
var fontRegular []byte

//go:embed fonts/DejaVuSans-Bold.ttf
var fontBold []byte

const (
	fontRegularName = "cvsans"
	fontBoldName    = "cvsans-bold"

	pageMargin  = 46.0 // points
	bodySize    = 9.5
	headingSize = 10.0
	nameSize    = 19.0
	lineGap     = 1.35 // multiplier of font size
)

// RenderPDF produces the CV file that gets attached to an application.
func RenderPDF(tailored Tailored) ([]byte, error) {
	doc := &gopdf.GoPdf{}
	doc.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := doc.AddTTFFontData(fontRegularName, fontRegular); err != nil {
		return nil, fmt.Errorf("could not load the CV font: %w", err)
	}
	if err := doc.AddTTFFontData(fontBoldName, fontBold); err != nil {
		return nil, fmt.Errorf("could not load the CV font: %w", err)
	}
	doc.AddPage()

	p := &painter{
		doc:    doc,
		left:   pageMargin,
		top:    pageMargin,
		bottom: gopdf.PageSizeA4.H - pageMargin,
		width:  gopdf.PageSizeA4.W - 2*pageMargin,
	}
	doc.SetXY(p.left, p.top)

	name := fallbackText(tailored.Name, "Candidate")
	if err := p.use(fontBoldName, nameSize, rgb{0, 0, 0}); err != nil {
		return nil, err
	}
	size := nameSize
	if width, err := doc.MeasureTextWidth(name); err == nil && width > p.width {
		// A contact name must remain intact on one line.
		size *= (p.width - 1) / width
	}
	if err := p.block(fontBoldName, size, rgb{0, 0, 0}, name); err != nil {
		return nil, err
	}
	if headline := strings.TrimSpace(tailored.Headline); headline != "" {
		if err := p.block(fontRegularName, 11, rgb{70, 70, 70}, headline); err != nil {
			return nil, err
		}
	}
	if len(tailored.Contact) > 0 {
		if err := p.contact(tailored.Contact); err != nil {
			return nil, err
		}
	}

	if summary := strings.TrimSpace(tailored.Summary); summary != "" {
		if err := p.section("SUMMARY"); err != nil {
			return nil, err
		}
		if err := p.block(fontRegularName, bodySize, rgb{0, 0, 0}, summary); err != nil {
			return nil, err
		}
	}

	if len(tailored.Skills) > 0 {
		if err := p.section("SKILLS"); err != nil {
			return nil, err
		}
		if err := p.block(fontRegularName, bodySize, rgb{0, 0, 0}, strings.Join(tailored.Skills, "   ·   ")); err != nil {
			return nil, err
		}
	}

	if len(tailored.Experiences) > 0 {
		if err := p.section("EXPERIENCE"); err != nil {
			return nil, err
		}
		for _, role := range tailored.Experiences {
			if err := p.role(role); err != nil {
				return nil, err
			}
		}
	}

	for _, list := range []struct {
		title string
		lines []string
	}{{"EDUCATION", tailored.Education}, {"CERTIFICATIONS", tailored.Certifications}, {"LANGUAGES", tailored.Languages}} {
		if len(list.lines) == 0 {
			continue
		}
		if err := p.section(list.title); err != nil {
			return nil, err
		}
		for _, line := range list.lines {
			if err := p.block(fontRegularName, bodySize, rgb{0, 0, 0}, line); err != nil {
				return nil, err
			}
		}
	}

	var buffer bytes.Buffer
	if _, err := doc.WriteTo(&buffer); err != nil {
		return nil, fmt.Errorf("could not render the CV: %w", err)
	}
	return buffer.Bytes(), nil
}

type rgb struct{ r, g, b uint8 }

// painter carries the page geometry so every block can break the page itself.
type painter struct {
	doc                      *gopdf.GoPdf
	left, top, bottom, width float64
}

// Wrap between contact entries so spaces inside a phone do not split it.
func (p *painter) contact(entries []string) error {
	const size = 8.5
	color := rgb{70, 70, 70}
	if err := p.use(fontRegularName, size, color); err != nil {
		return err
	}
	line := ""
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		candidate := entry
		if line != "" {
			candidate = line + "   ·   " + entry
		}
		width, err := p.doc.MeasureTextWidth(candidate)
		if err != nil {
			return err
		}
		if width > p.width && line != "" {
			if err := p.block(fontRegularName, size, color, line); err != nil {
				return err
			}
			line = entry
		} else {
			line = candidate
		}
	}
	if line == "" {
		return nil
	}
	return p.block(fontRegularName, size, color, line)
}

// ensure starts a new page when the next block would not fit, so a role never
// ends up split from its first line.
func (p *painter) ensure(height float64) {
	if p.doc.GetY()+height > p.bottom {
		p.doc.AddPage()
		p.doc.SetXY(p.left, p.top)
	}
}

func (p *painter) use(font string, size float64, color rgb) error {
	if err := p.doc.SetFont(font, "", size); err != nil {
		return err
	}
	p.doc.SetTextColor(color.r, color.g, color.b)
	return nil
}

// block writes wrapped text at the current position and advances past it.
func (p *painter) block(font string, size float64, color rgb, text string) error {
	if err := p.use(font, size, color); err != nil {
		return err
	}
	height := size * lineGap
	lines, err := p.doc.SplitTextWithWordWrap(text, p.width)
	if err != nil {
		// A word longer than the line cannot be wrapped; write it as-is rather
		// than dropping content from the candidate's CV.
		lines = []string{text}
	}
	for _, line := range lines {
		p.ensure(height)
		p.doc.SetX(p.left)
		if err = p.doc.Cell(nil, line); err != nil {
			return err
		}
		p.doc.SetY(p.doc.GetY() + height)
	}
	return nil
}

func (p *painter) section(title string) error {
	p.doc.SetY(p.doc.GetY() + 7)
	p.ensure(26)
	if err := p.use(fontBoldName, headingSize, rgb{0, 0, 0}); err != nil {
		return err
	}
	p.doc.SetX(p.left)
	if err := p.doc.Cell(nil, title); err != nil {
		return err
	}
	y := p.doc.GetY() + headingSize*lineGap
	p.doc.SetLineWidth(0.6)
	p.doc.SetStrokeColor(185, 185, 185)
	p.doc.Line(p.left, y, p.left+p.width, y)
	p.doc.SetY(y + 5)
	return nil
}

// role keeps the full heading before its dates for machine readers.
func (p *painter) role(role TailoredRole) error {
	heading := strings.TrimSpace(role.Title)
	if role.Company != "" {
		heading = strings.TrimSpace(heading + " — " + role.Company)
	}
	period := strings.TrimSpace(role.Period)

	p.ensure(bodySize * lineGap * 3)
	if err := p.use(fontRegularName, bodySize-0.5, rgb{95, 95, 95}); err != nil {
		return err
	}
	periodWidth := 0.0
	if period != "" {
		width, err := p.doc.MeasureTextWidth(period)
		if err == nil {
			periodWidth = width + 6
		}
	}

	y := p.doc.GetY()
	if err := p.use(fontBoldName, bodySize+0.5, rgb{0, 0, 0}); err != nil {
		return err
	}
	headingWidth, err := p.doc.MeasureTextWidth(heading)
	if err != nil {
		return err
	}
	if headingWidth > p.width-periodWidth {
		if err := p.block(fontBoldName, bodySize+0.5, rgb{0, 0, 0}, heading); err != nil {
			return err
		}
		if period != "" {
			if err := p.block(fontRegularName, bodySize-0.5, rgb{95, 95, 95}, period); err != nil {
				return err
			}
		}
	} else {
		p.doc.SetX(p.left)
		if err := p.doc.Cell(nil, heading); err != nil {
			return err
		}
		if period != "" {
			if err := p.use(fontRegularName, bodySize-0.5, rgb{95, 95, 95}); err != nil {
				return err
			}
			p.doc.SetXY(p.left+p.width-periodWidth+6, y)
			if err := p.doc.Cell(nil, period); err != nil {
				return err
			}
		}
		p.doc.SetY(y + (bodySize+0.5)*lineGap)
	}

	if context := strings.TrimSpace(role.Context); context != "" {
		if err := p.block(fontRegularName, bodySize-0.5, rgb{70, 70, 70}, context); err != nil {
			return err
		}
	}
	for _, bullet := range role.Bullets {
		if err := p.bullet(bullet); err != nil {
			return err
		}
	}
	p.doc.SetY(p.doc.GetY() + 4)
	return nil
}

func (p *painter) bullet(text string) error {
	if err := p.use(fontRegularName, bodySize, rgb{0, 0, 0}); err != nil {
		return err
	}
	const indent = 11.0
	height := bodySize * lineGap
	lines, err := p.doc.SplitTextWithWordWrap("• "+text, p.width-indent)
	if err != nil {
		lines = []string{"• " + text}
	}
	for index, line := range lines {
		p.ensure(height)
		x := p.left + indent
		if index > 0 {
			// Continuation lines align under the text, not the marker.
			x += 8
		}
		p.doc.SetX(x)
		if err = p.doc.Cell(nil, line); err != nil {
			return err
		}
		p.doc.SetY(p.doc.GetY() + height)
	}
	return nil
}
