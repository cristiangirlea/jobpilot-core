package cv

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// walkText reads a page's text runs with their real positions and widths.
//
// The PDF library's own row reader is not enough for CVs: it takes a run's
// position only from Tm, so it ignores the cm, Td, TL and T* operators that
// ReportLab and many other writers place text with (every line of a paragraph
// lands on one row), and it reports no widths at all (Word's hyphens, drawn as
// runs of their own, then look like separate words). This walker keeps the
// text state the way a renderer does, for text drawn directly in the page's
// content stream. It reports false when the stream cannot be interpreted.
func walkText(page pdf.Page) (runs []chunk, ok bool) {
	defer func() {
		if recover() != nil {
			runs, ok = nil, false
		}
	}()
	contents := page.V.Key("Contents")
	if contents.IsNull() {
		return nil, true
	}

	type graphicsState struct {
		ctm                                              affine
		font                                             *walkFont
		size, charSpace, wordSpace, scale, leading, rise float64
	}
	g := graphicsState{ctm: identity, scale: 1}
	// PDF allows 28 nested saves; a hostile stream of "q" must not grow
	// without bound, so saves past the cap are only counted, to keep Q paired.
	const maxSaved = 256
	var saved []graphicsState
	unsaved := 0
	tm, tlm := identity, identity
	fonts := map[string]*walkFont{}

	show := func(raw string) {
		if g.font == nil || raw == "" {
			return
		}
		advance, known := 0.0, g.font.widths != nil
		for i := 0; i+g.font.codeLength <= len(raw); i += g.font.codeLength {
			code := int(raw[i])
			if g.font.codeLength == 2 {
				code = code<<8 | int(raw[i+1])
			}
			glyph := g.font.width(code)/1000*g.size + g.charSpace
			if g.font.codeLength == 1 && code == ' ' {
				glyph += g.wordSpace
			}
			advance += glyph * g.scale
		}
		placed := tm.then(g.ctm)
		rendering := affine{g.size * g.scale, 0, 0, g.size, 0, g.rise}.then(placed)
		run := chunk{x: rendering[4], y: rendering[5], size: math.Hypot(rendering[2], rendering[3]), s: g.font.decode(raw)}
		if known {
			run.w = advance * math.Hypot(placed[0], placed[1])
		}
		runs = append(runs, run)
		tm = translate(advance, 0).then(tm)
	}
	nextLine := func(tx, ty float64) {
		tlm = translate(tx, ty).then(tlm)
		tm = tlm
	}

	pdf.Interpret(contents, func(stack *pdf.Stack, op string) {
		args := make([]pdf.Value, stack.Len())
		for i := len(args) - 1; i >= 0; i-- {
			args[i] = stack.Pop()
		}
		number := func(i int) float64 { return args[i].Float64() }
		switch {
		case op == "q" && len(saved) < maxSaved:
			saved = append(saved, g)
		case op == "q":
			unsaved++
		case op == "Q" && unsaved > 0:
			unsaved--
		case op == "Q" && len(saved) > 0:
			g = saved[len(saved)-1]
			saved = saved[:len(saved)-1]
		case op == "cm" && len(args) == 6:
			g.ctm = matrixOf(args).then(g.ctm)
		case op == "BT":
			tm, tlm = identity, identity
		case op == "Tf" && len(args) == 2:
			name := args[0].Name()
			if fonts[name] == nil {
				fonts[name] = loadWalkFont(page.Font(name))
			}
			g.font, g.size = fonts[name], number(1)
		case op == "Tc" && len(args) == 1:
			g.charSpace = number(0)
		case op == "Tw" && len(args) == 1:
			g.wordSpace = number(0)
		case op == "Tz" && len(args) == 1:
			g.scale = number(0) / 100
		case op == "TL" && len(args) == 1:
			g.leading = number(0)
		case op == "Ts" && len(args) == 1:
			g.rise = number(0)
		case op == "Td" && len(args) == 2:
			nextLine(number(0), number(1))
		case op == "TD" && len(args) == 2:
			g.leading = -number(1)
			nextLine(number(0), number(1))
		case op == "Tm" && len(args) == 6:
			tm = matrixOf(args)
			tlm = tm
		case op == "T*":
			nextLine(0, -g.leading)
		case op == "Tj" && len(args) == 1:
			show(args[0].RawString())
		case op == "'" && len(args) == 1:
			nextLine(0, -g.leading)
			show(args[0].RawString())
		case op == "\"" && len(args) == 3:
			g.wordSpace, g.charSpace = number(0), number(1)
			nextLine(0, -g.leading)
			show(args[2].RawString())
		case op == "TJ" && len(args) == 1:
			for i := 0; i < args[0].Len(); i++ {
				item := args[0].Index(i)
				if item.Kind() == pdf.String {
					show(item.RawString())
					continue
				}
				tm = translate(-item.Float64()/1000*g.size*g.scale, 0).then(tm)
			}
		}
	})
	return runs, true
}

