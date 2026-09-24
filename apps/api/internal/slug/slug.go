// Package slug builds the readable names jobs are addressed by in URLs.
package slug

import (
	"net/url"
	"regexp"
	"strings"
)

// maxLength keeps a slug readable in an address bar.
const maxLength = 90

// Parts of a link that name a page type rather than the job itself.
var genericSegments = map[string]bool{"apply": true, "application": true, "jobs": true, "job": true, "o": true, "view": true}

// Host prefixes that say nothing about whose site it is.
var hostPrefixes = []string{"www.", "jobs.", "careers.", "boards.", "job-boards.", "apply."}

var uuidLike = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Job names a job after the end of its link and where it came from, such as
// "arbeitnow-remote-python-backend-engineer-berlin-100001". When the link ends
// in a bare id, which says nothing to a reader, the company and title name it
// and a short form of the id keeps it distinct. The result is not guaranteed
// unique; the store settles collisions.
func Job(applyURL, source, company, title string) string {
	origin, segment := describeLink(applyURL)
	if source != "" {
		origin = source
	}
	prefix := Make(origin)
	if prefix == "" {
		prefix = "job"
	}
	body := Make(segment)
	if !readable(body) {
		id := body
		if uuidLike.MatchString(id) {
			id = id[:8]
		}
		body = Make(company + " " + title + " " + id)
	}
	return trim(prefix + "-" + body)
}

// describeLink returns the site a link belongs to and the last path segment
// that names the job.
func describeLink(raw string) (site, segment string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", ""
	}
	host := strings.ToLower(u.Hostname())
	for _, prefix := range hostPrefixes {
		host = strings.TrimPrefix(host, prefix)
	}
	site, _, _ = strings.Cut(host, ".")
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		part, err := url.PathUnescape(parts[i])
		if err != nil {
			part = parts[i]
		}
		if part != "" && !genericSegments[strings.ToLower(part)] {
			return site, part
		}
	}
	return site, ""
}

// readable reports whether a slugified segment reads as words: at least one
// part of three or more characters with a letter a hex id would not have.
func readable(s string) bool {
	if uuidLike.MatchString(s) {
		return false
	}
	for _, part := range strings.Split(s, "-") {
		if len(part) >= 3 && strings.IndexFunc(part, func(r rune) bool { return r >= 'g' && r <= 'z' }) >= 0 {
			return true
		}
	}
	return false
}

// Spellings for letters outside a-z, so "München" becomes "muenchen" rather
// than "m-nchen".
var letters = map[rune]string{
	'ä': "ae", 'ö': "oe", 'ü': "ue", 'ß': "ss", 'æ': "ae", 'œ': "oe", 'ø': "o", 'å': "a",
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ă': "a", 'ç': "c", 'č': "c", 'ć': "c",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ì': "i", 'í': "i", 'î': "i", 'ï': "i",
	'ñ': "n", 'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ù': "u", 'ú': "u", 'û': "u",
	'ș': "s", 'ş': "s", 'š': "s", 'ț': "t", 'ţ': "t", 'ý': "y", 'ÿ': "y", 'ž': "z", 'ł': "l",
}

// Make lowercases s and keeps only a-z, 0-9 and single hyphens between words.
func Make(s string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			hyphen = false
		case letters[r] != "":
			b.WriteString(letters[r])
			hyphen = false
		default:
			if !hyphen && b.Len() > 0 {
				b.WriteByte('-')
				hyphen = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// trim shortens a slug to maxLength at a word boundary.
func trim(s string) string {
	if len(s) <= maxLength {
		return s
	}
	s = s[:maxLength]
	if cut := strings.LastIndexByte(s, '-'); cut > maxLength/2 {
		s = s[:cut]
	}
	return strings.TrimSuffix(s, "-")
}
