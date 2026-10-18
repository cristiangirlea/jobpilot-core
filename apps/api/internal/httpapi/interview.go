package httpapi

import (
	"errors"
	"net/http"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/generator"
)

func (s *Server) prepareInterview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := auth.UserID(ctx)
	app, err := s.Store.Application(ctx, userID, r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	// Refuse before reading anything else, so a missing job or document cannot replace the 409.
	if app.Status != "interview" {
		problem(w, http.StatusConflict, generator.ErrInterviewStatus.Error())
		return
	}
	posting, err := s.Store.Job(ctx, userID, app.JobID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	profile, err := s.Store.Profile(ctx, userID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	docs, err := s.Store.InterviewDocuments(ctx, userID, app.ID, app.JobID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	pack, err := generator.PrepareInterview(app, profile, posting, docs)
	if errors.Is(err, generator.ErrInterviewStatus) {
		problem(w, http.StatusConflict, err.Error())
		return
	}
	respond(w, pack, err)
}
