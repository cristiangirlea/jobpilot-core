package ats

import (
	"context"
	"fmt"
	"strings"
)

// ashbyAPI serves the job pages on jobs.ashbyhq.com. A posting's application
// form is read from the GraphQL endpoint that public page itself calls: it
// needs no account and returns only what the page shows anyone.
const ashbyAPI = "https://jobs.ashbyhq.com"

// ashbyPostingQuery asks for the form and nothing else. Survey forms hold the
// voluntary equal-opportunity questions; they are read only to be counted.
const ashbyPostingQuery = `query ApiJobPosting($organizationHostedJobsPageName: String!, $jobPostingId: String!) {
  jobPosting(organizationHostedJobsPageName: $organizationHostedJobsPageName, jobPostingId: $jobPostingId) {
    title
    applicationForm { sections { fieldEntries { ... on FormFieldEntry { isRequired field } } } }
    surveyForms { sections { fieldEntries { ... on FormFieldEntry { field } } } }
  }
}`

type ashbyField struct {
	Path             string `json:"path"`
	Title            string `json:"title"`
	Type             string `json:"type"`
	IsDeactivated    bool   `json:"isDeactivated"`
	SelectableValues []struct {
		Label string `json:"label"`
	} `json:"selectableValues"`
}

type ashbySection struct {
	FieldEntries []struct {
		IsRequired bool       `json:"isRequired"`
		Field      ashbyField `json:"field"`
	} `json:"fieldEntries"`
}

func (r *Reader) ashbyOrigin() string {
	if r.ashbyBase == "" {
		return ashbyAPI
	}
	return r.ashbyBase
}

func (r *Reader) ashby(ctx context.Context, board, jobID, applyURL string) (Form, error) {
	request := map[string]any{
		"operationName": "ApiJobPosting",
		"variables":     map[string]string{"organizationHostedJobsPageName": board, "jobPostingId": jobID},
		"query":         ashbyPostingQuery,
	}
	var payload struct {
		Data struct {
			JobPosting *struct {
				Title           string `json:"title"`
				ApplicationForm struct {
					Sections []ashbySection `json:"sections"`
				} `json:"applicationForm"`
				SurveyForms []struct {
					Sections []ashbySection `json:"sections"`
				} `json:"surveyForms"`
			} `json:"jobPosting"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := r.post(ctx, r.ashbyOrigin()+"/api/non-user-graphql?op=ApiJobPosting", request, &payload); err != nil {
		return Form{}, err
	}
	if len(payload.Errors) > 0 {
		return Form{}, fmt.Errorf("the employer's job board refused the request: %s", payload.Errors[0].Message)
	}
	// Ashby answers a posting it no longer has, or a board that does not
	// exist, with null rather than a 404.
	posting := payload.Data.JobPosting
	if posting == nil {
		return Form{}, ErrNotFound
	}

	form := Form{Provider: ProviderAshby, Board: board, JobID: jobID, Title: posting.Title, ApplyURL: applyURL}
	for _, section := range posting.ApplicationForm.Sections {
		for _, entry := range section.FieldEntries {
			label := strings.TrimSpace(entry.Field.Title)
			// A deactivated field is not on the page an applicant sees.
			if entry.Field.IsDeactivated || label == "" {
				continue
			}
			if protectedPattern.MatchString(label) {
				form.Skipped = append(form.Skipped, label)
				continue
			}
			field := convertAshby(entry.Field, label, entry.IsRequired)
			if declarationPattern.MatchString(label) {
				field.Role = RoleDeclaration
			}
			form.Fields = append(form.Fields, field)
		}
	}
	// Equal-opportunity surveys are voluntary and concern protected
	// characteristics, so, as on Greenhouse, they are counted and never
	// answered.
	for _, survey := range posting.SurveyForms {
		for _, section := range survey.Sections {
			for _, entry := range section.FieldEntries {
				if label := strings.TrimSpace(entry.Field.Title); label != "" {
					form.Skipped = append(form.Skipped, label)
				}
			}
		}
	}
	return form, nil
}

// convertAshby normalises one Ashby field. A type this reader does not know is
// kept as text rather than dropped: leaving out a required question would
// make an application look complete when the employer's form is not.
func convertAshby(source ashbyField, label string, required bool) Field {
	field := Field{Name: source.Path, Label: label, Type: TypeText, Required: required, Role: RoleQuestion}
	switch source.Type {
	case "LongText":
		field.Type = TypeTextarea
	case "File":
		field.Type = TypeFile
	case "Boolean":
		field.Type, field.Options = TypeSelect, []string{"Yes", "No"}
	case "ValueSelect":
		field.Type = TypeSelect
	case "MultiValueSelect":
		field.Type = TypeMultiSelect
	}
	for _, value := range source.SelectableValues {
		if text := strings.TrimSpace(value.Label); text != "" {
			field.Options = append(field.Options, text)
		}
	}
	// Only an upload takes the CV or the letter: a text field labelled
	// "resume link" wants a URL, not the attached document.
	if field.Type == TypeFile {
		switch {
		case source.Path == "_systemfield_resume" || resumePattern.MatchString(label):
			field.Role = RoleResume
		case coverLetterPattern.MatchString(label):
			field.Role = RoleCoverLetter
		}
	}
	return field
}
