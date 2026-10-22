package provider_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/config"
	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/dbtest"
	"smart-campaign-composer/internal/provider"
)

// providerDB migrates a fresh database as the app and returns two pools: the
// admin one and one logged in as the provider's own role (D6).
func providerDB(t *testing.T) (admin, prov *pgxpool.Pool) {
	t.Helper()
	admin = dbtest.FreshDB(t)
	if err := db.Migrate(context.Background(), admin); err != nil {
		t.Fatal(err)
	}
	return admin, dbtest.ProviderPool(t, admin)
}

var instant = config.ProviderChannel{} // no delay, no failures

func start(t *testing.T, pool *pgxpool.Pool, b config.ProviderChannel, tune func(*provider.Server)) string {
	t.Helper()
	s := provider.New(pool, map[string]config.ProviderChannel{"sms": b, "email": b}, 7)
	if tune != nil {
		tune(s)
	}
	mux := http.NewServeMux()
	s.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

type reply struct {
	status  int
	body    string
	header  http.Header
	timeout bool
}

func send(t *testing.T, url, channel, key, body string, client *http.Client) reply {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/"+channel+"/send", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return reply{timeout: true}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, body: string(b), header: resp.Header}
}

const smsBody = `{"to":"+905551234567","body":"Merhaba Ayşe!"}`

func keyState(t *testing.T, pool *pgxpool.Pool, key string) string {
	t.Helper()
	var state string
	err := pool.QueryRow(context.Background(), `SELECT state FROM provider.idempotency_keys WHERE key = $1`, key).Scan(&state)
	if err != nil {
		return "absent"
	}
	return state
}

func TestSend_SuccessIsStoredAndReplayed(t *testing.T) {
	admin, prov := providerDB(t)
	url := start(t, prov, instant, nil)

	first := send(t, url, "sms", "msg-1", smsBody, nil)
	again := send(t, url, "sms", "msg-1", smsBody, nil)

	if first.status != 200 || !strings.Contains(first.body, `"accepted"`) {
		t.Fatalf("want 200 accepted, got %d %s", first.status, first.body)
	}
	if again.status != 200 || again.body != first.body || again.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("same key: want the stored result replayed, got %d %s", again.status, again.body)
	}
	if keyState(t, admin, "msg-1") != "completed" {
		t.Fatal("key not stored as completed")
	}
}

func TestSend_KeyIsInsertedBeforeSimulating(t *testing.T) {
	admin, prov := providerDB(t)
	slow := config.ProviderChannel{DelayMin: 400 * time.Millisecond, DelayMax: 400 * time.Millisecond}
	url := start(t, prov, slow, nil)

	done := make(chan reply)
	go func() { done <- send(t, url, "sms", "msg-1", smsBody, nil) }()
	time.Sleep(150 * time.Millisecond)

	if got := keyState(t, admin, "msg-1"); got != "in_progress" {
		t.Fatalf("during the simulated delay the key must already be stored, got %s", got)
	}
	if r := <-done; r.status != 200 || keyState(t, admin, "msg-1") != "completed" {
		t.Fatalf("want 200 and completed, got %d", r.status)
	}
}

// D6: two requests with the same key never both send.
func TestSend_ConcurrentSameKey(t *testing.T) {
	_, prov := providerDB(t)
	slow := config.ProviderChannel{DelayMin: 300 * time.Millisecond, DelayMax: 300 * time.Millisecond}
	url := start(t, prov, slow, nil)

	replies := make([]reply, 2)
	var wg sync.WaitGroup
	for i := range replies {
		wg.Add(1)
		go func() { defer wg.Done(); replies[i] = send(t, url, "sms", "msg-1", smsBody, nil) }()
		time.Sleep(50 * time.Millisecond) // the second arrives while the first is in progress
	}
	wg.Wait()

	ok, busy := replies[0], replies[1]
	if ok.status != 200 || busy.status != http.StatusServiceUnavailable || busy.header.Get("Retry-After") == "" {
		t.Fatalf("want 200 and a retryable 503 with Retry-After, got %d and %d %v", ok.status, busy.status, busy.header)
	}
	if later := send(t, url, "sms", "msg-1", smsBody, nil); later.body != ok.body {
		t.Fatalf("retry after completion: want the same result, got %s", later.body)
	}
}

// Failed attempts release the key, so the worker's retry is processed (D7).
func TestSend_FailuresReleaseTheKey(t *testing.T) {
	admin, prov := providerDB(t)

	url := start(t, prov, config.ProviderChannel{ErrorRate: 1}, nil)
	if r := send(t, url, "sms", "msg-5xx", smsBody, nil); r.status < 500 || keyState(t, admin, "msg-5xx") != "absent" {
		t.Fatalf("5xx: want a server error and no stored key, got %d %s", r.status, keyState(t, admin, "msg-5xx"))
	}

	url = start(t, prov, config.ProviderChannel{RateLimitRate: 1}, nil)
	r := send(t, url, "sms", "msg-429", smsBody, nil)
	if r.status != http.StatusTooManyRequests || r.header.Get("Retry-After") != "1" || keyState(t, admin, "msg-429") != "absent" {
		t.Fatalf("429: want Retry-After 1 and no stored key, got %d %v %s", r.status, r.header, keyState(t, admin, "msg-429"))
	}

	url = start(t, prov, instant, nil)
	if r := send(t, url, "sms", "msg-429", smsBody, nil); r.status != 200 {
		t.Fatalf("retry after 429: want 200, got %d", r.status)
	}
}

