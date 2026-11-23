package httpapi

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnEveryCore(t *testing.T) {
	for _, n := range []int{0, 1, 7, 1000} {
		seen := make([]atomic.Int32, n)
		if err := onEveryCore(context.Background(), n, func(i int) { seen[i].Add(1) }); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		for i := range seen {
			if got := seen[i].Load(); got != 1 {
				t.Fatalf("n=%d: index %d was called %d times", n, i, got)
			}
		}
	}

	// Two calls run at the same time: each waits for the other to have started.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(2))
	started := make(chan struct{}, 2)
	both := make(chan struct{})
	var once sync.Once
	if err := onEveryCore(context.Background(), 2, func(int) {
		started <- struct{}{}
		if len(started) == 2 {
			once.Do(func() { close(both) })
		}
		select {
		case <-both:
		case <-time.After(5 * time.Second):
			t.Error("the two calls did not overlap")
		}
	}); err != nil {
		t.Fatal(err)
	}

	// A panic comes back as an error, as it would from the request's own goroutine.
	err := onEveryCore(context.Background(), 100, func(i int) {
		if i == 42 {
			panic("bad posting")
		}
	})
	if err == nil || !strings.Contains(err.Error(), "bad posting") {
		t.Fatalf("a panic was not reported: %v", err)
	}

	// A context that ended stops the work and is reported.
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	err = onEveryCore(ctx, 100000, func(int) {
		if calls.Add(1) == 10 {
			cancel()
		}
	})
	if err != context.Canceled || calls.Load() == 100000 {
		t.Fatalf("a cancelled run answered %v after %d calls", err, calls.Load())
	}
}
