package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/store"
)

// repairBody reads one small JSON object and nothing after it. A repair request
// carries an id or a decision, never profile facts, so unknown fields are refused.
func repairBody(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		problem(w, 400, "Invalid repair request.")
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		problem(w, 400, "Expected one JSON object.")
		return false
	}
	return true
}

func validUUID(s string) bool {
	var id pgtype.UUID
	return id.Scan(s) == nil && id.Valid
}

// ProfileBusy is the answer when a repair or an import ran out of its time,
// which happens while another write holds the same user's profile lock.
const ProfileBusy = "The profile is busy with another change. Reload and try again in a moment."

// profileBusy reports the errors of a profile transaction that ran out of time:
// its own deadline, a lock wait, a statement or an idle limit. All roll back.
func profileBusy(e error) bool {
	if errors.Is(e, context.DeadlineExceeded) {
		return true
	}
	var pg *pgconn.PgError
	return errors.As(e, &pg) && (pg.Code == "55P03" || pg.Code == "57014" || pg.Code == "25P03")
}

func respondRepair(w http.ResponseWriter, v any, e error) {
	if profileBusy(e) {
		problem(w, 503, ProfileBusy)
		return
	}
	if errors.Is(e, store.ErrRepairConflict) {
		problem(w, 409, e.Error())
		return
	}
	if errors.Is(e, store.ErrRepairInvalid) {
		problem(w, 400, e.Error())
		return
	}
	respond(w, v, e)
}

func (s *Server) proposeProfileRepair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourceCVID string `json:"source_cv_id"`
	}
	if !repairBody(w, r, &body) {
		return
	}
	if !validUUID(body.SourceCVID) {
		problem(w, 400, "A source CV ID is required.")
		return
	}
	by := store.RepairByBrowser
	if !auth.IsSession(r.Context()) {
		by = store.RepairByAgent
	}
	v, e := s.Store.ProposeProfileRepair(r.Context(), auth.UserID(r.Context()), body.SourceCVID, by)
	respondRepair(w, v, e)
}

func (s *Server) getProfileRepair(w http.ResponseWriter, r *http.Request) {
	if !validUUID(r.PathValue("id")) {
		problem(w, 404, "Record not found.")
		return
	}
	v, e := s.Store.ProfileRepair(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	respondRepair(w, v, e)
}

func (s *Server) listProfileRepairs(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.ProfileRepairs(r.Context(), auth.UserID(r.Context()))
	respondRepair(w, v, e)
}

func (s *Server) decideProfileRepair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision            string `json:"decision"`
		ExpectedFingerprint string `json:"expected_fingerprint"`
	}
	if !repairBody(w, r, &body) {
		return
	}
	if body.Decision != "apply" && body.Decision != "reject" && body.Decision != "restore" {
		problem(w, 400, "Choose apply, reject or restore.")
		return
	}
	if !validUUID(r.PathValue("id")) {
		problem(w, 404, "Record not found.")
		return
	}
	v, e := s.Store.DecideProfileRepair(r.Context(), auth.UserID(r.Context()), r.PathValue("id"), body.Decision, body.ExpectedFingerprint)
	if e != nil {
		respondRepair(w, v, e)
		return
	}
	// Roles, skills and exclusions feed matching, so stored jobs are scored again.
	// The decision is committed by now: a failure here is not a refused decision.
	if body.Decision != "reject" {
		if e = s.reevaluateJobs(r); e != nil {
			internal(w, e)
			return
		}
	}
	write(w, 200, v)
}
