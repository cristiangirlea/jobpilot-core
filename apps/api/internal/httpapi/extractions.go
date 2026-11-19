package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/models"
)

const extractionPostingChanged = "The posting changed since it was read."

// pendingExtractions lists the postings the configured model has not read yet.
func (s *Server) pendingExtractions(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	limit := 50
	if err == nil && query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
	}
	if err != nil || limit < 1 || limit > 200 {
		problem(w, 400, "Limit must be an integer between 1 and 200.")
		return
	}

	out := struct {
		Model         string                     `json:"model"`
		PromptVersion string                     `json:"prompt_version"`
		Jobs          []models.PendingExtraction `json:"jobs"`
	}{
		Model:         s.ExtractionModel,
		PromptVersion: extraction.PromptVersion,
		Jobs:          []models.PendingExtraction{},
	}
	if s.ExtractionModel != "" {
		list := s.pendingJobs
		if list == nil {
			list = s.Store.PendingExtractions
		}
		out.Jobs, err = list(r.Context(), s.ExtractionModel, extraction.PromptVersion, limit)
		if err != nil {
			internal(w, err)
			return
		}
	}
	write(w, 200, out)
}

// saveExtraction verifies a model's answer against the stored posting, never against text the client sends.
func (s *Server) saveExtraction(w http.ResponseWriter, r *http.Request) {
	if s.ExtractionModel == "" {
		problem(w, 409, extraction.NotConfigured)
		return
	}
	var in struct {
		JobID    string  `json:"job_id"`
		TextHash string  `json:"text_hash"`
		Answer   *string `json:"answer"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF {
		problem(w, 400, "Invalid extraction request.")
		return
	}
	if in.JobID == "" || in.TextHash == "" || in.Answer == nil {
		problem(w, 400, "Job ID, text hash and answer are required.")
		return
	}

	ctx := r.Context()
	title, description, err := s.Store.ExtractionSource(ctx, in.JobID)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Record not found.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	if extraction.Hash(title, description) != in.TextHash {
		problem(w, 409, extractionPostingChanged)
		return
	}

	x := models.JobExtraction{
		JobID:         in.JobID,
		Model:         s.ExtractionModel,
		PromptVersion: extraction.PromptVersion,
		TextHash:      in.TextHash,
		Status:        "ok",
	}
	result, err := extraction.Verify(title, description, []byte(*in.Answer))
	// PostgreSQL JSONB cannot store a NUL in the model's constraint wording.
	hasNUL := slices.ContainsFunc(result.Constraints, func(c models.ExtractedConstraint) bool {
		return strings.ContainsRune(c.Value, 0)
	})
	if err != nil || hasNUL {
		x.Status = "rejected"
		x.Reason = "invalid"
		var refusal *extraction.Error
		if errors.As(err, &refusal) {
			x.Reason = refusal.Reason
		}
	} else {
		x.Extraction = &result
	}

	saved, err := s.Store.SaveExtraction(ctx, x)
	if err != nil {
		internal(w, err)
		return
	}
	if !saved {
		problem(w, 409, extractionPostingChanged)
		return
	}
	if x.Status == "rejected" {
		write(w, 200, map[string]string{"status": x.Status, "reason": x.Reason})
		return
	}
	write(w, 200, struct {
		Status  string `json:"status"`
		Dropped int    `json:"dropped"`
	}{Status: x.Status, Dropped: result.Dropped})
}
