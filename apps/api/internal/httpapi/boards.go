package httpapi

import (
	"net/http"
	"strings"

	"jobpilot/core/api/internal/boards"
)

// boardSuggestions returns employer boards worth following. A board token is
// otherwise only discoverable by reading a careers URL, which is a poor place
// to start from.
func (s *Server) boardSuggestions(w http.ResponseWriter, r *http.Request) {
	write(w, 200, boards.Suggested)
}

type detectRequest struct {
	URL string `json:"url"`
}

// detectBoard turns a pasted careers or posting URL into a provider and board
// token, so the user does not have to work out which part of the URL matters.
func (s *Server) detectBoard(w http.ResponseWriter, r *http.Request) {
	var body detectRequest
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		problem(w, 400, "Paste a link to a company's job board or one of its postings.")
		return
	}
	detected, ok := boards.Extract(body.URL)
	if !ok {
		problem(w, 422, "That link is not a job board this app can follow. Links to Greenhouse, Lever, Ashby and SmartRecruiters boards or their postings work; a company's own careers page and job aggregators do not.")
		return
	}
	if !detected.Supported {
		problem(w, 422, "That board's provider is recognised but cannot be followed as a discovery source yet.")
		return
	}
	write(w, 200, detected)
}
