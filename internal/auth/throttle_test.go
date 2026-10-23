package auth

import (
	"testing"
	"time"
)

// D55: 5 failed sign-ins per 15 minutes per key, then a wait.
func TestThrottle_FiveFailuresThenWait(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	th := newThrottle(5, 15*time.Minute, func() time.Time { return now })

	for i := range 5 {
		if ok, _ := th.allowed("k"); !ok {
			t.Fatalf("attempt %d must be allowed", i+1)
		}
		th.failed("k")
	}
	ok, wait := th.allowed("k")
	if ok || wait <= 0 || wait > 3*time.Minute {
		t.Fatalf("after 5 failures: want a wait of up to 3 minutes, got allowed=%v wait=%v", ok, wait)
	}
	if ok, _ := th.allowed("other"); !ok {
		t.Fatal("another key is not affected")
	}

	now = now.Add(wait)
	if ok, _ := th.allowed("k"); !ok {
		t.Fatal("after the wait, one attempt is allowed again")
	}
}

func TestThrottle_ResetClearsFailures(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	th := newThrottle(5, 15*time.Minute, func() time.Time { return now })
	for range 4 {
		th.failed("k")
	}
	th.reset("k")
	for range 4 {
		th.failed("k")
	}
	if ok, _ := th.allowed("k"); !ok {
		t.Fatal("a successful sign-in resets the count")
	}
}

// Keys that have fully recovered are dropped, so the map can't grow forever.
func TestThrottle_ForgetsRecoveredKeys(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	th := newThrottle(5, 15*time.Minute, func() time.Time { return now })
	th.failed("old")
	now = now.Add(16 * time.Minute)
	th.failed("new")
	if _, kept := th.keys["old"]; kept {
		t.Fatal("a key idle for longer than the window is forgotten")
	}
}
