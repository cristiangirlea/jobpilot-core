// Package ats reads the application form an employer's ATS publishes for a job.
//
// Knowing the form up front is what makes automated applying tractable. Every
// question can be resolved against the answer bank before anything is submitted,
// so the only fields a human sees are the ones genuinely new to them, and a
// select is answered with one of its own options rather than by guessing at a
// page's DOM.
//
// Only read-only, public endpoints are used: Greenhouse's documented board API,
// and for Ashby the endpoint its own public job page calls. Both return what
// any visitor to the posting sees. Nothing here submits an application.
package ats

import (
	"bytes"
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
	ProviderRecruitee       = "recruitee"
	ProviderPersonio        = "personio"

	// Aggregators. Discovery can crawl these for jobs, but their links stay on
	// the aggregator rather than reaching an employer's application form, so
	// they are never returned by Detect.
	ProviderRemotive  = "remotive"
	ProviderArbeitnow = "arbeitnow"
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
	// RoleDeclaration marks an undertaking the applicant makes personally, such
	// as confirming the writing is their own. Never answered automatically.
	RoleDeclaration = "declaration"
	// RolePersonal marks a choice only the applicant makes, such as a
	// salutation that states their gender. Never answered automatically.
	RolePersonal = "personal"
)

var (
	// ErrUnsupported means the apply URL is not an ATS this package can read.
	// It is an expected outcome, not a failure: such a job is applied to by
	// hand, and the composer says so.
	ErrUnsupported = errors.New("this employer's application form cannot be read automatically")
	ErrNotFound    = errors.New("the employer's job posting could not be found")

	// errUnreadable marks an answer that is not the JSON a board API sends,
	// which from an unknown host means it is not that board at all.
	errUnreadable = errors.New("the employer's job board returned an unreadable response")
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

// greenhouseAPI is the public board API. A Reader may be pointed elsewhere
// so a test can serve a form itself; nothing else about the fetch changes.
const greenhouseAPI = "https://boards-api.greenhouse.io"

// Reader fetches published application forms.
type Reader struct {
	client         *http.Client
	greenhouseBase string
	// ashbyBase and recruiteeBase replace those origins, from configuration
	// or a test; greenhouseLegacyBase and personioBase only in tests, and
	// public replaces the guarded client for employer domains.
	ashbyBase            string
	greenhouseLegacyBase string
	recruiteeBase        string
	personioBase         string
	public               *http.Client
}

// publicClient is the client for hosts named only by a job link, which
// refuses private addresses.
func (r *Reader) publicClient() *http.Client {
	if r.public != nil {
		return r.public
	}
	return publicClient
}

func NewReader() *Reader {
	return &Reader{client: &http.Client{Timeout: 20 * time.Second}}
}

// WithGreenhouseBase replaces the Greenhouse origin. An empty base keeps the
// public service, so passing configuration through unconditionally is safe.
func (r *Reader) WithGreenhouseBase(base string) *Reader {
	r.greenhouseBase = strings.TrimSuffix(strings.TrimSpace(base), "/")
	return r
}

// WithAshbyBase replaces the Ashby job page origin the same way.
func (r *Reader) WithAshbyBase(base string) *Reader {
	r.ashbyBase = strings.TrimSuffix(strings.TrimSpace(base), "/")
	return r
}

// WithRecruiteeBase sends every Recruitee careers site's API requests to one
// origin. Recruitee has no single public origin, so an empty base keeps each
// site's own host.
func (r *Reader) WithRecruiteeBase(base string) *Reader {
	r.recruiteeBase = strings.TrimSuffix(strings.TrimSpace(base), "/")
	return r
}

// ValidateGreenhouseBase rejects an origin the reader could not use, so a
// configuration mistake fails at startup with its real cause instead of
// surfacing later as an unreadable form on every composed application.
func ValidateGreenhouseBase(base string) error {
	return validateBase("ATS_GREENHOUSE_URL", greenhouseAPI, base)
}

// ValidateAshbyBase checks ATS_ASHBY_URL as ValidateGreenhouseBase does.
func ValidateAshbyBase(base string) error {
	return validateBase("ATS_ASHBY_URL", ashbyAPI, base)
}

// ValidateRecruiteeBase checks ATS_RECRUITEE_URL as ValidateGreenhouseBase does.
func ValidateRecruiteeBase(base string) error {
	return validateBase("ATS_RECRUITEE_URL", "https://acme.recruitee.com", base)
}

func validateBase(setting, example, base string) error {
	base = strings.TrimSpace(base)
	if base == "" {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s must be an http or https origin such as %s, not %q", setting, example, base)
	}
	return nil
}

func (r *Reader) greenhouseOrigin() string {
	if r.greenhouseBase == "" {
		return greenhouseAPI
	}
	return r.greenhouseBase
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
	case personioPattern.MatchString(host):
		if match := personioJobPath.FindStringSubmatch(path); match != nil {
			return ProviderPersonio, personioPattern.FindStringSubmatch(host)[1], match[1], true
		}
	case recruiteePattern.MatchString(host):
		if match := recruiteeOfferPath.FindStringSubmatch(path); match != nil {
			return ProviderRecruitee, recruiteePattern.FindStringSubmatch(host)[1], match[1], true
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
		// A Greenhouse posting on the employer's own domain carries only its
		// job id; Greenhouse names the board, and the form is read from there.
		if id, isGreenhouse := greenhouseJobID(applyURL); isGreenhouse {
			board, err := r.greenhouseBoard(ctx, id)
			if err != nil {
				return Form{Provider: ProviderGreenhouse, JobID: id, ApplyURL: applyURL}, err
			}
			return r.greenhouse(ctx, board, id, applyURL)
		}
		// Recruitee also serves careers sites on employers' own domains, at
		// the same /o/<slug> paths; its API there has to confirm it.
		if _, _, isOffer := recruiteeOfferAt(applyURL); isOffer {
			return r.recruitee(ctx, applyURL, false)
		}
		return Form{}, ErrUnsupported
	}
	switch provider {
	case ProviderGreenhouse:
		return r.greenhouse(ctx, board, jobID, applyURL)
	case ProviderAshby:
		return r.ashby(ctx, board, jobID, applyURL)
	case ProviderRecruitee:
		return r.recruitee(ctx, applyURL, true)
	case ProviderPersonio:
		return r.personio(ctx, hostOf(applyURL), board, jobID, applyURL)
	default:
		// The other providers are detected so the composer can report which ATS
		// a job uses, but their forms are not read yet.
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
	endpoint := fmt.Sprintf("%s/v1/boards/%s/jobs/%s?questions=true",
		r.greenhouseOrigin(), url.PathEscape(board), url.PathEscape(jobID))

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
		if protectedPattern.MatchString(question.Label) {
			form.Skipped = append(form.Skipped, question.Label)
			continue
		}
		field, ok := convertGreenhouse(question)
		if !ok {
			form.Skipped = append(form.Skipped, question.Label)
			continue
		}
		form.admit(field)
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

// admit adds a question to the form, the same way for every provider. One
// about a protected characteristic is only counted as skipped, and a
// declaration is marked so it is never answered for the applicant.
func (f *Form) admit(field Field) {
	if protectedPattern.MatchString(field.Label) {
		f.Skipped = append(f.Skipped, field.Label)
		return
	}
	if declarationPattern.MatchString(field.Label) {
		field.Role = RoleDeclaration
	}
	f.Fields = append(f.Fields, field)
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
	return r.getWith(ctx, r.client, endpoint, out)
}

func (r *Reader) getWith(ctx context.Context, client *http.Client, endpoint string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	return r.doWith(client, request, out)
}

// post sends body as JSON, for the boards whose form is read through a query.
func (r *Reader) post(ctx context.Context, endpoint string, body, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	return r.do(request, out)
}

func (r *Reader) do(request *http.Request, out any) error {
	return r.doWith(r.client, request, out)
}

func (r *Reader) doWith(client *http.Client, request *http.Request, out any) error {
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
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
		return fmt.Errorf("%w: %w", errUnreadable, err)
	}
	return nil
}

// origin is where a reader sends requests for a careers host: always https,
// or a test's stand-in when one is set.
func origin(base, host string) string {
	if base != "" {
		return base
	}
	return "https://" + host
}

// hostOf is a link's host, port included.
func hostOf(link string) string {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return ""
	}
	return parsed.Host
}
