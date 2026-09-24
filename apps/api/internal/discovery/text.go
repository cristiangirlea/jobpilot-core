package discovery

import (
	"html"
	"regexp"
	"strings"
)

var (
	scriptOrStyle = regexp.MustCompile(`(?is)<(script|style)\b.*?</(script|style)\s*>`)
	lineBreak     = regexp.MustCompile(`(?i)<br\s*/?>`)
	listItem      = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	blockTag      = regexp.MustCompile(`(?i)</?(p|div|ul|ol|h[1-6]|section|article|header|footer|blockquote|table|tr|pre|hr)\b[^>]*>`)
	cellTag       = regexp.MustCompile(`(?i)</?(td|th)\b[^>]*>`)
	// A tag starts with a letter, a slash or "!" right after "<", so text such
	// as "5 < 10 and 10 > 5" is left alone.
	anyTag      = regexp.MustCompile(`<[a-zA-Z/!][^>]*>`)
	inlineSpace = regexp.MustCompile(`[ \t\f\v\x{00a0}]+`)
	blankRun    = regexp.MustCompile(`\n{3,}`)
)

// Readable turns a posting's HTML into text that keeps its shape: paragraphs
// are separated by a blank line, list items become "• " lines and <br> breaks
// a line. Providers send HTML in different states, including escaped (the
// Greenhouse board API) and escaped twice (some Arbeitnow posts), so entities
// are decoded until nothing changes before any tag is read. Plain text passes
// through with only its spacing tidied.
func Readable(v string) string {
	for i := 0; i < 3; i++ {
		decoded := html.UnescapeString(v)
		if decoded == v {
			break
		}
		v = decoded
	}
	v = strings.ReplaceAll(v, "\r\n", "\n")
	v = scriptOrStyle.ReplaceAllString(v, "")
	v = lineBreak.ReplaceAllString(v, "\n")
	v = listItem.ReplaceAllString(v, "\n• ")
	v = blockTag.ReplaceAllString(v, "\n\n")
	v = cellTag.ReplaceAllString(v, " ")
	v = anyTag.ReplaceAllString(v, "")
	lines := strings.Split(v, "\n")
	kept := lines[:0]
	for _, line := range lines {
		line = strings.TrimSpace(inlineSpace.ReplaceAllString(line, " "))
		if line == "•" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(blankRun.ReplaceAllString(strings.Join(kept, "\n"), "\n\n"))
}
