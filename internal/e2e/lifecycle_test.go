package e2e_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"smart-campaign-composer/internal/config"
	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/dbtest"
	"smart-campaign-composer/internal/provider"
	"smart-campaign-composer/internal/worker"
)

// Delivery lifecycle tests: the worker against the real mock provider, whose
// stored results make replays (and therefore duplicates) observable.

type life struct {
	t    *testing.T
	pool *pgxpool.Pool
	prov *pgxpool.Pool
	q    *worker.Queue
	logs []*providerLog // one per provider server; they share the key store
}

// newLife: users 1–4 live in Ankara; 1, 3 and 4 allow both channels, 2 only SMS.
func newLife(t *testing.T) *life {
	t.Helper()
	pool := dbtest.FreshDB(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	l := &life{t: t, pool: pool, prov: dbtest.ProviderPool(t, pool), q: &worker.Queue{Pool: pool}}
	l.exec(`INSERT INTO marketers (id, email, name, password_hash) VALUES (1, 'm@example.com', 'M', 'x')`)
	l.exec(`INSERT INTO users (id, first_name, last_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at) VALUES
		(1, 'Ali', 'Kaya', 'ali@example.com', '+905000000001', 'Ankara', 'tr', true, true, now()),
		(2, 'Ayşe', 'Demir', 'ayse@example.com', '+905000000002', 'Ankara', 'tr', true, false, now()),
		(3, 'Can', NULL, 'can@example.com', '+905000000003', 'Ankara', 'tr', true, true, now()),
		(4, 'Deniz', 'Ak', 'deniz@example.com', '+905000000004', 'Ankara', 'tr', true, true, now())`)
	return l
}

func (l *life) exec(sql string, args ...any) {
	l.t.Helper()
	if _, err := l.pool.Exec(context.Background(), sql, args...); err != nil {
		l.t.Fatalf("%v: %s", err, sql)
	}
}

func (l *life) int(sql string, args ...any) int {
	l.t.Helper()
	var n int
	if err := l.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		l.t.Fatalf("%v: %s", err, sql)
	}
	return n
}

func (l *life) str(sql string, args ...any) string {
	l.t.Helper()
	var s string
	if err := l.pool.QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		l.t.Fatalf("%v: %s", err, sql)
	}
	return s
}

// provider starts a provider server on the shared key store.
func (l *life) provider(b config.ProviderChannel, hang time.Duration) string {
	l.t.Helper()
	p := provider.New(l.prov, map[string]config.ProviderChannel{"sms": b, "email": b}, 9)
	if hang > 0 {
		p.Hang = hang
	}
	mux := http.NewServeMux()
	p.Routes(mux)
	log := &providerLog{requests: map[string]int{}, processed: map[string]int{}, first: map[string]time.Time{}}
	l.logs = append(l.logs, log)
	srv := httptest.NewServer(log.wrap(mux))
	l.t.Cleanup(srv.Close)
	return srv.URL
}

// processed is how often the provider really sent a key, over all its servers.
func (l *life) processed(messageID int64) int {
	n := 0
	for _, log := range l.logs {
		log.mu.Lock()
		n += log.processed[strconv.FormatInt(messageID, 10)]
		log.mu.Unlock()
	}
	return n
}

func (l *life) requests(messageID int64) int {
	n := 0
	for _, log := range l.logs {
		log.mu.Lock()
		n += log.requests[strconv.FormatInt(messageID, 10)]
		log.mu.Unlock()
	}
	return n
}

// waitUntil polls cond until it holds; after 10 s the test fails. It replaces
// fixed sleeps, which a loaded machine can outlast.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for give := time.Now().Add(10 * time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(give) {
			t.Fatalf("timed out waiting until %s", what)
		}
	}
}

// shutdownWhen returns a context cancelled as soon as cond holds, so a test
// stops the worker at a known step rather than after a guessed delay, which a
// slow database read could use up. After 5 s it is cancelled anyway, and the
// test's own checks fail.
func shutdownWhen(t *testing.T, cond func() bool) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for give := time.Now().Add(5 * time.Second); !cond() && ctx.Err() == nil && time.Now().Before(give); {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	return ctx
}

