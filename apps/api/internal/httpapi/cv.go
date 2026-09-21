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

type cvImportRequest struct {
	// Fields names which parsed values to copy. Empty means all of them.
	Fields []string `json:"fields"`
	// Importing experience also excludes those employers from job matches,
	// which is a safety rule, so it is opt-in and reported back explicitly.
	ImportExperiences bool `json:"import_experiences"`
	ImportSkills      bool `json:"import_skills"`
}

type cvImportResponse struct {
	Profile            models.Profile `json:"profile"`
	ImportedFields     []string       `json:"imported_fields"`
	AddedExperiences   int            `json:"added_experiences"`
	AddedSkills        int            `json:"added_skills"`
	ExcludedEmployers  []string       `json:"excluded_employers"`
	SkippedEmptyFields []string       `json:"skipped_empty_fields"`
}

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

	document, err := s.Store.CV(ctx, uid, r.PathValue("id"))
	if err != nil {
		respond(w, document, err)
		return
	}
	var parsed cv.Parsed
	if raw, marshalErr := json.Marshal(document.Parsed); marshalErr == nil {
		_ = json.Unmarshal(raw, &parsed)
	}

	profile, err := s.Store.Profile(ctx, uid)
	if err != nil {
		internal(w, err)
		return
	}

	wanted := map[string]bool{}
	for _, field := range body.Fields {
		wanted[strings.ToLower(strings.TrimSpace(field))] = true
	}
	include := func(field string) bool { return len(wanted) == 0 || wanted[field] }

	response := cvImportResponse{ImportedFields: []string{}, ExcludedEmployers: []string{}, SkippedEmptyFields: []string{}}
	assign := func(field string, value string, target *string) {
		if !include(field) {
			return
		}
		if strings.TrimSpace(value) == "" {
			response.SkippedEmptyFields = append(response.SkippedEmptyFields, field)
			return
		}
		*target = value
		response.ImportedFields = append(response.ImportedFields, field)
	}
	assign("name", parsed.Name, &profile.Name)
	assign("email", parsed.Email, &profile.Email)
	assign("phone", parsed.Phone, &profile.Phone)
	assign("location", parsed.Location, &profile.Location)
	assign("linkedin", parsed.LinkedIn, &profile.LinkedIn)
	assign("github", parsed.GitHub, &profile.GitHub)
	assign("portfolio", parsed.Portfolio, &profile.Portfolio)
	assign("current_title", parsed.CurrentTitle, &profile.CurrentTitle)
	assign("summary", parsed.Summary, &profile.Summary)
	if include("years_experience") && parsed.YearsExperience > 0 {
		profile.YearsExperience = parsed.YearsExperience
		response.ImportedFields = append(response.ImportedFields, "years_experience")
	}
	if include("languages") && len(parsed.Languages) > 0 {
		profile.Languages = parsed.Languages
		response.ImportedFields = append(response.ImportedFields, "languages")
	}
	if include("certifications") && len(parsed.Certifications) > 0 {
		profile.Certifications = parsed.Certifications
		response.ImportedFields = append(response.ImportedFields, "certifications")
	}

	if err = s.Store.SaveProfile(ctx, uid, profile); err != nil {
		internal(w, err)
		return
	}

	if body.ImportExperiences {
		existing, listErr := s.Store.Experiences(ctx, uid)
		if listErr != nil {
			internal(w, listErr)
			return
		}
		seen := map[string]bool{}
		for _, experience := range existing {
			seen[experienceKey(experience)] = true
		}
		for _, experience := range parsed.Experiences {
			if experience.Title == "" || seen[experienceKey(experience)] {
				continue
			}
			if _, addErr := s.Store.AddExperience(ctx, uid, experience); addErr != nil {
				internal(w, addErr)
				return
			}
			seen[experienceKey(experience)] = true
			response.AddedExperiences++
			if experience.Company != "" {
				response.ExcludedEmployers = append(response.ExcludedEmployers, experience.Company)
			}
		}
	}

	if body.ImportSkills {
		existing, listErr := s.Store.Skills(ctx, uid)
		if listErr != nil {
			internal(w, listErr)
			return
		}
		seen := map[string]bool{}
		for _, skill := range existing {
			seen[strings.ToLower(skill.Name)] = true
		}
		for _, skill := range parsed.Skills {
			key := strings.ToLower(strings.TrimSpace(skill.Name))
			if key == "" || seen[key] {
				continue
			}
			if _, addErr := s.Store.AddSkill(ctx, uid, skill); addErr != nil {
				internal(w, addErr)
				return
			}
			seen[key] = true
			response.AddedSkills++
		}
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

// experienceKey identifies a role well enough to avoid importing it twice.
func experienceKey(x models.WorkExperience) string {
	return strings.ToLower(strings.TrimSpace(x.Title) + "|" + strings.TrimSpace(x.Company) + "|" + strings.TrimSpace(x.StartDate))
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