// joinRuns merges runs that continue one another on the same line, so a word
// a writer drew in pieces (Word draws every hyphen as its own run, and splits
// runs at each kerning pair) is one chunk again. Spacing between the chunks
// that remain is left to renderLine.
func joinRuns(runs []chunk) []chunk {
	var out []chunk
	// Text is gathered per chunk and joined once: a stream of one-glyph runs
	// must not cost a copy of the whole line per glyph.
	var texts []*strings.Builder
	for _, run := range runs {
		if run.s == "" {
			continue
		}
		if n := len(out); n > 0 && continuesRun(out[n-1], run) {
			last := &out[n-1]
			texts[n-1].WriteString(run.s)
			if last.w > 0 {
				last.w = math.Max(last.w, run.x+run.w-last.x)
			} else {
				last.w = run.w
			}
			continue
		}
		// Spaces the document encodes are the most reliable word breaks, but
		// a space drawn as a run of its own may sit anywhere; it is folded into
		// the run it follows and travels with it.
		if strings.TrimSpace(run.s) == "" {
			if n := len(out); n > 0 {
				texts[n-1].WriteByte(' ')
			}
			continue
		}
		text := &strings.Builder{}
		text.WriteString(run.s)
		out, texts = append(out, run), append(texts, text)
	}
	for i := range out {
		out[i].s = texts[i].String()
	}
	return out
}

// continuesRun reports whether next starts where previous ends, on the same
// baseline at the same size. A run whose width is unknown only continues when
// the next one starts at the same point, as a writer that gives no widths
// leaves consecutive strings.
func continuesRun(previous, next chunk) bool {
	if absolute(previous.y-next.y) > previous.size*0.05 || absolute(previous.size-next.size) > previous.size*0.05 {
		return false
	}
	if previous.w == 0 {
		return absolute(next.x-previous.x) < 0.01
	}
	return absolute(next.x-(previous.x+previous.w)) < previous.size*0.1
}

// extent is how far a chunk reaches to the right. Where the PDF gives no
// widths (the standard fonts it names without embedding), an average glyph is
// assumed: a guess, but a far better one than nothing when deciding whether a
// band of the page is empty.
func extent(c chunk) float64 {
	if c.w > 0 {
		return c.w
	}
	return float64(utf8.RuneCountInString(c.s)) * c.size * 0.5
}

// affine is a PDF transformation matrix [a b c d e f].
type affine [6]float64

var identity = affine{1, 0, 0, 1, 0, 0}

