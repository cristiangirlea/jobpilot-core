package models

import (
	"strconv"
	"strings"
)

// SalaryText states the profile's pay expectation as an employer reads it:
// "48,000 EUR per year", or a contract rate per day. The salary is yearly
// because that is how matching compares it with job salary ranges. It is empty
// when the profile states neither.
func (p Profile) SalaryText() string {
	amount, period := p.SalaryExpectation, "per year"
	if amount <= 0 {
		amount, period = p.RateExpectation, "per day"
	}
	if amount <= 0 {
		return ""
	}
	return strings.Join(strings.Fields(groupThousands(amount)+" "+p.Currency+" "+period), " ")
}

// NoticeText states the notice period in words where the stored value is
// only a number: zero is being available at once. Any other bare number has
// no unit, so it is returned as written and unitless reports that.
func (p Profile) NoticeText() (text string, unitless bool) {
	text = strings.TrimSpace(p.NoticePeriod)
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return text, false
	}
	if n == 0 {
		return "Available immediately", false
	}
	return text, true
}

// groupThousands writes a whole amount with comma thousands separators.
func groupThousands(amount float64) string {
	digits := strconv.FormatFloat(amount, 'f', 0, 64)
	var out strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(digit)
	}
	return out.String()
}
