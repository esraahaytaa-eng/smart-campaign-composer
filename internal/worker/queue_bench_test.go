package worker

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/dbtest"
)

// queryCounter counts the statements the pool runs.
type queryCounter struct{ n atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}

func (c *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// benchQueue seeds 3,000 customers, ten finished campaigns of 6,000 messages
// each (history), and BENCH_DUE_CAMPAIGNS (default 1) sending campaigns whose
// 6,000 messages each are all due, as after a send starts.
func benchQueue(b *testing.B, pool *pgxpool.Pool) {
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		b.Fatal(err)
	}
	if err := db.Seed(ctx, pool, 1, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), "password1"); err != nil {
		b.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			b.Fatalf("%v: %s", err, sql)
		}
	}
	due := 1
	if n, err := strconv.Atoi(os.Getenv("BENCH_DUE_CAMPAIGNS")); err == nil && n > 0 {
		due = n
	}
	for i := 0; i < 10+due; i++ {
		status, msg := "completed", "sent"
		if i >= 10 {
			status, msg = "sending", "pending"
		}
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO campaigns (request_text, channels, rules, status, created_by, time_zone, audience_built_at, default_language)
			VALUES ('bench', '{sms,email}', '[]', $1, 1, 'Europe/Istanbul', now(), 'tr') RETURNING id`, status).Scan(&id); err != nil {
			b.Fatal(err)
		}
		exec(`INSERT INTO campaign_languages (campaign_id, language) VALUES ($1, 'tr')`, id)
		exec(`INSERT INTO campaign_content (campaign_id, channel, language, subject, body) VALUES
			($1, 'sms', 'tr', NULL, 'Merhaba {{first_name}}!'), ($1, 'email', 'tr', 'Merhaba', 'Merhaba {{first_name}}!')`, id)
		// One build time per campaign, as buildOne gives it: every row ties on next_attempt_at.
		exec(`INSERT INTO messages (campaign_id, user_id, channel, status, language, attempt_count, next_attempt_at)
			SELECT $1, u.id, ch, $2, 'tr', ($2 = 'sent')::int, now() - interval '1 minute'
			FROM users u CROSS JOIN unnest('{sms,email}'::text[]) ch`, id, msg)
		exec(`INSERT INTO delivery_attempts (message_id, attempt_no, started_at, finished_at, outcome, http_status)
			SELECT id, 1, now(), now(), 'accepted', 200 FROM messages WHERE campaign_id = $1 AND status = 'sent'`, id)
	}
	exec(`ANALYZE`)
}

// BenchmarkQueue times the worker's database path per message (claim, the two
// loads, complete) against a due backlog, one at a time and with the worker's
// 20 senders per channel. With -v it prints the claim's plan.
// Run: go test -run '^$' -bench Queue/one -benchtime 2000x -v ./internal/worker/
// and -bench Queue/20 -benchtime 1x for the throughput of one 4,000-message round.
// More backlog: BENCH_DUE_CAMPAIGNS=5 (30,000 due messages).
func BenchmarkQueue(b *testing.B) {
	base := dbtest.FreshDB(b)
	benchQueue(b, base)
	cfg := base.Config().Copy()
	qc := &queryCounter{}
	cfg.ConnConfig.Tracer = qc
	cfg.MaxConns = 44 // 2 × 20 senders, the campaign loop and spare: connection waits out of the measurement
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(pool.Close)
	w := &Worker{Queue: &Queue{Pool: pool}}
	ctx := context.Background()
	var due int
	pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE status = 'pending'`).Scan(&due)
	b.Logf("%d due messages", due)

	one := func(channel string) bool {
		jobs, err := w.Queue.Claim(ctx, channel, 1, time.Now(), DefaultLease)
		if err != nil {
			b.Fatal(err)
		}
		if len(jobs) == 0 {
			return false
		}
		for range 2 {
			if _, err := w.load(ctx, jobs[0]); err != nil {
				b.Fatal(err)
			}
		}
		now := time.Now()
		if err := w.Queue.Complete(ctx, jobs[0], Result{Status: "sent",
			Attempt: &Attempt{StartedAt: now, FinishedAt: now, Outcome: "accepted", HTTPStatus: 200}}); err != nil {
			b.Fatal(err)
		}
		return true
	}

	if testing.Verbose() {
		rows, err := base.Query(ctx, `EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) SELECT id FROM messages WHERE channel = 'sms'
				AND status IN ('pending', 'retry_scheduled') AND next_attempt_at <= now()
			ORDER BY next_attempt_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			b.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var line string
			rows.Scan(&line)
			plan = append(plan, line)
		}
		b.Logf("claim plan:\n%s", strings.Join(plan, "\n"))
	}

	b.Run("one_at_a_time", func(b *testing.B) {
		start := qc.n.Load()
		for b.Loop() {
			if !one("sms") {
				b.Fatal("backlog ran out; raise BENCH_DUE_CAMPAIGNS")
			}
		}
		b.ReportMetric(float64(qc.n.Load()-start)/float64(b.N), "queries/msg")
	})
	b.Run("20_senders_per_channel", func(b *testing.B) {
		var sent atomic.Int64
		start := time.Now()
		for b.Loop() {
			var wg sync.WaitGroup
			for _, ch := range []string{"sms", "email"} {
				for range DefaultPoolSize {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for i := 0; i < 100 && one(ch); i++ { // 4,000 messages per round
							sent.Add(1)
						}
					}()
				}
			}
			wg.Wait()
		}
		b.ReportMetric(float64(sent.Load())/time.Since(start).Seconds(), "msgs/s")
	})
}
