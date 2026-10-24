package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/auth"
	"smart-campaign-composer/internal/campaign"
	"smart-campaign-composer/internal/config"
	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/dbtest"
	"smart-campaign-composer/internal/llm"
	"smart-campaign-composer/internal/provider"
	"smart-campaign-composer/internal/worker"
)

// providerLog records every response the provider gives, per key.
type providerLog struct {
	mu        sync.Mutex
	requests  map[string]int       // key → requests received
	processed map[string]int       // key → requests answered 200 without replay (a real send)
	first     map[string]time.Time // key → first request
	body      map[string]string    // key → the message text sent
}

func (l *providerLog) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, at := r.Header.Get("Idempotency-Key"), time.Now()
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var msg struct{ Body string }
		json.Unmarshal(raw, &msg)
		l.mu.Lock() // count the request on arrival: a hung one is still a request
		l.requests[key]++
		l.body[key] = msg.Body
		if _, ok := l.first[key]; !ok {
			l.first[key] = at
		}
		l.mu.Unlock()
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		l.mu.Lock()
		if rec.Code == 200 && rec.Header().Get("Idempotent-Replayed") == "" {
			l.processed[key]++
		}
		l.mu.Unlock()
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
	})
}

type stack struct {
	t      *testing.T
	pool   *pgxpool.Pool
	api    *http.ServeMux
	cookie *http.Cookie
	log    *providerLog
}

// Approval sees a clock 59 s behind the real one, so a send 3 real seconds
// ahead (62 s on the API's clock: 2 s of margin for a slow machine) passes the
// 1-minute minimum (D29), while the worker uses real time.
const apiClockLag = 59 * time.Second

func newStack(t *testing.T) *stack {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.MigratedDB(t)
	if err := db.Seed(ctx, pool, db.DefaultRNGSeed, time.Now(), "e2e-password"); err != nil {
		t.Fatal(err)
	}
	provPool := dbtest.ProviderPool(t, pool)

	// Real provider with failure injection: errors, 429s and lost responses.
	flaky := config.ProviderChannel{DelayMax: 5 * time.Millisecond, ErrorRate: 0.15, RateLimitRate: 0.10, TimeoutRate: 0.03}
	prov := provider.New(provPool, map[string]config.ProviderChannel{"sms": flaky, "email": flaky}, 42)
	// Lost responses: hang past the worker's timeout. The timeout is generous so a
	// loaded machine (all test packages run in parallel) does not turn every
	// request into a timeout.
	prov.Hang = 2500 * time.Millisecond
	pmux := http.NewServeMux()
	prov.Routes(pmux)
	log := &providerLog{requests: map[string]int{}, processed: map[string]int{}, first: map[string]time.Time{}, body: map[string]string{}}
	provSrv := httptest.NewServer(log.wrap(pmux))
	t.Cleanup(provSrv.Close)

	api := http.NewServeMux()
	guarded := auth.Routes(api, pool, "mock")
	(&campaign.Handler{Pool: pool, LLM: llm.NewMock(), Timeout: 5 * time.Second,
		Now: func() time.Time { return time.Now().Add(-apiClockLag) }}).Routes(guarded)

	w := &worker.Worker{Queue: &worker.Queue{Pool: pool}, ProviderURL: provSrv.URL, Client: worker.NewClient(2 * time.Second),
		// Short retry waits: the retries still happen; the backoff's own values are
		// tested in internal/worker (D53).
		MaxAttempts: 5, BackoffMax: 2 * time.Second, Poll: 50 * time.Millisecond}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error)
	go func() { done <- w.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("worker: %v", err)
		}
	})

	s := &stack{t: t, pool: pool, api: api, log: log}
	rec := s.do(http.MethodPost, "/api/login", `{"email":"marketer@marketer.com","password":"e2e-password"}`)
	if rec.Code != 200 {
		t.Fatalf("login: %d", rec.Code)
	}
	s.cookie = rec.Result().Cookies()[0]
	return s
}

func (s *stack) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if s.cookie != nil {
		req.AddCookie(s.cookie)
	}
	rec := httptest.NewRecorder()
	s.api.ServeHTTP(rec, req)
	return rec
}

func (s *stack) json(method, path, body string, want int) map[string]any {
	s.t.Helper()
	rec := s.do(method, path, body)
	if rec.Code != want {
		s.t.Fatalf("%s %s: want %d, got %d %s", method, path, want, rec.Code, rec.Body)
	}
	var v map[string]any
	json.Unmarshal(rec.Body.Bytes(), &v)
	return v
}

