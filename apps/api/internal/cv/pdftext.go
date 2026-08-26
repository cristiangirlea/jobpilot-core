package cv

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
)

// A PDF stores glyphs at coordinates, not words in reading order. Two things
// therefore have to be reconstructed before a CV is readable:
//
//   - Word breaks. Most CV templates emit no space glyphs at all, so spacing has
//     to be inferred from the horizontal gap between adjacent glyphs.
//   - Reading order. Glyphs arrive in drawing order, which is not left-to-right,
//     and a two-column CV interleaves its sidebar with the main column unless the
//     columns are separated first.
//
// Getting this wrong is not cosmetic: a mangled parse feeds wrong facts into the
// profile, and every downstream answer and CV inherits them.

// spaceRatio is the fraction of the font size a horizontal gap must exceed to
// count as a word break. Roughly a third of an em sits comfortably between
// normal inter-glyph kerning and a real space in the templates seen so far.
const spaceRatio = 0.33

// lineRatio is how far apart two glyph baselines may be and still be one line.
const lineRatio = 0.55

// gutterRatio is the minimum width of a blank vertical band, as a fraction of
// the text width, before it is treated as a column separator rather than
// ordinary spacing.
const gutterRatio = 0.035

type chunk struct {
	x, y, w, size float64
	s             string
}

func extractPDF(data []byte) (string, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("this PDF could not be read: %w", err)
	}
	var out strings.Builder
	for i := 1; i <= reader.NumPage(); i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		chunks := collectChunks(page)
		if len(chunks) == 0 {
			continue
		}
		// Some writers emit text with no usable coordinates at all. Their runs
		// still arrive one logical line at a time, so those boundaries are the
		// only structure available, and honouring them beats fusing the whole
		// page into a single line.
		if !hasPageGeometry(chunks) {
			for _, c := range chunks {
				if line := strings.TrimSpace(c.s); line != "" {
					out.WriteString(line)
					out.WriteByte('\n')
				}
			}
			out.WriteByte('\n')
			continue
		}
		for _, column := range splitColumns(chunks) {
			out.WriteString(renderColumn(column))
			out.WriteByte('\n')
		}
		out.WriteByte('\n')
	}
	return tidySpacing(out.String()), nil
}

// hasPageGeometry reports whether a page carries any positional information.
// A page whose chunks all sit at the origin has none.
func hasPageGeometry(chunks []chunk) bool {
	for _, c := range chunks {
		if c.x != 0 || c.y != 0 {
			return true
		}
	}
	return false
}

// collectChunks flattens a page into positioned glyph runs.
func collectChunks(page pdf.Page) []chunk {
	rows, err := page.GetTextByRow()
	if err != nil {
		return nil
	}
	var out []chunk
	for _, row := range rows {
		for _, text := range row.Content {
			if text.S == "" {
				continue
			}
			// Spaces the document already encodes are the most reliable word
			// breaks available, but they are frequently emitted as their own run
			// with no position of their own. Left standing they would sort to the
			// front of the line and fuse every word behind them, so each is
			// folded into the run it follows and travels with it.
			if strings.TrimSpace(text.S) == "" {
				if len(out) > 0 {
					out[len(out)-1].s += " "
				}
				continue
			}
			size := text.FontSize
			if size <= 0 {
				size = 10
			}
			out = append(out, chunk{x: text.X, y: text.Y, w: text.W, size: size, s: text.S})
		}
	}
	return out
}

// splitColumns separates a page into vertical columns by finding a blank band
// that runs the full height of the text area. Returns one column unchanged when
// the page is a single block, which is the common case.
func splitColumns(chunks []chunk) [][]chunk {
	minX, maxX := chunks[0].x, chunks[0].x+chunks[0].w
	for _, c := range chunks {
		if c.x < minX {
			minX = c.x
		}
		if right := c.x + c.w; right > maxX {
			maxX = right
		}
	}
	width := maxX - minX
	if width <= 0 || len(chunks) < 40 {
		return [][]chunk{chunks}
	}

	// Mark which horizontal slices of the page carry any glyph at all.
	const bins = 240
	occupied := make([]bool, bins)
	for _, c := range chunks {
		from := int((c.x - minX) / width * bins)
		to := int((c.x + c.w - minX) / width * bins)
		for b := from; b <= to && b < bins; b++ {
			if b >= 0 {
				occupied[b] = true
			}
		}
	}

	// The widest empty band in the middle of the page is the gutter.
	bestStart, bestLength, start := -1, 0, -1
	for b := 0; b <= bins; b++ {
		if b < bins && !occupied[b] {
			if start < 0 {
				start = b
			}
			continue
		}
		if start >= 0 {
			length := b - start
			center := float64(start+b) / 2 / bins
			if length > bestLength && center > 0.2 && center < 0.8 {
				bestStart, bestLength = start, length
			}
			start = -1
		}
	}
	if bestStart < 0 || float64(bestLength)/bins < gutterRatio {
		return [][]chunk{chunks}
	}

	boundary := minX + (float64(bestStart)+float64(bestLength)/2)/bins*width
	var left, right []chunk
	for _, c := range chunks {
		if c.x+c.w/2 < boundary {
			left = append(left, c)
			continue
		}
		right = append(right, c)
	}
	// A split is only worth taking when both sides hold real content; otherwise
	// the "gutter" was just a wide margin.
	minimum := len(chunks) / 8
	if len(left) < minimum || len(right) < minimum {
		return [][]chunk{chunks}
	}
	return [][]chunk{left, right}
}

