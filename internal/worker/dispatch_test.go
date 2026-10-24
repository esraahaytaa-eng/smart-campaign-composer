package worker_test

import (
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"

	"smart-campaign-composer/internal/obstest"
	"smart-campaign-composer/internal/worker"
)

// fakeProvider answers every request with handle and records what it got.
type fakeProvider struct {
	mu       sync.Mutex
	requests []providerRequest
	handle   func(w http.ResponseWriter, r *http.Request)
}

type providerRequest struct {
	channel, key string
	body         map[string]string
	at           time.Time
}

func (p *fakeProvider) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at := time.Now()
		b, _ := io.ReadAll(r.Body)
		var body map[string]string
		json.Unmarshal(b, &body)
		p.mu.Lock()
		p.requests = append(p.requests, providerRequest{r.URL.Path[1 : len(r.URL.Path)-len("/send")], r.Header.Get("Idempotency-Key"), body, at})
		p.mu.Unlock()
		if p.handle != nil {
			p.handle(w, r)
			return
		}
		w.Write([]byte(`{"id":"x","status":"accepted"}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func status(code int, header ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.WriteHeader(code)
	}
}

func (f *fixture) newWorker(url string) *worker.Worker {
	return &worker.Worker{
		Queue:       f.q,
		ProviderURL: url,
		Client:      worker.NewClient(200 * time.Millisecond),
		Limiters:    worker.NewLimiters(),
		MaxAttempts: 5,
		Now:         func() time.Time { return now },
		Rand:        rand.New(rand.NewPCG(1, 1)),
	}
}

// addLanguage gives a campaign a language version; the first one added is the default.
func (f *fixture) addLanguage(id int64, lang string) {
	f.t.Helper()
	f.exec(`INSERT INTO campaign_languages (campaign_id, language) VALUES ($1, $2)`, id, lang)
	f.exec(`UPDATE campaigns SET default_language = coalesce(default_language, $2) WHERE id = $1`, id, lang)
}

// sendingWithContent starts the cart-abandoner campaign with valid content.
func (f *fixture) sendingWithContent(sms string) int64 {
	f.t.Helper()
	id := f.approved(cartAbandoners, "{sms,email}", now)
	f.addLanguage(id, "tr")
	f.exec(`INSERT INTO campaign_content (campaign_id, channel, language, subject, body) VALUES
		($1, 'sms', 'tr', NULL, $2), ($1, 'email', 'tr', '{{first_name}}, sepetin seni bekliyor', $3)`, id, sms, "Merhaba {{first_name}},\n\n{{city}} için fırsat!")
	f.build(now)
	return id
}

func (f *fixture) claimOne(channel string, userID int64) worker.Job {
	f.t.Helper()
	for {
		jobs, err := f.q.Claim(context.Background(), channel, 1, now, 30*time.Second)
		if err != nil || len(jobs) == 0 {
			f.t.Fatalf("no job for user %d: %v", userID, err)
		}
		if jobs[0].UserID == userID {
			return jobs[0]
		}
	}
}

type messageRow struct {
	Status, SkipReason, LastError string
	NextAttemptAt                 time.Time
	Attempts                      int
	Outcome                       string
	HTTPStatus                    int
}

func (f *fixture) message(id int64) messageRow {
	f.t.Helper()
	var m messageRow
	err := f.pool.QueryRow(context.Background(), `SELECT m.status, coalesce(m.skip_reason, ''), coalesce(m.last_error, ''), m.next_attempt_at,
			(SELECT count(*) FROM delivery_attempts a WHERE a.message_id = m.id),
			coalesce((SELECT a.outcome FROM delivery_attempts a WHERE a.message_id = m.id ORDER BY a.attempt_no DESC LIMIT 1), ''),
			coalesce((SELECT a.http_status FROM delivery_attempts a WHERE a.message_id = m.id ORDER BY a.attempt_no DESC LIMIT 1), 0)
		FROM messages m WHERE m.id = $1`, id).
		Scan(&m.Status, &m.SkipReason, &m.LastError, &m.NextAttemptAt, &m.Attempts, &m.Outcome, &m.HTTPStatus)
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}

func TestProcess_SendsPersonalizedMessageWithIdempotencyKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := &fakeProvider{}
	w := f.newWorker(p.serve(t))
	f.sendingWithContent("Merhaba {{first_name}}! Sepetin seni bekliyor.")
	sms, email := f.claimOne("sms", 1), f.claimOne("email", 1)

	if err := w.Process(context.Background(), sms); err != nil {
		t.Fatal(err)
	}
	if err := w.Process(context.Background(), email); err != nil {
		t.Fatal(err)
	}

	if len(p.requests) != 2 {
		t.Fatalf("want 2 provider requests, got %d", len(p.requests))
	}
	s, e := p.requests[0], p.requests[1]
	if s.channel != "sms" || s.key != strconv.FormatInt(sms.MessageID, 10) || s.body["to"] != "+905000000001" || s.body["body"] != "Merhaba A! Sepetin seni bekliyor." {
		t.Fatalf("sms request: %+v", s)
	}
	if e.channel != "email" || e.body["to"] != "a@example.com" || e.body["subject"] != "A, sepetin seni bekliyor" || e.body["body"] != "Merhaba A,\n\nAnkara için fırsat!" {
		t.Fatalf("email request: %+v", e)
	}
	if m := f.message(sms.MessageID); m.Status != "sent" || m.Outcome != "accepted" || m.HTTPStatus != 200 {
		t.Fatalf("sms: %+v", m)
	}
}

// D7: 200 sent; 429, 5xx, timeouts and network errors retried; other 4xx failed.
func TestProcess_Classification(t *testing.T) {
	cases := []struct {
		name        string
		handle      func(http.ResponseWriter, *http.Request)
		closed      bool // provider unreachable
		status      string
		outcome     string
		httpStatus  int
		minNext     time.Duration
		maxNext     time.Duration
		lastErrHint string
	}{
		{name: "200", handle: status(200), status: "sent", outcome: "accepted", httpStatus: 200},
		{name: "429 with Retry-After", handle: status(429, "Retry-After", "3"), status: "retry_scheduled", outcome: "rate_limited", httpStatus: 429, minNext: 3 * time.Second, maxNext: 3 * time.Second, lastErrHint: "429"},
		{name: "500", handle: status(500), status: "retry_scheduled", outcome: "server_error", httpStatus: 500, minNext: 500 * time.Millisecond, maxNext: time.Second, lastErrHint: "500"},
		{name: "503 in progress", handle: status(503, "Retry-After", "1"), status: "retry_scheduled", outcome: "server_error", httpStatus: 503, minNext: time.Second, maxNext: time.Second},
		{name: "timeout", handle: func(w http.ResponseWriter, r *http.Request) { time.Sleep(400 * time.Millisecond) }, status: "retry_scheduled", outcome: "timeout", minNext: 500 * time.Millisecond, maxNext: time.Second, lastErrHint: "timeout"},
		{name: "network error", closed: true, status: "retry_scheduled", outcome: "network_error", minNext: 500 * time.Millisecond, maxNext: time.Second},
		{name: "400", handle: status(400), status: "failed", outcome: "rejected", httpStatus: 400, lastErrHint: "400"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			p := &fakeProvider{handle: tc.handle}
			url := p.serve(t)
			if tc.closed {
				srv := httptest.NewServer(nil)
				url = srv.URL
				srv.Close()
			}
			w := f.newWorker(url)
			f.sendingWithContent("Merhaba {{first_name}}!")
			job := f.claimOne("sms", 1)
			attempts := obstest.Value(t, "scc_send_attempts_total", "channel", "sms", "outcome", tc.outcome)
			retries := obstest.Value(t, "scc_send_retries_total", "channel", "sms")
			latency := obstest.Value(t, "scc_provider_request_duration_seconds", "channel", "sms")

			if err := w.Process(context.Background(), job); err != nil {
				t.Fatal(err)
			}

			m := f.message(job.MessageID)
			if m.Status != tc.status || m.Outcome != tc.outcome || m.HTTPStatus != tc.httpStatus || m.Attempts != 1 {
				t.Fatalf("got %+v", m)
			}
			wantRetries := 0.0
			if tc.status == "retry_scheduled" {
				wantRetries = 1
			}
			if a, r, l := obstest.Value(t, "scc_send_attempts_total", "channel", "sms", "outcome", tc.outcome)-attempts,
				obstest.Value(t, "scc_send_retries_total", "channel", "sms")-retries,
				obstest.Value(t, "scc_provider_request_duration_seconds", "channel", "sms")-latency; a != 1 || r != wantRetries || l != 1 {
				t.Fatalf("metrics: attempts +%v, retries +%v (want +%v), latency samples +%v", a, r, wantRetries, l)
			}
			if tc.status == "retry_scheduled" {
				if d := m.NextAttemptAt.Sub(now); d < tc.minNext || d > tc.maxNext {
					t.Fatalf("next attempt in %v, want [%v, %v]", d, tc.minNext, tc.maxNext)
				}
			}
			if tc.lastErrHint != "" && !strings.Contains(m.LastError, tc.lastErrHint) {
				t.Fatalf("last_error %q should mention %q", m.LastError, tc.lastErrHint)
			}
		})
	}
}

func TestProcess_FifthFailureFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := &fakeProvider{handle: status(500)}
	w := f.newWorker(p.serve(t))
	f.sendingWithContent("Merhaba {{first_name}}!")
	// Four recorded failed attempts: attempts count from delivery_attempts (D5 history).
	f.exec(`UPDATE messages SET attempt_count = 4 WHERE user_id = 1 AND channel = 'sms'`)
	f.exec(`INSERT INTO delivery_attempts (message_id, attempt_no, started_at, finished_at, outcome, http_status)
		SELECT m.id, g, now(), now(), 'server_error', 500 FROM messages m, generate_series(1, 4) g WHERE m.user_id = 1 AND m.channel = 'sms'`)
	job := f.claimOne("sms", 1)

	w.Process(context.Background(), job)

	if m := f.message(job.MessageID); job.Attempt != 5 || m.Status != "failed" || !strings.Contains(m.LastError, "500") {
		t.Fatalf("5th failed attempt: want failed with the last error, got attempt %d %+v", job.Attempt, m)
	}
}

// Skips never reach the provider and take no rate-limit token (D3, D19).
func TestProcess_SkipPaths(t *testing.T) {
	cases := []struct {
		name, sms, setup, status, reason string
	}{
		{"consent revoked", "Merhaba {{first_name}}!", `UPDATE users SET sms_opt_in = false WHERE id = 1`, "skipped", "no_consent"},
		{"campaign cancelled", "Merhaba {{first_name}}!", `UPDATE campaigns SET status = 'cancelled'`, "cancelled", ""},
		{"too long once personalized", "{{first_name}} " + strings.Repeat("x", 158), `UPDATE users SET first_name = 'Alexandria' WHERE id = 1`, "skipped", "character_limit_exceeded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			p := &fakeProvider{}
			w := f.newWorker(p.serve(t))
			// One token, refilled only after ~17 minutes: any Wait would show.
			w.Limiters = map[string]*rate.Limiter{"sms": rate.NewLimiter(rate.Limit(0.001), 1), "email": rate.NewLimiter(rate.Limit(0.001), 1)}
			f.sendingWithContent(tc.sms)
			job := f.claimOne("sms", 1)
			f.exec(tc.setup)

			if err := w.Process(context.Background(), job); err != nil {
				t.Fatal(err)
			}

			m := f.message(job.MessageID)
			if m.Status != tc.status || m.SkipReason != tc.reason || m.Attempts != 0 || len(p.requests) != 0 {
				t.Fatalf("want %s/%s without a provider call, got %+v and %d requests", tc.status, tc.reason, m, len(p.requests))
			}
			if tokens := w.Limiters["sms"].Tokens(); tokens < 0.99 {
				t.Fatalf("a skip took a rate-limit token (left: %.2f)", tokens)
			}
		})
	}
}

// D19: no 1-second window holds more than 100 provider requests per channel,
// retries included. 1,000+ messages against an instant provider.
func TestRun_RateLimitNoWindowOver100(t *testing.T) {
	f := newFixture(t)
	f.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		SELECT g, 'U', 'u' || g || '@example.com', '+90510' || g, 'Ankara', 'tr', true, true, $1 FROM generate_series(1000, 1499) g`, now)
	f.exec(`INSERT INTO cart_items (user_id, category, quantity, unit_price, added_at) SELECT g, 'books', 1, 10, $1 FROM generate_series(1000, 1499) g`, now.Add(-time.Hour))
	id := f.approved(cartAbandoners, "{sms,email}", now)
	f.addLanguage(id, "tr")
	f.exec(`INSERT INTO campaign_content (campaign_id, channel, language, subject, body) VALUES ($1, 'sms', 'tr', NULL, 'Hi {{first_name}}'), ($1, 'email', 'tr', 'Hi', 'Hello')`, id)
	// Every 10th request is rate limited once, so retries are part of the stream.
	var mu sync.Mutex
	seen := map[string]bool{}
	p := &fakeProvider{handle: func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		mu.Lock()
		first := !seen[key]
		seen[key] = true
		mu.Unlock()
		if n, _ := strconv.Atoi(key); first && n%10 == 0 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		w.Write([]byte(`{"status":"accepted"}`))
	}}
	w := f.newWorker(p.serve(t))
	w.Now = time.Now
	w.Poll = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- w.Run(ctx) }()
	deadline := time.Now().Add(40 * time.Second)
	for f.count(`SELECT count(*) FROM campaigns WHERE id = $1 AND status = 'completed'`, id) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("not completed in time: %d messages still open", f.count(`SELECT count(*) FROM messages WHERE status NOT IN ('sent','failed','skipped','cancelled')`))
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	if sent := f.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status = 'sent'`, id); sent != 1003 {
		t.Fatalf("want all 1,003 messages sent, got %d", sent)
	}
	// Checked on both sides: what the cap counted and what the provider received.
	for _, ch := range []string{"sms", "email"} {
		rows, _ := f.pool.Query(context.Background(), `SELECT a.started_at FROM delivery_attempts a
			JOIN messages m ON m.id = a.message_id WHERE m.channel = $1`, ch)
		departures, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
		if err != nil {
			t.Fatal(err)
		}
		var arrivals []time.Time
		for _, r := range p.requests {
			if r.channel == ch {
				arrivals = append(arrivals, r.at)
			}
		}
		if len(departures) != len(arrivals) || len(arrivals) < 500 {
			t.Fatalf("%s: %d recorded departures for %d requests", ch, len(departures), len(arrivals))
		}
		if got := mostInOneSecond(departures); got > 100 {
			t.Fatalf("%s: %d recorded departures in one second, limit is 100", ch, got)
		}
		if got := mostInOneSecond(arrivals); got > 100 {
			t.Fatalf("%s: the provider received %d requests in one second, limit is 100", ch, got)
		}
	}
}

// Audit F6: a message whose text is gone can't be sent: it fails at once
// instead of being reclaimed every 30 s, and nothing says it may have been delivered.
func TestProcess_MissingTextFailsWithoutSending(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.sendingWithContent("Merhaba {{first_name}}")
	f.exec(`DELETE FROM campaign_content WHERE channel = 'sms'`) // only a direct database edit can do this
	p := &fakeProvider{}
	job := f.claimOne("sms", 1)

	f.newWorker(p.serve(t)).Process(context.Background(), job)

	m := f.message(job.MessageID)
	if m.Status != "failed" || m.Attempts != 0 || len(p.requests) != 0 ||
		f.str(`SELECT delivery_uncertain::text FROM messages WHERE id = $1`, job.MessageID) != "false" {
		t.Fatalf("want failed, unsent and not uncertain; got %+v, %d requests", m, len(p.requests))
	}
}

// Audit F6: when the message can't be read (a database error, or shutdown
// mid-read), nothing was sent: it goes back to the queue, not flagged uncertain.
func TestProcess_LoadErrorHandsTheMessageBack(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.sendingWithContent("Merhaba {{first_name}}")
	p := &fakeProvider{}
	job := f.claimOne("sms", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := f.newWorker(p.serve(t)).Process(ctx, job); err == nil {
		t.Fatal("the read error is still reported")
	}

	m := f.message(job.MessageID)
	if m.Status != "retry_scheduled" || m.Attempts != 0 || len(p.requests) != 0 ||
		f.str(`SELECT delivery_uncertain::text FROM messages WHERE id = $1`, job.MessageID) != "false" {
		t.Fatalf("want it back in the queue, unsent and not uncertain; got %+v, %d requests", m, len(p.requests))
	}
}

// A worker that died mid-attempt leaves its message "sending"; the running
// worker takes it back once the lease expires, flagged uncertain, and sends it.
func TestRun_ReclaimsExpiredLease(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.sendingWithContent("Merhaba {{first_name}}")
	f.exec(`UPDATE messages SET status = 'sending', lease_token = gen_random_uuid(), lease_expires_at = $1, attempt_count = 1`, time.Now().Add(-time.Second))
	p := &fakeProvider{}
	w := f.newWorker(p.serve(t))
	w.Now, w.Poll = time.Now, 20*time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- w.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for f.count(`SELECT count(*) FROM messages WHERE status <> 'sent' OR NOT delivery_uncertain`) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the expired leases were not reclaimed and sent")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
}

// mostInOneSecond is the largest number of times in any 1 s window.
func mostInOneSecond(times []time.Time) int {
	slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
	most := 0
	for i, j := 0, 0; j < len(times); j++ {
		for times[j].Sub(times[i]) >= time.Second {
			i++
		}
		most = max(most, j-i+1)
	}
	return most
}

// Review Focus #3: a cancel that lands while a message waits for its
// rate-limit token stops the send — no provider call after the cancel.
func TestProcess_CancelDuringRateLimitWait(t *testing.T) {
	f := newFixture(t)
	p := &fakeProvider{}
	w := f.newWorker(p.serve(t))
	id := f.sendingWithContent("Merhaba {{first_name}}!")
	job := f.claimOne("sms", 1)
	const interval = 3 * time.Second
	slow := rate.NewLimiter(rate.Every(interval), 1)
	slow.Allow() // empty: the next token is due one interval from now
	due := time.Now().Add(interval)
	w.Limiters = map[string]*rate.Limiter{"sms": slow, "email": rate.NewLimiter(rate.Inf, 1)}

	// Cancel once the worker is waiting, not after a guessed delay. Tokens is
	// at least 0 until the worker's reservation takes the token, and below 0
	// from then until the token is due.
	cancelled := make(chan time.Time, 1)
	go func() {
		for slow.Tokens() >= 0 && time.Now().Before(due) {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(due) {
			close(cancelled) // the worker never waited before the token was due
			return
		}
		f.exec(`UPDATE campaigns SET status = 'cancelled' WHERE id = $1`, id)
		cancelled <- time.Now()
	}()
	if err := w.Process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	// The limiter can't be released early, so the cancel must commit before the
	// token is due; otherwise this run proves nothing.
	select {
	case at, ok := <-cancelled:
		if !ok || at.After(due) {
			t.Fatal("the cancel did not commit while the worker waited for the token; the machine is too slow for this test")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the cancel never finished")
	}

	if m := f.message(job.MessageID); m.Status != "cancelled" || len(p.requests) != 0 {
		t.Fatalf("want cancelled without a provider call, got %+v and %d requests", m, len(p.requests))
	}
}
