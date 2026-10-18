package jobpilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultTimeout = 30 * time.Second
	// A discovery run fetches every active provider inside one request; the
	// API's own write timeout is two minutes, so allow a little more than that.
	discoveryTimeout = 3 * time.Minute
	// Validating a source fetches from the provider, so it needs more than
	// the default timeout but far less than a whole discovery run.
	validateTimeout = 90 * time.Second
	// Saving a filter preset re-scores every job before the API answers,
	// which it allows a minute for.
	rescoreTimeout = 90 * time.Second
	// The list endpoints are not paginated and carry full descriptions, so
	// the cap is generous; exceeding it is reported, never silently cut.
	maxResponseBytes = 32 << 20
	// Error bodies that are not the API's JSON shape (a proxy's HTML page)
	// are trimmed so they cannot flood the model's context.
	maxErrorMessage = 300
)

// Options configures a Client. Either Token or both Email and Password must be
// set. When Email and Password are present the client logs in lazily and once
// more after a 401, which covers an expired token without restarting the host.
type Options struct {
	BaseURL  string
	Token    string
	Email    string
	Password string
	Timeout  time.Duration
	// CVDir is the one folder upload_cv may read from; empty turns the
	// tool off.
	CVDir string
}

// requiredContract is the API behaviour this server was written against:
// see httpapi.Contract in apps/api for what each number guarantees. The
// server tolerates a newer API and warns about an older one.
const requiredContract = 1

// scopesContract is the API contract from which an answer carries a scope. An
// older API ignores the field and keeps the answer for the whole role family,
// which is wider than the user chose, so a narrower scope is refused there.
const scopesContract = 6

// An older API silently ignores the opt-in filter rules.
const filterGatesContract = 11

// contractUnknown means no health check has succeeded yet. Unknown counts as
// compatible: an API that is unreachable at startup must not disable tools
// that will work once it is up.
const contractUnknown = -1

// Health is what GET /api/health returns. Contract is 0 on an API that
// predates the field.
type Health struct {
	Status   string `json:"status"`
	Contract int    `json:"contract"`
}

// Client is a thin HTTP client for the JobPilot REST API. It carries the
// user's bearer token on every request so the API enforces isolation and the
// rejection locks exactly as it does for the web UI.
type Client struct {
	base     string
	http     *http.Client
	email    string
	password string
	timeout  time.Duration
	maxBytes int64
	cvDir    string

	// configErr is set when the server started without usable settings.
	// Every request then fails with it instead of reaching the network.
	configErr *ConfigError

	mu       sync.Mutex
	token    string
	contract int
}

// ConfigError explains why the server cannot call the API and what the user
// can do about it. It travels back as an ordinary tool error, which is the
// only channel a stdio server has that the model and the user both see.
type ConfigError struct{ Reason error }

func (e *ConfigError) Error() string {
	return fmt.Sprintf("the JobPilot MCP server is not configured (%v). "+
		"To fix it, mint an agent token in the JobPilot checkout with `pwsh -File scripts/agent-token.ps1 -Write`, "+
		"register it with the claude mcp add command the script prints (or set JOBPILOT_TOKEN, and JOBPILOT_API_URL if the API is not on localhost:8080), "+
		"then restart the session so the server starts with it", e.Reason)
}

func (e *ConfigError) Unwrap() error { return e.Reason }

// Unconfigured returns a client that answers every call with the reason it
// cannot work. The server starts with it rather than exiting, because a stdio
// server that exits before the handshake shows the host nothing but a closed
// connection, and its stderr is somewhere the user rarely looks.
func Unconfigured(reason error) *Client {
	return &Client{http: &http.Client{}, timeout: defaultTimeout, maxBytes: maxResponseBytes,
		contract: contractUnknown, configErr: &ConfigError{Reason: reason}}
}

// Configured reports whether the client can reach the API at all.
func (c *Client) Configured() bool { return c.configErr == nil }

// APIError is a non-2xx response with the API's own error message.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("jobpilot api: %d: %s", e.Status, e.Message) }

