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
	"strings"
	"sync"
	"time"
)

const (
	defaultTimeout = 30 * time.Second
	// A discovery run fetches every active provider inside one request; the
	// API's own write timeout is two minutes, so allow a little more than that.
	discoveryTimeout = 3 * time.Minute
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

	mu    sync.Mutex
	token string
}

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
	return &Client{base: u.String(), http: &http.Client{}, email: o.Email, password: o.Password, timeout: timeout, maxBytes: maxResponseBytes, token: o.Token}, nil
}

func (c *Client) BaseURL() string { return c.base }

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
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
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
		req.Header.Set("Content-Type", "application/json")
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
	if err = json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("jobpilot api: decode %s %s: %w", method, path, err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out, c.timeout)
}

func (c *Client) Profile(ctx context.Context) (Profile, error) {
	var out Profile
	return out, c.get(ctx, "/api/profile", &out)
}

// Jobs lists every job evaluated for the user, or only accepted matches. The
// API orders both by rejected state, then score descending.
func (c *Client) Jobs(ctx context.Context, acceptedOnly bool) ([]JobWithMatch, error) {
	path := "/api/jobs"
	if acceptedOnly {
		path = "/api/matches"
	}
	var out []JobWithMatch
	return out, c.get(ctx, path, &out)
}

func (c *Client) Job(ctx context.Context, id string) (JobWithMatch, error) {
	var out JobWithMatch
	return out, c.get(ctx, "/api/jobs/"+url.PathEscape(id), &out)
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
func (c *Client) UpdateApplication(ctx context.Context, id, status, notes string) (Application, error) {
	var out Application
	body := map[string]string{"status": status, "notes": notes}
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
