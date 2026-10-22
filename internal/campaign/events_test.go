package campaign_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"smart-campaign-composer/internal/llm"
)

// liveStream reads GET /api/campaigns/{id}/events from a real server, so the
// test can change the database while the stream is open.
type liveStream struct {
	events  chan event
	cancel  context.CancelFunc
	handled chan struct{} // closed when the server handler returns
}

func (a *app) stream(id int64) *liveStream {
	a.t.Helper()
	s := &liveStream{events: make(chan event, 16), handled: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(s.handled)
		a.mux.ServeHTTP(w, r)
	}))
	a.t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	a.t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/campaigns/"+itoa(id)+"/events", nil)
	req.AddCookie(a.cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusOK || ct != "text/event-stream" {
		a.t.Fatalf("want an event stream, got %d %q", resp.StatusCode, ct)
	}
	go func() {
		defer resp.Body.Close()
		defer close(s.events)
		sc := bufio.NewScanner(resp.Body)
		var e event
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				e.Name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e.Data)
			case line == "" && e.Name != "":
				s.events <- e
				e = event{}
			}
		}
	}()
	return s
}

// next waits for the next event; ok is false if the stream closed.
func (s *liveStream) next(t *testing.T) (event, bool) {
	t.Helper()
	select {
	case e, ok := <-s.events:
		return e, ok
	case <-time.After(3 * time.Second):
		t.Fatal("no event within 3s")
		return event{}, false
	}
}

// quiet fails if an event arrives within d (the poll interval is 20 ms).
func (s *liveStream) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case e, ok := <-s.events:
		t.Fatalf("want no event, got %v (open %v)", e, ok)
	case <-time.After(d):
	}
}

