package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func limiter(max int, period time.Duration) (*Limiter, *clock) {
	c := &clock{t: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	l := New(max, period)
	l.now = c.now
	return l, c
}

func TestAllowsUpToTheLimitThenRefusesUntilTheWindowEnds(t *testing.T) {
	l, c := limiter(3, time.Minute)
	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	c.t = c.t.Add(20 * time.Second)
	ok, wait := l.Allow("a")
	if ok || wait != 40*time.Second {
		t.Fatalf("the fourth attempt should wait out the window: ok=%v wait=%v", ok, wait)
	}
	// Refused attempts are not counted, so the wait does not grow.
	l.Allow("a")
	if _, wait = l.Allow("a"); wait != 40*time.Second {
		t.Fatalf("refused attempts must not extend the window, wait=%v", wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("another key has its own count")
	}
	c.t = c.t.Add(40 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("a new window starts once the old one ends")
	}
}

func TestResetForgetsAKey(t *testing.T) {
	l, _ := limiter(1, time.Minute)
	l.Allow("a")
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("the limit should apply")
	}
	l.Reset("a")
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("a reset key starts again")
	}
}

func TestExpiredWindowsAreDroppedAndTheTableIsBounded(t *testing.T) {
	l, c := limiter(1, time.Minute)
	for i := range maxKeys {
		l.Allow(fmt.Sprint(i))
	}
	if ok, _ := l.Allow("new"); ok {
		t.Fatal("a full table refuses keys it has not seen")
	}
	if ok, _ := l.Allow("0"); ok {
		t.Fatal("a known key keeps its own count, which is spent")
	}
	c.t = c.t.Add(time.Minute)
	if ok, _ := l.Allow("new"); !ok {
		t.Fatal("once the windows end the table is swept and new keys are allowed")
	}
	if len(l.hits) != 1 {
		t.Fatalf("expired windows should be dropped, %d remain", len(l.hits))
	}
}
