package jobpilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type repairSourceInput struct {
	SourceCVID string `json:"source_cv_id"`
}
type repairReadInput struct {
	ProposalID string `json:"proposal_id,omitempty"`
}
type repairResult struct {
	Proposal  *repairView     `json:"proposal,omitempty"`
	Proposals json.RawMessage `json:"proposals,omitempty"`
}

// repairView is what an agent gets of a proposal. The API's record also holds
// the CV's text and two whole profile snapshots, which are for the review page:
// here they would be most of a host's result limit, so the lists are reduced to
// what was added and removed.
type repairView struct {
	ID       string             `json:"id"`
	Status   string             `json:"status"`
	Created  string             `json:"created_at,omitempty"`
	Source   repairSourceView   `json:"source"`
	Warnings []string           `json:"warnings"`
	Changes  []repairChangeView `json:"changes"`
	ReviewAt string             `json:"review_at"`
}
type repairSourceView struct {
	ID       string `json:"id"`
	Filename string `json:"filename,omitempty"`
	Label    string `json:"label,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
}
type repairChangeView struct {
	Field string `json:"field"`
	// Before and After carry a text field or a list of words whole.
	Before any `json:"before,omitempty"`
	After  any `json:"after,omitempty"`
	// Roles, skills and exclusions are compared by name instead.
	BeforeCount *int     `json:"before_count,omitempty"`
	AfterCount  *int     `json:"after_count,omitempty"`
	Removed     []string `json:"removed,omitempty"`
	Added       []string `json:"added,omitempty"`
}

// repairRecord reads the parts of the API's proposal the view is built from.
type repairRecord struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Created string `json:"created_at"`
	Source  struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
		Label    string `json:"label"`
	} `json:"source"`
	SourceSHA256 string   `json:"source_sha256"`
	Warnings     []string `json:"warnings"`
	Changes      []struct {
		Field  string          `json:"field"`
		Before json.RawMessage `json:"before"`
		After  json.RawMessage `json:"after"`
	} `json:"changes"`
}

const repairTextLimit = 1000

func viewRepair(raw json.RawMessage) (*repairView, error) {
	var r repairRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("the API returned a repair this server cannot read: %w", err)
	}
	v := &repairView{ID: r.ID, Status: r.Status, Created: r.Created, Warnings: r.Warnings, Changes: []repairChangeView{}, ReviewAt: "/profile/repairs"}
	v.Source = repairSourceView{ID: r.Source.ID, Filename: r.Source.Filename, Label: r.Source.Label, SHA256: r.SourceSHA256}
	if v.Warnings == nil {
		v.Warnings = []string{}
	}
	for _, c := range r.Changes {
		change := repairChangeView{Field: c.Field}
		before, beforeIsRows := repairRows(c.Before)
		after, afterIsRows := repairRows(c.After)
		if beforeIsRows || afterIsRows {
			nb, na := len(before), len(after)
			change.BeforeCount, change.AfterCount = &nb, &na
			change.Removed, change.Added = missingFrom(before, after), missingFrom(after, before)
		} else {
			change.Before, change.After = repairValue(c.Before), repairValue(c.After)
		}
		v.Changes = append(v.Changes, change)
	}
	return v, nil
}

// repairRows names each element of a list of roles, skills or exclusions.
func repairRows(raw json.RawMessage) ([]string, bool) {
	var rows []map[string]any
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return nil, false
	}
	text := func(row map[string]any, key string) string { s, _ := row[key].(string); return strings.TrimSpace(s) }
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		name := text(row, "name")
		switch {
		case text(row, "title") != "":
			name = text(row, "title")
			if company := text(row, "company"); company != "" {
				name += " at " + company
			}
			if start, end := text(row, "start_date"), text(row, "end_date"); start != "" || end != "" {
				if current, _ := row["current"].(bool); current {
					end = "present"
				}
				name += " (" + start + " to " + end + ")"
			}
		case name != "":
			if years, _ := row["years"].(float64); years > 0 {
				name += fmt.Sprintf(" (%g years)", years)
			}
		default:
			name = text(row, "company_name")
			if name == "" {
				name = text(row, "company_domain")
			}
		}
		out = append(out, clip(name))
	}
	return out, true
}

func missingFrom(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	out := []string{}
	for _, x := range a {
		if !in[x] {
			out = append(out, x)
		}
	}
	return out
}

func repairValue(raw json.RawMessage) any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return clip(s)
	}
	return v
}

func clip(s string) string {
	if r := []rune(s); len(r) > repairTextLimit {
		return string(r[:repairTextLimit]) + "… (cut; the review page shows it whole)"
	}
	return s
}

func addProfileRepairTools(s *mcp.Server, c *Client) {
	addTool(s, &mcp.Tool{Name: "propose_profile_repair", Title: "Propose profile repair", Description: "Propose refreshing the profile from one stored base CV's parsed facts (source_cv_id from list_cvs). Changes nothing: the user reviews and applies, rejects or later restores it only in the web app at /profile/repairs, so never say the profile was updated. Returns the proposal id, the parser's warnings and what would change: text fields before and after, roles, skills and excluded companies as added and removed names. Show the warnings in the exact words returned. Applying replaces the role and skill lists with the CV's; facts the CV lacks are kept, and pay, preferences, authorization, notice and projects are never touched.", Annotations: writeTool("Propose profile repair", false)}, func(ctx context.Context, _ *mcp.CallToolRequest, in repairSourceInput) (*mcp.CallToolResult, repairResult, error) {
		if strings.TrimSpace(in.SourceCVID) == "" {
			return nil, repairResult{}, errors.New("source_cv_id is required")
		}
		var raw json.RawMessage
		if err := c.do(ctx, http.MethodPost, "/api/profile/repairs", in, &raw, c.timeout); err != nil {
			return nil, repairResult{}, toolError(err)
		}
		view, err := viewRepair(raw)
		return nil, repairResult{Proposal: view}, err
	})
	addTool(s, &mcp.Tool{Name: "get_profile_repairs", Title: "Read profile repairs", Description: "Without proposal_id: the latest 100 profile repair proposals with their status (pending, applied, rejected, restored). With proposal_id: that proposal's warnings and changes, as propose_profile_repair returns them. A pending proposal has changed nothing. Applying, rejecting and restoring happen only in the web app at /profile/repairs.", Annotations: readOnly("Read profile repairs")}, func(ctx context.Context, _ *mcp.CallToolRequest, in repairReadInput) (*mcp.CallToolResult, repairResult, error) {
		var raw json.RawMessage
		if in.ProposalID == "" {
			err := c.get(ctx, "/api/profile/repairs", &raw)
			return nil, repairResult{Proposals: raw}, toolError(err)
		}
		if err := c.get(ctx, "/api/profile/repairs/"+url.PathEscape(in.ProposalID), &raw); err != nil {
			return nil, repairResult{}, toolError(err)
		}
		view, err := viewRepair(raw)
		return nil, repairResult{Proposal: view}, err
	})
}