func (l *life) worker(url string, timeout time.Duration) *worker.Worker {
	return &worker.Worker{Queue: l.q, ProviderURL: url, Client: worker.NewClient(timeout), MaxAttempts: 5, Poll: 20 * time.Millisecond}
}

// campaign creates an approved campaign for Ankara (or a resend of parent)
// with content, and starts it.
func (l *life) campaign(channels string, sms string, parent *int64) int64 {
	l.t.Helper()
	var id int64
	rules := any(`[{"field":"city","op":"in","value":["Ankara"]}]`)
	if parent != nil {
		rules = nil
	}
	err := l.pool.QueryRow(context.Background(), `INSERT INTO campaigns (request_text, channels, rules, status, send_at, time_zone, created_by, parent_campaign_id)
		VALUES ('brief', $1, $2, 'approved', now() - interval '1 second', 'Europe/Istanbul', 1, $3) RETURNING id`, channels, rules, parent).Scan(&id)
	if err != nil {
		l.t.Fatal(err)
	}
	l.exec(`INSERT INTO campaign_content (campaign_id, channel, subject, body) VALUES ($1, 'sms', NULL, $2), ($1, 'email', 'Merhaba {{first_name}}', 'Fırsat!')`, id, sms)
	if _, err := l.q.BuildAudience(context.Background(), time.Now()); err != nil {
		l.t.Fatal(err)
	}
	return id
}

func (l *life) message(campaignID, userID int64, channel string) int64 {
	return int64(l.int(`SELECT id FROM messages WHERE campaign_id = $1 AND user_id = $2 AND channel = $3`, campaignID, userID, channel))
}

// claim leases the given message, at `at` (use a later time to reclaim an expired lease).
func (l *life) claim(messageID int64, at time.Time) worker.Job {
	l.t.Helper()
	// Claim everything due on the channel at once, keep the wanted message and
	// put the others back exactly as they were.
	jobs, err := l.q.Claim(context.Background(), l.str(`SELECT channel FROM messages WHERE id = $1`, messageID), 1000, at, 30*time.Second)
	if err != nil {
		l.t.Fatal(err)
	}
	var found *worker.Job
	for i, j := range jobs {
		if j.MessageID == messageID {
			found = &jobs[i]
			continue
		}
		l.exec(`UPDATE messages SET status = CASE WHEN attempt_count > 1 THEN 'retry_scheduled' ELSE 'pending' END,
			lease_token = NULL, lease_expires_at = NULL, attempt_count = attempt_count - 1 WHERE id = $1`, j.MessageID)
	}
	if found == nil {
		l.t.Fatalf("message %d could not be claimed", messageID)
	}
	return *found
}

// deadWorkerSends delivers a message the way a worker would right before it
// crashes: the provider accepts, nobody records the result.
func (l *life) deadWorkerSends(url string, messageID int64, channel string) {
	l.t.Helper()
	body := `{"to":"+905000000001","body":"Merhaba"}`
	if channel == "email" {
		body = `{"to":"ali@example.com","subject":"S","body":"B"}`
	}
	req, _ := http.NewRequest(http.MethodPost, url+"/"+channel+"/send", strings.NewReader(body))
	req.Header.Set("Idempotency-Key", strconv.FormatInt(messageID, 10))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		l.t.Fatalf("dead worker's send: %v %v", err, resp)
	}
	resp.Body.Close()
}

func (l *life) state(messageID int64) (status, lastError string, uncertain bool) {
	l.t.Helper()
	err := l.pool.QueryRow(context.Background(), `SELECT status, coalesce(last_error, ''), delivery_uncertain FROM messages WHERE id = $1`, messageID).
		Scan(&status, &lastError, &uncertain)
	if err != nil {
		l.t.Fatal(err)
	}
	return
}

