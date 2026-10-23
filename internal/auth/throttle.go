package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// throttle limits failed sign-ins per key (an email or a client address,
// D55): a burst of n failures, then one more per window/n. A success resets
// the key. In memory, per api process.
type throttle struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	now    func() time.Time
	keys   map[string]*entry
}

type entry struct {
	lim  *rate.Limiter
	last time.Time // last failure
}

func newThrottle(n int, window time.Duration, now func() time.Time) *throttle {
	return &throttle{n: n, window: window, now: now, keys: map[string]*entry{}}
}

// allowed reports whether key may try again, and if not, how long to wait.
func (t *throttle) allowed(key string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.keys[key]
	if e == nil {
		return true, 0
	}
	now := t.now()
	tokens := e.lim.TokensAt(now)
	if tokens >= 1 {
		return true, 0
	}
	return false, time.Duration((1 - tokens) * float64(t.window/time.Duration(t.n)))
}

// failed records a failed sign-in for key.
func (t *throttle) failed(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	// Forget keys that have been quiet for a whole window: they are back to
	// a full allowance anyway, and the map must not grow without end.
	for k, e := range t.keys {
		if now.Sub(e.last) > t.window {
			delete(t.keys, k)
		}
	}
	e := t.keys[key]
	if e == nil {
		e = &entry{lim: rate.NewLimiter(rate.Every(t.window/time.Duration(t.n)), t.n)}
		t.keys[key] = e
	}
	e.lim.AllowN(now, 1)
	e.last = now
}

// reset clears key after a successful sign-in.
func (t *throttle) reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.keys, key)
}
