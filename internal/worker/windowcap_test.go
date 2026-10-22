package worker

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

// maxInWindow is the most times falling in any window of length w.
func maxInWindow(times []time.Time, w time.Duration) int {
	slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
	most := 0
	for i, j := 0, 0; j < len(times); j++ {
		for times[j].Sub(times[i]) >= w {
			i++
		}
		most = max(most, j-i+1)
	}
	return most
}

func TestWindowCap_NeverMoreThanNInAnyWindow(t *testing.T) {
	c := newWindowCap(5, 100*time.Millisecond)
	var mu sync.Mutex
	var times []time.Time
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			at, err := c.wait(context.Background())
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			times = append(times, at)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if got := maxInWindow(times, 100*time.Millisecond); got > 5 {
		t.Fatalf("%d departures in one window, cap is 5", got)
	}
	if span := slices.MaxFunc(times, func(a, b time.Time) int { return a.Compare(b) }).Sub(slices.MinFunc(times, func(a, b time.Time) int { return a.Compare(b) })); span < 500*time.Millisecond {
		t.Fatalf("30 departures at 5 per 100 ms need at least 500 ms, took %v", span)
	}
}

func TestWindowCap_StopsWaitingWhenCancelled(t *testing.T) {
	c := newWindowCap(1, time.Hour)
	c.wait(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := c.wait(ctx); err == nil {
		t.Fatal("want the context error while the window is full")
	}
}
