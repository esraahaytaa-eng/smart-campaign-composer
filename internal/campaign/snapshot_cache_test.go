package campaign

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a settable time for the cache tests.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// D59: the streams of one campaign share its snapshot.
func TestSnapshotCache_StreamsShareOneRead(t *testing.T) {
	clk := &clock{now: time.Unix(0, 0)}
	cache := &snapshotCache{now: clk.Now}
	var reads atomic.Int32
	release := make(chan struct{})
	load := func(ctx context.Context, id int64) (statusSnapshot, error) {
		reads.Add(1)
		<-release // slow enough that every stream asks while it runs
		return statusSnapshot{Status: "sending"}, nil
	}

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, err := cache.get(context.Background(), 7, 500*time.Millisecond, load); err != nil || s.Status != "sending" {
				t.Errorf("got %+v, %v", s, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond) // let all 20 ask
	close(release)
	wg.Wait()
	if n := reads.Load(); n != 1 {
		t.Fatalf("20 streams asking at once: want 1 database read, got %d", n)
	}

	cache.get(context.Background(), 7, 500*time.Millisecond, load)
	if n := reads.Load(); n != 1 {
		t.Fatalf("a fresh snapshot is reused, got %d reads", n)
	}
	clk.add(500 * time.Millisecond)
	cache.get(context.Background(), 7, 500*time.Millisecond, load)
	if n := reads.Load(); n != 2 {
		t.Fatalf("an old snapshot is read again, got %d reads", n)
	}
	cache.get(context.Background(), 8, 500*time.Millisecond, load)
	if n := reads.Load(); n != 3 {
		t.Fatalf("another campaign has its own snapshot, got %d reads", n)
	}
}

func TestSnapshotCache_ErrorsAreNotKept(t *testing.T) {
	cache := &snapshotCache{now: (&clock{now: time.Unix(0, 0)}).Now}
	fail := errors.New("database down")
	calls := 0
	load := func(ctx context.Context, id int64) (statusSnapshot, error) {
		calls++
		if calls == 1 {
			return statusSnapshot{}, fail
		}
		return statusSnapshot{Status: "completed"}, nil
	}
	if _, err := cache.get(context.Background(), 7, time.Second, load); !errors.Is(err, fail) {
		t.Fatalf("want the error, got %v", err)
	}
	if s, err := cache.get(context.Background(), 7, time.Second, load); err != nil || s.Status != "completed" {
		t.Fatalf("the next stream reads again: %+v, %v", s, err)
	}
}

// The stream that started a read may leave; the others still get the snapshot.
func TestSnapshotCache_ALeavingStreamDoesNotCancelTheRead(t *testing.T) {
	cache := &snapshotCache{now: (&clock{now: time.Unix(0, 0)}).Now}
	started, release := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context, id int64) (statusSnapshot, error) {
		close(started)
		<-release
		return statusSnapshot{Status: "sending"}, ctx.Err()
	}
	leaving, leave := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		_, err := cache.get(leaving, 7, time.Second, load)
		done <- err
	}()
	<-started
	waiter := make(chan statusSnapshot)
	go func() {
		s, _ := cache.get(context.Background(), 7, time.Second, load)
		waiter <- s
	}()
	time.Sleep(20 * time.Millisecond)
	leave()
	close(release)
	<-done
	if s := <-waiter; s.Status != "sending" {
		t.Fatalf("the waiting stream got %+v", s)
	}
}

func TestSnapshotCache_DropsCampaignsNobodyReads(t *testing.T) {
	clk := &clock{now: time.Unix(0, 0)}
	cache := &snapshotCache{now: clk.Now}
	load := func(ctx context.Context, id int64) (statusSnapshot, error) { return statusSnapshot{}, nil }
	for id := range int64(100) {
		cache.get(context.Background(), id, time.Second, load)
	}
	clk.add(2 * time.Minute)
	cache.get(context.Background(), 1000, time.Second, load)
	if n := len(cache.entries); n != 1 {
		t.Fatalf("want only the campaign read just now, got %d entries", n)
	}
}
