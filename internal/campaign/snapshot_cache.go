package campaign

import (
	"context"
	"sync"
	"time"
)

// snapshotCache shares status snapshots between the open streams of a
// campaign (D59): a snapshot younger than maxAge is reused, and streams that
// ask while it is being read wait for that one read. Errors are not kept.
// The zero value is ready to use.
type snapshotCache struct {
	now     func() time.Time // nil: time.Now
	mu      sync.Mutex
	entries map[int64]*cachedSnapshot
}

type cachedSnapshot struct {
	ready chan struct{} // closed when the read has finished
	at    time.Time
	snap  statusSnapshot
	err   error
}

// forgetAfter drops campaigns no stream has read for a while.
const forgetAfter = time.Minute

func (c *snapshotCache) get(ctx context.Context, id int64, maxAge time.Duration,
	load func(context.Context, int64) (statusSnapshot, error)) (statusSnapshot, error) {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[int64]*cachedSnapshot{}
	}
	e := c.entries[id]
	if e == nil || e.done() && now().Sub(e.at) >= maxAge {
		for k, old := range c.entries {
			if old.done() && now().Sub(old.at) >= forgetAfter {
				delete(c.entries, k)
			}
		}
		e = &cachedSnapshot{ready: make(chan struct{})}
		c.entries[id] = e
		c.mu.Unlock()
		// Not tied to this stream: others wait for the same read.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		e.snap, e.err = load(readCtx, id)
		cancel()
		c.mu.Lock()
		e.at = now()
		if e.err != nil && c.entries[id] == e {
			delete(c.entries, id)
		}
		close(e.ready)
		c.mu.Unlock()
		return e.snap, e.err
	}
	c.mu.Unlock()
	select {
	case <-e.ready:
		return e.snap, e.err
	case <-ctx.Done():
		return statusSnapshot{}, ctx.Err()
	}
}

func (e *cachedSnapshot) done() bool {
	select {
	case <-e.ready:
		return true
	default:
		return false
	}
}
