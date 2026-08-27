package answers

import (
	"regexp"
	"strings"
)

// Location-gated questions are extremely common on remote postings: "Are you
// currently located in Bangalore?", "Are you based in Germany, Spain, Sweden,
// Ireland or the UK?". Each one is answerable from a stored location, and each
// one otherwise blocks an application until a human clears it.
//
// The rule is deliberately asymmetric, because the two mistakes are not equally
// bad. Claiming to be somewhere the candidate is not would be a false statement
// to an employer; failing to answer merely costs a click. So:
//
//   - A place the candidate is in resolves to Yes.
//   - A specific place that is plainly not where the candidate lives resolves to
//     No. Being wrong there costs an application; claiming to live somewhere
//     they do not would be a false statement.
//   - A grouping this file cannot evaluate - "the Nordics", "the Balkans" - is
//     left unanswered, because it might well contain the candidate.

// europeanUnion is used to answer region-scoped questions for EU residents.
var europeanUnion = map[string]bool{
	"austria": true, "belgium": true, "bulgaria": true, "croatia": true, "cyprus": true,
	"czechia": true, "czech republic": true, "denmark": true, "estonia": true, "finland": true,
	"france": true, "germany": true, "greece": true, "hungary": true, "ireland": true,
	"italy": true, "latvia": true, "lithuania": true, "luxembourg": true, "malta": true,
	"netherlands": true, "poland": true, "portugal": true, "romania": true, "slovakia": true,
	"slovenia": true, "spain": true, "sweden": true,
}

// europeNonEU covers the rest of geographic Europe, for questions phrased about
// Europe or EMEA rather than the EU.
var europeNonEU = map[string]bool{
	"united kingdom": true, "switzerland": true, "norway": true, "iceland": true,
	"serbia": true, "ukraine": true, "moldova": true, "albania": true, "bosnia and herzegovina": true,
	"north macedonia": true, "montenegro": true, "turkey": true, "georgia": true, "armenia": true,
}

// knownCountries is the set this file is willing to reason about. A place
// outside it yields no answer rather than a guess.
var knownCountries = buildKnownCountries()

func buildKnownCountries() map[string]bool {
	out := map[string]bool{}
	for country := range europeanUnion {
		out[country] = true
	}
	for country := range europeNonEU {
		out[country] = true
	}
	for _, country := range []string{
		"united states", "canada", "mexico", "brazil", "argentina", "chile", "colombia",
		"india", "china", "japan", "singapore", "australia", "new zealand", "south korea",
		"israel", "united arab emirates", "south africa", "nigeria", "kenya", "egypt",
		"philippines", "indonesia", "vietnam", "thailand", "malaysia", "pakistan", "bangladesh",
	} {
		out[country] = true
	}
	return out
}

// regions maps a region name onto a test for whether a country lies in it.
var regions = map[string]func(country string) bool{
	"eu":             func(c string) bool { return europeanUnion[c] },
	"european union": func(c string) bool { return europeanUnion[c] },
	"eea":            func(c string) bool { return europeanUnion[c] || c == "norway" || c == "iceland" },
	"europe":         func(c string) bool { return europeanUnion[c] || europeNonEU[c] },
	"emea":           func(c string) bool { return europeanUnion[c] || europeNonEU[c] },
	"north america":  func(c string) bool { return c == "united states" || c == "canada" || c == "mexico" },
	"americas":       func(c string) bool { return northOrSouthAmerica(c) },
	"apac":           func(c string) bool { return asiaPacific(c) },
	"asia":           func(c string) bool { return asiaPacific(c) },
}

func northOrSouthAmerica(country string) bool {
	switch country {
	case "united states", "canada", "mexico", "brazil", "argentina", "chile", "colombia":
		return true
	}
	return false
}

func asiaPacific(country string) bool {
	switch country {
	case "india", "china", "japan", "singapore", "australia", "new zealand", "south korea",
		"philippines", "indonesia", "vietnam", "thailand", "malaysia", "pakistan", "bangladesh":
		return true
	}
	return false
}

// locatedInPattern captures the place list from a location-gated question.
var locatedInPattern = regexp.MustCompile(`(?i)(?:are you|do you)\s+(?:currently\s+)?(?:located|based|living|residing|reside|live|work(?:ing)?)\s*(?:in|from|out of)\s+(.+)`)

// splitPlaces breaks "Germany, Spain, Sweden, Ireland or the UK" into parts.
var placeSplitter = regexp.MustCompile(`(?i)\s*(?:,|/|\bor\b|\band\b)\s*`)

// LocationGate answers "are you in X?" from a stored location. The second
// return value reports whether an answer could be determined at all.
func LocationGate(question, location string) (yes bool, ok bool) {
	match := locatedInPattern.FindStringSubmatch(question)
	if len(match) != 2 {
		return false, false
	}
	city, country := SplitLocation(location)
	city, country = strings.ToLower(strings.TrimSpace(city)), strings.ToLower(strings.TrimSpace(country))
	if city == "" && country == "" {
		return false, false
	}

	places := placeSplitter.Split(strings.TrimRight(strings.TrimSpace(match[1]), "?. "), -1)
	recognised := 0
	for _, place := range places {
		place = normalizePlace(place)
		if place == "" {
			continue
		}
		if place == country || (city != "" && place == city) {
			return true, true
		}
		if canonical, aliased := countryAliases[place]; aliased && strings.ToLower(canonical) == country {
			return true, true
		}
		if test, isRegion := regions[place]; isRegion {
			if country != "" && test(country) {
				return true, true
			}
			recognised++
			continue
		}
		if knownCountries[place] {
			recognised++
			continue
		}
		if canonical, aliased := countryAliases[place]; aliased && knownCountries[strings.ToLower(canonical)] {
			recognised++
			continue
		}
		// An unrecognised grouping may still contain the candidate, so it stops
		// the question from being answered at all.
		if looksLikeGrouping(place) {
			return false, false
		}
		// An unrecognised specific place - a city, usually - is somewhere the
		// candidate is not, since it matched neither their city nor country.
		recognised++
	}
	if recognised > 0 && recognised == countPlaces(places) && (country != "" || city != "") {
		return false, true
	}
	return false, false
}

// groupingWords name collections of places this file has no membership test
// for. A question about one cannot be answered from a city and a country alone.
var groupingWords = []string{
	"nordic", "benelux", "dach", "balkan", "baltic", "iberia", "scandinavia",
	"middle east", "latam", "latin america", "oceania", "caribbean", "sub-saharan",
	"central europe", "western europe", "eastern europe", "southern europe", "northern europe",
	"southeast asia", "south asia", "east asia", "north africa", "anywhere", "worldwide",
	"global", "continent", "union", "commonwealth", "schengen",
}

func looksLikeGrouping(place string) bool {
	for _, word := range groupingWords {
		if strings.Contains(place, word) {
			return true
		}
	}
	return false
}

func countPlaces(places []string) int {
	count := 0
	for _, place := range places {
		if normalizePlace(place) != "" {
			count++
		}
	}
	return count
}

// normalizePlace strips the articles and punctuation a question wraps around a
// place name: "the UK" and "the United States" both reduce to the bare name.
func normalizePlace(place string) string {
	place = strings.ToLower(strings.TrimSpace(place))
	place = strings.Trim(place, " .?!,:;()")
	place = strings.TrimPrefix(place, "the ")
	place = strings.TrimSpace(place)
	// Trailing qualifiers such as "region" or "area" add nothing.
	for _, suffix := range []string{" region", " area", " zone"} {
		place = strings.TrimSuffix(place, suffix)
	}
	return strings.TrimSpace(place)
}
