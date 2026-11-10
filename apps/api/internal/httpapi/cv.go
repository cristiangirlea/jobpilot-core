package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/answers"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/cv"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// uploadCV accepts a CV file, extracts and parses it, and stores it unapproved.
//
// Parsing never writes to the profile: the response is a proposal the user
// reviews, and importing is a separate, explicit call. That is the approval gate
// the product promises, and it is enforced here rather than in the UI.
func (s *Server) uploadCV(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, cv.MaxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(cv.MaxUploadBytes); err != nil {
		problem(w, 400, "Upload a CV file as multipart form data under the field name \"file\".")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		problem(w, 400, "No file was uploaded under the field name \"file\".")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, cv.MaxUploadBytes+1))
	if err != nil {
		internal(w, err)
		return
	}
	filename := filepath.Base(header.Filename)
	text, format, err := cv.ExtractText(filename, data)
	if err != nil {
		// These are all user-correctable conditions, not server faults.
		problem(w, 400, err.Error())
		return
	}
	parsed := cv.Parse(text)

	sum := sha256.Sum256(data)
	document := models.CVDocument{
		Kind:          "base",
		Label:         strings.TrimSpace(r.FormValue("label")),
		Filename:      filename,
		Format:        format,
		ContentType:   header.Header.Get("Content-Type"),
		SizeBytes:     len(data),
		ExtractedText: text,
		Parsed:        toMap(parsed),
		RoleFamily:    answers.RoleFamily(parsed.CurrentTitle),
		Technologies:  answers.TechnologySignature(20, parsed.CurrentTitle, text),
	}
	document.Fingerprint = hex.EncodeToString(sum[:])

	saved, err := s.Store.SaveCV(r.Context(), auth.UserID(r.Context()), document, data)
	if err != nil {
		internal(w, err)
		return
	}
	write(w, 201, saved)
}

func (s *Server) listCVs(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.ListCVs(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}

func (s *Server) getCV(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.CV(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	respond(w, x, e)
}

// downloadCV serves the original bytes, which is what an application attaches.
func (s *Server) downloadCV(w http.ResponseWriter, r *http.Request) {
	doc, data, err := s.Store.CVFile(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	contentType := doc.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+doc.Filename+"\"")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

type approvalRequest struct {
	Approved *bool `json:"approved"`
}

// approveCV records the user's decision about whether this CV may be sent.
func (s *Server) approveCV(w http.ResponseWriter, r *http.Request) {
	var body approvalRequest
	if !decode(w, r, &body) {
		return
	}
	approved := true
	if body.Approved != nil {
		approved = *body.Approved
	}
	x, e := s.Store.SetCVApproval(r.Context(), auth.UserID(r.Context()), r.PathValue("id"), approved)
	respond(w, x, e)
}

func (s *Server) deleteCV(w http.ResponseWriter, r *http.Request) {
	if e := s.Store.DeleteCV(r.Context(), auth.UserID(r.Context()), r.PathValue("id")); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			problem(w, 404, "CV not found.")
			return
		}
		internal(w, e)
		return
	}
	w.WriteHeader(204)
}

type cvImportRequest = store.CVImportRequest
type cvImportResponse = store.CVImportResponse

// importCV copies reviewed CV data into the profile. Only non-empty values are
// written, so an unreadable field in the CV can never blank out something the
// user already filled in by hand.
func (s *Server) importCV(w http.ResponseWriter, r *http.Request) {
	var body cvImportRequest
	if !decode(w, r, &body) {
		return
	}
	ctx := r.Context()
	uid := auth.UserID(ctx)

	response, err := s.Store.ImportCV(ctx, uid, r.PathValue("id"), body)
	if err != nil {
		respondRepair(w, nil, err)
		return
	}

	// Employers and skills feed matching and exclusion, so existing jobs are
	// re-evaluated immediately rather than waiting for the next discovery run.
	if err = s.reevaluateJobs(r); err != nil {
		internal(w, err)
		return
	}
	if response.Profile, err = s.Store.Profile(ctx, uid); err != nil {
		internal(w, err)
		return
	}
	write(w, 200, response)
}

// toMap round-trips the parse through JSON so the store holds exactly what the
// API returns, with no second definition of the shape to keep in sync.
func toMap(parsed cv.Parsed) map[string]any {
	raw, err := json.Marshal(parsed)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
