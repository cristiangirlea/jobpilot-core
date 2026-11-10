package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

func (s *Server) listExternalHistory(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 100 {
			problem(w, 400, "limit must be 1 to 100")
			return
		}
		limit = n
	}
	out, e := s.Store.ExternalHistory(r.Context(), auth.UserID(r.Context()), r.URL.Query().Get("after"), limit)
	if errors.Is(e, store.ErrHistoryCursor) {
		problem(w, 400, e.Error())
		return
	}
	respond(w, out, e)
}

func (s *Server) importExternalHistory(w http.ResponseWriter, r *http.Request) {
	var in models.HistoryImport
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024))
	d.DisallowUnknownFields()
	if e := d.Decode(&in); e != nil {
		problem(w, 400, "Invalid history import body.")
		return
	}
	if e := d.Decode(&struct{}{}); e != io.EOF {
		problem(w, 400, "Expected one history import object.")
		return
	}
	if in.Mode == "" {
		in.Mode = "preview"
	}
	if in.Mode != "preview" && in.Mode != "commit" {
		problem(w, 422, "mode must be preview or commit")
		return
	}
	if e := store.ValidateHistory(in.Record); e != nil {
		problem(w, 422, e.Error())
		return
	}
	out, e := s.Store.ImportExternalHistory(r.Context(), auth.UserID(r.Context()), in, []byte(s.Secret))
	if errors.Is(e, store.ErrHistoryConflict) {
		write(w, 409, map[string]any{"error": e.Error(), "preview": out})
		return
	}
	respond(w, out, e)
}
