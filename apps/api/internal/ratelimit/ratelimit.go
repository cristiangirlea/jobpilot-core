// Package ratelimit counts attempts per key (a client address, an email) in a
// fixed window. It is held in memory, so it is right for one API instance:
// with several replicas each would count separately, and a restart forgets
// every count. JobPilot runs one replica (deploy/base/api.yaml).
package ratelimit

import (
	"sync"
	"time"
)

// maxKeys bounds memory under a spray of distinct keys. Past it, after
// expired windows are dropped, a key not seen before is refused: an attacker
// filling the table locks out new callers for one window rather than
// exhausting the host's memory.
const maxKeys = 100_000

type window struct {
	count int
	reset time.Time
}

type Limiter struct {
	max    int
	period time.Duration
	now    func() time.Time

	mu    sync.Mutex
	hits  map[string]*window
	swept time.Time
}

// New allows max attempts per key in each period.
func New(max int, period time.Duration) *Limiter {
	return &Limiter{max: max, period: period, now: time.Now, hits: map[string]*window{}}
}

// Allow records an attempt for key and reports whether it is within the
// limit. When it is not, wait is how long until the key's window ends; a
// refused attempt is not counted, so it does not extend the wait.
func (l *Limiter) Allow(key string) (ok bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.swept) >= l.period {
		for k, w := range l.hits {
			if !now.Before(w.reset) {
				delete(l.hits, k)
			}
		}
		l.swept = now
	}
	w := l.hits[key]
	if w != nil && !now.Before(w.reset) {
		w = nil
	}
	if w == nil {
		if _, seen := l.hits[key]; !seen && len(l.hits) >= maxKeys {
			return false, l.period
		}
		w = &window{reset: now.Add(l.period)}
		l.hits[key] = w
	}
	if w.count >= l.max {
		return false, w.reset.Sub(now)
	}
	w.count++
	return true, 0
}

// Reset forgets a key, as after a successful login, so the attempts that
// came before it do not count against the account's owner.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
