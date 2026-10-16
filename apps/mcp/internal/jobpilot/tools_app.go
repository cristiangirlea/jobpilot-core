package jobpilot

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type blacklistInput struct {
	CompanyName   string `json:"company_name"`
	CompanyDomain string `json:"company_domain,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type settingsInput struct {
	Enabled         *bool `json:"enabled,omitempty"`
	IntervalMinutes *int  `json:"interval_minutes,omitempty" jsonschema:"At least 360 minutes"`
	AutoPrepare     *bool `json:"auto_prepare,omitempty"`
	MinScore        *int  `json:"min_score,omitempty" jsonschema:"0 to 100"`
	MaxJobsPerRun   *int  `json:"max_jobs_per_run,omitempty" jsonschema:"1 to 500"`
}

type updateSourceInput struct {
	ID                  string  `json:"id" jsonschema:"From list_discovery_sources"`
	Active              *bool   `json:"active,omitempty"`
	Name                *string `json:"name,omitempty"`
	SearchQuery         *string `json:"search_query,omitempty"`
	ExcludedSearchQuery *string `json:"excluded_search_query,omitempty"`
}

func addAppTools(s *mcp.Server, c *Client) {
	addTool(s, &mcp.Tool{
		Name: "list_blacklist", Title: "List excluded companies",
		Description: "List the user's company exclusions and reasons. Removing one stays in the web app.",
		Annotations: readOnly("List excluded companies"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, Blacklist, error) {
		entries, err := c.Blacklist(ctx)
		if entries == nil {
			entries = []BlacklistEntry{}
		}
		return nil, Blacklist{Entries: entries}, toolError(err)
	})
	addTool(s, &mcp.Tool{
		Name: "add_to_blacklist", Title: "Exclude a company",
		Description: "Exclude a company for this user and re-score their jobs. Matching jobs become hard rejections; removing an exclusion stays in the web app.",
		Annotations: writeTool("Exclude a company", false),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in blacklistInput) (*mcp.CallToolResult, BlacklistEntry, error) {
		if strings.TrimSpace(in.CompanyName) == "" {
			return nil, BlacklistEntry{}, errors.New("company_name is required")
		}
		out, err := c.AddToBlacklist(ctx, in)
		return nil, out, toolError(err)
	})
	addTool(s, &mcp.Tool{
		Name: "get_discovery_settings", Title: "Get discovery settings",
		Description: "Read the discovery schedule, limits and automatic draft settings. Admin only.",
		Annotations: readOnly("Get discovery settings"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, DiscoverySettings, error) {
		out, err := c.DiscoverySettings(ctx)
		return nil, out, toolError(err)
	})
	addTool(s, &mcp.Tool{
		Name: "set_discovery_settings", Title: "Change discovery settings",
		Description: "Admin only. Change the discovery schedule, limits or automatic drafts; omitted fields keep their current values. Never approves or sends applications.",
		Annotations: writeTool("Change discovery settings", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in settingsInput) (*mcp.CallToolResult, DiscoverySettings, error) {
		if (in.IntervalMinutes != nil && *in.IntervalMinutes < 360) ||
			(in.MinScore != nil && (*in.MinScore < 0 || *in.MinScore > 100)) ||
			(in.MaxJobsPerRun != nil && (*in.MaxJobsPerRun < 1 || *in.MaxJobsPerRun > 500)) {
			return nil, DiscoverySettings{}, errors.New("interval_minutes must be at least 360; min_score 0-100; max_jobs_per_run 1-500")
		}
		base, err := c.DiscoverySettings(ctx)
		if err != nil {
			return nil, DiscoverySettings{}, toolError(err)
		}
		if in.Enabled != nil {
			base.Enabled = *in.Enabled
		}
		if in.IntervalMinutes != nil {
			base.IntervalMinutes = *in.IntervalMinutes
		}
		if in.AutoPrepare != nil {
			base.AutoPrepare = *in.AutoPrepare
		}
		if in.MinScore != nil {
			base.MinScore = *in.MinScore
		}
		if in.MaxJobsPerRun != nil {
			base.MaxJobsPerRun = *in.MaxJobsPerRun
		}
		out, err := c.SaveDiscoverySettings(ctx, base)
		return nil, out, toolError(err)
	})
	addTool(s, &mcp.Tool{
		Name: "update_discovery_source", Title: "Change a discovery source",
		Description: "Admin only. Pause or resume a source by id, rename it or change its queries. Omitted fields keep their values; remotive ignores excluded_search_query.",
		Annotations: writeTool("Change a discovery source", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateSourceInput) (*mcp.CallToolResult, DiscoverySource, error) {
		id := strings.TrimSpace(in.ID)
		if id == "" {
			return nil, DiscoverySource{}, errors.New("id is required; call list_discovery_sources for ids")
		}
		if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
			return nil, DiscoverySource{}, errors.New("name cannot be empty")
		}
		sources, err := c.DiscoverySources(ctx)
		if err != nil {
			return nil, DiscoverySource{}, toolError(err)
		}
		for _, base := range sources {
			if base.ID != id {
				continue
			}
			if in.Active != nil {
				base.Active = *in.Active
			}
			if in.Name != nil {
				base.Name = *in.Name
			}
			if in.SearchQuery != nil {
				base.SearchQuery = *in.SearchQuery
			}
			if in.ExcludedSearchQuery != nil {
				base.ExcludedSearchQuery = *in.ExcludedSearchQuery
			}
			normalized, err := normalizeSource(sourceInput{Provider: base.Provider, Name: base.Name,
				Identifier: base.Identifier, SearchQuery: base.SearchQuery, ExcludedSearchQuery: base.ExcludedSearchQuery, Active: &base.Active})
			if err != nil {
				return nil, DiscoverySource{}, err
			}
			out, err := c.UpdateDiscoverySource(ctx, id, normalized)
			return nil, out, toolError(err)
		}
		return nil, DiscoverySource{}, &APIError{Status: http.StatusNotFound, Message: "Source not found. Call list_discovery_sources for ids."}
	})
	addTool(s, &mcp.Tool{
		Name: "find_jobs_for_profile", Title: "Find jobs for my profile",
		Description: "Score open shared-catalog jobs with no match yet for this user's profile and rules. Available to members; fetches no new postings. Returns scored count and catalog freshness summary.",
		Annotations: writeTool("Find jobs for my profile", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, FindJobsResult, error) {
		out, err := c.FindJobsForProfile(ctx)
		return nil, out, toolError(err)
	})
	addTool(s, &mcp.Tool{
		Name: "get_catalog_summary", Title: "Get catalog freshness",
		Description: "Read shared-catalog freshness, open job count and this user's open matches without scoring jobs. Available to members.",
		Annotations: readOnly("Get catalog freshness"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, CatalogSummary, error) {
		out, err := c.CatalogSummary(ctx)
		return nil, out, toolError(err)
	})
}