// cancel does what POST /api/campaigns/{id}/cancel does (D11).
func (l *life) cancel(campaignID int64) {
	l.exec(`UPDATE campaigns SET status = 'cancelled', cancelled_at = now() WHERE id = $1`, campaignID)
	l.exec(`UPDATE messages SET status = 'cancelled' WHERE campaign_id = $1 AND status IN ('pending', 'retry_scheduled')`, campaignID)
}

func (l *life) complete(campaignID int64) {
	l.exec(`UPDATE campaigns SET status = 'completed', completed_at = now() WHERE id = $1`, campaignID)
}

var (
	instant = config.ProviderChannel{}
	hangs   = config.ProviderChannel{TimeoutRate: 1} // every request hangs, then is accepted
	errors5 = config.ProviderChannel{ErrorRate: 1}
)

// L1: a timeout is a lost response; the retry gets the stored result.
func TestLifecycle_TimeoutThenRetry_DeliveredOnce(t *testing.T) {
	l := newLife(t)
	cid := l.campaign("{sms}", "Merhaba {{first_name}}", nil)
	mid := l.message(cid, 1, "sms")

	l.worker(l.provider(hangs, 300*time.Millisecond), 100*time.Millisecond).Process(context.Background(), l.claim(mid, time.Now()))
	if status, _, uncertain := l.state(mid); status != "retry_scheduled" || !uncertain {
		t.Fatalf("after a timeout: want retry_scheduled and uncertain, got %s %v", status, uncertain)
	}
	waitUntil(t, "the provider accepts the hung request", func() bool { return l.processed(mid) == 1 })

	l.worker(l.provider(instant, 0), time.Second).Process(context.Background(), l.claim(mid, time.Now().Add(2*time.Second)))

	if status, _, _ := l.state(mid); status != "sent" || l.processed(mid) != 1 {
		t.Fatalf("want sent and exactly one real send, got %s and %d", status, l.processed(mid))
	}
}

// L2 (finding 1): a worker shutdown mid-attempt is not a failure and does not
// use up an attempt; the message goes straight back with the same key.
func TestLifecycle_ShutdownDuringLastAttempt_NotFailed(t *testing.T) {
	l := newLife(t)
	cid := l.campaign("{sms}", "Merhaba {{first_name}}", nil)
	mid := l.message(cid, 1, "sms")
	// Four attempts already failed: this is the last one allowed.
	l.exec(`INSERT INTO delivery_attempts (message_id, attempt_no, started_at, finished_at, outcome, http_status)
		SELECT $1, g, now(), now(), 'server_error', 500 FROM generate_series(1, 4) g`, mid)
	l.exec(`UPDATE messages SET attempt_count = 4 WHERE id = $1`, mid)
	url := l.provider(hangs, 300*time.Millisecond)

	ctx := shutdownWhen(t, func() bool { return l.requests(mid) > 0 }) // the attempt is at the provider
	l.worker(url, 2*time.Second).Process(ctx, l.claim(mid, time.Now()))

	status, _, uncertain := l.state(mid)
	if status != "retry_scheduled" || !uncertain {
		t.Fatalf("shutdown during attempt 5: want retry_scheduled and uncertain, got %s %v", status, uncertain)
	}
	if got := l.str(`SELECT outcome FROM delivery_attempts WHERE message_id = $1 ORDER BY attempt_no DESC LIMIT 1`, mid); got != "interrupted" {
		t.Fatalf("the interrupted attempt must be recorded as interrupted, got %s", got)
	}
	if l.int(`SELECT count(*) FROM messages WHERE id = $1 AND next_attempt_at <= now()`, mid) != 1 {
		t.Fatal("an interrupted message is retried at once")
	}
	waitUntil(t, "the provider accepts it anyway", func() bool { return l.processed(mid) == 1 })

	l.worker(l.provider(instant, 0), time.Second).Process(context.Background(), l.claim(mid, time.Now()))

	if status, _, _ := l.state(mid); status != "sent" || l.processed(mid) != 1 {
		t.Fatalf("want sent via the stored result, exactly one real send; got %s, %d", status, l.processed(mid))
	}
}