func NewClient(o Options) (*Client, error) {
	base := strings.TrimSpace(o.BaseURL)
	if base == "" {
		return nil, errors.New("jobpilot: the API base URL is required")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("jobpilot: base URL %q must be an http or https URL", base)
	}
	// Accept both the API origin and the origin plus /api, since the smoke
	// scripts and the web client spell it differently.
	u.Path = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/api"), "/")
	u.RawQuery, u.Fragment = "", ""
	if (o.Email == "") != (o.Password == "") {
		return nil, errors.New("jobpilot: email and password must be set together")
	}
	if o.Token == "" && o.Email == "" {
		return nil, errors.New("jobpilot: a token, or an email and password, is required")
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{base: u.String(), http: &http.Client{}, email: o.Email, password: o.Password, timeout: timeout, maxBytes: maxResponseBytes, token: o.Token, contract: contractUnknown, cvDir: o.CVDir}, nil
}

func (c *Client) BaseURL() string { return c.base }

// CheckContract reads the health endpoint, which needs no token, and records
// the API's contract version for PredatesContract.
func (c *Client) CheckContract(ctx context.Context) (Health, error) {
	var h Health
	if err := c.send(ctx, http.MethodGet, "/api/health", nil, &h, "", c.timeout); err != nil {
		return h, err
	}
	c.mu.Lock()
	c.contract = h.Contract
	c.mu.Unlock()
	return h, nil
}

// PredatesContract reports whether the API was seen to be older than this
// server expects. Only a successful check can say yes.
func (c *Client) PredatesContract() bool { return c.predates(requiredContract) }

// supports reports whether the API is known to meet version. With no
// successful check yet it checks now, and an API it still cannot check counts
// as not supporting it.
func (c *Client) supports(ctx context.Context, version int) bool {
	c.mu.Lock()
	known := c.contract != contractUnknown
	c.mu.Unlock()
	if !known {
		if _, err := c.CheckContract(ctx); err != nil {
			return false
		}
	}
	return !c.predates(version)
}

// predates reports whether the API was seen to be older than version.
func (c *Client) predates(version int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.contract != contractUnknown && c.contract < version
}

func (c *Client) canLogin() bool { return c.email != "" && c.password != "" }

func (c *Client) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func (c *Client) login(ctx context.Context) error {
	var out struct {
		Token string `json:"token"`
	}
	body := map[string]string{"email": c.email, "password": c.password}
	if err := c.send(ctx, http.MethodPost, "/api/auth/login", body, &out, "", c.timeout); err != nil {
		return fmt.Errorf("jobpilot login failed: %w", err)
	}
	if out.Token == "" {
		return errors.New("jobpilot login returned no token")
	}
	c.mu.Lock()
	c.token = out.Token
	c.mu.Unlock()
	return nil
}

// do performs an authenticated request, logging in first when no token is
// held and retrying exactly once after a 401 when credentials allow it.
func (c *Client) do(ctx context.Context, method, path string, body, out any, timeout time.Duration) error {
	token := c.currentToken()
	if token == "" {
		if err := c.login(ctx); err != nil {
			return err
		}
		token = c.currentToken()
	}
	err := c.send(ctx, method, path, body, out, token, timeout)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized && c.canLogin() {
		if err = c.login(ctx); err != nil {
			return err
		}
		return c.send(ctx, method, path, body, out, c.currentToken(), timeout)
	}
	return err
}

func (c *Client) send(ctx context.Context, method, path string, body, out any, token string, timeout time.Duration) error {
	if c.configErr != nil {
		return c.configErr
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	contentType := "application/json"
	switch b := body.(type) {
	case nil:
	case rawBody:
		// Read afresh on every send, so the retry after a 401 sends it whole.
		reader, contentType = bytes.NewReader(b.data), b.contentType
	default:
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("jobpilot api: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return fmt.Errorf("jobpilot api: %s %s: %w", method, path, err)
	}
	if int64(len(data)) > c.maxBytes {
		return fmt.Errorf("jobpilot api: %s %s: response exceeded %d bytes; the catalog is too large for this unpaginated endpoint", method, path, c.maxBytes)
	}
	if resp.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &problem)
		message := problem.Error
		if message == "" {
			message = strings.TrimSpace(string(data))
		}
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		if runes := []rune(message); len(runes) > maxErrorMessage {
			message = string(runes[:maxErrorMessage]) + "..."
		}
		return &APIError{Status: resp.StatusCode, Message: message}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if counted, ok := out.(*withTotal); ok {
		if n, convErr := strconv.Atoi(resp.Header.Get("X-Total-Count")); convErr == nil {
			counted.total, counted.counted = n, true
		}
		out = counted.into
	}
	if err = json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("jobpilot api: decode %s %s: %w", method, path, err)
	}
	return nil
}

// rawBody is a request body sent as it is rather than as JSON.
type rawBody struct {
	data        []byte
	contentType string
}

// withTotal decodes a list into into and keeps the X-Total-Count the API sent
// with it; counted is false when it sent none.
type withTotal struct {
	into    any
	total   int
	counted bool
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out, c.timeout)
}

