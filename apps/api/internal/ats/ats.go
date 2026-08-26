// Package ats reads the application form an employer's ATS publishes for a job.
//
// Knowing the form up front is what makes automated applying tractable. Every
// question can be resolved against the answer bank before anything is submitted,
// so the only fields a human sees are the ones genuinely new to them, and a
// select is answered with one of its own options rather than by guessing at a
// page's DOM.
//
// Only read-only, publicly documented endpoints are used. Nothing here submits
// an application.
package ats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Supported providers.
const (
	ProviderGreenhouse      = "greenhouse"
	ProviderLever           = "lever"
	ProviderAshby           = "ashby"
	ProviderSmartRecruiters = "smartrecruiters"
)

// Field types, normalised across providers.
const (
	TypeText        = "text"
	TypeTextarea    = "textarea"
	TypeFile        = "file"
	TypeSelect      = "select"
	TypeMultiSelect = "multiselect"
)

// Roles a field plays in an application, so the composer knows what to put in
// it without re-reading the label.
const (
	RoleQuestion    = "question"
	RoleResume      = "resume"
	RoleCoverLetter = "cover_letter"
)

var (
	// ErrUnsupported means the apply URL is not an ATS this package can read.
	// It is an expected outcome, not a failure: such a job is applied to by
	// hand, and the composer says so.
	ErrUnsupported = errors.New("this employer's application form cannot be read automatically")
	ErrNotFound    = errors.New("the employer's job posting could not be found")
)

