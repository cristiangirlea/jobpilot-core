package httpapi

import (
	"encoding/json"
	"net/http"

	"jobpilot/core/api/internal/auth"
)

func (s *Server) accountSettings(w http.ResponseWriter, r *http.Request) {
	x, err := s.Store.AccountSettings(r.Context(), auth.UserID(r.Context()))
	respond(w, x, err)
}

func (s *Server) saveAccountSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		QuietApplicationDays json.RawMessage `json:"quiet_application_days"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx, uid := r.Context(), auth.UserID(r.Context())
	if len(in.QuietApplicationDays) == 0 {
		x, err := s.Store.AccountSettings(ctx, uid)
		respond(w, x, err)
		return
	}
	var days int
	if err := json.Unmarshal(in.QuietApplicationDays, &days); err != nil || days < 1 || days > 365 {
		problem(w, 422, "Quiet application days must be from 1 to 365.")
		return
	}
	x, err := s.Store.SaveAccountSettings(ctx, uid, days)
	respond(w, x, err)
}
