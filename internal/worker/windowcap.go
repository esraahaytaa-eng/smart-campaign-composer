package worker

import (
	"context"
	"sync"
	"time"
)

// windowCap allows at most n departures in any window, counted at the
// moment each departure is recorded. It backs up the rate limiter, which
// spaces reservations but cannot stop late-waking goroutines from bunching
// (D19 history, 2026-10-22).
type windowCap struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	times  []time.Time // the last n departures, oldest first
	// waiting, if set, runs each time a departure has to wait; tests use it to
	// act at that exact moment. Always nil in production.
	waiting func()
}

func newWindowCap(n int, window time.Duration) *windowCap {
	return &windowCap{n: n, window: window}
}

// wait blocks until a departure fits in the window, records it and returns its time.
func (c *windowCap) wait(ctx context.Context) (time.Time, error) {
	for {
		c.mu.Lock()
		now := time.Now()
		if len(c.times) < c.n || now.Sub(c.times[0]) >= c.window {
			if len(c.times) == c.n {
				c.times = c.times[1:]
			}
			c.times = append(c.times, now)
			c.mu.Unlock()
			return now, nil
		}
		delay := c.times[0].Add(c.window).Sub(now)
		c.mu.Unlock()
		if c.waiting != nil {
			c.waiting()
		}
		select {
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		case <-time.After(delay):
		}
	}
}
