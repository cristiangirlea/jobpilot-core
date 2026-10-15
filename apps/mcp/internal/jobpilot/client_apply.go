package jobpilot

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// composeTimeout is generous because composing reads the employer's form,
// decides on a CV, may render one, and may call a model for the letter.
const composeTimeout = 2 * time.Minute

func (c *Client) AIStatus(ctx context.Context) (AIStatus, error) {
	var out AIStatus
	return out, c.get(ctx, "/api/ai/status", &out)
}

// Answers lists the bank. status is pending, approved, or empty for all.
func (c *Client) Answers(ctx context.Context, status string) ([]AnswerEntry, error) {
	path := "/api/answers"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var out []AnswerEntry
	return out, c.get(ctx, path, &out)
}

type answerWrite struct {
	QuestionText string   `json:"question_text"`
	Answer       string   `json:"answer"`
	RoleFamily   string   `json:"role_family,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	JobID        string   `json:"job_id,omitempty"`
	Technologies []string `json:"technologies,omitempty"`
	Approved     *bool    `json:"approved"`
}

func (c *Client) SaveAnswer(ctx context.Context, in answerWrite) (AnswerEntry, error) {
	var out AnswerEntry
	return out, c.do(ctx, http.MethodPost, "/api/answers", in, &out, c.timeout)
}

type answerPatch struct {
	Answer   string `json:"answer"`
	Approved *bool  `json:"approved"`
	Scope    string `json:"scope,omitempty"`
	JobID    string `json:"job_id,omitempty"`
}

func (c *Client) UpdateAnswer(ctx context.Context, id string, in answerPatch) (AnswerEntry, error) {
	var out AnswerEntry
	return out, c.do(ctx, http.MethodPatch, "/api/answers/"+url.PathEscape(id), in, &out, c.timeout)
}

type resolveRequest struct {
	Questions []Question `json:"questions"`
}

type Question struct {
	Text      string   `json:"text"`
	Kind      string   `json:"kind,omitempty"`
	Options   []string `json:"options,omitempty"`
	MaxLength int      `json:"max_length,omitempty"`
}

// ResolveAnswers answers a set of screening questions for one job. The API
// persists any model-written draft unapproved, so this is a write.
func (c *Client) ResolveAnswers(ctx context.Context, jobID string, questions []Question) (ResolvedAnswers, error) {
	var out ResolvedAnswers
	body := resolveRequest{Questions: questions}
	return out, c.do(ctx, http.MethodPost, "/api/jobs/"+url.PathEscape(jobID)+"/answers", body, &out, composeTimeout)
}

func (c *Client) CVs(ctx context.Context) ([]CVDocument, error) {
	var out []CVDocument
	return out, c.get(ctx, "/api/cv", &out)
}

func (c *Client) CV(ctx context.Context, id string) (CVDocument, error) {
	var out CVDocument
	return out, c.get(ctx, "/api/cv/"+url.PathEscape(id), &out)
}

// ApproveCV always sends a body: the endpoint decodes one, so a bodiless
// POST is a 400.
func (c *Client) ApproveCV(ctx context.Context, id string, approved bool) (CVDocument, error) {
	var out CVDocument
	body := map[string]bool{"approved": approved}
	return out, c.do(ctx, http.MethodPost, "/api/cv/"+url.PathEscape(id)+"/approve", body, &out, c.timeout)
}

func (c *Client) TailorCV(ctx context.Context, jobID string) (TailoredCV, error) {
	var out TailoredCV
	return out, c.do(ctx, http.MethodPost, "/api/jobs/"+url.PathEscape(jobID)+"/cv", nil, &out, composeTimeout)
}

func (c *Client) Compose(ctx context.Context, jobID string) (OutboxEntry, error) {
	var out OutboxEntry
	return out, c.do(ctx, http.MethodPost, "/api/jobs/"+url.PathEscape(jobID)+"/compose", nil, &out, composeTimeout)
}

func (c *Client) Outbox(ctx context.Context, status string) ([]OutboxEntry, error) {
	path := "/api/outbox"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var out []OutboxEntry
	return out, c.get(ctx, path, &out)
}

func (c *Client) OutboxEntry(ctx context.Context, id string) (OutboxEntry, error) {
	var out OutboxEntry
	return out, c.get(ctx, "/api/outbox/"+url.PathEscape(id), &out)
}

func (c *Client) CancelOutboxEntry(ctx context.Context, id string) (OutboxEntry, error) {
	var out OutboxEntry
	return out, c.do(ctx, http.MethodPost, "/api/outbox/"+url.PathEscape(id)+"/cancel", nil, &out, c.timeout)
}

func (c *Client) DetectBoard(ctx context.Context, raw string) (DetectedBoard, error) {
	var out DetectedBoard
	body := map[string]string{"url": raw}
	return out, c.do(ctx, http.MethodPost, "/api/discovery/detect", body, &out, c.timeout)
}

func (c *Client) BoardSuggestions(ctx context.Context) ([]BoardSuggestion, error) {
	var out []BoardSuggestion
	return out, c.get(ctx, "/api/discovery/suggestions", &out)
}
