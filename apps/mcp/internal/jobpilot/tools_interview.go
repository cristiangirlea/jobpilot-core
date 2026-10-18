package jobpilot

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type InterviewDocuments struct {
	OutboxID    string         `json:"outbox_id"`
	SentAt      *time.Time     `json:"sent_at,omitempty"`
	CVText      *string        `json:"cv_text"`
	CoverLetter string         `json:"cover_letter"`
	Answers     []OutboxAnswer `json:"answers"`
}

type STARPrompt struct {
	Requirement string          `json:"requirement"`
	Experience  *WorkExperience `json:"experience,omitempty"`
	Project     *Project        `json:"project,omitempty"`
	Situation   string          `json:"situation"`
	Task        string          `json:"task"`
	Action      string          `json:"action"`
	Result      string          `json:"result"`
}

type GapPrompt struct {
	Gap    string `json:"gap"`
	Prompt string `json:"prompt"`
}

type InterviewPack struct {
	ApplicationID string              `json:"application_id"`
	Posting       JobWithMatch        `json:"posting"`
	Requirements  []string            `json:"requirements"`
	SentDocuments *InterviewDocuments `json:"sent_documents"`
	STARPrompts   []STARPrompt        `json:"star_prompts"`
	GapPrompts    []GapPrompt         `json:"gap_prompts"`
	Questions     []string            `json:"questions"`
	Notices       []string            `json:"notices"`
}

func addInterviewTools(s *mcp.Server, c *Client) {
	addTool(s, &mcp.Tool{
		Name: "prepare_interview", Title: "Prepare for an interview",
		Description: "Read an interview preparation pack for an application in interview status: the stored posting and match, available sent documents, STAR prompts, honest gap bridges, and questions grounded in the posting. Prompts are for the user to fill in with real examples and results, never invented claims. Use only facts from the profile, CV and job; do not fill in situation, task, action or result for the user. Show needs_review, unapproved items and blockers in the exact words returned. Read the notices about unavailable documents and historical snapshots. Nothing is stored, sent or researched on the web.",
		Annotations: readOnly("Prepare for an interview"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in applicationInput) (*mcp.CallToolResult, InterviewPack, error) {
		id := strings.TrimSpace(in.ApplicationID)
		if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\?#") {
			return nil, InterviewPack{}, errors.New("application_id is required and must be a single application ID")
		}
		var out InterviewPack
		err := c.get(ctx, "/api/applications/"+url.PathEscape(id)+"/interview-prep", &out)
		// An unregistered route uses Go's plain-text 404. An application that
		// is missing or belongs to another user gets the API's JSON error,
		// "Record not found."; preserve that refusal without probing ownership.
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && apiErr.Message == "404 page not found" {
			err = errors.New("The JobPilot API does not offer interview preparation yet. Upgrade the API to use prepare_interview.")
		}
		return nil, out, toolError(err)
	})
}
