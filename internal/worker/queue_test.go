package worker_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/dbtest"
	"smart-campaign-composer/internal/worker"
)

var now = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

type fixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	q    *worker.Queue
}

// newFixture: users 1 and 2 in Ankara carted 5 days ago and never ordered
// (1 allowed both channels, 2 only SMS); user 3 lives in İzmir.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := dbtest.MigratedDB(t)
	f := &fixture{t: t, pool: pool, q: &worker.Queue{Pool: pool}}
	f.exec(`INSERT INTO marketers (id, email, name, password_hash) VALUES (1, 'm@example.com', 'M', 'x')`)
	f.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at) VALUES
		(1, 'A', 'a@example.com', '+905000000001', 'Ankara', 'tr', true, true, $1),
		(2, 'B', 'b@example.com', '+905000000002', 'Ankara', 'tr', true, false, $1),
		(3, 'C', 'c@example.com', '+905000000003', 'İzmir', 'tr', true, true, $1)`, now.AddDate(-1, 0, 0))
	f.exec(`INSERT INTO cart_items (user_id, category, quantity, unit_price, added_at) VALUES (1, 'books', 1, 50, $1), (2, 'books', 1, 50, $1)`, now.AddDate(0, 0, -5))
	return f
}

func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("%v: %s", err, sql)
	}
}

func (f *fixture) count(sql string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		f.t.Fatalf("%v: %s", err, sql)
	}
	return n
}

func (f *fixture) str(sql string, args ...any) string {
	f.t.Helper()
	var s string
	if err := f.pool.QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		f.t.Fatalf("%v: %s", err, sql)
	}
	return s
}

// Abandoned carts in Ankara: carted in the last 30 days, no order since.
const cartAbandoners = `[{"field":"city","op":"in","value":["Ankara"]},{"field":"cart_added","op":"within","value":{"last_days":30}},{"field":"no_order","op":"within","value":{"last_days":30}}]`

func (f *fixture) approved(rules string, channels string, sendAt time.Time) int64 {
	f.t.Helper()
	var id int64
	err := f.pool.QueryRow(context.Background(), `INSERT INTO campaigns (request_text, channels, rules, status, send_at, time_zone, created_by, approved_by, approved_at)
		VALUES ('brief', $1, $2, 'approved', $3, 'Europe/Istanbul', 1, 1, $4) RETURNING id`, channels, rules, sendAt, now.Add(-time.Hour)).Scan(&id)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) build(at time.Time) int {
	f.t.Helper()
	n, err := f.q.BuildAudience(context.Background(), at)
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestBuildAudience_OnlyWhenDue(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sendAt := now.Add(time.Hour)
	id := f.approved(cartAbandoners, "{sms,email}", sendAt)

	if n := f.build(sendAt.Add(-time.Second)); n != 0 || f.count(`SELECT count(*) FROM messages`) != 0 {
		t.Fatalf("one second early: want nothing built, got %d campaigns", n)
	}
	if n := f.build(sendAt); n != 1 {
		t.Fatalf("at send_at: want 1 campaign built, got %d", n)
	}
	// User 1: SMS and email; user 2: SMS only (consent per channel).
	if got := f.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status = 'pending'`, id); got != 3 {
		t.Fatalf("want 3 pending messages, got %d", got)
	}
	if f.str(`SELECT status FROM campaigns WHERE id = $1`, id) != "sending" ||
		f.count(`SELECT count(*) FROM campaigns WHERE id = $1 AND audience_built_at = $2`, id, sendAt) != 1 {
		t.Fatal("campaign must be sending with audience_built_at set")
	}
	if n := f.build(sendAt.Add(time.Minute)); n != 0 || f.count(`SELECT count(*) FROM messages`) != 3 {
		t.Fatal("a campaign is built only once")
	}
}

