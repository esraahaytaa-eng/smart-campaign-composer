package worker

import (
	"math/rand/v2"
	"testing"
	"time"
)

func TestBackoff_GrowsExponentiallyWithJitter(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 1))
	for attempt, base := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 8 * time.Second} {
		seen := map[time.Duration]bool{}
		for range 50 {
			d := Backoff(attempt, 0, rnd)
			if d < base/2 || d > base {
				t.Fatalf("attempt %d: %v outside [%v, %v]", attempt, d, base/2, base)
			}
			seen[d] = true
		}
		if len(seen) < 10 {
			t.Fatalf("attempt %d: jitter missing, only %d distinct delays", attempt, len(seen))
		}
	}
}

func TestBackoff_CappedAt30s(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 1))
	for _, attempt := range []int{6, 10, 40} {
		if d := Backoff(attempt, 0, rnd); d > 30*time.Second || d < 15*time.Second {
			t.Fatalf("attempt %d: %v, want within [15s, 30s]", attempt, d)
		}
	}
}

// D7: Retry-After is honoured — the retry is never earlier than the provider asked.
func TestBackoff_HonoursRetryAfter(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 1))
	if d := Backoff(1, 3*time.Second, rnd); d != 3*time.Second {
		t.Fatalf("Retry-After 3s on attempt 1: want 3s, got %v", d)
	}
	if d := Backoff(5, time.Second, rnd); d < 8*time.Second {
		t.Fatalf("a short Retry-After must not shorten a longer backoff, got %v", d)
	}
	if d := Backoff(1, time.Hour, rnd); d != 5*time.Minute {
		t.Fatalf("an absurd Retry-After is capped at 5 minutes, got %v", d)
	}
}
