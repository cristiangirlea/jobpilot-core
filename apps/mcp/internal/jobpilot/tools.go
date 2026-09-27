package jobpilot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// blockedMessage is the API's wording for a rejected job's lock. The 409 hint
// is keyed on it so other conflicts, such as a duplicate discovery source,
// are not mislabelled. Keep it in step with store.ErrApplicationBlocked.
const blockedMessage = "application actions are blocked for this rejected job"

// discoveryProviders mirrors the API's allowlist so a typo is caught before a
// request is made.
var discoveryProviders = []string{"remotive", "greenhouse", "lever", "ashby", "smartrecruiters", "arbeitnow"}

// progressInterval paces progress notifications during a discovery run.
// A variable so tests can shorten it.
var progressInterval = 5 * time.Second

// instructions is sent to the host once per session. It carries the product
// invariants the tool descriptions cannot repeat on every call.
const instructions = `JobPilot is the user's self-hosted job search assistant. It discovers jobs from public APIs, scores each one against the user's profile and filters, drafts factual application material, and tracks applications.

Rules the API enforces and you must respect:
- Jobs rejected by a hard rule (current or previous employer, excluded company, or an active filter) are visible for explanation only. Documents, applications, and the applied status are refused for them with a 409; do not retry.
- Nothing here submits an application, opens a browser, or records an apply click. compose_application assembles a whole application into the outbox for the user to review; approving it, and any sending, happen only in the web app. For a job whose form cannot be read, give the user the employer_url (or the apply_url when there is none) and let them apply themselves, then record the outcome with update_application_status.
- Anything marked needs_review or unapproved, and every blocker, must be shown to the user in the exact words the tool returned. Never describe an application as approved, submitted, or sent.
- When you write anything for the user, use only facts returned by get_profile, get_cv, get_job, and generate_documents. Never invent experience, skills, or claims. An answer or CV whose source is ai or template is a draft until the user reads it and approves it.
- Listing tools return summaries without the job description; call get_job for the full text and the score breakdown.`

var applicationStatuses = []string{"draft", "ready", "applied", "interview", "rejected", "offer", "withdrawn"}

type listJobsInput struct {
	State    string `json:"state,omitempty" jsonschema:"Which evaluated jobs to return: accepted (default), rejected, or all"`
	MinScore int    `json:"min_score,omitempty" jsonschema:"Only return jobs whose match score is at or above this value, 0 to 100"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum jobs to return: default 20, maximum 100. The total before the cap is always reported"`
	// IncludeClosed also lists postings that have closed.
	IncludeClosed bool `json:"include_closed,omitempty" jsonschema:"Also list postings that have closed; they can no longer be applied to"`
}

type jobInput struct {
	JobID string `json:"job_id" jsonschema:"Job ID as returned by list_jobs, get_dashboard, or an application"`
}

type listApplicationsInput struct {
	Status string `json:"status,omitempty" jsonschema:"Only return applications in this status: draft, ready, applied, interview, rejected, offer, or withdrawn"`
}

type applicationInput struct {
	ApplicationID string `json:"application_id" jsonschema:"Application ID as returned by list_applications or create_application"`
}

type updateStatusInput struct {
	ApplicationID string  `json:"application_id" jsonschema:"Application ID as returned by list_applications or create_application"`
	Status        string  `json:"status" jsonschema:"New status: draft, ready, applied, interview, rejected, offer, or withdrawn. Use applied only after the user confirms they submitted"`
	Notes         *string `json:"notes,omitempty" jsonschema:"Replacement notes for the application. Omit to keep the stored notes unchanged"`
}

type listRunsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Maximum runs to return, newest first; default 20, and the API keeps only the 20 most recent"`
}

type JobList struct {
	Total int            `json:"total"`
	Jobs  []MatchSummary `json:"jobs"`
}

type ApplicationList struct {
	Applications []ApplicationSummary `json:"applications"`
}

type RunList struct {
	Runs []DiscoveryRun `json:"runs"`
}

// NewServer builds the MCP server over the given API client. Tool handlers
// return plain errors; the SDK turns those into tool results the host model
// can read and react to, rather than protocol failures.
// readOnly and writeTool are the annotation policy for every tool, in one
// place so the three registration files cannot drift apart. Nothing here is
// destructive: no tool deletes anything.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: ptr(false)}
}

// writeTool marks a tool that changes stored state. Most are idempotent:
// documents regenerate deterministically, applications upsert, and a repeated
// status change is a no-op.
func writeTool(title string, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: false, DestructiveHint: ptr(false), IdempotentHint: idempotent, OpenWorldHint: ptr(false)}
}

