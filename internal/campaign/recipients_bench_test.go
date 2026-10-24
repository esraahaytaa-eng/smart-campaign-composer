package campaign

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/dbtest"
)

// sqlRecorder keeps the statements a handler runs, so the benchmark can
// explain exactly those.
type sqlRecorder struct {
	mu    sync.Mutex
	on    bool
	stmts []pgx.TraceQueryStartData
}

func (r *sqlRecorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sql := strings.TrimSpace(d.SQL); r.on && (strings.HasPrefix(sql, "SELECT") || strings.HasPrefix(sql, "WITH")) {
		r.stmts = append(r.stmts, d)
	}
	return ctx
}

func (r *sqlRecorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// benchCampaign seeds 3,000 customers and a campaign that reached all of them
// on both channels (6,000 messages) with a typical mix of outcomes, plus nine
// older campaigns of the same size (BENCH_CAMPAIGNS sets the total) so the
// tables are not trivially small.
// It returns the newest campaign's id.
func benchCampaign(b *testing.B, pool *pgxpool.Pool) int64 {
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
	campaigns := 10
	if n, err := strconv.Atoi(os.Getenv("BENCH_CAMPAIGNS")); err == nil && n > 0 {
		campaigns = n // more history: BENCH_CAMPAIGNS=30
	}
	var id int64
	for i := 0; i < campaigns; i++ {
		if err := pool.QueryRow(ctx, `INSERT INTO campaigns (request_text, channels, rules, status, created_by, time_zone, audience_built_at)
			VALUES ('bench', '{sms,email}', '[]', 'sending', 1, 'Europe/Istanbul', now()) RETURNING id`).Scan(&id); err != nil {
			b.Fatal(err)
		}
		exec(`INSERT INTO campaign_languages (campaign_id, language) VALUES ($1, 'tr')`, id)
		exec(`UPDATE campaigns SET default_language = 'tr' WHERE id = $1`, id)
		exec(`INSERT INTO campaign_content (campaign_id, channel, language, subject, body) VALUES
			($1, 'sms', 'tr', NULL, 'Merhaba {{first_name}} {{last_name}}, sepetin {{city}} için bekliyor! %20 indirim: SEPET20'),
			($1, 'email', 'tr', '{{first_name}}, sepetin seni bekliyor', 'Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor.')`, id)
		// Of every 100 messages: 85 sent, 5 skipped, 1 failed, 1 uncertain, 3 retrying, 5 pending.
		exec(`INSERT INTO messages (campaign_id, user_id, channel, status, skip_reason, delivery_uncertain, attempt_count, next_attempt_at)
			SELECT $1, id, ch,
				CASE WHEN k < 85 THEN 'sent' WHEN k < 90 THEN 'skipped' WHEN k < 92 THEN 'failed'
					WHEN k < 95 THEN 'retry_scheduled' ELSE 'pending' END,
				CASE WHEN k BETWEEN 85 AND 87 THEN 'character_limit_exceeded' WHEN k BETWEEN 88 AND 89 THEN 'no_consent' END,
				k = 91,
				CASE WHEN k < 85 THEN 1 + (id % 10 = 0)::int WHEN k = 90 THEN 5 WHEN k = 91 THEN 2 WHEN k < 95 AND k >= 92 THEN 1 ELSE 0 END,
				now() + interval '5 seconds'
			FROM (SELECT u.id, ch, (u.id + length(ch)) % 100 k FROM users u CROSS JOIN unnest('{sms,email}'::text[]) ch) x`, id)
		exec(`INSERT INTO delivery_attempts (message_id, attempt_no, started_at, finished_at, outcome, http_status)
			SELECT m.id, n, t, t + interval '80 ms',
				CASE WHEN n < m.attempt_count THEN 'rate_limited'
					WHEN m.status = 'sent' THEN 'accepted' WHEN m.delivery_uncertain THEN 'timeout'
					WHEN m.status = 'failed' THEN 'server_error' ELSE 'server_error' END,
				CASE WHEN n < m.attempt_count THEN 429 WHEN m.status = 'sent' THEN 200 WHEN m.delivery_uncertain THEN NULL ELSE 503 END
			FROM messages m CROSS JOIN LATERAL generate_series(1, m.attempt_count) n
			CROSS JOIN LATERAL (SELECT now() - (m.id % 3600) * interval '1 second' + n * interval '2 seconds' t) x
			WHERE m.campaign_id = $1`, id)
	}
	exec(`ANALYZE`)
	return id
}

// BenchmarkRecipients times the recipient API and the status snapshot on a
// 3,000-customer campaign. With -v it also prints the plan of every query.
// Run: go test -run '^$' -bench Recipients -benchtime 50x -v ./internal/campaign/
func BenchmarkRecipients(b *testing.B) {
	base := dbtest.FreshDB(b)
	id := benchCampaign(b, base)
	cfg := base.Config().Copy()
	rec := &sqlRecorder{}
	cfg.ConnConfig.Tracer = rec
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(pool.Close)
	h := &Handler{Pool: pool, Now: time.Now}
	ctx := context.Background()

	var counts struct{ total, sent int }
	pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status = 'sent') FROM messages WHERE campaign_id = $1`, id).Scan(&counts.total, &counts.sent)
	b.Logf("campaign %d: %d messages, %d sent", id, counts.total, counts.sent)

	cases := []struct {
		name, group, channel, q string
		page                    int
	}{
		{"sent_first_page", "sent", "", "", 1},
		{"sent_last_page", "sent", "", "", counts.sent/recipientsPageSize + 1},
		{"sent_name_search", "sent", "", "ay", 1},
		{"failed_sms", "failed", "sms", "", 1},
		{"skipped_with_detail", "skipped", "", "", 1},
		{"pending_never_attempted", "pending", "", "", 1},
	}
	explain := func(name string, run func()) {
		if !testing.Verbose() {
			return
		}
		rec.mu.Lock()
		rec.on, rec.stmts = true, nil
		rec.mu.Unlock()
		run()
		rec.mu.Lock()
		rec.on = false
		stmts := rec.stmts
		rec.mu.Unlock()
		for _, s := range stmts {
			rows, err := base.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) "+s.SQL, s.Args...)
			if err != nil {
				b.Fatal(err)
			}
			var plan []string
			for rows.Next() {
				var line string
				rows.Scan(&line)
				plan = append(plan, line)
			}
			b.Logf("--- %s\n%s\n%s", name, strings.Join(strings.Fields(s.SQL), " ")[:min(160, len(strings.Join(strings.Fields(s.SQL), " ")))], strings.Join(plan, "\n"))
		}
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			explain(c.name, func() { h.listRecipients(ctx, id, c.group, c.channel, c.q, "", "", c.page) })
			for b.Loop() {
				out, err := h.listRecipients(ctx, id, c.group, c.channel, c.q, "", "", c.page)
				if err != nil || (c.group == "sent" && c.q == "" && out.Total != counts.sent) {
					b.Fatalf("%v, total %d", err, out.Total)
				}
			}
		})
	}
	b.Run("status_snapshot", func(b *testing.B) {
		explain("status_snapshot", func() { h.snapshot(ctx, id) })
		for b.Loop() {
			if _, err := h.snapshot(ctx, id); err != nil {
				b.Fatal(err)
			}
		}
	})
}