// Field is one input on an application form.
type Field struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Role     string   `json:"role"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
	// MaxLength is advisory; zero means the provider did not state one.
	MaxLength int `json:"max_length,omitempty"`
}

// Form is a job's application form as the employer publishes it.
type Form struct {
	Provider string  `json:"provider"`
	Board    string  `json:"board"`
	JobID    string  `json:"job_id"`
	Title    string  `json:"title"`
	Company  string  `json:"company"`
	ApplyURL string  `json:"apply_url"`
	Fields   []Field `json:"fields"`
	// Skipped names questions deliberately left out, so their absence is
	// visible rather than silent.
	Skipped []string `json:"skipped,omitempty"`
}

// Reader fetches published application forms.
type Reader struct{ client *http.Client }

func NewReader() *Reader {
	return &Reader{client: &http.Client{Timeout: 20 * time.Second}}
}

var (
	greenhousePattern      = regexp.MustCompile(`(?i)^(?:job-boards|boards)(?:\.eu)?\.greenhouse\.io$`)
	greenhousePathPattern  = regexp.MustCompile(`(?i)^/([^/]+)/jobs/(\d+)`)
	leverPattern           = regexp.MustCompile(`(?i)^jobs\.lever\.co$`)
	ashbyPattern           = regexp.MustCompile(`(?i)^jobs\.ashbyhq\.com$`)
	smartRecruitersPattern = regexp.MustCompile(`(?i)^jobs\.smartrecruiters\.com$`)
)

// Detect identifies the ATS behind an apply URL without fetching anything.
func Detect(applyURL string) (provider, board, jobID string, ok bool) {
	parsed, err := url.Parse(strings.TrimSpace(applyURL))
	if err != nil || parsed.Host == "" {
		return "", "", "", false
	}
	host := strings.ToLower(parsed.Host)
	path := parsed.Path

	switch {
	case greenhousePattern.MatchString(host):
		// The embedded form carries its identifiers in the query string.
		if strings.Contains(path, "/embed/job_app") {
			query := parsed.Query()
			if board, jobID = query.Get("for"), query.Get("token"); board != "" && jobID != "" {
				return ProviderGreenhouse, board, jobID, true
			}
			return "", "", "", false
		}
		if match := greenhousePathPattern.FindStringSubmatch(path); len(match) == 3 {
			return ProviderGreenhouse, match[1], match[2], true
		}
	case leverPattern.MatchString(host):
		if parts := splitPath(path); len(parts) >= 2 {
			return ProviderLever, parts[0], parts[1], true
		}
	case ashbyPattern.MatchString(host):
		if parts := splitPath(path); len(parts) >= 2 {
			return ProviderAshby, parts[0], parts[1], true
		}
	case smartRecruitersPattern.MatchString(host):
		if parts := splitPath(path); len(parts) >= 2 {
			return ProviderSmartRecruiters, parts[0], parts[1], true
		}
	}
	return "", "", "", false
}

func splitPath(path string) []string {
	var out []string
	for _, part := range strings.Split(path, "/") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Fetch reads the application form for an apply URL.
func (r *Reader) Fetch(ctx context.Context, applyURL string) (Form, error) {
	provider, board, jobID, ok := Detect(applyURL)
	if !ok {
		return Form{}, ErrUnsupported
	}
	switch provider {
	case ProviderGreenhouse:
		return r.greenhouse(ctx, board, jobID, applyURL)
	default:
		// The other providers are detected so the composer can report which ATS
		// a job uses, but their form schemas are not published the way
		// Greenhouse's are.
		return Form{Provider: provider, Board: board, JobID: jobID, ApplyURL: applyURL}, ErrUnsupported
	}
}

// greenhouseQuestion mirrors the public board API.
type greenhouseQuestion struct {
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Fields   []struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Values []struct {
			Label any `json:"label"`
			Value any `json:"value"`
		} `json:"values"`
	} `json:"fields"`
}

func (r *Reader) greenhouse(ctx context.Context, board, jobID, applyURL string) (Form, error) {
	endpoint := fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs/%s?questions=true",
		url.PathEscape(board), url.PathEscape(jobID))

	var payload struct {
		Title       string               `json:"title"`
		CompanyName string               `json:"company_name"`
		Questions   []greenhouseQuestion `json:"questions"`
		// Compliance and demographic blocks are read so they can be counted as
		// skipped, never answered.
		Compliance []struct {
			Questions []greenhouseQuestion `json:"questions"`
		} `json:"compliance"`
		Demographic struct {
			Questions []greenhouseQuestion `json:"questions"`
		} `json:"demographic_questions"`
	}
	if err := r.get(ctx, endpoint, &payload); err != nil {
		return Form{}, err
	}

	form := Form{
		Provider: ProviderGreenhouse, Board: board, JobID: jobID,
		Title: payload.Title, Company: payload.CompanyName, ApplyURL: applyURL,
	}
	for _, question := range payload.Questions {
		field, ok := convertGreenhouse(question)
		if !ok {
			form.Skipped = append(form.Skipped, question.Label)
			continue
		}
		form.Fields = append(form.Fields, field)
	}

	// Equal-opportunity, veteran, disability and demographic questions are
	// deliberately never auto-answered. They concern protected characteristics,
	// they are voluntary, and answering them on someone's behalf is not a
	// decision this software gets to make.
	for _, block := range payload.Compliance {
		for _, question := range block.Questions {
			form.Skipped = append(form.Skipped, question.Label)
		}
	}
	for _, question := range payload.Demographic.Questions {
		form.Skipped = append(form.Skipped, question.Label)
	}
	return form, nil
}

var resumePattern = regexp.MustCompile(`(?i)\b(resume|cv|curriculum)\b`)
var coverLetterPattern = regexp.MustCompile(`(?i)cover\s*letter`)

// convertGreenhouse normalises one published question into a Field.
func convertGreenhouse(question greenhouseQuestion) (Field, bool) {
	label := strings.TrimSpace(question.Label)
	if label == "" || len(question.Fields) == 0 {
		return Field{}, false
	}
	// A question can expose several inputs (a resume as either an upload or
	// pasted text). The first usable one wins, except that an upload is always
	// preferred for a document.
	chosen := -1
	for index, candidate := range question.Fields {
		switch candidate.Type {
		case "input_hidden":
			continue
		case "input_file":
			chosen = index
		default:
			if chosen < 0 {
				chosen = index
			}
		}
	}
	if chosen < 0 {
		return Field{}, false
	}
	source := question.Fields[chosen]

	field := Field{Name: source.Name, Label: label, Required: question.Required, Role: RoleQuestion}
	switch source.Type {
	case "input_text":
		field.Type = TypeText
	case "textarea":
		field.Type = TypeTextarea
	case "input_file":
		field.Type = TypeFile
	case "multi_value_single_select":
		field.Type = TypeSelect
	case "multi_value_multi_select":
		field.Type = TypeMultiSelect
	default:
		return Field{}, false
	}
	for _, value := range source.Values {
		if text := stringify(value.Label); text != "" {
			field.Options = append(field.Options, text)
		}
	}
	switch {
	case resumePattern.MatchString(label):
		field.Role = RoleResume
	case coverLetterPattern.MatchString(label):
		field.Role = RoleCoverLetter
	}
	return field, true
}

// stringify accepts the label shapes the API uses, which are not always strings.
func stringify(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%.0f", typed))
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func (r *Reader) get(ctx context.Context, endpoint string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return fmt.Errorf("could not reach the employer's job board: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("the employer's job board returned %d", response.StatusCode)
	}
	// Boards with very long option lists run to a few megabytes.
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("the employer's job board returned an unreadable response: %w", err)
	}
	return nil
}