// then applies m first and n after it, the order PDF concatenates matrices in.
func (m affine) then(n affine) affine {
	return affine{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func translate(x, y float64) affine { return affine{1, 0, 0, 1, x, y} }

func matrixOf(args []pdf.Value) affine {
	var m affine
	for i := range m {
		m[i] = args[i].Float64()
	}
	return m
}

// walkFont is what the walker needs from a font: how to decode its strings and
// how far each glyph advances.
type walkFont struct {
	encoding   pdf.TextEncoding
	codeLength int        // bytes per character code: 2 for composite (Type0) fonts
	widths     widthTable // nil when the PDF gives no widths
	missing    float64
	winAnsi    bool
}

func loadWalkFont(font pdf.Font) *walkFont {
	f := &walkFont{encoding: font.Encoder(), codeLength: 1}
	switch font.V.Key("Subtype").Name() {
	case "Type0":
		f.codeLength = 2
		descendant := font.V.Key("DescendantFonts").Index(0)
		f.missing = 1000
		if dw := descendant.Key("DW"); !dw.IsNull() {
			f.missing = dw.Float64()
		}
		f.widths = compositeWidths(descendant.Key("W"))
	case "Type3":
		// Its widths are in glyph space, not thousandths of an em; leave them
		// unknown rather than wrong.
	default:
		if widths := font.V.Key("Widths"); widths.Len() > 0 {
			run := widthRun{first: int(font.V.Key("FirstChar").Int64())}
			for i := 0; i < widths.Len(); i++ {
				run.widths = append(run.widths, widths.Index(i).Float64())
			}
			run.last = run.first + len(run.widths) - 1
			f.widths = widthTable{run}
			f.missing = font.V.Key("FontDescriptor").Key("MissingWidth").Float64()
		}
		f.winAnsi = font.V.Key("Encoding").Name() == "WinAnsiEncoding"
	}
	return f
}

// winAnsiBullets are the codes WinAnsiEncoding leaves unused. The PDF
// specification draws each of them as a bullet, and ReportLab draws a list
// bullet as code 127, which the library decodes as a control character or the
// replacement character.
var winAnsiBullets = strings.NewReplacer("\x7f", "•", "�", "•")

func (f *walkFont) decode(raw string) string {
	if f.encoding == nil {
		return ""
	}
	text := f.encoding.Decode(raw)
	if f.winAnsi {
		text = winAnsiBullets.Replace(text)
	}
	return text
}

func (f *walkFont) width(code int) float64 {
	if w, ok := f.widths.lookup(code); ok {
		return w
	}
	return f.missing
}

// widthTable maps character codes to advances, in thousandths of an em, as
// runs of consecutive codes sorted by their first code.
type widthTable []widthRun

type widthRun struct {
	first, last int
	widths      []float64 // one per code, or nil when the whole run shares width
	width       float64
}

func (t widthTable) lookup(code int) (float64, bool) {
	i := sort.Search(len(t), func(i int) bool { return t[i].last >= code })
	// Overlapping runs make the search inexact; a miss is then just a miss.
	if i == len(t) || t[i].first > code || t[i].last < code {
		return 0, false
	}
	if t[i].widths != nil {
		return t[i].widths[code-t[i].first], true
	}
	return t[i].width, true
}

// compositeWidths reads a CIDFont's W array, whose entries are either
// "first [w1 w2 ...]" or "first last w". Ranges are kept as ranges, so a
// hostile array cannot make the table larger than the array itself.
func compositeWidths(w pdf.Value) widthTable {
	table := widthTable{}
	for i := 0; i+1 < w.Len(); {
		first := int(w.Index(i).Int64())
		if next := w.Index(i + 1); next.Kind() == pdf.Array {
			run := widthRun{first: first}
			for j := 0; j < next.Len(); j++ {
				run.widths = append(run.widths, next.Index(j).Float64())
			}
			if len(run.widths) > 0 {
				run.last = first + len(run.widths) - 1
				table = append(table, run)
			}
			i += 2
			continue
		}
		if i+2 >= w.Len() {
			break
		}
		if last := int(w.Index(i + 1).Int64()); last >= first {
			table = append(table, widthRun{first: first, last: last, width: w.Index(i + 2).Float64()})
		}
		i += 3
	}
	sort.Slice(table, func(a, b int) bool { return table[a].first < table[b].first })
	return table
}
