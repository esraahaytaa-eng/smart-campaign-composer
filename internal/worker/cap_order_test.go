package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"smart-campaign-composer/internal/dbtest"
)

// Finding 3: the last cancel check happens after waiting for the departure
// cap, so a cancel during that wait still stops the send.
func TestProcess_CancelDuringCapWait(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.MigratedDB(t)
	for _, sql := range []string{
		`INSERT INTO marketers (id, email, name, password_hash) VALUES (1, 'm@example.com', 'M', 'x')`,
		`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
			VALUES (1, 'A', 'a@example.com', '+905000000001', 'Ankara', 'tr', true, true, now())`,
		`INSERT INTO campaigns (id, request_text, channels, rules, status, time_zone, created_by)
			VALUES (1, 'b', '{sms}', '[]', 'sending', 'Europe/Istanbul', 1)`,
		`INSERT INTO campaign_languages (campaign_id, language) VALUES (1, 'tr')`,
		`UPDATE campaigns SET default_language = 'tr' WHERE id = 1`,
		`INSERT INTO campaign_content (campaign_id, channel, language, body) VALUES (1, 'sms', 'tr', 'Hi {{first_name}}')`,
		`INSERT INTO messages (campaign_id, user_id, channel) VALUES (1, 1, 'sms')`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%v: %s", err, sql)
		}
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	w := &Worker{Queue: &Queue{Pool: pool}, ProviderURL: srv.URL, Limiters: map[string]*rate.Limiter{"sms": rate.NewLimiter(rate.Inf, 1)}}
	w.defaults()
	// Full: the next departure must wait for the window. It is long enough
	// that the setup below can't outlast it even on a loaded machine.
	w.caps["sms"] = newWindowCap(1, 3*time.Second)
	w.caps["sms"].wait(ctx)
	jobs, err := w.Queue.Claim(ctx, "sms", 1, time.Now(), 30*time.Second)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %v %v", jobs, err)
	}

	// Cancel while the worker waits for the cap: the hook runs at that moment
	// and commits the cancel before the wait can end, however slow the machine.
	var once sync.Once
	var waited atomic.Bool
	w.caps["sms"].waiting = func() {
		waited.Store(true)
		once.Do(func() {
			if _, err := pool.Exec(ctx, `UPDATE campaigns SET status = 'cancelled' WHERE id = 1`); err != nil {
				t.Error(err)
			}
		})
	}
	if err := w.Process(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	if !waited.Load() {
		t.Fatal("the worker never had to wait for the cap: the setup outlasted the 3 s window, so the machine is too slow for this test")
	}

	var status string
	pool.QueryRow(ctx, `SELECT status FROM messages WHERE id = $1`, jobs[0].MessageID).Scan(&status)
	if status != "cancelled" || calls.Load() != 0 {
		t.Fatalf("want cancelled without a provider call, got %s and %d calls", status, calls.Load())
	}
}