// A timeout is a lost response: the send still happens, and the retry with
// the same key gets the stored result instead of a second send.
func TestSend_TimeoutIsALostResponse(t *testing.T) {
	_, prov := providerDB(t)
	url := start(t, prov, config.ProviderChannel{TimeoutRate: 1}, func(s *provider.Server) { s.Hang = 300 * time.Millisecond })

	if r := send(t, url, "sms", "msg-1", smsBody, &http.Client{Timeout: 100 * time.Millisecond}); !r.timeout {
		t.Fatalf("want a client timeout, got %d", r.status)
	}
	time.Sleep(400 * time.Millisecond)

	r := send(t, url, "sms", "msg-1", smsBody, nil)
	if r.status != 200 || r.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry after the timeout: want the stored 200 replayed, got %d %v", r.status, r.header)
	}
}

func TestSend_StaleInProgressKeyIsTakenOver(t *testing.T) {
	admin, prov := providerDB(t)
	if _, err := admin.Exec(context.Background(), `INSERT INTO provider.idempotency_keys (key, channel, state, created_at)
		VALUES ('msg-1', 'sms', 'in_progress', now() - interval '2 minutes')`); err != nil {
		t.Fatal(err)
	}
	url := start(t, prov, instant, nil)

	if r := send(t, url, "sms", "msg-1", smsBody, nil); r.status != 200 || keyState(t, admin, "msg-1") != "completed" {
		t.Fatalf("a key left in progress by a crash must not block forever, got %d", r.status)
	}
}

func TestSend_RestartKeepsKeys(t *testing.T) {
	_, prov := providerDB(t)
	first := send(t, start(t, prov, instant, nil), "email", "msg-1", `{"to":"a@example.com","subject":"S","body":"B"}`, nil)

	after := send(t, start(t, prov, instant, nil), "email", "msg-1", `{"to":"a@example.com","subject":"S","body":"B"}`, nil)

	if first.status != 200 || after.body != first.body {
		t.Fatalf("a new provider process must replay stored results: %s vs %s", first.body, after.body)
	}
}

func TestSend_MalformedRequests(t *testing.T) {
	_, prov := providerDB(t)
	url := start(t, prov, instant, nil)
	send(t, url, "sms", "used-for-sms", smsBody, nil)

	for name, tc := range map[string]struct{ channel, key, body string }{
		"no key":                {"sms", "", smsBody},
		"key too long":          {"sms", strings.Repeat("k", 201), smsBody},
		"bad JSON":              {"sms", "k1", `{"to":`},
		"unknown field":         {"sms", "k2", `{"to":"+905551234567","body":"x","cc":"y"}`},
		"sms without to":        {"sms", "k3", `{"body":"x"}`},
		"sms without body":      {"sms", "k4", `{"to":"+905551234567"}`},
		"email without subject": {"email", "k5", `{"to":"a@example.com","body":"x"}`},
		"key used on sms":       {"email", "used-for-sms", `{"to":"a@example.com","subject":"S","body":"B"}`},
	} {
		if r := send(t, url, tc.channel, tc.key, tc.body, nil); r.status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %s", name, r.status, r.body)
		}
	}
	if r := send(t, url, "fax", "k6", smsBody, nil); r.status != http.StatusNotFound {
		t.Errorf("unknown channel: want 404, got %d", r.status)
	}
}

// The same seed gives the same sequence of outcomes, so test runs repeat.
func TestFailureInjection_IsDeterministic(t *testing.T) {
	_, prov := providerDB(t)
	mixed := config.ProviderChannel{ErrorRate: 0.3, RateLimitRate: 0.3}
	outcomes := func(seed int64, prefix string) []int {
		s := provider.New(prov, map[string]config.ProviderChannel{"sms": mixed, "email": mixed}, seed)
		mux := http.NewServeMux()
		s.Routes(mux)
		var got []int
		for i := range 40 {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/sms/send", bytes.NewBufferString(smsBody))
			req.Header.Set("Idempotency-Key", prefix+strconv.Itoa(i))
			mux.ServeHTTP(rec, req)
			got = append(got, rec.Code)
		}
		return got
	}

	a, b, c := outcomes(7, "a-"), outcomes(7, "b-"), outcomes(8, "c-")

	if !slices.Equal(a, b) {
		t.Fatalf("same seed, different outcomes:\n%v\n%v", a, b)
	}
	if slices.Equal(a, c) {
		t.Fatalf("different seeds gave identical outcomes: %v", a)
	}
	var codes = map[int]bool{}
	for _, code := range a {
		codes[code] = true
	}
	if !codes[200] || !codes[429] || !codes[500] {
		t.Fatalf("want a mix of 200, 429 and 500, got %v", a)
	}
}

// D6: the provider's role sees only its own schema.
func TestProviderRole_CannotReadAppTables(t *testing.T) {
	_, prov := providerDB(t)
	ctx := context.Background()

	var n int
	if err := prov.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("provider role must not read users, got n=%d err=%v", n, err)
	}
	if err := prov.QueryRow(ctx, `SELECT count(*) FROM provider.idempotency_keys`).Scan(&n); err != nil {
		t.Fatalf("provider role must read its own table: %v", err)
	}
}
