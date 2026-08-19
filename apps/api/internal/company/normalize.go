package company

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var legalSuffix = regexp.MustCompile(`(?i)\s+(ltd|limited|llc|inc|incorporated|gmbh|srl|s\s*r\s*l|sa|s\s*a|bv|plc|ag)\s*$`)
var aliasSeparator = regexp.MustCompile(`(?i)\s+(?:aka|a/k/a|dba|d/b/a|formerly)\s+`)

func Normalize(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	s = strings.Join(strings.Fields(b.String()), " ")
	for {
		next := strings.TrimSpace(legalSuffix.ReplaceAllString(s, ""))
		if next == s {
			break
		}
		s = next
	}
	return strings.Join(strings.Fields(s), " ")
}

func NormalizeDomain(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(u.Hostname(), "www.")
	return strings.TrimSuffix(host, ".")
}

// Aliases returns normalized names explicitly supplied in common "AKA", "DBA",
// or "formerly" forms. It lets imports and exclusions compare every stated name.
func Aliases(name string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, part := range aliasSeparator.Split(strings.TrimSpace(name), -1) {
		n := Normalize(part)
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func Similarity(a, b string) float64 {
	a, b = Normalize(a), Normalize(b)
	if a == b {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	d := levenshtein([]rune(a), []rune(b))
	max := len([]rune(a))
	if len([]rune(b)) > max {
		max = len([]rune(b))
	}
	return 1 - float64(d)/float64(max)
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
