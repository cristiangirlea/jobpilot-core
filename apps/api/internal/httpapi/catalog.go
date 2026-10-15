package httpapi

import (
	"net/http"

	"jobpilot/core/api/internal/auth"
)

// findJobs scores the shared catalog's open jobs that the caller has no match
// for yet against their own profile, exclusions and active filter. It fetches
// nothing: the jobs are the ones discovery already stored.
func (s *Server) findJobs(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), auth.UserID(r.Context())
	scored, err := s.Discovery.ScoreCatalog(ctx, uid)
	if err != nil {
		internal(w, err)
		return
	}
	summary, err := s.Store.CatalogSummary(ctx, uid)
	if err != nil {
		internal(w, err)
		return
	}
	write(w, 200, map[string]any{"scored": scored, "summary": summary})
}

// catalogSummary is the caller's view of discovery: how fresh the catalog is
// and how many open jobs fit them, with nothing about sources or runs.
func (s *Server) catalogSummary(w http.ResponseWriter, r *http.Request) {
	x, err := s.Store.CatalogSummary(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		internal(w, err)
		return
	}
	write(w, 200, x)
}
