package httpapi

import (
	"net/http"
	"strconv"

	"jobpilot/core/api/internal/auth"
)

// listNotifications returns what waits for the user: state "all" adds the
// resolved history, limit caps the page (default 50, at most 200), after
// continues from the previous page's next, and open always counts every open
// item, which is the web app's badge.
func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	if state != "" && state != "open" && state != "all" {
		problem(w, 400, "state must be open or all.")
		return
	}
	limit := 0
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			problem(w, 400, "limit must be a number from 1 to 200.")
			return
		}
		limit = n
	}
	feed, err := s.Store.Notifications(r.Context(), auth.UserID(r.Context()), state, q.Get("after"), limit)
	respond(w, feed, err)
}