// renderColumn puts one column back into reading order: glyphs grouped into
// lines by baseline, sorted left to right, with word breaks inferred from gaps.
func renderColumn(chunks []chunk) string {
	if len(chunks) == 0 {
		return ""
	}
	// PDF y grows upwards, so a larger y is higher on the page.
	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].y != chunks[j].y {
			return chunks[i].y > chunks[j].y
		}
		return chunks[i].x < chunks[j].x
	})

	var out strings.Builder
	var line []chunk
	flush := func() {
		if len(line) == 0 {
			return
		}
		out.WriteString(renderLine(line))
		out.WriteByte('\n')
		line = line[:0]
	}
	for _, c := range chunks {
		if len(line) > 0 {
			previous := line[len(line)-1]
			if absolute(previous.y-c.y) > previous.size*lineRatio {
				flush()
			}
		}
		line = append(line, c)
	}
	flush()
	return out.String()
}

// renderLine joins one line's glyphs, inserting a space wherever the horizontal
// gap is too wide to be kerning.
//
// A fixed fraction of the font size is not enough on its own: reported glyph
// advances vary between the fonts a single CV uses, so a threshold that works
// for the body text splits headings mid-word or fuses sidebar entries. The
// threshold is therefore also scaled against the line's own median gap, which
// adapts per font without needing to know which font it is.
func renderLine(line []chunk) string {
	sorted := make([]chunk, len(line))
	copy(sorted, line)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].x < sorted[j].x })

	// A gap is the advance from one run to the next. Where glyph widths are
	// reported it is the true blank space; where they are zero it collapses to
	// the raw x delta, which still separates words, just with the glyph's own
	// width folded in. Both are usable; the threshold below adapts to which.
	gaps := make([]float64, 0, len(sorted))
	for i := 1; i < len(sorted); i++ {
		gaps = append(gaps, sorted[i].x-(sorted[i-1].x+sorted[i-1].w))
	}

	// When a line reports one position for every run, its coordinates say
	// nothing about word boundaries. Such lines carry their own spaces, so the
	// text is emitted exactly as written rather than guessed at.
	if !hasPositionInfo(gaps) {
		var plain strings.Builder
		for _, c := range sorted {
			plain.WriteString(c.s)
		}
		return strings.TrimSpace(plain.String())
	}
	threshold := adaptiveThreshold(gaps, sorted[0].size)

	var b strings.Builder
	for i, c := range sorted {
		// Never double up on a space the document already provides.
		if i > 0 && gaps[i-1] > threshold && !endsWithSpace(b.String()) && !startsWithSpace(c.s) {
			b.WriteByte(' ')
		}
		b.WriteString(c.s)
	}
	return strings.TrimSpace(b.String())
}

// hasPositionInfo reports whether a line's coordinates vary enough to say
// anything about where its words begin and end.
func hasPositionInfo(gaps []float64) bool {
	for _, gap := range gaps {
		if gap > 0.01 {
			return true
		}
	}
	return false
}

func endsWithSpace(v string) bool {
	return v != "" && (v[len(v)-1] == ' ' || v[len(v)-1] == '\t')
}

func startsWithSpace(v string) bool {
	return v != "" && (v[0] == ' ' || v[0] == '\t')
}

// adaptiveThreshold picks the gap width that separates words on one line. The
// median gap is the typical inter-glyph advance for whichever font drew the
// line, so a real space sits well above it.
func adaptiveThreshold(gaps []float64, size float64) float64 {
	floor := size * spaceRatio
	if len(gaps) < 4 {
		return floor
	}
	positive := make([]float64, 0, len(gaps))
	for _, gap := range gaps {
		if gap > 0 {
			positive = append(positive, gap)
		}
	}
	if len(positive) < 4 {
		return floor
	}
	sort.Float64s(positive)
	median := positive[len(positive)/2]
	// Raise the bar only when nearly every run is separated, which means the
	// generator reports true glyph advances and the typical gap is already a
	// word break. Otherwise a positive gap is itself the signal.
	if float64(len(positive)) > float64(len(gaps))*0.8 && median > floor {
		return median * 1.2
	}
	return floor
}

func absolute(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// Reconstructed spacing errs towards splitting, because a missing word break
// destroys more meaning than a stray one. These rules repair the splits that
// are unambiguous, and only those: anything requiring a guess about intent is
// left for the user to correct when reviewing the parse.
var (
	spaceBeforePunctuation = regexp.MustCompile(`\s+([.,;:!?%])`)
	spaceAfterOpenBracket  = regexp.MustCompile(`([(\[])\s+`)
	splitYear              = regexp.MustCompile(`\b(\d{3})\s(\d)\b`)
	splitHyphenCompound    = regexp.MustCompile(`([a-z]) - ([a-z])`)
	splitLeadingInitial    = regexp.MustCompile(`\b([B-HJ-Z])\s([a-z]{3,})`)
	splitDotSuffix         = regexp.MustCompile(`([A-Za-z])\s+\.([a-z]{2,})`)
)

func tidySpacing(text string) string {
	text = spaceBeforePunctuation.ReplaceAllString(text, "$1")
	text = spaceAfterOpenBracket.ReplaceAllString(text, "$1")
	text = splitYear.ReplaceAllString(text, "$1$2")
	text = splitHyphenCompound.ReplaceAllString(text, "$1-$2")
	// "S enior" is a split heading; "I focus" and "A test" are real words, so
	// single-letter words that stand alone in English are excluded above.
	text = splitLeadingInitial.ReplaceAllString(text, "$1$2")
	// "Node .js" -> "Node.js"
	text = splitDotSuffix.ReplaceAllString(text, "$1.$2")
	return text
}
