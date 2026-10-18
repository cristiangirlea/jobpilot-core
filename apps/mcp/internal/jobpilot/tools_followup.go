package jobpilot

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type FollowUpDraft struct {
	ID            string    `json:"id"`
	ApplicationID string    `json:"application_id"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"created_at"`
}

func addFollowUpTools(s *mcp.Server, c *Client) {
	addTool(s, &mcp.Tool{Name: "draft_follow_up", Title: "Draft a follow-up", Description: "Save a short follow-up draft from the application's sent materials and profile. Applied or interview applications only, at most two drafts per application. Show the draft to the user; it is never sent. Nothing is emailed, uploaded or posted.", Annotations: writeTool("Draft a follow-up", false)}, func(ctx context.Context, _ *mcp.CallToolRequest, in applicationInput) (*mcp.CallToolResult, FollowUpDraft, error) {
		id := strings.TrimSpace(in.ApplicationID)
		if id == "" {
			return nil, FollowUpDraft{}, fmt.Errorf("application_id is required")
		}
		var out FollowUpDraft
		err := c.do(ctx, http.MethodPost, "/api/applications/"+url.PathEscape(id)+"/follow-up", struct{}{}, &out, c.timeout)
		if err != nil {
			return nil, FollowUpDraft{}, toolError(err)
		}
		return nil, out, nil
	})
}