// looseObject is the output schema every tool advertises. The SDK would
// otherwise infer one from each handler's result type, and those inferred
// schemas were 38 of the 60 KB an agent host downloads at the start of every
// session, before any work happens. Results are still typed, validated as
// objects, and returned as structured content; only the per-field schema is
// no longer shipped. The semantics that mattered live in tool descriptions.
var looseObject = map[string]any{"type": "object"}

// addTool registers a typed tool with the shared output-schema policy.
func addTool[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	if t.OutputSchema == nil {
		t.OutputSchema = looseObject
	}
	mcp.AddTool(s, t, h)
}

func NewServer(c *Client, version string, logger *slog.Logger) *mcp.Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "jobpilot", Version: version}, &mcp.ServerOptions{Instructions: instructions, Logger: logger})
	s.AddReceivingMiddleware(logging(logger))
	addPrompts(s)

	addTool(s, &mcp.Tool{
		Name:        "get_profile",
		Title:       "Get candidate profile",
		Description: "Return the user's candidate profile: contact details, current and desired titles, seniorities, skills, work history, projects, preferences, and constraints. These are the only facts you may use when writing on the user's behalf.",
		Annotations: readOnly("Get candidate profile"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, Profile, error) {
		p, err := c.Profile(ctx)
		return nil, p, toolError(err)
	})

	addTool(s, &mcp.Tool{
		Name:        "list_jobs",
		Title:       "List evaluated jobs",
		Description: "List jobs evaluated for the user as summaries, sorted by match score. By default only accepted matches are returned; ask for rejected or all to see jobs a hard rule excluded, together with the rejection reasons. Descriptions are omitted; call get_job for the full posting and score breakdown. Closed postings are left out unless include_closed, and then are marked closed. total counts the matching jobs before the limit; score is 0 to 100 and a rejection always wins over it.",
		Annotations: readOnly("List evaluated jobs"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listJobsInput) (*mcp.CallToolResult, JobList, error) {
		state := strings.ToLower(strings.TrimSpace(in.State))
		if state == "" {
			state = "accepted"
		}
		if state != "accepted" && state != "rejected" && state != "all" {
			return nil, JobList{}, fmt.Errorf("state must be accepted, rejected, or all, not %q", in.State)
		}
		if in.MinScore < 0 || in.MinScore > 100 {
			return nil, JobList{}, fmt.Errorf("min_score must be between 0 and 100, not %d", in.MinScore)
		}
		jobs, total, counted, err := c.Jobs(ctx, JobQuery{State: state, MinScore: in.MinScore, OpenOnly: !in.IncludeClosed, Limit: pageSize(in.Limit), Summary: true})
		if err != nil {
			return nil, JobList{}, toolError(err)
		}
		out := JobList{Jobs: make([]MatchSummary, 0, len(jobs))}
		if counted {
			out.Total = total
			for _, x := range jobs {
				out.Jobs = append(out.Jobs, summarizeMatch(x))
			}
			return nil, out, nil
		}
		// An API from before paging sent every job; filter them here.
		for _, x := range jobs {
			if state == "rejected" && !x.Match.Rejected {
				continue
			}
			if x.Match.Score < in.MinScore {
				continue
			}
			if x.Job.ClosedAt != nil && !in.IncludeClosed {
				continue
			}
			out.Total++
			if len(out.Jobs) < pageSize(in.Limit) {
				out.Jobs = append(out.Jobs, summarizeMatch(x))
			}
		}
		return nil, out, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "get_job",
		Title:       "Get job and match detail",
		Description: "Return one job with its full description and the user's match evaluation: score, per-component breakdown, reasons, warnings, red flags, matched and missing skills, and any rejection reasons. The breakdown gives points per component: title_match, skill_match, seniority_match, location_remote_match, salary_match, domain_industry_match, freshness. The apply_url is for the user to open themselves and is empty for rejected jobs.",
		Annotations: readOnly("Get job and match detail"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in jobInput) (*mcp.CallToolResult, JobWithMatch, error) {
		if strings.TrimSpace(in.JobID) == "" {
			return nil, JobWithMatch{}, errors.New("job_id is required")
		}
		x, err := c.Job(ctx, in.JobID)
		if err != nil {
			return nil, JobWithMatch{}, toolError(err)
		}
		x.Match.normalize()
		if x.Match.Rejected {
			x.Job.ApplyURL, x.Job.EmployerURL = "", ""
		}
		return nil, x, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "list_applications",
		Title:       "List applications",
		Description: "List the user's applications with the job each one targets, newest activity first. Filter by status to see, for example, everything still in draft or ready.",
		Annotations: readOnly("List applications"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listApplicationsInput) (*mcp.CallToolResult, ApplicationList, error) {
		status := strings.ToLower(strings.TrimSpace(in.Status))
		if status != "" && !validStatus(status) {
			return nil, ApplicationList{}, fmt.Errorf("status must be one of %s, not %q", strings.Join(applicationStatuses, ", "), in.Status)
		}
		apps, err := c.Applications(ctx)
		if err != nil {
			return nil, ApplicationList{}, toolError(err)
		}
		out := ApplicationList{Applications: make([]ApplicationSummary, 0, len(apps))}
		for _, a := range apps {
			if status != "" && a.Status != status {
				continue
			}
			out.Applications = append(out.Applications, summarizeApplication(a))
		}
		return nil, out, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "get_application",
		Title:       "Get application and documents",
		Description: "Return one application with its generated documents (recruiter message, cover letter, candidate summary, screening answers) and whether the job has since been blocked.",
		Annotations: readOnly("Get application and documents"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in applicationInput) (*mcp.CallToolResult, ApplicationView, error) {
		if strings.TrimSpace(in.ApplicationID) == "" {
			return nil, ApplicationView{}, errors.New("application_id is required")
		}
		d, err := c.Application(ctx, in.ApplicationID)
		if err != nil {
			return nil, ApplicationView{}, toolError(err)
		}
		d.Documents.normalize()
		view := ApplicationView{Application: summarizeApplication(d.Application), Documents: d.Documents, Blocked: d.Blocked, BlockReasons: nonNil(d.BlockReasons)}
		if view.Blocked && view.Application.Job != nil {
			view.Application.Job.ApplyURL = ""
		}
		return nil, view, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "get_dashboard",
		Title:       "Get dashboard",
		Description: "Return the user's pipeline at a glance: profile completeness as a percentage, catalog and rejection counts, good_matches (accepted matches scoring 70 or more), application counts, the top recommended jobs, and suggested next actions.",
		Annotations: readOnly("Get dashboard"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, DashboardView, error) {
		d, err := c.Dashboard(ctx)
		if err != nil {
			return nil, DashboardView{}, toolError(err)
		}
		out := DashboardView{ProfileCompleteness: d.ProfileCompleteness, JobsImported: d.JobsImported, JobsRejectedBlacklist: d.JobsRejectedBlacklist, JobsRejectedFilters: d.JobsRejectedFilters, GoodMatches: d.GoodMatches, ApplicationsDrafted: d.ApplicationsDrafted, ApplicationsApplied: d.ApplicationsApplied, TopJobs: make([]MatchSummary, 0, len(d.TopJobs)), NextActions: nonNil(d.NextActions)}
		for _, x := range d.TopJobs {
			out.TopJobs = append(out.TopJobs, summarizeMatch(x))
		}
		return nil, out, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "list_discovery_runs",
		Title:       "List discovery runs",
		Description: "Return recent discovery runs with how many postings each one saw, how many jobs it added and how many of those it rejected, how many were already known (duplicates_skipped, re-scored), and how many it auto-prepared, plus any provider errors. A run's budget is shared across sources.",
		Annotations: readOnly("List discovery runs"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listRunsInput) (*mcp.CallToolResult, RunList, error) {
		runs, err := c.DiscoveryRuns(ctx)
		if err != nil {
			return nil, RunList{}, toolError(err)
		}
		if runs == nil {
			runs = []DiscoveryRun{}
		}
		if n := pageSize(in.Limit); len(runs) > n {
			runs = runs[:n]
		}
		return nil, RunList{Runs: runs}, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "generate_documents",
		Title:       "Generate application documents",
		Description: "Generate and store the deterministic application documents for a job: recruiter message, cover letter, candidate summary, and common screening answers, built only from stored profile facts. Safe to repeat. Refused for rejected jobs.",
		Annotations: writeTool("Generate application documents", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in jobInput) (*mcp.CallToolResult, Documents, error) {
		if strings.TrimSpace(in.JobID) == "" {
			return nil, Documents{}, errors.New("job_id is required")
		}
		d, err := c.Generate(ctx, in.JobID)
		if err != nil {
			return nil, Documents{}, toolError(err)
		}
		d.normalize()
		return nil, d, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "create_application",
		Title:       "Create draft application",
		Description: "Create a draft application record for a job, or return the existing one. This only tracks intent inside JobPilot; it does not contact the employer. Refused for rejected jobs.",
		Annotations: writeTool("Create draft application", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in jobInput) (*mcp.CallToolResult, Application, error) {
		if strings.TrimSpace(in.JobID) == "" {
			return nil, Application{}, errors.New("job_id is required")
		}
		a, err := c.CreateApplication(ctx, in.JobID)
		if err != nil {
			return nil, Application{}, toolError(err)
		}
		a.Job = nil
		return nil, a, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "update_application_status",
		Title:       "Update application status",
		Description: "Move an application to a new status and record the change in its history. Set applied only after the user confirms they submitted on the employer's site. Notes are kept unless you pass replacement notes. Moving a blocked application to applied is refused.",
		Annotations: writeTool("Update application status", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateStatusInput) (*mcp.CallToolResult, Application, error) {
		if strings.TrimSpace(in.ApplicationID) == "" {
			return nil, Application{}, errors.New("application_id is required")
		}
		status := strings.ToLower(strings.TrimSpace(in.Status))
		if !validStatus(status) {
			return nil, Application{}, fmt.Errorf("status must be one of %s, not %q", strings.Join(applicationStatuses, ", "), in.Status)
		}
		// Omitting notes tells the API to keep what is stored, so there is
		// nothing to read back first. An older API replaced them instead,
		// which is the one case worth refusing rather than risking.
		if in.Notes == nil && c.PredatesContract() {
			return nil, Application{}, errors.New("this API predates notes-preserving updates, so omitting notes would erase them; read the current notes with get_application and pass them explicitly")
		}
		a, err := c.UpdateApplication(ctx, in.ApplicationID, status, in.Notes)
		if err != nil {
			return nil, Application{}, toolError(err)
		}
		a.Job = nil
		return nil, a, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "run_discovery",
		Title:       "Run discovery now",
		Description: "Fetch every active discovery source now, add new jobs to the catalog, evaluate them for the user, and auto-prepare applications when that setting is on. Bounded by the configured per-run limit and may take a minute or two. Returns the run summary.",
		Annotations: &mcp.ToolAnnotations{Title: "Run discovery now", ReadOnlyHint: false, DestructiveHint: ptr(false), IdempotentHint: false, OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, DiscoveryRun, error) {
		stop := reportProgress(ctx, req)
		run, err := c.RunDiscovery(ctx)
		stop()
		return nil, run, toolError(err)
	})

	addCatalogTools(s, c)
	addApplyTools(s, c)
	return s
}

// reportProgress emits progress notifications while a slow tool runs, but
// only when the host asked for them by sending a progress token. It returns a
// stop function the caller must invoke when the work finishes.
func reportProgress(ctx context.Context, req *mcp.CallToolRequest) func() {
	token := req.Params.GetProgressToken()
	if token == nil || req.Session == nil {
		return func() {}
	}
	notify := func(elapsed time.Duration) {
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token,
			Message:       "Fetching discovery sources",
			Progress:      elapsed.Seconds(),
		})
	}
	notify(0)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				notify(time.Since(start))
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// toolError keeps the API's message and adds the one hint the model needs to
// act on the two errors it is most likely to hit.
func toolError(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Status {
	case http.StatusConflict:
		if strings.Contains(apiErr.Message, blockedMessage) {
			return hinted{err, "Rejected jobs never receive documents or applications; call get_job (or get_application for its block_reasons) and tell the user why."}
		}
		return err
	case http.StatusUnauthorized:
		return hinted{err, "JOBPILOT_TOKEN is invalid, expired, or revoked; the user can mint a new agent token under Settings, or with pwsh -File scripts/agent-token.ps1, and update the MCP server's configuration."}
	case http.StatusForbidden:
		return hinted{err, "This is a permission limit, not a product rule: the configured token lacks the scope this action needs, and the user can create one with write access under Settings, or with pwsh -File scripts/agent-token.ps1 -Write."}
	}
	return err
}

// hinted adds a hint to an error as the next sentence. The API's messages end
// in a full stop already, and appending ". " after one read "scope.. This".
// The error it wraps stays reachable with errors.As.
type hinted struct {
	err  error
	hint string
}

func (h hinted) Error() string { return strings.TrimRight(h.err.Error(), ". ") + ". " + h.hint }
func (h hinted) Unwrap() error { return h.err }

func validStatus(status string) bool {
	for _, s := range applicationStatuses {
		if s == status {
			return true
		}
	}
	return false
}

func pageSize(limit int) int {
	switch {
	case limit <= 0:
		return defaultPageSize
	case limit > maxPageSize:
		return maxPageSize
	}
	return limit
}

func ptr[T any](v T) *T { return &v }
