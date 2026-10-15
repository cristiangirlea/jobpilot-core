package jobpilot

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Notification is one thing that waits, or waited, for the user, as the API
// returns it. Kind is open: answer, cv and outbox today, more later.
type Notification struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	SubjectID  string         `json:"subject_id"`
	Title      string         `json:"title"`
	Detail     map[string]any `json:"detail"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	ResolvedAt *time.Time     `json:"resolved_at,omitempty"`
}

type NotificationFeed struct {
	Open  int            `json:"open"`
	Items []Notification `json:"items"`
	Next  string         `json:"next,omitempty"`
}

// PendingDecision is a notification with the way an agent may help resolve it.
type PendingDecision struct {
	Notification
	HowToResolve string `json:"how_to_resolve"`
}

type PendingDecisions struct {
	Open  int               `json:"open"`
	Items []PendingDecision `json:"items"`
	// Next, when set, is the after value that fetches the following page.
	Next string `json:"next,omitempty"`
}

type pendingDecisionsInput struct {
	IncludeResolved bool   `json:"include_resolved,omitempty" jsonschema:"Also return items already resolved: every item, open or resolved, newest first"`
	Limit           int    `json:"limit,omitempty" jsonschema:"Maximum items to return: default 20, maximum 100. open always counts every open item"`
	After           string `json:"after,omitempty" jsonschema:"The next value from the previous call, to fetch the following page"`
}

// Notifications reads one page of the feed. state is open or all; after is the
// previous page's next, or empty for the first page.
func (c *Client) Notifications(ctx context.Context, state, after string, limit int) (NotificationFeed, error) {
	q := url.Values{"state": {state}, "limit": {strconv.Itoa(limit)}}
	if after != "" {
		q.Set("after", after)
	}
	var out NotificationFeed
	return out, c.get(ctx, "/api/notifications?"+q.Encode(), &out)
}

// howToResolve says what an agent may do about an item of each kind. A kind
// this server does not know yet is still shown, and left to the web app.
func howToResolve(n Notification) string {
	if n.ResolvedAt != nil {
		return "Nothing: this item is resolved."
	}
	switch n.Kind {
	case "answer":
		return "Show the user the question and detail.answer word for word. Call approve_answer with answer_id " + n.SubjectID + " only when the user approves that exact wording, passing their edited wording if they change it."
	case "cv":
		return "Call get_cv with cv_id " + n.SubjectID + " and show the user what it says. Call approve_cv only when the user approves it."
	case "outbox":
		return "Call get_outbox_entry with outbox_id " + n.SubjectID + " and show the user every blocker and needs_review item in the exact words returned. Approving and sending happen only in the web app, on the Outbox page; cancel_outbox_entry withdraws it if the user asks."
	}
	if href, _ := n.Detail["href"].(string); href != "" {
		return fmt.Sprintf("Show the user the title and detail; they resolve it in the web app at %s.", href)
	}
	return "Show the user the title and detail; they resolve it in the web app."
}

func addNotificationTools(s *mcp.Server, c *Client) {
	addTool(s, &mcp.Tool{
		Name:  "list_pending_decisions",
		Title: "List what waits for the user",
		Description: "Return everything waiting for the user's decision, oldest first: unapproved answers and CVs, and composed applications to review or approve. " +
			"Each item has a kind, a title, detail, and how_to_resolve, which says what you may do about it. Ask the user about one item at a time, show unapproved text and blockers in the exact words returned, and never approve anything without the user's explicit word. " +
			"Items resolve on their own once the underlying thing is approved, cancelled or deleted. When next is set, more items follow: call again with after set to it.",
		Annotations: readOnly("List what waits for the user"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pendingDecisionsInput) (*mcp.CallToolResult, PendingDecisions, error) {
		state := "open"
		if in.IncludeResolved {
			state = "all"
		}
		feed, err := c.Notifications(ctx, state, strings.TrimSpace(in.After), pageSize(in.Limit))
		if err != nil {
			return nil, PendingDecisions{}, toolError(err)
		}
		out := PendingDecisions{Open: feed.Open, Next: feed.Next, Items: make([]PendingDecision, 0, len(feed.Items))}
		for _, n := range feed.Items {
			out.Items = append(out.Items, PendingDecision{Notification: n, HowToResolve: howToResolve(n)})
		}
		return nil, out, nil
	})
}