func (c *Client) Profile(ctx context.Context) (Profile, error) {
	var out Profile
	return out, c.get(ctx, "/api/profile", &out)
}

// JobQuery narrows a job list the way the API's list parameters do.
type JobQuery struct {
	State    string // accepted, rejected, or all
	MinScore int
	OpenOnly bool
	Limit    int
	Summary  bool
}

// Jobs lists the user's evaluated jobs, best first. An API that pages applies
// q and reports how many jobs match before the limit, and counted is true. An
// API from before paging ignores q and returns every job, or every accepted
// one, which the caller must then filter itself.
func (c *Client) Jobs(ctx context.Context, q JobQuery) (jobs []JobWithMatch, total int, counted bool, err error) {
	values := url.Values{}
	path := "/api/jobs"
	if q.State == "accepted" {
		// Accepted matches have their own list, which an older API also serves
		// without the rejections.
		path = "/api/matches"
	} else if q.State != "" {
		values.Set("state", q.State)
	}
	if q.MinScore > 0 {
		values.Set("min_score", strconv.Itoa(q.MinScore))
	}
	if q.OpenOnly {
		values.Set("open", "true")
	}
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Summary {
		values.Set("fields", "summary")
	}
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	list := &withTotal{into: &jobs}
	err = c.get(ctx, path, list)
	return jobs, list.total, list.counted, err
}

func (c *Client) Job(ctx context.Context, id string) (JobWithMatch, error) {
	var out JobWithMatch
	return out, c.get(ctx, "/api/jobs/"+url.PathEscape(id), &out)
}

// ReportJob reports a posting as no longer available. The API checks the
// link at once, so this can take as long as one link check.
func (c *Client) ReportJob(ctx context.Context, id string) (JobReport, error) {
	var out JobReport
	return out, c.do(ctx, http.MethodPost, "/api/jobs/"+url.PathEscape(id)+"/report", nil, &out, c.timeout)
}

func (c *Client) Generate(ctx context.Context, jobID string) (Documents, error) {
	var out Documents
	return out, c.do(ctx, http.MethodPost, "/api/jobs/"+url.PathEscape(jobID)+"/generate", nil, &out, c.timeout)
}

func (c *Client) Applications(ctx context.Context) ([]Application, error) {
	var out []Application
	return out, c.get(ctx, "/api/applications", &out)
}

func (c *Client) Application(ctx context.Context, id string) (ApplicationDetail, error) {
	var out ApplicationDetail
	return out, c.get(ctx, "/api/applications/"+url.PathEscape(id), &out)
}

func (c *Client) CreateApplication(ctx context.Context, jobID string) (Application, error) {
	var out Application
	return out, c.do(ctx, http.MethodPost, "/api/applications", map[string]string{"job_id": jobID}, &out, c.timeout)
}

// UpdateApplication sends both fields because the API replaces notes on every
// update; callers that want to keep the stored notes must pass them back.
// UpdateApplication changes an application's status. A nil notes omits the
// field, which the API reads as "keep the stored notes"; a non-nil value
// replaces them, and an empty string clears them.
func (c *Client) UpdateApplication(ctx context.Context, id, status string, notes *string) (Application, error) {
	var out Application
	body := struct {
		Status string  `json:"status"`
		Notes  *string `json:"notes,omitempty"`
	}{Status: status, Notes: notes}
	return out, c.do(ctx, http.MethodPatch, "/api/applications/"+url.PathEscape(id), body, &out, c.timeout)
}

func (c *Client) Dashboard(ctx context.Context) (Dashboard, error) {
	var out Dashboard
	return out, c.get(ctx, "/api/dashboard", &out)
}

func (c *Client) DiscoveryRuns(ctx context.Context) ([]DiscoveryRun, error) {
	var out []DiscoveryRun
	return out, c.get(ctx, "/api/discovery/runs", &out)
}