// D30: the audience is evaluated when sending starts, not at approval.
func TestBuildAudience_EvaluatesAtSendTime(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sendAt := now.Add(time.Hour)
	id := f.approved(cartAbandoners, "{sms}", sendAt)
	// Between approval and sending: user 1 orders, a new user 4 carts.
	f.exec(`INSERT INTO orders (user_id, ordered_at, total_amount) VALUES (1, $1, 50)`, now)
	f.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		VALUES (4, 'D', 'd@example.com', '+905000000004', 'Ankara', 'tr', true, true, $1)`, now)
	f.exec(`INSERT INTO cart_items (user_id, category, quantity, unit_price, added_at) VALUES (4, 'home', 1, 80, $1)`, now)

	f.build(sendAt)

	var users []int64
	rows, _ := f.pool.Query(context.Background(), `SELECT user_id FROM messages WHERE campaign_id = $1 ORDER BY user_id`, id)
	for rows.Next() {
		var u int64
		rows.Scan(&u)
		users = append(users, u)
	}
	if len(users) != 2 || users[0] != 2 || users[1] != 4 {
		t.Fatalf("want users 2 and 4 (1 ordered meanwhile, 4 is new), got %v", users)
	}
}

// D14: an all_customers campaign gets one message per user who allowed the
// channel, and nobody else.
func TestBuildAudience_AllCustomersWithConsentOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.exec(`UPDATE users SET sms_opt_in = false WHERE id = 1`)
	id := f.approved(`[{"field":"all_customers","op":"is","value":true}]`, "{sms}", now)

	f.build(now)

	var messages, consenting, optedOut int
	f.pool.QueryRow(context.Background(), `SELECT count(*) FROM messages WHERE campaign_id = $1`, id).Scan(&messages)
	f.pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE sms_opt_in`).Scan(&consenting)
	f.pool.QueryRow(context.Background(), `SELECT count(*) FROM messages WHERE campaign_id = $1 AND user_id = 1`, id).Scan(&optedOut)
	if messages == 0 || messages != consenting || optedOut != 0 {
		t.Fatalf("want one message per consenting user (%d) and none for user 1, got %d (user 1: %d)", consenting, messages, optedOut)
	}
}

func TestBuildAudience_CancelledIsNeverBuilt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := f.approved(cartAbandoners, "{sms}", now)
	f.exec(`UPDATE campaigns SET status = 'cancelled' WHERE id = $1`, id)

	if n := f.build(now.Add(time.Hour)); n != 0 || f.count(`SELECT count(*) FROM messages`) != 0 {
		t.Fatal("a cancelled campaign must never get messages")
	}
}

func TestBuildAudience_EmptyAudienceCompletes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := f.approved(`[{"field":"cart_value","op":"gte","value":1000000}]`, "{sms}", now)

	f.build(now)

	if f.str(`SELECT status FROM campaigns WHERE id = $1`, id) != "completed" ||
		f.count(`SELECT count(*) FROM campaigns WHERE id = $1 AND completed_at IS NOT NULL AND audience_built_at IS NOT NULL`, id) != 1 {
		t.Fatal("an empty audience must complete the campaign with 0 messages")
	}
}