func (s *stack) count(sql string, args ...any) int {
	s.t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		s.t.Fatalf("%v: %s", err, sql)
	}
	return n
}

func (s *stack) waitStatus(id int64, status string, within time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(within)
	for s.count(`SELECT count(*) FROM campaigns WHERE id = $1 AND status = $2`, id, status) == 0 {
		if time.Now().After(deadline) {
			s.t.Fatalf("campaign %d not %s within %v; open messages: %d", id, status, within,
				s.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status IN ('pending','sending','retry_scheduled')`, id))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func id(v map[string]any) int64 { return int64(v["id"].(float64)) }

// outcomes summarizes a campaign's attempts, e.g. "accepted=290 timeout=3".
func (s *stack) outcomes(campaignID int64) string {
	rows, _ := s.pool.Query(context.Background(), `SELECT a.outcome || '=' || count(*) FROM delivery_attempts a
		JOIN messages m ON m.id = a.message_id WHERE m.campaign_id = $1 GROUP BY a.outcome ORDER BY a.outcome`, campaignID)
	parts, _ := pgx.CollectRows(rows, pgx.RowTo[string])
	return strings.Join(parts, " ")
}

// ids reads all message IDs first: asserting while rows are open would leak
// the connection on t.Fatal and hang the pool's cleanup.
func (s *stack) ids(sql string, campaignID int64) []string {
	s.t.Helper()
	rows, _ := s.pool.Query(context.Background(), sql, campaignID)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		s.t.Fatal(err)
	}
	keys := make([]string, len(ids))
	for i, n := range ids {
		keys[i] = strconv.FormatInt(n, 10)
	}
	return keys
}

func (s *stack) messageKeys(campaignID int64) []string {
	return s.ids(`SELECT id FROM messages WHERE campaign_id = $1`, campaignID)
}

// checkDelivery: every message is final, no key was really sent twice, every
// message marked sent was really sent, and every message the provider really
// sent is recorded as sent, unless its delivery is honestly marked uncertain
// (e.g. the last attempt timed out although the provider accepted later).
func (s *stack) checkDelivery(campaignID int64) {
	s.t.Helper()
	if open := s.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status IN ('pending','sending','retry_scheduled')`, campaignID); open != 0 {
		s.t.Fatalf("%d messages not final", open)
	}
	s.log.mu.Lock()
	defer s.log.mu.Unlock()
	for _, key := range s.messageKeys(campaignID) {
		if s.log.processed[key] > 1 {
			s.t.Fatalf("message %s was sent %d times", key, s.log.processed[key])
		}
	}
	for _, key := range s.ids(`SELECT id FROM messages WHERE campaign_id = $1 AND status = 'sent'`, campaignID) {
		if s.log.processed[key] != 1 {
			s.t.Fatalf("message %s is sent but the provider never accepted it", key)
		}
	}
	for _, key := range s.ids(`SELECT id FROM messages WHERE campaign_id = $1 AND status <> 'sent' AND NOT delivery_uncertain`, campaignID) {
		if s.log.processed[key] != 0 {
			s.t.Fatalf("message %s was really sent but is recorded as not sent, without being marked uncertain", key)
		}
	}
}

const caseExample = "Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but did not buy anything. Keep the message short and friendly."

func TestEndToEnd(t *testing.T) {
	t.Parallel() // own database; nothing shared with other tests
	s := newStack(t)

	t.Run("send now through a flaky provider", func(t *testing.T) {
		s.t = t
		c := s.json(http.MethodPost, "/api/campaigns", `{"request":`+strconv.Quote(caseExample)+`,"channels":["sms","email"],"time_zone":"Europe/Istanbul"}`, 201)
		cid := id(c)
		gen := s.do(http.MethodPost, "/api/campaigns/"+strconv.FormatInt(cid, 10)+"/content", `{"version":1,"languages":["tr"]}`)
		if !strings.Contains(gen.Body.String(), "event: done") {
			t.Fatalf("content generation did not finish: %s", gen.Body)
		}
		s.json(http.MethodPost, "/api/campaigns/"+strconv.FormatInt(cid, 10)+"/languages/tr/approve", `{"version":2}`, 200)
		approved := s.json(http.MethodPost, "/api/campaigns/"+strconv.FormatInt(cid, 10)+"/approve", `{"version":2,"send":"now"}`, 200)
		est := approved["approval_estimate"].(map[string]any)

		s.waitStatus(cid, "completed", 90*time.Second)

		s.checkDelivery(cid)
		// Counts match the approval estimate: nobody's data changed in between.
		sms := s.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND channel = 'sms'`, cid)
		email := s.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND channel = 'email'`, cid)
		if float64(sms) != est["sms"] || float64(email) != est["email"] || sms == 0 {
			t.Fatalf("messages sms=%d email=%d, estimate %v", sms, email, est)
		}
		// The failure injection really happened and was retried.
		retried := s.count(`SELECT count(*) FROM delivery_attempts a JOIN messages m ON m.id = a.message_id
			WHERE m.campaign_id = $1 AND a.outcome IN ('server_error','rate_limited','timeout')`, cid)
		sent := s.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status = 'sent'`, cid)
		t.Logf("%d messages: %d sent, %d retried attempts; outcomes: %s", sms+email, sent, retried, s.outcomes(cid))
		if retried == 0 || sent < (sms+email)*9/10 {
			t.Fatalf("want retries to happen and almost everything sent, got %d retries, %d/%d sent", retried, sent, sms+email)
		}
	})

	t.Run("scheduled send waits for its time", func(t *testing.T) {
		s.t = t
		c := s.json(http.MethodPost, "/api/campaigns", `{"request":"Send a weekend sale announcement to all users in Muğla.","channels":["sms"],"time_zone":"Europe/Istanbul"}`, 201)
		cid := id(c)
		path := "/api/campaigns/" + strconv.FormatInt(cid, 10)
		s.json(http.MethodPut, path+"/content", `{"version":1,"default_language":"tr","content":{"tr":{"sms":{"body":"Merhaba {{first_name}}, hafta sonu indirimi!"}}}}`, 200)
		sendAt := time.Now().Add(4 * time.Second)
		s.json(http.MethodPost, path+"/languages/tr/approve", `{"version":2}`, 200)
		s.json(http.MethodPost, path+"/approve", `{"version":2,"send":{"at":"`+sendAt.Format(time.RFC3339Nano)+`"}}`, 200)

		time.Sleep(time.Until(sendAt.Add(-2 * time.Second)))
		// Only meaningful while send_at is still ahead, by the database's clock.
		if s.count(`SELECT count(*) WHERE now() < $1`, sendAt) != 1 {
			t.Fatal("the check before send_at ran after it; the machine is too slow for this test")
		}
		if n := s.count(`SELECT count(*) FROM messages WHERE campaign_id = $1`, cid); n != 0 ||
			s.count(`SELECT count(*) FROM campaigns WHERE id = $1 AND status = 'approved'`, cid) != 1 {
			t.Fatalf("before send_at: want approved and no messages, got %d messages", n)
		}

		s.waitStatus(cid, "completed", 60*time.Second)
		s.checkDelivery(cid)
		s.log.mu.Lock()
		defer s.log.mu.Unlock()
		for _, key := range s.messageKeys(cid) {
			if first, ok := s.log.first[key]; ok && first.Before(sendAt) {
				t.Fatalf("message %s reached the provider at %v, before send_at %v", key, first, sendAt)
			}
		}
	})

	// D64: each recipient gets their language's text, else the default.
	t.Run("each recipient gets their language", func(t *testing.T) {
		for _, tc := range []struct {
			name, content, langs string
		}{
			{"Turkish and English", `{"version":1,"default_language":"tr","content":{"tr":{"sms":{"body":"Merhaba {{first_name}}!"}},"en":{"sms":{"body":"Hello {{first_name}}!"}}}}`, "tr en"},
			{"Turkish only", `{"version":1,"default_language":"tr","content":{"tr":{"sms":{"body":"Merhaba {{first_name}}!"}}}}`, "tr"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s.t = t
				c := s.json(http.MethodPost, "/api/campaigns", `{"request":"Send a weekend sale announcement to all users in İzmir.","channels":["sms"],"time_zone":"Europe/Istanbul"}`, 201)
				cid := id(c)
				path := "/api/campaigns/" + strconv.FormatInt(cid, 10)
				s.json(http.MethodPut, path+"/content", tc.content, 200)
				for _, l := range strings.Fields(tc.langs) {
					s.json(http.MethodPost, path+"/languages/"+l+"/approve", `{"version":2}`, 200)
				}
				s.json(http.MethodPost, path+"/approve", `{"version":2,"send":"now"}`, 200)
				s.waitStatus(cid, "completed", 90*time.Second)
				s.checkDelivery(cid)

				rows, err := s.pool.Query(context.Background(), `SELECT m.id::text, u.language FROM messages m JOIN users u ON u.id = m.user_id
					WHERE m.campaign_id = $1 AND m.status = 'sent'`, cid)
				if err != nil {
					t.Fatal(err)
				}
				speakers := map[string]int{}
				s.log.mu.Lock()
				defer s.log.mu.Unlock()
				var key, lang string
				if _, err := pgx.ForEachRow(rows, []any{&key, &lang}, func() error {
					speakers[lang]++
					want := "Merhaba "
					if lang == "en" && strings.Contains(tc.langs, "en") {
						want = "Hello "
					}
					if !strings.HasPrefix(s.log.body[key], want) {
						t.Errorf("message %s to a %s speaker: got %q", key, lang, s.log.body[key])
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if speakers["tr"] == 0 || speakers["en"] == 0 {
					t.Fatalf("the audience must include both languages to prove anything: %v", speakers)
				}
			})
		}
	})

	t.Run("over-limit recipients are skipped, then resent", func(t *testing.T) {
		s.t = t
		c := s.json(http.MethodPost, "/api/campaigns", `{"request":`+strconv.Quote(caseExample)+`,"channels":["sms"],"time_zone":"Europe/Istanbul"}`, 201)
		cid := id(c)
		path := "/api/campaigns/" + strconv.FormatInt(cid, 10)
		// 143 characters + first name, last name and city (seeded: about 8 to 25):
		// roughly half the recipients go over 160.
		long := "Merhaba {{first_name}} {{last_name}}, " + strings.Repeat("ş", 123) + " {{city}}'da %30!"
		s.json(http.MethodPut, path+"/content", `{"version":1,"default_language":"tr","content":{"tr":{"sms":{"body":`+strconv.Quote(long)+`}}}}`, 200)
		s.json(http.MethodPost, path+"/languages/tr/approve", `{"version":2}`, 200)
		s.json(http.MethodPost, path+"/approve", `{"version":2,"send":"now"}`, 200)
		s.waitStatus(cid, "completed", 90*time.Second)
		s.checkDelivery(cid)

		skipped := s.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND skip_reason = 'character_limit_exceeded'`, cid)
		if skipped == 0 || skipped == s.count(`SELECT count(*) FROM messages WHERE campaign_id = $1`, cid) {
			t.Fatalf("want some but not all recipients skipped for length, got %d", skipped)
		}

		r := s.json(http.MethodPost, path+"/resend", `{"channel":"sms"}`, 201)
		rid := id(r)
		rpath := "/api/campaigns/" + strconv.FormatInt(rid, 10)
		s.json(http.MethodPut, rpath+"/content", `{"version":1,"default_language":"tr","content":{"tr":{"sms":{"body":"{{first_name}}, %30 indirim seni bekliyor!"}}}}`, 200)
		s.json(http.MethodPost, rpath+"/languages/tr/approve", `{"version":2}`, 200)
		s.json(http.MethodPost, rpath+"/approve", `{"version":2,"send":"now"}`, 200)
		s.waitStatus(rid, "completed", 90*time.Second)
		s.checkDelivery(rid)

		// The resend went to exactly the skipped users, and nobody got two SMS.
		if n := s.count(`SELECT count(*) FROM messages r WHERE r.campaign_id = $1 AND NOT EXISTS (SELECT 1 FROM messages p
				WHERE p.campaign_id = $2 AND p.user_id = r.user_id AND p.skip_reason = 'character_limit_exceeded')`, rid, cid); n != 0 {
			t.Fatalf("%d resend messages went to users who were not skipped", n)
		}
		if n := s.count(`SELECT count(*) FROM messages WHERE campaign_id = $1`, rid); n != skipped {
			t.Fatalf("resend has %d messages, %d were skipped", n, skipped)
		}
		if n := s.count(`SELECT count(*) FROM (SELECT user_id FROM messages WHERE campaign_id IN ($1, $2) AND channel = 'sms'
				AND status = 'sent' GROUP BY user_id HAVING count(*) > 1) d`, cid, rid); n != 0 {
			t.Fatalf("%d users got the SMS twice", n)
		}
		t.Logf("%d skipped for length, resent: %d sent", skipped, s.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status = 'sent'`, rid))
	})
}
