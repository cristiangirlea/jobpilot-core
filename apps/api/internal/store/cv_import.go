package store

import (
	"context"
	"encoding/json"
	"strings"

	"jobpilot/core/api/internal/cv"
	"jobpilot/core/api/internal/models"
)

type CVImportRequest struct {
	// Fields names which parsed values to copy. Empty means all of them.
	Fields []string `json:"fields"`
	// Importing experience also excludes those employers from job matches,
	// which is a safety rule, so it is opt-in and reported back explicitly.
	ImportExperiences bool `json:"import_experiences"`
	ImportSkills      bool `json:"import_skills"`
}

type CVImportResponse struct {
	Profile            models.Profile `json:"profile"`
	ImportedFields     []string       `json:"imported_fields"`
	AddedExperiences   int            `json:"added_experiences"`
	AddedSkills        int            `json:"added_skills"`
	ExcludedEmployers  []string       `json:"excluded_employers"`
	SkippedEmptyFields []string       `json:"skipped_empty_fields"`
}

// ImportCV serializes the full read/merge/write and additive child changes with
// repair. Matching runs in the handler only after this transaction commits.
func (s *Store) ImportCV(ctx context.Context, uid, id string, body CVImportRequest) (CVImportResponse, error) {
	response := CVImportResponse{ImportedFields: []string{}, ExcludedEmployers: []string{}, SkippedEmptyFields: []string{}}
	ctx, tx, cleanup, err := repairTx(ctx, s, uid)
	if err != nil {
		return response, err
	}
	defer cleanup()
	ps := &profileStore{tx}
	document, err := scanCV(tx.QueryRow(ctx, `SELECT `+cvColumns+` FROM cv_documents WHERE user_id=$1 AND id=$2 FOR UPDATE`, uid, id))
	if err != nil {
		return response, err
	}
	var parsed cv.Parsed
	if raw, marshalErr := json.Marshal(document.Parsed); marshalErr == nil {
		_ = json.Unmarshal(raw, &parsed)
	}

	profile, err := ps.Profile(ctx, uid)
	if err != nil {
		return response, err
	}

	wanted := map[string]bool{}
	for _, field := range body.Fields {
		wanted[strings.ToLower(strings.TrimSpace(field))] = true
	}
	include := func(field string) bool { return len(wanted) == 0 || wanted[field] }

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

	if err = ps.SaveProfile(ctx, uid, profile); err != nil {
		return response, err
	}

	if body.ImportExperiences {
		existing, listErr := ps.Experiences(ctx, uid)
		if listErr != nil {
			return response, listErr
		}
		seen := map[string]bool{}
		for _, experience := range existing {
			seen[experienceKey(experience)] = true
		}
		for _, experience := range parsed.Experiences {
			if experience.Title == "" || seen[experienceKey(experience)] {
				continue
			}
			if _, addErr := ps.AddExperience(ctx, uid, experience); addErr != nil {
				return response, addErr
			}
			seen[experienceKey(experience)] = true
			response.AddedExperiences++
			if experience.Company != "" {
				response.ExcludedEmployers = append(response.ExcludedEmployers, experience.Company)
			}
		}
	}

	if body.ImportSkills {
		existing, listErr := ps.Skills(ctx, uid)
		if listErr != nil {
			return response, listErr
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
			if _, addErr := ps.AddSkill(ctx, uid, skill); addErr != nil {
				return response, addErr
			}
			seen[key] = true
			response.AddedSkills++
		}
	}

	response.Profile, err = ps.Profile(ctx, uid)
	if err != nil {
		return response, err
	}
	return response, tx.Commit(ctx)
}

// experienceKey identifies a role well enough to avoid importing it twice.
func experienceKey(x models.WorkExperience) string {
	return strings.ToLower(strings.TrimSpace(x.Title) + "|" + strings.TrimSpace(x.Company) + "|" + strings.TrimSpace(x.StartDate))
}