func TestBuildAudience_Resend(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	parent := f.approved(cartAbandoners, "{sms}", now)
	f.exec(`UPDATE campaigns SET status = 'completed' WHERE id = $1`, parent)
	f.exec(`INSERT INTO messages (campaign_id, user_id, channel, status, skip_reason) VALUES
		($1, 1, 'sms', 'skipped', 'character_limit_exceeded'), ($1, 2, 'sms', 'sent', NULL), ($1, 3, 'sms', 'failed', NULL)`, parent)
	var resend int64
	f.pool.QueryRow(context.Background(), `INSERT INTO campaigns (request_text, channels, status, send_at, time_zone, created_by, parent_campaign_id)
		VALUES ('brief', '{sms}', 'approved', $1, 'Europe/Istanbul', 1, $2) RETURNING id`, now, parent).Scan(&resend)

	f.build(now)

	if got := f.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND user_id = 1`, resend); got != 1 ||
		f.count(`SELECT count(*) FROM messages WHERE campaign_id = $1`, resend) != 1 {
		t.Fatal("a resend's messages are exactly the parent's skipped recipients")
	}
}

func TestBuildAudience_ConcurrentCallsBuildOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for range 5 {
		f.approved(cartAbandoners, "{sms,email}", now)
	}

	var wg sync.WaitGroup
	built := make([]int, 4)
	for i := range built {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := f.q.BuildAudience(context.Background(), now)
			if err != nil {
				t.Error(err)
			}
			built[i] = n
		}()
	}
	wg.Wait()

	if total := built[0] + built[1] + built[2] + built[3]; total != 5 {
		t.Fatalf("want 5 campaigns built in total, got %v", built)
	}
	if got := f.count(`SELECT count(*) FROM messages`); got != 15 {
		t.Fatalf("want 15 messages (5 × 3), got %d", got)
	}
}

// A failure mid-build rolls the whole campaign back; the next run builds it cleanly.
func TestBuildAudience_CrashMidBuildRollsBack(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := f.approved(cartAbandoners, "{sms,email}", now)
	// Make the email insert fail after the SMS messages were inserted.
	f.exec(`CREATE FUNCTION fail_email() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.channel = 'email' THEN RAISE EXCEPTION 'simulated crash'; END IF; RETURN NEW; END $$`)
	f.exec(`CREATE TRIGGER fail_email BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION fail_email()`)

	if _, err := f.q.BuildAudience(context.Background(), now); err == nil {
		t.Fatal("want the simulated failure")
	}
	if f.count(`SELECT count(*) FROM messages`) != 0 || f.str(`SELECT status FROM campaigns WHERE id = $1`, id) != "approved" {
		t.Fatal("a failed build must leave no messages and the campaign approved")
	}

	f.exec(`DROP TRIGGER fail_email ON messages`)
	if n := f.build(now); n != 1 || f.count(`SELECT count(*) FROM messages`) != 3 {
		t.Fatalf("rerun: want a clean build, got %d campaigns", n)
	}
}

func (f *fixture) sending() int64 {
	f.t.Helper()
	id := f.approved(cartAbandoners, "{sms,email}", now)
	f.build(now)
	return id
}

func TestClaim_ConcurrentClaimersNeverShareARow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		SELECT g, 'U', 'u' || g || '@example.com', '+90510' || g, 'Ankara', 'tr', true, true, $1 FROM generate_series(100, 299) g`, now)
	f.exec(`INSERT INTO cart_items (user_id, category, quantity, unit_price, added_at) SELECT g, 'books', 1, 10, $1 FROM generate_series(100, 299) g`, now)
	f.sending()

	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				jobs, err := f.q.Claim(context.Background(), "sms", 7, now, 30*time.Second)
				if err != nil {
					t.Error(err)
					return
				}
				if len(jobs) == 0 {
					return
				}
				mu.Lock()
				for _, j := range jobs {
					seen[j.MessageID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	want := f.count(`SELECT count(*) FROM messages WHERE channel = 'sms'`)
	if len(seen) != want {
		t.Fatalf("want all %d SMS claimed, got %d", want, len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("message %d claimed %d times", id, n)
		}
	}
}

func TestClaim_OnlyDueMessages(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.sending()
	f.exec(`UPDATE messages SET status = 'retry_scheduled', next_attempt_at = $1 WHERE user_id = 1 AND channel = 'sms'`, now.Add(time.Minute))

	jobs, _ := f.q.Claim(context.Background(), "sms", 10, now, 30*time.Second)
	if len(jobs) != 1 || jobs[0].UserID != 2 {
		t.Fatalf("want only user 2's SMS (user 1's retry is not due), got %+v", jobs)
	}
	// Finish user 2's job; otherwise its 30 s lease would have expired by then and be reclaimed too.
	if err := f.q.Complete(context.Background(), jobs[0], worker.Result{Status: "sent"}); err != nil {
		t.Fatal(err)
	}
	later, _ := f.q.Claim(context.Background(), "sms", 10, now.Add(time.Minute), 30*time.Second)
	if len(later) != 1 || later[0].UserID != 1 || later[0].Attempt != 1 {
		t.Fatalf("at next_attempt_at: want user 1's retry, got %+v", later)
	}
}

func TestClaim_ExpiredLeaseIsReclaimedAndStaleCompleteIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.sending()
	ctx := context.Background()

	first, _ := f.q.Claim(ctx, "email", 1, now, 30*time.Second)
	if again, _ := f.q.Claim(ctx, "email", 1, now.Add(29*time.Second), 30*time.Second); len(again) != 0 {
		t.Fatal("a leased message must not be claimed again before the lease expires")
	}
	// A claim takes only due messages; expired leases come back through ReclaimExpired.
	if early, _ := f.q.Claim(ctx, "email", 1, now.Add(31*time.Second), 30*time.Second); len(early) != 0 {
		t.Fatal("a claim must not take an expired lease itself")
	}
	if n, err := f.q.ReclaimExpired(ctx, now.Add(29*time.Second)); err != nil || n != 0 {
		t.Fatalf("a live lease is not reclaimed, got %d, %v", n, err)
	}
	if n, err := f.q.ReclaimExpired(ctx, now.Add(31*time.Second)); err != nil || n != 1 {
		t.Fatalf("want the expired lease reclaimed, got %d, %v", n, err)
	}
	reclaimed, _ := f.q.Claim(ctx, "email", 1, now.Add(31*time.Second), 30*time.Second)

	if len(first) != 1 || len(reclaimed) != 1 || reclaimed[0].MessageID != first[0].MessageID ||
		reclaimed[0].LeaseToken == first[0].LeaseToken || reclaimed[0].Attempt != 2 {
		t.Fatalf("want the same message with a new lease and attempt 2, got %+v then %+v", first, reclaimed)
	}

	err := f.q.Complete(ctx, first[0], worker.Result{Status: "sent", Attempt: &worker.Attempt{StartedAt: now, FinishedAt: now, Outcome: "accepted", HTTPStatus: 200}})
	if !errors.Is(err, worker.ErrLeaseLost) || f.str(`SELECT status FROM messages WHERE id = $1`, first[0].MessageID) != "sending" {
		t.Fatalf("the stale worker's result must change nothing, got %v", err)
	}
	// Its attempt still happened and is recorded, for honest retry and 429 counts.
	if f.count(`SELECT count(*) FROM delivery_attempts WHERE message_id = $1 AND attempt_no = 1`, first[0].MessageID) != 1 {
		t.Fatal("the stale attempt must still be recorded")
	}
	if err := f.q.Complete(ctx, reclaimed[0], worker.Result{Status: "sent"}); err != nil {
		t.Fatalf("the current lease holder completes: %v", err)
	}
}

