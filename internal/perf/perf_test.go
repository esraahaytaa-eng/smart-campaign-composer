//go:build perf

package perf

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The stack under test (make perf: docker-compose.perf.yml). DATABASE_URL is
// an admin login, used for fixtures and for reading results.
var (
	base     = env("BASE", "http://localhost:8080")
	password = env("DEMO_PASSWORD", "Demo1234!")
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

const finalStatuses = `('sent', 'failed', 'skipped', 'cancelled')`

type result struct {
	id, target, measured string
	ok                   bool
}

// TestPerformance measures targets P1–P6 on the running stack, in order: a
// full send watched by 20 live screens, then 10 marketers using the API.
func TestPerformance(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var results []result
	add := func(id, target string, ok bool, measured string, args ...any) {
		results = append(results, result{id, target, fmt.Sprintf(measured, args...), ok})
	}

	c := signIn(t)
	var catalog struct{ Cities []string }
	c.decode(c.must(http.MethodGet, "/api/catalog", nil, 200), &catalog)

	if os.Getenv("PERF_ONLY") != "marketers" { // PERF_ONLY=marketers skips the send when trying load settings
		t.Run("full send", func(t *testing.T) { fullSend(t, ctx, pool, c, catalog.Cities, add) })
	}
	t.Run("marketers", func(t *testing.T) { marketers(t, ctx, pool, catalog.Cities, add) })

	var b strings.Builder
	fmt.Fprintf(&b, "\n%-4s %-60s %-70s %s\n", "", "target", "measured", "met")
	for _, r := range results {
		fmt.Fprintf(&b, "%-4s %-60s %-70s %s\n", r.id, r.target, r.measured, map[bool]string{true: "yes", false: "NO"}[r.ok])
		if !r.ok {
			t.Errorf("%s missed: %s (measured %s)", r.id, r.target, r.measured)
		}
	}
	t.Log(b.String())
}

// fullSend: P1 (send time), P2 (rate limit) and P6 (live screens).
func fullSend(t *testing.T, ctx context.Context, pool *pgxpool.Pool, c *client, cities []string, add func(string, string, bool, string, ...any)) {
	var draft struct{ ID, Version int64 }
	c.decode(c.must(http.MethodPost, "/api/campaigns", map[string]any{
		"request": "Performance check: every consenting customer", "channels": []string{"sms", "email"}, "time_zone": "Europe/Istanbul",
		"rules": []any{map[string]any{"field": "city", "op": "in", "value": cities}},
	}, 201), &draft)
	c.decode(c.must(http.MethodPut, fmt.Sprintf("/api/campaigns/%d/content", draft.ID), map[string]any{"version": draft.Version, "default_language": "tr", "content": map[string]any{"tr": map[string]any{
		"sms":   map[string]string{"body": "Merhaba {{first_name}}, hafta sonu indirimi başladı!"},
		"email": map[string]string{"subject": "Hafta sonu indirimi", "body": "Merhaba {{first_name}}, hafta sonu indirimi başladı."},
	}, "en": map[string]any{ // two languages (D64): each recipient gets their own
		"sms":   map[string]string{"body": "Hi {{first_name}}, the weekend sale has started!"},
		"email": map[string]string{"subject": "Weekend sale", "body": "Hi {{first_name}}, the weekend sale has started."},
	}}}, 200), &draft)

	// 20 live screens, connected before sending starts.
	const screens = 20
	streamCtx, stopStreams := context.WithCancel(ctx)
	defer stopStreams()
	events := make([][]screenEvent, screens)
	var connected, done sync.WaitGroup
	connected.Add(screens)
	done.Add(screens)
	for i := range screens {
		go func() {
			defer done.Done()
			events[i] = c.watch(streamCtx, draft.ID, connected.Done)
		}()
	}
	connected.Wait()

	// What the database holds, every 100 ms, to tell how far behind each screen is.
	type sample struct {
		at        time.Time
		final     int
		completed bool
	}
	var samples []sample
	polled := make(chan struct{})
	go func() {
		defer close(polled)
		for {
			var s sample
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM messages WHERE campaign_id = $1 AND status IN `+finalStatuses+`),
				status = 'completed' FROM campaigns WHERE id = $1`, draft.ID).Scan(&s.final, &s.completed); err != nil {
				t.Error(err)
				return
			}
			s.at = time.Now()
			samples = append(samples, s)
			if s.completed {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	if _, err := pool.Exec(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatal(err)
	}
	statsFrom := time.Now()
	for _, lang := range []string{"tr", "en"} {
		c.must(http.MethodPost, fmt.Sprintf("/api/campaigns/%d/languages/%s/approve", draft.ID, lang), map[string]any{"version": draft.Version}, 200)
	}
	c.must(http.MethodPost, fmt.Sprintf("/api/campaigns/%d/approve", draft.ID), map[string]any{"version": draft.Version, "send": "now"}, 200)
	select {
	case <-polled:
	case <-time.After(6 * time.Minute):
		t.Fatal("the send did not complete within 6 minutes")
	}
	waitOrStop(&done, 10*time.Second, stopStreams)
	statsTo := time.Now()

	// P1: from sending starting (audience built) to each message's final state.
	var start, end time.Time
	var total int
	must(t, pool.QueryRow(ctx, `SELECT audience_built_at, completed_at, (SELECT count(*) FROM messages WHERE campaign_id = $1)
		FROM campaigns WHERE id = $1`, draft.ID).Scan(&start, &end, &total))
	finals := floats(t, pool, `SELECT extract(epoch FROM updated_at - $2) FROM messages WHERE campaign_id = $1 AND status IN `+finalStatuses+` ORDER BY 1`, draft.ID, start)
	breakdown := text(t, pool, `SELECT string_agg(channel || ' ' || status || ' ' || n, ', ' ORDER BY channel, status)
		FROM (SELECT channel, status, count(*) n FROM messages WHERE campaign_id = $1 GROUP BY 1, 2) x`, draft.ID)
	t.Logf("messages: %d (%s)", total, breakdown)
	p95, last := pct(finals, 95), slices.Max(finals)
	add("P1", "full send: 95% final ≤ 35 s, all ≤ 75 s", len(finals) == total && p95 <= 35 && last <= 75,
		"%d messages: 95%% at %.1f s, all at %.1f s", total, p95, last)

	// P2: provider requests per channel in any 1-second window, retries included.
	var worst []string
	ok := true
	for _, ch := range []string{"sms", "email"} {
		starts := floats(t, pool, `SELECT extract(epoch FROM a.started_at) FROM delivery_attempts a JOIN messages m ON m.id = a.message_id
			WHERE m.campaign_id = $1 AND m.channel = $2 ORDER BY 1`, draft.ID, ch)
		peak := 0
		for i, j := 0, 0; j < len(starts); j++ {
			for starts[j]-starts[i] >= 1 {
				i++
			}
			peak = max(peak, j-i+1)
		}
		ok = ok && peak <= 100
		worst = append(worst, fmt.Sprintf("%s %d (of %d attempts)", ch, peak, len(starts)))
	}
	t.Logf("attempts by outcome: %s", text(t, pool, `SELECT string_agg(coalesce(outcome, 'none') || ' ' || n, ', ' ORDER BY n DESC)
		FROM (SELECT a.outcome, count(*) n FROM delivery_attempts a JOIN messages m ON m.id = a.message_id WHERE m.campaign_id = $1 GROUP BY 1) x`, draft.ID))
	add("P2", "at most 100 requests per channel in any 1 s window", ok, "peak %s", strings.Join(worst, ", "))

	// P6: the api's database time while 20 screens watched, and how far behind they were.
	var apiMS float64
	var calls int64
	must(t, pool.QueryRow(ctx, `SELECT coalesce(sum(total_exec_time), 0), coalesce(sum(calls), 0)::bigint FROM pg_stat_statements s
		JOIN pg_roles r ON r.oid = s.userid WHERE r.rolname = 'scc_api'`).Scan(&apiMS, &calls))
	perSecond := apiMS / statsTo.Sub(statsFrom).Seconds()
	reached := func(final int, completed bool) time.Time {
		for _, s := range samples {
			if s.final >= final && (!completed || s.completed) {
				return s.at
			}
		}
		return samples[len(samples)-1].at
	}
	var lags []float64
	updates, ended := 0, 0
	for _, screen := range events {
		for _, e := range screen {
			if e.at.Before(statsFrom) {
				continue // the snapshot sent on connecting
			}
			lags = append(lags, max(0, e.at.Sub(reached(e.final, e.end)).Seconds()))
			updates++
			if e.end {
				ended++
			}
		}
	}
	slices.Sort(lags)
	add("P6", "20 live screens: db time ≤ 400 ms/s, each update ≤ 2 s behind", perSecond <= 400 && len(lags) > 0 && lags[len(lags)-1] <= 2 && ended == screens,
		"%.0f ms/s (%d statements); %d updates, behind by p95 %.2f s, max %.2f s; %d of %d screens got the end", perSecond, calls, updates, pct(lags, 95), lags[len(lags)-1], ended, screens)
}

// marketers: P3 (10 concurrent marketers), P4 (campaign list) and P5 (recipient details).
func marketers(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cities []string, add func(string, string, bool, string, ...any)) {
	var marketer int64
	must(t, pool.QueryRow(ctx, `SELECT id FROM marketers WHERE email = 'marketer@marketer.com'`).Scan(&marketer))
	// 10,000 campaigns (none sending: the worker leaves them alone; approved ones send in days).
	var firstID, lastID int64
	must(t, pool.QueryRow(ctx, `WITH c AS (INSERT INTO campaigns (request_text, channels, rules, status, time_zone, created_by, send_at, updated_at)
		SELECT (ARRAY['Sepet hatırlatması İstanbul', 'Ankara hafta sonu indirimi', 'Welcome discount for new users',
			'Kitap kampanyası Işıl', 'Cart reminder 20% off'])[1 + i % 5] || ' #' || i, '{sms}', '[]',
			(ARRAY['draft', 'approved', 'completed', 'cancelled', 'completed', 'completed'])[1 + i % 6],
			'UTC', $1, now() + (1 + i % 30) * interval '1 day', now() - i * interval '1 minute'
		FROM generate_series(1, 10000) i RETURNING id) SELECT min(id), max(id) FROM c`, marketer).Scan(&firstID, &lastID))
	// A finished campaign that reached every customer on both channels: 6,000
	// messages in the mix of the recipient benchmark (docs/performance.md).
	var big int64
	must(t, pool.QueryRow(ctx, `INSERT INTO campaigns (request_text, channels, rules, status, created_by, time_zone, audience_built_at, completed_at)
		VALUES ('Performance check: 6,000 messages', '{sms,email}', '[]', 'completed', $1, 'Europe/Istanbul', now(), now()) RETURNING id`, marketer).Scan(&big))
	for _, sql := range []string{
		`INSERT INTO messages (campaign_id, user_id, channel, status, skip_reason, delivery_uncertain, attempt_count, next_attempt_at)
			SELECT $1, id, ch,
				CASE WHEN k < 85 THEN 'sent' WHEN k < 90 THEN 'skipped' WHEN k < 92 THEN 'failed' ELSE 'cancelled' END,
				CASE WHEN k BETWEEN 85 AND 87 THEN 'character_limit_exceeded' WHEN k BETWEEN 88 AND 89 THEN 'no_consent' END,
				k = 91,
				CASE WHEN k < 85 THEN 1 + (id % 10 = 0)::int WHEN k = 90 THEN 5 WHEN k = 91 THEN 2 ELSE 0 END,
				now()
			FROM (SELECT u.id, ch, (u.id + length(ch)) % 100 k FROM users u CROSS JOIN unnest('{sms,email}'::text[]) ch) x`,
		`INSERT INTO delivery_attempts (message_id, attempt_no, started_at, finished_at, outcome, http_status)
			SELECT m.id, n, t, t + interval '80 ms',
				CASE WHEN n < m.attempt_count THEN 'rate_limited' WHEN m.status = 'sent' THEN 'accepted'
					WHEN m.delivery_uncertain THEN 'timeout' ELSE 'server_error' END,
				CASE WHEN n < m.attempt_count THEN 429 WHEN m.status = 'sent' THEN 200 WHEN m.delivery_uncertain THEN NULL ELSE 503 END
			FROM messages m CROSS JOIN LATERAL generate_series(1, m.attempt_count) n
			CROSS JOIN LATERAL (SELECT now() - (m.id % 3600) * interval '1 second' + n * interval '2 seconds' t) x
			WHERE m.campaign_id = $1`,
	} {
		if _, err := pool.Exec(ctx, sql, big); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}

	duration, err := time.ParseDuration(env("PERF_DURATION", "2m"))
	if err != nil {
		t.Fatal(err)
	}
	// PERF_THINK is a pause after each request (0: back to back, the worst case);
	// PERF_MARKETERS how many work at once.
	think, err := time.ParseDuration(env("PERF_THINK", "0s"))
	if err != nil {
		t.Fatal(err)
	}
	count, err := strconv.Atoi(env("PERF_MARKETERS", "10"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatal(err)
	}
	reads := map[string]bool{"list search+filter": true, "list page": true, "dashboard": true, "campaign": true, "estimate": true, "recipients": true, "catalog": true}
	// One marketer's round: mostly reading, two saves.
	round := []string{"list search+filter", "campaign", "recipients", "estimate", "list page", "save messages", "dashboard", "recipients", "save rules", "campaign", "list search+filter", "catalog"}
	groups := []string{"sent", "failed", "skipped", "cancelled"}

	type timing struct {
		op     string
		ms     float64
		status int
	}
	timings := make([][]timing, count)
	var wg sync.WaitGroup
	stop := time.Now().Add(duration)
	for m := range timings {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := signIn(t)
			rng := rand.New(rand.NewPCG(uint64(m), 7))
			var own struct{ ID, Version int64 }
			c.decode(c.must(http.MethodPost, "/api/campaigns", map[string]any{"request": fmt.Sprintf("Marketer %d's draft", m), "channels": []string{"sms"},
				"time_zone": "Europe/Istanbul", "rules": []any{map[string]any{"field": "city", "op": "in", "value": cities[:1]}}}, 201), &own)
			for i := m; time.Now().Before(stop); i++ {
				op := round[i%len(round)]
				var status int
				var body []byte
				var took time.Duration
				switch op {
				case "list search+filter":
					status, body, took = c.call(http.MethodGet, "/api/campaigns?status=draft&q=istanbul", nil)
				case "list page":
					status, body, took = c.call(http.MethodGet, fmt.Sprintf("/api/campaigns?page=%d", 1+rng.IntN(400)), nil)
				case "dashboard":
					status, body, took = c.call(http.MethodGet, "/api/dashboard", nil)
				case "campaign":
					status, body, took = c.call(http.MethodGet, fmt.Sprintf("/api/campaigns/%d", firstID+rng.Int64N(lastID-firstID+1)), nil)
				case "estimate":
					status, body, took = c.call(http.MethodGet, fmt.Sprintf("/api/campaigns/%d/estimate", own.ID), nil)
				case "recipients":
					g := groups[rng.IntN(len(groups))]
					status, body, took = c.call(http.MethodGet, fmt.Sprintf("/api/campaigns/%d/recipients?group=%s&page=%d", big, g, 1+rng.IntN(map[string]int{"sent": 100}[g]+1)), nil)
				case "catalog":
					status, body, took = c.call(http.MethodGet, "/api/catalog", nil)
				case "save messages":
					status, body, took = c.call(http.MethodPut, fmt.Sprintf("/api/campaigns/%d/content", own.ID), map[string]any{"version": own.Version, "default_language": "tr",
						"content": map[string]any{"tr": map[string]any{"sms": map[string]string{"body": fmt.Sprintf("Merhaba {{first_name}}, kampanya %d!", i)}},
							"en": map[string]any{"sms": map[string]string{"body": fmt.Sprintf("Hi {{first_name}}, campaign %d!", i)}}}})
				case "save rules":
					status, body, took = c.call(http.MethodPut, fmt.Sprintf("/api/campaigns/%d/rules", own.ID), map[string]any{"version": own.Version,
						"rules": []any{map[string]any{"field": "city", "op": "in", "value": []string{cities[i%len(cities)]}}}})
				}
				if !reads[op] && status == 200 {
					json.Unmarshal(body, &own)
				}
				timings[m] = append(timings[m], timing{op, float64(took.Microseconds()) / 1000, status})
				time.Sleep(think)
			}
		}()
	}
	wg.Wait()

	byOp := map[string][]float64{}
	var allReads, allWrites []float64
	serverErrors, unexpected := 0, map[string]int{}
	for _, ts := range timings {
		for _, x := range ts {
			byOp[x.op] = append(byOp[x.op], x.ms)
			if reads[x.op] {
				allReads = append(allReads, x.ms)
			} else {
				allWrites = append(allWrites, x.ms)
			}
			if x.status >= 500 {
				serverErrors++
			} else if x.status != 200 {
				unexpected[fmt.Sprintf("%s %d", x.op, x.status)]++
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%-20s %8s %8s %8s %8s\n", "request", "count", "p50 ms", "p95 ms", "max ms")
	for _, op := range slices.Sorted(maps.Keys(byOp)) {
		v := byOp[op]
		slices.Sort(v)
		fmt.Fprintf(&b, "%-20s %8d %8.1f %8.1f %8.1f\n", op, len(v), pct(v, 50), pct(v, 95), v[len(v)-1])
	}
	slices.Sort(allReads)
	slices.Sort(allWrites)
	t.Logf("%d marketers, %s pause: %d requests in %s (%.0f per second)%s", count, think, len(allReads)+len(allWrites), duration, float64(len(allReads)+len(allWrites))/duration.Seconds(), b.String())
	t.Logf("the api's database time, top statements:\n%s", text(t, pool, `SELECT string_agg(format('%8s ms total %7s calls %6s ms mean  %s', round(total_exec_time), calls,
		round(mean_exec_time::numeric, 2), left(regexp_replace(query, '\s+', ' ', 'g'), 90)), E'\n' ORDER BY total_exec_time DESC)
		FROM (SELECT * FROM pg_stat_statements s JOIN pg_roles r ON r.oid = s.userid WHERE r.rolname = 'scc_api' ORDER BY total_exec_time DESC LIMIT 8) x`))

	add("P3", fmt.Sprintf("%d marketers, %s pause: p95", count, think)+" reads ≤ 200 ms, writes ≤ 300 ms, 0 server errors",
		pct(allReads, 95) <= 200 && pct(allWrites, 95) <= 300 && serverErrors == 0 && len(unexpected) == 0,
		"reads %.1f ms, writes %.1f ms (%d requests); %d server errors, other non-200: %v", pct(allReads, 95), pct(allWrites, 95), len(allReads)+len(allWrites), serverErrors, unexpected)
	list, recipients := byOp["list search+filter"], byOp["recipients"]
	add("P4", "campaign list, 10,000 campaigns: p95 ≤ 100 ms (search and filter)", pct(list, 95) <= 100,
		"p95 %.1f ms over %d requests", pct(list, 95), len(list))
	add("P5", "recipient details, 6,000 messages: p95 ≤ 50 ms", pct(recipients, 95) <= 50,
		"p95 %.1f ms over %d requests", pct(recipients, 95), len(recipients))
}

// client is one signed-in browser session.
type client struct {
	t      *testing.T
	http   *http.Client
	cookie string
}

func signIn(t *testing.T) *client {
	body, _ := json.Marshal(map[string]string{"email": "marketer@marketer.com", "password": password})
	req, _ := http.NewRequest(http.MethodPost, base+"/api/login", bytes.NewReader(body))
	req.Header.Set("Origin", base)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || len(res.Cookies()) == 0 {
		t.Fatalf("sign-in: %d", res.StatusCode)
	}
	// Ten marketers at once need more than the default two idle connections each way.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 4
	return &client{t: t, http: &http.Client{Transport: transport}, cookie: res.Cookies()[0].String()}
}

// call times one request, from sending it to the end of the answer.
func (c *client) call(method, path string, body any) (int, []byte, time.Duration) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, r)
	req.Header.Set("Cookie", c.cookie)
	req.Header.Set("Origin", base)
	start := time.Now()
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Errorf("%s %s: %v", method, path, err)
		return 0, nil, time.Since(start)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	took := time.Since(start)
	if err != nil {
		c.t.Errorf("%s %s: %v", method, path, err)
	}
	return res.StatusCode, out, took
}

func (c *client) must(method, path string, body any, want int) []byte {
	status, out, _ := c.call(method, path, body)
	if status != want {
		c.t.Fatalf("%s %s: want %d, got %d %s", method, path, want, status, out)
	}
	return out
}

func (c *client) decode(body []byte, v any) {
	if err := json.Unmarshal(body, v); err != nil {
		c.t.Fatal(err)
	}
}

type screenEvent struct {
	at    time.Time
	final int  // messages in a final state, all channels
	end   bool // the stream's "end"
}

// watch reads one live screen's status stream until it ends; connected is
// called once the first snapshot has arrived.
func (c *client) watch(ctx context.Context, id int64, connected func()) []screenEvent {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/campaigns/%d/events", base, id), nil)
	req.Header.Set("Cookie", c.cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Error(err)
		connected()
		return nil
	}
	defer res.Body.Close()
	var out []screenEvent
	once := sync.OnceFunc(connected)
	defer once()
	lines := bufio.NewScanner(res.Body)
	lines.Buffer(make([]byte, 1<<20), 1<<20)
	event, final := "", 0
	for lines.Scan() {
		line := lines.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: ") && event == "status":
			var snap struct {
				Channels map[string]struct{ Counts map[string]int }
			}
			json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &snap)
			final = 0
			for _, ch := range snap.Channels {
				for _, s := range []string{"sent", "failed", "skipped", "cancelled"} {
					final += ch.Counts[s]
				}
			}
			out = append(out, screenEvent{at: time.Now(), final: final})
			once()
		case strings.HasPrefix(line, "data: ") && event == "end":
			return append(out, screenEvent{at: time.Now(), final: final, end: true})
		}
	}
	return out
}

func waitOrStop(wg *sync.WaitGroup, limit time.Duration, stop func()) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(limit):
		stop()
		<-done
	}
}

// pct is the p-th percentile of sorted values (nearest rank).
func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted))*p/100+0.999999) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}

func floats(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) []float64 {
	rows, err := pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	var out []float64
	for rows.Next() {
		var v float64
		must(t, rows.Scan(&v))
		out = append(out, v)
	}
	must(t, rows.Err())
	return out
}

func text(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) string {
	var s *string
	must(t, pool.QueryRow(context.Background(), sql, args...).Scan(&s))
	if s == nil {
		return ""
	}
	return *s
}

func must(t *testing.T, err error) {
	if err != nil {
		t.Helper()
		t.Fatal(err)
	}
}