// L3: a worker dies after the provider accepted; the expired lease is
// reclaimed, flagged uncertain, and the retry gets the stored result.
func TestLifecycle_CrashAfterAcceptance_ReclaimedAndDeliveredOnce(t *testing.T) {
	l := newLife(t)
	cid := l.campaign("{sms}", "Merhaba {{first_name}}", nil)
	mid := l.message(cid, 1, "sms")
	url := l.provider(instant, 0)
	l.claim(mid, time.Now())
	l.deadWorkerSends(url, mid, "sms")

	job := l.claim(mid, time.Now().Add(31*time.Second)) // lease expired
	if _, _, uncertain := l.state(mid); !uncertain || job.Attempt != 2 {
		t.Fatalf("a reclaimed lease means the earlier attempt may have been delivered; got uncertain=%v attempt=%d", uncertain, job.Attempt)
	}
	l.worker(url, time.Second).Process(context.Background(), job)

	if status, _, _ := l.state(mid); status != "sent" || l.processed(mid) != 1 {
		t.Fatalf("want sent, one real send; got %s, %d", status, l.processed(mid))
	}
}

// L4 (findings 2, 4): delivered but unconfirmed, then cancelled. The record
// says delivery is uncertain, and a later resend must not send it again.
func TestLifecycle_CrashAfterAcceptanceThenCancel_NoteAndNoResend(t *testing.T) {
	l := newLife(t)
	parent := l.campaign("{sms}", "Merhaba {{first_name}}", nil)
	l.exec(`UPDATE messages SET status = 'skipped', skip_reason = 'character_limit_exceeded' WHERE campaign_id = $1 AND user_id = 1`, parent)
	l.exec(`UPDATE messages SET status = 'sent' WHERE campaign_id = $1 AND user_id <> 1`, parent)
	l.complete(parent)
	r1 := l.campaign("{sms}", "Kısa {{first_name}}", &parent)
	mid := l.message(r1, 1, "sms")
	url := l.provider(instant, 0)
	l.claim(mid, time.Now())
	l.deadWorkerSends(url, mid, "sms")
	l.cancel(r1)

	l.worker(url, time.Second).Process(context.Background(), l.claim(mid, time.Now().Add(31*time.Second)))

	status, lastError, uncertain := l.state(mid)
	if status != "cancelled" || !uncertain || !strings.Contains(lastError, "may have") {
		t.Fatalf("want cancelled with an uncertainty note, got %s %q %v", status, lastError, uncertain)
	}
	r2 := l.campaign("{sms}", "Kısa {{first_name}}", &parent)
	if n := l.int(`SELECT count(*) FROM messages WHERE campaign_id = $1`, r2); n != 0 {
		t.Fatalf("a new resend must not include a recipient whose delivery is uncertain, got %d messages", n)
	}
	if l.processed(mid) != 1 {
		t.Fatal("exactly one real send expected")
	}
}

// L5 (finding 2, the reviewer's scenario): a resend's message times out (the
// provider accepts later), the resend is cancelled, a new resend is made.
func TestLifecycle_TimeoutThenCancelledResend_NewResendSkipsUncertain(t *testing.T) {
	l := newLife(t)
	parent := l.campaign("{sms}", "Merhaba {{first_name}}", nil)
	l.exec(`UPDATE messages SET status = 'skipped', skip_reason = 'character_limit_exceeded' WHERE campaign_id = $1 AND user_id IN (1, 2)`, parent)
	l.exec(`UPDATE messages SET status = 'sent' WHERE campaign_id = $1 AND user_id NOT IN (1, 2)`, parent)
	l.complete(parent)
	r1 := l.campaign("{sms}", "Kısa {{first_name}}", &parent)
	timedOut, untouched := l.message(r1, 1, "sms"), l.message(r1, 2, "sms")
	l.worker(l.provider(hangs, 300*time.Millisecond), 100*time.Millisecond).Process(context.Background(), l.claim(timedOut, time.Now()))
	l.cancel(r1)
	waitUntil(t, "the hung request is accepted after all", func() bool { return l.processed(timedOut) == 1 })

	r2 := l.campaign("{sms}", "Kısa {{first_name}}", &parent)

	if l.int(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND user_id = 1`, r2) != 0 {
		t.Fatal("user 1 may already have the SMS (timeout, accepted later): the new resend must skip them")
	}
	if l.int(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND user_id = 2`, r2) != 1 {
		t.Fatalf("user 2 was never attempted (cancelled cleanly): the new resend must include them; untouched message %d", untouched)
	}
}

