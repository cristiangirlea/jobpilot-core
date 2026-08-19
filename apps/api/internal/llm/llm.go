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
	ErrBudget      = errors.New("llm call budget exhausted for this window")
	ErrEmpty       = errors.New("llm returned an empty response")
)

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

// Enabled reports whether a provider is configured at all.
func (c *Client) Enabled() bool { return c != nil && c.provider != nil }

// Describe returns "provider/model" for diagnostics, or "disabled".
func (c *Client) Describe() string {
	if !c.Enabled() {
		return "disabled"
	}
	return c.provider.Name() + "/" + c.provider.Model()
}

// Complete returns a cached completion when one exists, otherwise calls the
// provider and caches the result. Cache hits never consume budget.
func (c *Client) Complete(ctx context.Context, req Request) (Result, error) {
	if !c.Enabled() {
		return Result{}, ErrUnavailable
	}
	name, model := c.provider.Name(), c.provider.Model()
	key := CacheKey(name, model, req)
	if c.cache != nil {
		if text, found, err := c.cache.LookupCompletion(ctx, key); err == nil && found {
			return Result{Text: text, Provider: name, Model: model, Cached: true}, nil
		}
	}
	if err := c.spend(); err != nil {
		return Result{}, err
	}
	text, err := c.provider.Complete(ctx, req)
	if err != nil {
		return Result{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Result{}, ErrEmpty
	}
	if c.cache != nil {
		_ = c.cache.StoreCompletion(ctx, key, req.Task, name, model, text)
	}
	return Result{Text: text, Provider: name, Model: model}, nil
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
