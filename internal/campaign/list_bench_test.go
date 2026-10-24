package campaign

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/dbtest"
)

// BenchmarkCampaignList times the campaign list and the dashboard (D54) with
// 10,000 campaigns (target P4: ≤ 100 ms for a page with search and filter).
// With -v it also prints the plan of every query.
// Run: go test -run '^$' -bench CampaignList -benchtime 50x -v ./internal/campaign/
func BenchmarkCampaignList(b *testing.B) {
	base := dbtest.MigratedDB(b)
	ctx := context.Background()
	for _, sql := range []string{
		`INSERT INTO marketers (id, email, name, password_hash) VALUES (1, 'm@example.com', 'M', 'x')`,
		// 10,000 campaigns across the statuses, briefs with varied words.
		`INSERT INTO campaigns (request_text, channels, rules, status, time_zone, created_by, send_at, updated_at)
			SELECT (ARRAY['Sepet hatırlatması İstanbul', 'Ankara hafta sonu indirimi', 'Welcome discount for new users',
				'Kitap kampanyası Işıl', 'Cart reminder 20% off'])[1 + i % 5] || ' #' || i, '{sms}', '[]',
				(ARRAY['draft', 'approved', 'sending', 'completed', 'cancelled', 'completed', 'completed'])[1 + i % 7],
				'UTC', 1, now() + (i % 30) * interval '1 day', now() - i * interval '1 minute'
			FROM generate_series(1, 10000) i`,
		`ANALYZE campaigns`,
	} {
		if _, err := base.Exec(ctx, sql); err != nil {
			b.Fatal(err)
		}
	}
	cfg := base.Config().Copy()
	rec := &sqlRecorder{}
	cfg.ConnConfig.Tracer = rec
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(pool.Close)
	h := &Handler{Pool: pool, Now: time.Now}

	for _, c := range []struct{ name, path string }{
		{"first_page", "/api/campaigns"},
		{"status_filter", "/api/campaigns?status=completed"},
		{"search_and_filter", "/api/campaigns?status=draft&q=istanbul"},
		{"last_page", "/api/campaigns?page=400"},
		{"dashboard", "/api/dashboard"},
	} {
		handler := h.list
		if strings.HasPrefix(c.path, "/api/dashboard") {
			handler = h.dashboard
		}
		serve := func() int {
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest(http.MethodGet, c.path, nil))
			return w.Code
		}
		b.Run(c.name, func(b *testing.B) {
			if testing.Verbose() {
				rec.mu.Lock()
				rec.on, rec.stmts = true, nil
				rec.mu.Unlock()
				serve()
				rec.mu.Lock()
				rec.on = false
				stmts := rec.stmts
				rec.mu.Unlock()
				for _, s := range stmts {
					rows, err := base.Query(ctx, "EXPLAIN (ANALYZE, COSTS OFF) "+s.SQL, s.Args...)
					if err != nil {
						b.Fatal(err)
					}
					var plan []string
					for rows.Next() {
						var line string
						rows.Scan(&line)
						plan = append(plan, line)
					}
					b.Logf("--- %s\n%s", c.name, strings.Join(plan, "\n"))
				}
			}
			for b.Loop() {
				if code := serve(); code != http.StatusOK {
					b.Fatalf("status %d", code)
				}
			}
		})
	}
}