// sendingCampaign approves the case-example draft and gives it messages and
// attempts as the worker would. Users 3–7 are added for the SMS channel.
func (a *app) sendingCampaign() int64 {
	a.t.Helper()
	d := a.readyDraft()
	if code, body := a.approve(d.ID, `{"version":2,"send":"now"}`); code != http.StatusOK {
		a.t.Fatalf("approve: %d %v", code, body)
	}
	a.exec(`UPDATE campaigns SET status = 'sending', audience_built_at = $2 WHERE id = $1`, d.ID, fixedNow)
	a.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		SELECT i, 'U', 'u' || i || '@example.com', '+90500000000' || i, 'İstanbul', 'tr', true, true, $1
		FROM generate_series(3, 7) i`, fixedNow.AddDate(-1, 0, 0))
	a.exec(`INSERT INTO messages (campaign_id, user_id, channel, status, skip_reason, last_error, delivery_uncertain) VALUES
		($1, 1, 'sms', 'sent', NULL, NULL, false),
		($1, 2, 'sms', 'failed', NULL, 'HTTP 400', false),
		($1, 3, 'sms', 'failed', NULL, 'HTTP 400', false),
		($1, 4, 'sms', 'skipped', 'character_limit_exceeded', NULL, false),
		($1, 5, 'sms', 'skipped', 'no_consent', NULL, false),
		($1, 6, 'sms', 'pending', NULL, NULL, false),
		($1, 7, 'sms', 'retry_scheduled', NULL, 'HTTP 500', true),
		($1, 1, 'email', 'sending', NULL, NULL, false)`, d.ID)
	a.exec(`INSERT INTO delivery_attempts (message_id, attempt_no, started_at, outcome, http_status)
		SELECT m.id, a.no, $2, a.outcome, a.code FROM messages m
		JOIN (VALUES (1, 1, 'rate_limited', 429), (1, 2, 'accepted', 200), (2, 1, 'rejected', 400),
			(3, 1, 'rejected', 400), (7, 1, 'server_error', 500)) a(user_id, no, outcome, code)
			ON a.user_id = m.user_id
		WHERE m.campaign_id = $1 AND m.channel = 'sms'`, d.ID, fixedNow)
	return d.ID
}

func TestEvents_SnapshotCounts(t *testing.T) {
	a := newApp(t, llm.NewMock())
	id := a.sendingCampaign()

	e, _ := a.stream(id).next(t)

	if e.Name != "status" || e.Data["status"] != "sending" || e.Data["audience_built_at"] == nil {
		t.Fatalf("want a sending status snapshot, got %v", e)
	}
	estimate := e.Data["approval_estimate"].(map[string]any)
	if estimate["sms"] != float64(1) || estimate["email"] != float64(1) {
		t.Fatalf("want the approval estimate, got %v", estimate)
	}
	channels := e.Data["channels"].(map[string]any)
	sms := channels["sms"].(map[string]any)
	want := map[string]any{
		"audience": float64(7),
		"counts": map[string]any{"pending": float64(1), "sending": float64(0), "retry_scheduled": float64(1), "sent": float64(1),
			"failed": float64(2), "skipped": float64(2), "cancelled": float64(0)},
		"retries":          float64(1),
		"rate_limited":     float64(1),
		"skip_reasons":     map[string]any{"character_limit_exceeded": float64(1), "no_consent": float64(1)},
		"failure_reasons":  []any{map[string]any{"reason": "HTTP 400", "count": float64(2)}},
		"uncertain":        float64(1),
		"resendable":       float64(1),
		"active_resend_id": nil,
	}
	if !reflect.DeepEqual(sms, want) {
		t.Fatalf("sms:\n got %v\nwant %v", sms, want)
	}
	email := channels["email"].(map[string]any)
	if email["audience"] != float64(1) || email["counts"].(map[string]any)["sending"] != float64(1) ||
		email["failure_reasons"] == nil || len(email["failure_reasons"].([]any)) != 0 {
		t.Fatalf("email: %v", email)
	}
}

func TestEvents_SendsOnlyOnChange(t *testing.T) {
	a := newApp(t, llm.NewMock())
	id := a.sendingCampaign()
	s := a.stream(id)
	s.next(t)

	s.quiet(t, 200*time.Millisecond) // ten polls, nothing changed

	a.exec(`UPDATE messages SET status = 'sent' WHERE campaign_id = $1 AND user_id = 6 AND channel = 'sms'`, id)
	e, _ := s.next(t)
	counts := e.Data["channels"].(map[string]any)["sms"].(map[string]any)["counts"].(map[string]any)
	if e.Name != "status" || counts["sent"] != float64(2) || counts["pending"] != float64(0) {
		t.Fatalf("want the change, got %v", e)
	}
	s.quiet(t, 100*time.Millisecond)
}

func TestEvents_StopsOnClientDisconnect(t *testing.T) {
	a := newApp(t, llm.NewMock())
	s := a.stream(a.sendingCampaign())
	s.next(t)

	s.cancel()

	select {
	case <-s.handled:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler kept running after the client left")
	}
}

// A cancel leaves messages a worker holds in "sending"; the stream ends only
// once they are final, so the last snapshot is the true result.
func TestEvents_EndsAfterFinalSnapshot(t *testing.T) {
	a := newApp(t, llm.NewMock())
	id := a.sendingCampaign()
	if rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(id)+"/cancel", ""); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	s := a.stream(id)
	if e, _ := s.next(t); e.Data["status"] != "cancelled" {
		t.Fatalf("want cancelled, got %v", e)
	}
	s.quiet(t, 200*time.Millisecond) // the email message is still in flight

	a.exec(`UPDATE messages SET status = 'sent' WHERE campaign_id = $1 AND status = 'sending'`, id)

	final, _ := s.next(t)
	email := final.Data["channels"].(map[string]any)["email"].(map[string]any)["counts"].(map[string]any)
	if final.Name != "status" || email["sent"] != float64(1) || email["sending"] != float64(0) {
		t.Fatalf("want the final snapshot, got %v", final)
	}
	if e, _ := s.next(t); e.Name != "end" {
		t.Fatalf("want end, got %v", e)
	}
	if _, open := s.next(t); open {
		t.Fatal("want the stream closed after end")
	}
}

func TestEvents_FinishedCampaignShowsActiveResend(t *testing.T) {
	a := newApp(t, llm.NewMock())
	id := a.sendingCampaign()
	a.exec(`UPDATE messages SET status = 'cancelled' WHERE campaign_id = $1 AND status IN ('pending', 'sending', 'retry_scheduled')`, id)
	a.exec(`UPDATE campaigns SET status = 'completed', completed_at = $2 WHERE id = $1`, id, fixedNow)
	rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(id)+"/resend", `{"channel":"sms"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("resend: %d %s", rec.Code, rec.Body)
	}
	resend := decode[draft](t, rec)
	s := a.stream(id)

	e, _ := s.next(t)

	sms := e.Data["channels"].(map[string]any)["sms"].(map[string]any)
	if e.Data["completed_at"] == nil || sms["active_resend_id"] != float64(resend.ID) || sms["resendable"] != float64(1) {
		t.Fatalf("want completed with the active resend, got %v", e)
	}
	if e, _ := s.next(t); e.Name != "end" {
		t.Fatalf("want end right after the snapshot, got %v", e)
	}
}

func TestEvents_UnknownCampaign404(t *testing.T) {
	a := newApp(t, llm.NewMock())

	if rec := a.do(http.MethodGet, "/api/campaigns/999/events", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}
