package models

import "testing"

// Found in early use: answers carried "48000 EUR" with no period and
// a notice period of "0". Matching compares the salary with job salary
// ranges, which are yearly, so that is the period it has; a rate is per day.
func TestSalaryTextStatesTheAmountAndItsPeriod(t *testing.T) {
	for _, c := range []struct {
		p    Profile
		want string
	}{
		{Profile{SalaryExpectation: 48000, Currency: "EUR"}, "48,000 EUR per year"},
		{Profile{SalaryExpectation: 1250000, Currency: "RON"}, "1,250,000 RON per year"},
		{Profile{SalaryExpectation: 950}, "950 per year"},
		{Profile{RateExpectation: 600, Currency: "EUR"}, "600 EUR per day"},
		{Profile{}, ""},
	} {
		if got := c.p.SalaryText(); got != c.want {
			t.Errorf("SalaryText(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}

// A notice period of zero is being available at once. A bare number has no
// unit, so it stays as written and the caller holds it for review.
func TestNoticeTextSaysWhatZeroMeans(t *testing.T) {
	for _, c := range []struct {
		notice, want string
		unitless     bool
	}{
		{"0", "Available immediately", false},
		{" 0 ", "Available immediately", false},
		{"30 days", "30 days", false},
		{"30", "30", true},
		{"", "", false},
	} {
		p := Profile{NoticePeriod: c.notice}
		if got, unitless := p.NoticeText(); got != c.want || unitless != c.unitless {
			t.Errorf("NoticeText(%q) = %q, %v; want %q, %v", c.notice, got, unitless, c.want, c.unitless)
		}
	}
}
