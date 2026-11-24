// Package llm provides an optional, pluggable text-completion adapter.
//
// The product must keep working with no provider configured: callers are
// expected to try their deterministic path first and only consult the client
// when that path cannot answer. A disabled or failing client returns
// ErrUnavailable so the caller can fall back rather than surface an error.
package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnavailable = errors.New("no llm provider is configured")
	ErrNoTime      = errors.New("no model time left for this request")
	ErrBudget      = errors.New("llm call budget exhausted for this window")
	ErrEmpty       = errors.New("llm returned an empty response")
)

type deadlineKey struct{}

// WithDeadline records the deadline shared by all model calls in a request.
// Only Complete applies it, so callers can still save deterministic drafts.
func WithDeadline(ctx context.Context, d time.Time) context.Context {
	return context.WithValue(ctx, deadlineKey{}, d)
}

// Request is one completion. Task is a short stable label ("answer_question",
// "cover_letter") used for cache accounting and diagnostics.
type Request struct {
	Task        string
	System      string
	Prompt      string
	MaxTokens   int
	Temperature float64
}

// Result carries the completion plus where it came from.
type Result struct {
	Text     string `json:"text"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Cached   bool   `json:"cached"`
	Backup   bool   `json:"backup"`
}

// Provider is one concrete backend (a CLI subprocess, a local server, an API).
type Provider interface {
	Name() string
	Model() string
	Complete(ctx context.Context, req Request) (string, error)
}

// Cache persists completions so an identical request is never paid for twice.
// Implemented by the store layer; a nil cache simply disables caching.
type Cache interface {
	LookupCompletion(ctx context.Context, key string) (string, bool, error)
	StoreCompletion(ctx context.Context, key, task, provider, model, response string) error
}

// Client wraps a provider with caching and a call budget.
type Client struct {
	provider Provider
	backup   Provider
	cache    Cache

	mu       sync.Mutex
	limit    int
	window   time.Duration
	used     int
	resetsAt time.Time
	now      func() time.Time
}

// New builds a client. A nil provider yields a disabled client whose Complete
// always returns ErrUnavailable, which is the correct default for this product.
func New(provider Provider, cache Cache, limit int, window time.Duration) *Client {
	if window <= 0 {
		window = time.Hour
	}
	return &Client{provider: provider, cache: cache, limit: limit, window: window, now: time.Now}
}

// WithBackup configures the optional fallback before the client is used.
func (c *Client) WithBackup(p Provider) *Client {
	c.backup = p
	return c
}

// Enabled reports whether a provider is configured at all.
func (c *Client) Enabled() bool { return c != nil && c.provider != nil }

// Describe identifies the primary provider for diagnostics.
func (c *Client) Describe() string {
	if !c.Enabled() {
		return "disabled"
	}
	return c.provider.Name() + "/" + c.provider.Model()
}

// BackupDescribe identifies the fallback provider, or returns an empty string.
func (c *Client) BackupDescribe() string {
	if c == nil || c.backup == nil {
		return ""
	}
	return c.backup.Name() + "/" + c.backup.Model()
}

// Status reports the enabled state and the primary and backup descriptions.
func (c *Client) Status() (enabled bool, provider, backup string) {
	return c.Enabled(), c.Describe(), c.BackupDescribe()
}

// Complete spends one budget unit on a primary cache miss. Only a failed
// primary call can reach the backup, even when the backup answer is cached.
func (c *Client) Complete(ctx context.Context, req Request) (Result, error) {
	if !c.Enabled() {
		return Result{}, ErrUnavailable
	}
	if d, ok := ctx.Value(deadlineKey{}).(time.Time); ok {
		if !time.Now().Before(d) {
			return Result{}, ErrNoTime
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, d)
		defer cancel()
	}
	key := CacheKey(c.provider.Name(), c.provider.Model(), req)
	if result, ok := c.cached(ctx, c.provider, key); ok {
		return result, nil
	}
	if err := c.spend(); err != nil {
		return Result{}, err
	}
	result, primaryErr := c.completeWith(ctx, c.provider, req, key)
	if primaryErr == nil || ctx.Err() != nil || c.backup == nil {
		return result, primaryErr
	}
	key = CacheKey("backup:"+c.backup.Name(), c.backup.Model(), req)
	result, ok := c.cached(ctx, c.backup, key)
	if !ok {
		var err error
		result, err = c.completeWith(ctx, c.backup, req, key)
		if err != nil {
			return Result{}, errors.Join(primaryErr, err)
		}
	}
	result.Backup = true
	return result, nil
}

func (c *Client) cached(ctx context.Context, p Provider, key string) (Result, bool) {
	if c.cache != nil {
		if text, found, err := c.cache.LookupCompletion(ctx, key); err == nil && found {
			return Result{Text: text, Provider: p.Name(), Model: p.Model(), Cached: true}, true
		}
	}
	return Result{}, false
}

func (c *Client) completeWith(ctx context.Context, p Provider, req Request, key string) (Result, error) {
	text, err := p.Complete(ctx, req)
	if err != nil {
		return Result{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Result{}, ErrEmpty
	}
	if c.cache != nil {
		_ = c.cache.StoreCompletion(ctx, key, req.Task, p.Name(), p.Model(), text)
	}
	return Result{Text: text, Provider: p.Name(), Model: p.Model()}, nil
}

// spend consumes one unit of the rolling call budget. A limit of zero or less
// means unlimited.
func (c *Client) spend() error {
	if c.limit <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if now.After(c.resetsAt) {
		c.used, c.resetsAt = 0, now.Add(c.window)
	}
	if c.used >= c.limit {
		return ErrBudget
	}
	c.used++
	return nil
}

// CacheKey is a deterministic fingerprint of everything that can change the
// completion, so a provider or model switch cannot serve a stale answer.
func CacheKey(provider, model string, req Request) string {
	raw := strings.Join([]string{provider, model, req.Task, req.System, req.Prompt}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
