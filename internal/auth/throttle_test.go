package auth

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// D55: 5 sign-in attempts per 15 minutes per key, then a wait; a success resets.
func TestThrottle_FiveAttemptsThenWait(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	th := newThrottle(5, 15*time.Minute, func() time.Time { return now })

	for i := range 5 {
		if ok, _ := th.take("k"); !ok {
			t.Fatalf("attempt %d must be allowed", i+1)
		}
	}
	ok, wait := th.take("k")
	if ok || wait <= 0 || wait > 3*time.Minute {
		t.Fatalf("after 5 failures: want a wait of up to 3 minutes, got allowed=%v wait=%v", ok, wait)
	}
	if ok, _ := th.take("other"); !ok {
		t.Fatal("another key is not affected")
	}

	now = now.Add(wait)
	if ok, _ := th.take("k"); !ok {
		t.Fatal("after the wait, one attempt is allowed again")
	}
}

// Attempts are counted before the password check, so a burst of parallel
// requests can't all pass while the first ones are still being checked.
func TestThrottle_ParallelAttemptsCountedUpFront(t *testing.T) {
	t.Parallel()
	th := newThrottle(5, 15*time.Minute, time.Now)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			if ok, _ := th.take("k"); ok {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if n := allowed.Load(); n != 5 {
		t.Fatalf("100 parallel attempts: want 5 allowed, got %d", n)
	}
}

func TestThrottle_ResetClearsAttempts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	th := newThrottle(5, 15*time.Minute, func() time.Time { return now })
	for range 4 {
		th.take("k")
	}
	th.reset("k")
	for range 4 {
		th.take("k")
	}
	if ok, _ := th.take("k"); !ok {
		t.Fatal("a successful sign-in resets the count")
	}
}

// Keys that have fully recovered are dropped, so the map can't grow forever.
func TestThrottle_ForgetsRecoveredKeys(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	th := newThrottle(5, 15*time.Minute, func() time.Time { return now })
	th.take("old")
	now = now.Add(16 * time.Minute)
	th.take("new")
	if _, kept := th.keys["old"]; kept {
		t.Fatal("a key idle for longer than the window is forgotten")
	}
}
