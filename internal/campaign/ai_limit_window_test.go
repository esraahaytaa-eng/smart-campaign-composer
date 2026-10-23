package campaign

import (
	"testing"
	"time"
)

// D63: at most n calls in any hour, per marketer; not a token bucket.
func TestAICallLimit_SlidingHour(t *testing.T) {
	t.Parallel()
	clk := &clock{now: time.Unix(0, 0)}
	l := NewAICallLimit(3)
	l.now = clk.Now

	for i := range 3 {
		if wait, ok := l.take(1); !ok {
			t.Fatalf("call %d: refused, wait %v", i+1, wait)
		}
		clk.add(10 * time.Minute)
	}
	// Calls at 0, 10 and 20 minutes; now 30 minutes.
	if wait, ok := l.take(1); ok || wait != 30*time.Minute {
		t.Fatalf("the 4th call within the hour: want refused for 30 min, got %v %v", ok, wait)
	}
	if wait := l.wait(1); wait != 30*time.Minute {
		t.Fatalf("checking does not use a call: want 30 min, got %v", wait)
	}
	if _, ok := l.take(2); !ok {
		t.Fatal("another marketer has their own allowance")
	}
	clk.add(30 * time.Minute) // the first call is an hour old
	if _, ok := l.take(1); !ok {
		t.Fatal("a call is allowed once the oldest is an hour old")
	}
	if _, ok := l.take(1); ok {
		t.Fatal("and only one: the calls at 10 and 20 minutes still count")
	}
}
