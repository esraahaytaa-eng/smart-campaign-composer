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

// reserve records a departure now if it fits in the window and returns its
// time; otherwise it records nothing and returns how long until one fits.
func (c *windowCap) reserve() (time.Time, time.Duration) {
	c.mu.Lock()
	now := time.Now()
	if len(c.times) < c.n || now.Sub(c.times[0]) >= c.window {
		if len(c.times) == c.n {
			c.times = c.times[1:]
		}
		c.times = append(c.times, now)
		c.mu.Unlock()
		return now, 0
	}
	delay := c.times[0].Add(c.window).Sub(now)
	c.mu.Unlock()
	if c.waiting != nil {
		c.waiting()
	}
	return time.Time{}, delay
}

// wait blocks until a departure fits in the window, records it and returns its time.
func (c *windowCap) wait(ctx context.Context) (time.Time, error) {
	for {
		at, delay := c.reserve()
		if delay == 0 {
			return at, nil
		}
		select {
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		case <-time.After(delay):
		}
	}
}