// L6: consent revoked between retries stops further sends; after an
// uncertain attempt the record says so.
func TestLifecycle_ConsentRevokedBetweenRetries(t *testing.T) {
	l := newLife(t)
	cid := l.campaign("{sms}", "Merhaba {{first_name}}", nil)
	clean, unsure := l.message(cid, 1, "sms"), l.message(cid, 3, "sms")
	l.worker(l.provider(errors5, 0), time.Second).Process(context.Background(), l.claim(clean, time.Now()))
	l.worker(l.provider(hangs, 300*time.Millisecond), 100*time.Millisecond).Process(context.Background(), l.claim(unsure, time.Now()))
	l.exec(`UPDATE users SET sms_opt_in = false WHERE id IN (1, 3)`)
	url := l.provider(instant, 0)

	l.worker(url, time.Second).Process(context.Background(), l.claim(clean, time.Now().Add(2*time.Second)))
	l.worker(url, time.Second).Process(context.Background(), l.claim(unsure, time.Now().Add(2*time.Second)))

	if status, lastErr, _ := l.state(clean); status != "skipped" || l.str(`SELECT skip_reason FROM messages WHERE id = $1`, clean) != "no_consent" ||
		strings.Contains(lastErr, "may have") || l.requests(clean) != 1 {
		t.Fatalf("clean: want skipped/no_consent without a note and no second request, got %s %q, %d requests", status, lastErr, l.requests(clean))
	}
	if status, lastErr, _ := l.state(unsure); status != "skipped" || !strings.Contains(lastErr, "may have") || l.requests(unsure) != 1 {
		t.Fatalf("uncertain: want skipped with a note and no second request, got %s %q, %d requests", status, lastErr, l.requests(unsure))
	}
}