func TestComplete_RecordsAttemptAndOutcome(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.sending()
	ctx := context.Background()
	jobs, _ := f.q.Claim(ctx, "sms", 2, now, 30*time.Second)
	next := now.Add(2 * time.Second)

	err := f.q.Complete(ctx, jobs[0], worker.Result{Status: "retry_scheduled", NextAttemptAt: next, LastError: "429 rate limited",
		Attempt: &worker.Attempt{StartedAt: now, FinishedAt: now.Add(80 * time.Millisecond), Outcome: "rate_limited", HTTPStatus: 429, Error: "429 rate limited"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.q.Complete(ctx, jobs[1], worker.Result{Status: "sent", Attempt: &worker.Attempt{StartedAt: now, FinishedAt: now, Outcome: "accepted", HTTPStatus: 200}}); err != nil {
		t.Fatal(err)
	}

	if f.count(`SELECT count(*) FROM messages WHERE id = $1 AND status = 'retry_scheduled' AND next_attempt_at = $2
		AND last_error = '429 rate limited' AND lease_token IS NULL`, jobs[0].MessageID, next) != 1 {
		t.Fatal("retry not scheduled as given")
	}
	if f.count(`SELECT count(*) FROM messages WHERE id = $1 AND status = 'sent' AND sent_at IS NOT NULL`, jobs[1].MessageID) != 1 {
		t.Fatal("sent message not marked")
	}
	if f.count(`SELECT count(*) FROM delivery_attempts WHERE outcome = 'rate_limited' AND http_status = 429 AND finished_at > started_at`) != 1 {
		t.Fatal("attempt not recorded with outcome, status and timings")
	}
}

// Audit F3: the message keeps its latest attempt's start, for sorting the
// recipient list, even when that attempt's lease was lost meanwhile.
func TestComplete_RecordsLastAttemptTime(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.sending()
	ctx := context.Background()
	jobs, _ := f.q.Claim(ctx, "sms", 2, now, 30*time.Second)
	at := now.Add(time.Second)
	f.q.Complete(ctx, jobs[0], worker.Result{Status: "sent", Attempt: &worker.Attempt{StartedAt: at, FinishedAt: at, Outcome: "accepted", HTTPStatus: 200}})
	f.q.Complete(ctx, jobs[1], worker.Result{Status: "skipped", SkipReason: "no_consent"})

	if f.count(`SELECT count(*) FROM messages WHERE id = $1 AND last_attempt_at = $2`, jobs[0].MessageID, at) != 1 ||
		f.count(`SELECT count(*) FROM messages WHERE id = $1 AND last_attempt_at IS NULL`, jobs[1].MessageID) != 1 {
		t.Fatal("want the attempt's start on the sent message and none on the skip")
	}

	// A stale worker's late attempt still happened: it moves the time on, never back.
	f.exec(`UPDATE messages SET status = 'sending', lease_token = gen_random_uuid() WHERE id = $1`, jobs[0].MessageID)
	later := now.Add(time.Minute)
	stale := jobs[0]
	stale.Attempt = 9
	if err := f.q.Complete(ctx, stale, worker.Result{Status: "sent", Attempt: &worker.Attempt{StartedAt: later, FinishedAt: later, Outcome: "accepted", HTTPStatus: 200}}); !errors.Is(err, worker.ErrLeaseLost) {
		t.Fatalf("want a lost lease, got %v", err)
	}
	if f.count(`SELECT count(*) FROM messages WHERE id = $1 AND last_attempt_at = $2`, jobs[0].MessageID, later) != 1 {
		t.Fatal("the late attempt must still be the latest")
	}
}

func TestComplete_RetryAfterCancelIsCancelled(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := f.sending()
	ctx := context.Background()
	jobs, _ := f.q.Claim(ctx, "sms", 1, now, 30*time.Second)
	f.exec(`UPDATE campaigns SET status = 'cancelled' WHERE id = $1`, id) // cancelled while the attempt ran

	if err := f.q.Complete(ctx, jobs[0], worker.Result{Status: "retry_scheduled", NextAttemptAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if got := f.str(`SELECT status FROM messages WHERE id = $1`, jobs[0].MessageID); got != "cancelled" {
		t.Fatalf("a retry of a cancelled campaign must become cancelled, got %s", got)
	}
}

func TestFinishCampaigns(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := f.sending()
	ctx := context.Background()

	if n, _ := f.q.FinishCampaigns(ctx, now); n != 0 {
		t.Fatal("a campaign with pending messages is not finished")
	}
	f.exec(`UPDATE messages SET status = 'sent' WHERE channel = 'sms'`)
	f.exec(`UPDATE messages SET status = 'failed' WHERE channel = 'email'`)
	n, err := f.q.FinishCampaigns(ctx, now)

	if err != nil || n != 1 || f.str(`SELECT status FROM campaigns WHERE id = $1`, id) != "completed" ||
		f.count(`SELECT count(*) FROM campaigns WHERE id = $1 AND completed_at = $2`, id, now) != 1 {
		t.Fatalf("all messages final: want completed, got n=%d err=%v", n, err)
	}
}

// Option A (D31 history): a resend skips everyone whose delivery on the
// channel is uncertain, anywhere in the parent's family of campaigns.
func TestBuildAudience_ResendSkipsUncertainDeliveries(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		SELECT g, 'U', 'u' || g || '@example.com', '+90510' || g, 'Ankara', 'tr', true, true, $1 FROM generate_series(10, 15) g`, now)
	parent := f.approved(cartAbandoners, "{sms}", now)
	f.exec(`UPDATE campaigns SET status = 'completed' WHERE id = $1`, parent)
	f.exec(`INSERT INTO messages (campaign_id, user_id, channel, status, skip_reason)
		SELECT $1, g, 'sms', 'skipped', 'character_limit_exceeded' FROM generate_series(10, 15) g`, parent)
	var old int64 // an earlier resend, cancelled with deliveries in every state
	f.pool.QueryRow(context.Background(), `INSERT INTO campaigns (request_text, channels, status, send_at, time_zone, created_by, parent_campaign_id)
		VALUES ('brief', '{sms}', 'cancelled', $1, 'Europe/Istanbul', 1, $2) RETURNING id`, now, parent).Scan(&old)
	f.exec(`INSERT INTO messages (id, campaign_id, user_id, channel, status, delivery_uncertain) VALUES
		(910, $1, 10, 'sms', 'sending', false),
		(911, $1, 11, 'sms', 'cancelled', true),
		(912, $1, 12, 'sms', 'cancelled', true),
		(913, $1, 13, 'sms', 'cancelled', true),
		(914, $1, 14, 'sms', 'cancelled', false)`, old)
	f.exec(`INSERT INTO delivery_attempts (message_id, attempt_no, started_at, outcome) VALUES
		(911, 1, $1, 'timeout'), (912, 1, $1, 'network_error'), (913, 1, $1, 'interrupted'), (914, 1, $1, 'server_error')`, now)
	var resend int64
	f.pool.QueryRow(context.Background(), `INSERT INTO campaigns (request_text, channels, status, send_at, time_zone, created_by, parent_campaign_id)
		VALUES ('brief', '{sms}', 'approved', $1, 'Europe/Istanbul', 1, $2) RETURNING id`, now, parent).Scan(&resend)

	f.build(now)

	rows, _ := f.pool.Query(context.Background(), `SELECT user_id FROM messages WHERE campaign_id = $1 ORDER BY 1`, resend)
	var users []int64
	for rows.Next() {
		var u int64
		rows.Scan(&u)
		users = append(users, u)
	}
	// 10 still sending, 11–13 possibly delivered; 14 failed for sure (500), 15 never tried.
	if len(users) != 2 || users[0] != 14 || users[1] != 15 {
		t.Fatalf("want users 14 and 15 only, got %v", users)
	}
}

func TestReclaimExpired_MarksDeliveryUncertain(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.sending()
	ctx := context.Background()
	first, _ := f.q.Claim(ctx, "sms", 1, now, 30*time.Second)
	if f.str(`SELECT delivery_uncertain::text FROM messages WHERE id = $1`, first[0].MessageID) != "false" {
		t.Fatal("a first claim is not uncertain")
	}

	f.q.ReclaimExpired(ctx, now.Add(31*time.Second)) // the first worker may have sent it

	if f.str(`SELECT delivery_uncertain::text FROM messages WHERE id = $1`, first[0].MessageID) != "true" {
		t.Fatal("reclaiming an expired lease must mark the delivery uncertain")
	}
}
