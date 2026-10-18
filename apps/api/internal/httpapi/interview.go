package httpapi

import (
	"errors"
	"net/http"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/generator"
)

func (s *Server) prepareInterview(w http.ResponseWriter, r *http.Request) {
	pack, err := s.Store.InterviewPrep(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	if errors.Is(err, generator.ErrInterviewStatus) {
		problem(w, http.StatusConflict, err.Error())
		return
	}
	respond(w, pack, err)
}