// L7: a recipient whose name grew after approval is skipped for length at
// send time and gets the message exactly once through a resend (D3, D31).
func TestLifecycle_RenamedAfterApproval_SkippedThenResentOnce(t *testing.T) {
	l := newLife(t)
	cid := l.campaign("{sms}", "{{first_name}} "+strings.Repeat("x", 150), nil) // "Ali" fits: 154
	l.exec(`UPDATE users SET first_name = 'Alexandria' WHERE id = 1`)           // now 161
	url := l.provider(instant, 0)
	w := l.worker(url, time.Second)
	for _, uid := range []int64{1, 2, 3, 4} {
		w.Process(context.Background(), l.claim(l.message(cid, uid, "sms"), time.Now()))
	}
	if l.str(`SELECT skip_reason FROM messages WHERE campaign_id = $1 AND user_id = 1`, cid) != "character_limit_exceeded" ||
		l.int(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status = 'sent'`, cid) != 3 {
		t.Fatal("want user 1 skipped for length and the other three sent")
	}
	l.complete(cid)

	resend := l.campaign("{sms}", "{{first_name}}, fırsat!", &cid)
	w.Process(context.Background(), l.claim(l.message(resend, 1, "sms"), time.Now()))

	if n := l.int(`SELECT count(*) FROM messages WHERE campaign_id IN ($1, $2) AND status = 'sent' AND user_id = 1`, cid, resend); n != 1 ||
		l.int(`SELECT count(*) FROM messages WHERE campaign_id = $1`, resend) != 1 {
		t.Fatalf("want user 1 to get the SMS exactly once, through the resend only; got %d", n)
	}
}

// L8 (Review Focus #3): cancelling mid-campaign stops it — no request leaves
// after the cancel committed (attempts already in progress may finish, D11).
func TestLifecycle_CancelMidCampaign_NoDepartureAfterCancel(t *testing.T) {
	l := newLife(t)
	l.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		SELECT g, 'U', 'u' || g || '@example.com', '+90520' || g, 'Ankara', 'tr', true, true, now() FROM generate_series(100, 159) g`)
	cid := l.campaign("{sms,email}", "Merhaba {{first_name}}", nil)
	w := l.worker(l.provider(instant, 0), time.Second)
	w.Limiters = map[string]*rate.Limiter{"sms": rate.NewLimiter(20, 1), "email": rate.NewLimiter(20, 1)} // slow: the cancel lands mid-way

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- w.Run(ctx) }()
	waitUntil(t, "some messages are sent", func() bool {
		return l.int(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status = 'sent'`, cid) > 0
	})
	l.cancel(cid)
	cancelledAt := time.Now()
	// Messages already claimed reach their cancel check as rate-limit slots come up.
	deadline := time.Now().Add(15 * time.Second)
	for l.int(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status IN ('pending','sending','retry_scheduled')`, cid) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("messages still open 15 s after the cancel")
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	<-done

	if n := l.int(`SELECT count(*) FROM delivery_attempts a JOIN messages m ON m.id = a.message_id
		WHERE m.campaign_id = $1 AND a.started_at > $2`, cid, cancelledAt); n != 0 {
		t.Fatalf("%d requests left after the cancel committed", n)
	}
	sent := l.int(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status = 'sent'`, cid)
	cancelled := l.int(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status = 'cancelled'`, cid)
	total := l.int(`SELECT count(*) FROM messages WHERE campaign_id = $1`, cid)
	if sent == 0 || cancelled == 0 || sent+cancelled != total {
		t.Fatalf("want some sent before and the rest cancelled: sent %d, cancelled %d of %d", sent, cancelled, total)
	}
}

// L9: two workers on one queue (a misconfigured second replica) with a flaky
// provider: still no message is really sent twice.
func TestLifecycle_TwoWorkers_NoDoubleSend(t *testing.T) {
	l := newLife(t)
	l.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		SELECT g, 'U', 'u' || g || '@example.com', '+90520' || g, 'Ankara', 'tr', true, true, now() FROM generate_series(100, 179) g`)
	cid := l.campaign("{sms,email}", "Merhaba {{first_name}}", nil)
	url := l.provider(config.ProviderChannel{ErrorRate: 0.2, RateLimitRate: 0.1, TimeoutRate: 0.05}, 300*time.Millisecond)

	ctx, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); l.worker(url, 200*time.Millisecond).Run(ctx) }()
	}
	deadline := time.Now().Add(90 * time.Second)
	for l.int(`SELECT count(*) FROM campaigns WHERE id = $1 AND status = 'completed'`, cid) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("campaign did not complete")
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	wg.Wait()

	rows, _ := l.pool.Query(context.Background(), `SELECT id, status FROM messages WHERE campaign_id = $1`, cid)
	type msg struct {
		id     int64
		status string
	}
	var msgs []msg
	for rows.Next() {
		var m msg
		rows.Scan(&m.id, &m.status)
		msgs = append(msgs, m)
	}
	for _, m := range msgs {
		if l.processed(m.id) > 1 {
			t.Fatalf("message %d really sent %d times", m.id, l.processed(m.id))
		}
		if m.status == "sent" && l.processed(m.id) != 1 {
			t.Fatalf("message %d is sent but the provider never sent it", m.id)
		}
	}
}

// L10: retries are limited: after 5 failed attempts the message fails with
// the last error, and nothing was sent.
func TestLifecycle_RetriesExhausted(t *testing.T) {
	l := newLife(t)
	cid := l.campaign("{sms}", "Merhaba {{first_name}}", nil)
	mid := l.message(cid, 1, "sms")
	w := l.worker(l.provider(errors5, 0), time.Second)

	at := time.Now()
	for range 5 {
		at = at.Add(time.Minute) // past any backoff
		w.Process(context.Background(), l.claim(mid, at))
	}

	status, lastError, _ := l.state(mid)
	if status != "failed" || !strings.Contains(lastError, "500") || l.int(`SELECT count(*) FROM delivery_attempts WHERE message_id = $1`, mid) != 5 || l.processed(mid) != 0 {
		t.Fatalf("want failed after exactly 5 attempts with the last error and no real send, got %s %q", status, lastError)
	}
	jobs, _ := l.q.Claim(context.Background(), "sms", 1000, at.Add(time.Hour), 30*time.Second)
	for _, j := range jobs {
		if j.MessageID == mid {
			t.Fatal("a failed message must never be claimed again")
		}
	}
}

// A graceful shutdown hands back a message still waiting for its rate-limit
// slot at once: nothing was sent, no attempt is counted, no 30 s lease to wait out.
func TestLifecycle_ShutdownWhileWaiting_ReleasesTheMessage(t *testing.T) {
	l := newLife(t)
	cid := l.campaign("{sms}", "Merhaba {{first_name}}", nil)
	mid := l.message(cid, 1, "sms")
	w := l.worker(l.provider(instant, 0), time.Second)
	empty := rate.NewLimiter(rate.Every(time.Hour), 1)
	empty.Allow()
	w.Limiters = map[string]*rate.Limiter{"sms": empty, "email": rate.NewLimiter(rate.Inf, 1)}

	// Waiting reserves the only token, so Tokens drops below 0. (A deadline
	// would not do: Wait refuses at once a deadline it cannot meet.)
	ctx := shutdownWhen(t, func() bool { return empty.Tokens() < -0.5 })
	w.Process(ctx, l.claim(mid, time.Now()))

	if n := l.int(`SELECT count(*) FROM messages WHERE id = $1 AND status IN ('pending', 'retry_scheduled')
		AND lease_token IS NULL AND next_attempt_at <= now() AND NOT delivery_uncertain`, mid); n != 1 {
		t.Fatalf("want the message back in the queue, due now, not uncertain; got %s", l.str(`SELECT status FROM messages WHERE id = $1`, mid))
	}
	if l.int(`SELECT count(*) FROM delivery_attempts WHERE message_id = $1`, mid) != 0 || l.requests(mid) != 0 {
		t.Fatal("nothing was sent, so no attempt may be recorded")
	}
}

// An interrupted attempt does not count toward the limit: after 3 errors, an
// interruption and another error, only 4 attempts count, so it is retried.
func TestLifecycle_InterruptedAttemptDoesNotCount(t *testing.T) {
	l := newLife(t)
	cid := l.campaign("{sms}", "Merhaba {{first_name}}", nil)
	mid := l.message(cid, 1, "sms")
	l.exec(`INSERT INTO delivery_attempts (message_id, attempt_no, started_at, finished_at, outcome, http_status)
		SELECT $1, g, now(), now(), 'server_error', 500 FROM generate_series(1, 3) g`, mid)
	l.exec(`INSERT INTO delivery_attempts (message_id, attempt_no, started_at, finished_at, outcome) VALUES ($1, 4, now(), now(), 'interrupted')`, mid)
	l.exec(`UPDATE messages SET attempt_count = 4 WHERE id = $1`, mid)

	l.worker(l.provider(errors5, 0), time.Second).Process(context.Background(), l.claim(mid, time.Now())) // attempt 5, counted 4th

	if status, _, _ := l.state(mid); status != "retry_scheduled" {
		t.Fatalf("4 counted failures of 5 allowed: want retry_scheduled, got %s", status)
	}
}
