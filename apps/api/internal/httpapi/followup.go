package httpapi

import (
	"errors"
	"net/http"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/generator"
	"jobpilot/core/api/internal/store"
)

func (s *Server) draftFollowUp(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), auth.UserID(r.Context())
	a, err := s.Store.Application(ctx, uid, r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if a.Status != "applied" && a.Status != "interview" {
		problem(w, 409, store.ErrFollowUpStatus.Error())
		return
	}
	job, err := s.Store.Job(ctx, uid, a.JobID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if job.Match.Rejected {
		problem(w, 409, store.ErrApplicationBlocked.Error())
		return
	}
	drafts, err := s.Store.FollowUps(ctx, uid, a.ID)
	if err != nil {
		internal(w, err)
		return
	}
	if len(drafts) >= 2 {
		problem(w, 409, store.ErrFollowUpLimit.Error())
		return
	}
	materials, err := s.Store.FollowUpMaterials(ctx, uid, a.ID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	profile, err := s.Store.Profile(ctx, uid)
	if err != nil {
		internal(w, err)
		return
	}
	body, _ := generator.FollowUp(ctx, s.llmFor(ctx), profile, materials)
	draft, err := s.Store.SaveFollowUp(ctx, uid, a.ID, body)
	if errors.Is(err, store.ErrFollowUpLimit) || errors.Is(err, store.ErrFollowUpStatus) {
		problem(w, 409, err.Error())
		return
	}
	respondCreated(w, draft, err)
}

func (s *Server) dismissQuietApplication(w http.ResponseWriter, r *http.Request) {
	err := s.Store.DismissQuietApplication(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	respond(w, map[string]bool{"dismissed": err == nil}, err)
}