func (c *Client) RunDiscovery(ctx context.Context) (DiscoveryRun, error) {
	var out DiscoveryRun
	return out, c.do(ctx, http.MethodPost, "/api/discovery/run", nil, &out, discoveryTimeout)
}

// ImportJob adds one posting to the shared catalog and evaluates it for the
// user. An existing catalog entry with the same fingerprint is reused.
func (c *Client) ImportJob(ctx context.Context, in JobInput) (JobWithMatch, error) {
	var out JobWithMatch
	return out, c.do(ctx, http.MethodPost, "/api/jobs", in, &out, c.timeout)
}

func (c *Client) Filters(ctx context.Context) ([]FilterPreset, error) {
	var out []FilterPreset
	return out, c.get(ctx, "/api/filters", &out)
}

// SaveFilter creates a preset when it has no id and replaces the stored one
// otherwise. The API re-scores every job before it answers.
func (c *Client) SaveFilter(ctx context.Context, in FilterPreset) (FilterPreset, error) {
	var out FilterPreset
	if in.ID == "" {
		return out, c.do(ctx, http.MethodPost, "/api/filters", in, &out, rescoreTimeout)
	}
	return out, c.do(ctx, http.MethodPut, "/api/filters/"+url.PathEscape(in.ID), in, &out, rescoreTimeout)
}

func (c *Client) DiscoverySources(ctx context.Context) ([]DiscoverySource, error) {
	var out []DiscoverySource
	return out, c.get(ctx, "/api/discovery/sources", &out)
}

// ValidateDiscoverySource fetches a small sample from the provider, so it is
// given the longer provider timeout rather than the default one.
func (c *Client) ValidateDiscoverySource(ctx context.Context, in DiscoverySource) (SourceValidation, error) {
	var out SourceValidation
	return out, c.do(ctx, http.MethodPost, "/api/discovery/validate", in, &out, validateTimeout)
}

func (c *Client) AddDiscoverySource(ctx context.Context, in DiscoverySource) (DiscoverySource, error) {
	var out DiscoverySource
	return out, c.do(ctx, http.MethodPost, "/api/discovery/sources", in, &out, c.timeout)
}

func (c *Client) Blacklist(ctx context.Context) ([]BlacklistEntry, error) {
	var out []BlacklistEntry
	return out, c.get(ctx, "/api/blacklist", &out)
}

func (c *Client) AddToBlacklist(ctx context.Context, in blacklistInput) (BlacklistEntry, error) {
	var out BlacklistEntry
	return out, c.do(ctx, http.MethodPost, "/api/blacklist", in, &out, rescoreTimeout)
}

func (c *Client) DiscoverySettings(ctx context.Context) (DiscoverySettings, error) {
	var out DiscoverySettings
	return out, c.get(ctx, "/api/discovery/settings", &out)
}

func (c *Client) SaveDiscoverySettings(ctx context.Context, in DiscoverySettings) (DiscoverySettings, error) {
	var out DiscoverySettings
	return out, c.do(ctx, http.MethodPut, "/api/discovery/settings", in, &out, c.timeout)
}

func (c *Client) UpdateDiscoverySource(ctx context.Context, id string, in DiscoverySource) (DiscoverySource, error) {
	var out DiscoverySource
	// Send the editable fields only, without crawl metadata or the URL's id.
	body := struct {
		Provider            string `json:"provider"`
		Name                string `json:"name"`
		Identifier          string `json:"identifier"`
		SearchQuery         string `json:"search_query"`
		ExcludedSearchQuery string `json:"excluded_search_query"`
		Active              bool   `json:"active"`
	}{in.Provider, in.Name, in.Identifier, in.SearchQuery, in.ExcludedSearchQuery, in.Active}
	return out, c.do(ctx, http.MethodPut, "/api/discovery/sources/"+url.PathEscape(id), body, &out, c.timeout)
}

func (c *Client) FindJobsForProfile(ctx context.Context) (FindJobsResult, error) {
	var out FindJobsResult
	return out, c.do(ctx, http.MethodPost, "/api/jobs/find", nil, &out, rescoreTimeout)
}

func (c *Client) CatalogSummary(ctx context.Context) (CatalogSummary, error) {
	var out CatalogSummary
	return out, c.get(ctx, "/api/catalog/summary", &out)
}
