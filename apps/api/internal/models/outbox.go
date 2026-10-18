package models

import "strings"

// SendsFormValue reports an answer the sender fills in. Declarations, personal
// choices and other uploads are the applicant's own and are left; compose has
// already held back any of them that is required. An optional answer a model
// or the template drafted is left too: a draft is never sent before the user
// approves it, and approval only checks the required ones in the answer bank.
// CV and cover-letter fields use the entry's documents rather than Answer.
func SendsFormValue(answer OutboxAnswer) bool {
	switch answer.Source {
	case "declaration", "personal", "upload":
		return false
	case "ai", "template":
		if answer.NeedsReview && !answer.Required {
			return false
		}
	case "cv", "cover_letter":
		return true
	}
	return strings.TrimSpace(answer.Answer) != ""
}
