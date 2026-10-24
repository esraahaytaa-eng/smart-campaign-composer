package worker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/worker"
)

// slowLoads delays the worker's message loads, as a busy database would.
type slowLoads struct{ d time.Duration }

func (s slowLoads) TraceQueryStart(ctx context.Context, _ *pgx.Conn, q pgx.TraceQueryStartData) context.Context {
	if strings.Contains(q.SQL, "JOIN campaign_content cc") {
		time.Sleep(s.d)
	}
	return ctx
}

func (slowLoads) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// Audit F4: the departure the rate cap counts is when the request leaves, not
// before the last database check, so a slow database can't bunch requests at
// the provider beyond what the cap allowed.
func TestProcess_RecordedDepartureIsTheRealDeparture(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.sendingWithContent("Merhaba {{first_name}}")
	cfg := f.pool.Config().Copy()
	cfg.ConnConfig.Tracer = slowLoads{200 * time.Millisecond}
	slow, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(slow.Close)
	p := &fakeProvider{}
	w := f.newWorker(p.serve(t))
	w.Queue = &worker.Queue{Pool: slow}

	job := f.claimOne("sms", 1)
	if err := w.Process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	var started time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT started_at FROM delivery_attempts WHERE message_id = $1`, job.MessageID).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 1 {
		t.Fatalf("want one request, got %d", len(p.requests))
	}
	if gap := p.requests[0].at.Sub(started); gap > 50*time.Millisecond {
		t.Fatalf("the request reached the provider %v after its recorded departure; the database wait is counted inside the cap", gap)
	}
}
