package jobpilot

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ExternalApplicationInput contains historical facts only; dates stay empty when unknown.
// CVVersion is an opaque historical reference, never a selection or an approval.
type ExternalApplicationInput struct {
	ExternalKey  string `json:"external_key"`
	Employer     string `json:"employer"`
	Title        string `json:"title,omitempty"`
	Requisition  string `json:"requisition,omitempty"`
	CanonicalURL string `json:"canonical_url,omitempty"`
	AppliedAt    string `json:"applied_at,omitempty"`
	OutcomeAt    string `json:"outcome_at,omitempty"`
	Status       string `json:"status"`
	Source       string `json:"source"`
	EvidenceRef  string `json:"evidence_ref"`
	EvidenceKind string `json:"evidence_kind"`
	CVVersion    string `json:"cv_version,omitempty"`
}

type ExternalApplication struct {
	ID string `json:"id"`
	ExternalApplicationInput
	ImportedAt time.Time `json:"imported_at"`
}

type HistoryImport struct {
	Record                  ExternalApplicationInput `json:"record"`
	Mode                    string                   `json:"mode,omitempty"` // preview or commit
	PreviewToken            string                   `json:"preview_token,omitempty"`
	AllowPossibleDuplicates bool                     `json:"allow_possible_duplicates,omitempty"`
}

type HistoryCandidate struct {
	Record ExternalApplication `json:"record"`
	Kind   string              `json:"kind"` // exact or possible; never automatically merged
}

type HistoryPreview struct {
	Action       string               `json:"action"` // create, unchanged, conflict, review
	PreviewToken string               `json:"preview_token"`
	Candidates   []HistoryCandidate   `json:"candidates"`
	Record       *ExternalApplication `json:"record,omitempty"`
}

type HistoryPage struct {
	Items []ExternalApplication `json:"items"`
	Next  string                `json:"next"`
}

func addHistoryTools(s *mcp.Server, c *Client) {
	addTool(s, &mcp.Tool{Name: "import_external_application", Title: "Import external application history",
		Description: "Preview private historical facts without a catalog job or submission. Default mode preview. Review candidates, then commit the identical record with preview_token. Conflicts never overwrite. allow_possible_duplicates acknowledges possible related roles only; exact duplicates require reconciliation. Generic receipts/alerts must remain unknown. Dates are empty when unknown, YYYY-MM-DD or RFC3339. CV version is a historical reference, not approval. Never pass email bodies, credentials or codes.",
		Annotations: writeTool("Import external application history", true)}, func(ctx context.Context, _ *mcp.CallToolRequest, in HistoryImport) (*mcp.CallToolResult, HistoryPreview, error) {
		if in.Mode == "" {
			in.Mode = "preview"
		}
		if in.Mode != "preview" && in.Mode != "commit" {
			return nil, HistoryPreview{}, errors.New("mode must be preview or commit")
		}
		var out HistoryPreview
		err := c.do(ctx, http.MethodPost, "/api/application-history/import", in, &out, defaultTimeout)
		return nil, out, toolError(err)
	})
	addTool(s, &mcp.Tool{Name: "list_external_applications", Title: "Read external application history",
		Description: "Read the user's immutable external history, including original dates, evidence and CV reference. No apply links or actions. Follow next with after for another page.", Annotations: readOnly("Read external application history")}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		After string `json:"after,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}) (*mcp.CallToolResult, HistoryPage, error) {
		if in.Limit == 0 {
			in.Limit = 50
		}
		if in.Limit < 1 || in.Limit > 100 {
			return nil, HistoryPage{}, errors.New("limit must be 1 to 100")
		}
		var out HistoryPage
		err := c.do(ctx, http.MethodGet, "/api/application-history?after="+url.QueryEscape(in.After)+"&limit="+strconv.Itoa(in.Limit), nil, &out, defaultTimeout)
		return nil, out, toolError(err)
	})
}
