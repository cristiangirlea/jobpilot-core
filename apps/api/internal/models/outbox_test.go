package models

import "testing"

func TestSendsFormValueForAIDrafts(t *testing.T) {
	for _, source := range []string{"ai", "ai_backup"} {
		for _, tc := range []struct {
			required, review bool
			answer           string
			send             bool
		}{
			{false, true, "draft", false},
			{false, false, "reviewed", true},
			{true, true, "required draft", true},
			{true, false, "reviewed", true},
			{true, true, "  ", false},
			{false, false, "", false},
		} {
			a := OutboxAnswer{Source: source, Required: tc.required, NeedsReview: tc.review, Answer: tc.answer}
			if got := SendsFormValue(a); got != tc.send {
				t.Errorf("SendsFormValue(%+v) = %v, want %v", a, got, tc.send)
			}
		}
	}
}
