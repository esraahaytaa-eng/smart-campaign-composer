package campaign_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"smart-campaign-composer/internal/llm"
)

// readyDraft is the case-example draft (1 recipient on each channel) with
// valid Turkish content, approved by the marketer; it is at version 2.
func (a *app) readyDraft() draft {
	a.t.Helper()
	d := a.newDraft()
	if rec := a.do(http.MethodPut, "/api/campaigns/"+itoa(d.ID)+"/content", `{"version":1,"default_language":"tr","content":{"tr":`+validContent+`}}`); rec.Code != http.StatusOK {
		a.t.Fatalf("content: %d %s", rec.Code, rec.Body)
	}
	a.approveLanguages(d.ID, 2, "tr")
	return d
}

func (a *app) approve(id int64, body string) (int, map[string]any) {
	a.t.Helper()
	rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(id)+"/approve", body)
	return rec.Code, decode[map[string]any](a.t, rec)
}

func (a *app) exec(sql string, args ...any) {
	a.t.Helper()
	if _, err := a.pool.Exec(context.Background(), sql, args...); err != nil {
		a.t.Fatalf("%v: %s", err, sql)
	}
}

func at(d time.Duration) string { return fixedNow.Add(d).Format(time.RFC3339) }

func TestApprove_RequiresExplicitSendChoice(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	_, d := a.create(map[string]any{
		"request": "Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00.", "channels": []string{"sms"}, "time_zone": "Europe/Istanbul",
	})
	if d.SendAt == nil {
		t.Fatal("setup: the draft should have a proposed send time")
	}
	a.do(http.MethodPut, "/api/campaigns/"+itoa(d.ID)+"/content", `{"version":1,"default_language":"tr","content":{"tr":{"sms":{"body":"Hafta sonu indirimi!"}}}}`)

	code, _ := a.approve(d.ID, `{"version":2}`)

	if code != http.StatusUnprocessableEntity || a.count(`SELECT count(*) FROM campaigns WHERE status = 'draft'`) != 1 {
		t.Fatalf("approve without send: want 422 and still a draft, got %d", code)
	}
}

func TestApprove_NowStoresEstimateAndCreatesNoMessages(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()

	code, got := a.approve(d.ID, `{"version":2,"send":"now"}`)

	if code != http.StatusOK || got["status"] != "approved" || got["send_at"] != fixedNow.Format(time.RFC3339) || got["approved_by"] != float64(a.marketerID) {
		t.Fatalf("want approved now by the marketer, got %d %v", code, got)
	}
	est, _ := got["approval_estimate"].(map[string]any)
	if est["total"] != float64(1) || est["sms"] != float64(1) || est["email"] != float64(1) || est["over_limit"] == nil {
		t.Fatalf("approval_estimate: %v", got["approval_estimate"])
	}
	if a.count(`SELECT count(*) FROM messages`) != 0 {
		t.Fatal("approval must not create messages (D30)")
	}
}

func TestApprove_Scheduled(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()

	code, got := a.approve(d.ID, `{"version":2,"send":{"at":"`+at(2*time.Hour)+`"}}`)

	if code != http.StatusOK || got["status"] != "approved" || got["send_at"] != at(2*time.Hour) {
		t.Fatalf("want approved for +2h, got %d %v", code, got)
	}
}

func TestApprove_Rejects(t *testing.T) {
	t.Parallel() // times are relative to the fixed test clock
	cases := []struct {
		name  string
		setup func(a *app, id int64) // runs on a ready draft at version 2
		body  string
		want  int
	}{
		{"send in the past", nil, `{"version":2,"send":{"at":"` + at(-time.Minute) + `"}}`, 422},
		{"send in under a minute", nil, `{"version":2,"send":{"at":"` + at(30*time.Second) + `"}}`, 422},
		{"send over 30 days ahead", nil, `{"version":2,"send":{"at":"` + at(30*24*time.Hour+time.Second) + `"}}`, 422},
		{"send without offset", nil, `{"version":2,"send":{"at":"2026-10-22T10:00:00"}}`, 422},
		{"unknown send", nil, `{"version":2,"send":"later"}`, 422},
		{"stale version", nil, `{"version":1,"send":"now"}`, 409},
		{"no content", func(a *app, id int64) { a.exec(`DELETE FROM campaign_content WHERE campaign_id = $1`, id) }, `{"version":2,"send":"now"}`, 422},
		{"zero audience", func(a *app, id int64) {
			a.exec(`UPDATE campaigns SET rules = '[{"field":"cart_value","op":"gte","value":1000000}]' WHERE id = $1`, id)
		}, `{"version":2,"send":"now"}`, 422},
		// "No order since 1 Nov" matches everyone today, so only the window check can reject it.
		{"fixed window starts after send", func(a *app, id int64) {
			a.exec(`UPDATE campaigns SET rules = '[{"field":"no_order","op":"within","value":{"from":"2026-11-01"}}]' WHERE id = $1`, id)
		}, `{"version":2,"send":"now"}`, 422},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel() // each case has its own database
			a := newApp(t, llm.NewMock())
			d := a.readyDraft()
			if tc.setup != nil {
				tc.setup(a, d.ID)
			}

			code, got := a.approve(d.ID, tc.body)

			if code != tc.want || a.count(`SELECT count(*) FROM campaigns WHERE status = 'draft' AND approval_estimate IS NULL`) != 1 {
				t.Fatalf("want %d and the draft unchanged, got %d %v", tc.want, code, got)
			}
		})
	}
}

func TestApprove_ConcurrentDoubleApprove(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()

	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = a.do(http.MethodPost, "/api/campaigns/"+itoa(d.ID)+"/approve", `{"version":2,"send":"now"}`).Code
		}()
	}
	wg.Wait()

	if !(codes[0] == 200 && codes[1] == 409 || codes[0] == 409 && codes[1] == 200) {
		t.Fatalf("want one 200 and one 409, got %v", codes)
	}
}

func TestApprovedCampaign_IsFrozen(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	a.approve(d.ID, `{"version":2,"send":"now"}`)
	id := itoa(d.ID)

	for _, r := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/campaigns/" + id + "/content", `{"version":3,"default_language":"tr","content":{"tr":` + validContent + `}}`},
		{http.MethodPut, "/api/campaigns/" + id + "/rules", `{"version":3,"rules":[{"field":"city","op":"in","value":["Ankara"]}]}`},
		{http.MethodPut, "/api/campaigns/" + id + "/schedule", `{"version":3,"send_at":null}`},
		{http.MethodPost, "/api/campaigns/" + id + "/content", `{"version":3,"languages":["tr"]}`},
		{http.MethodPost, "/api/campaigns/" + id + "/approve", `{"version":3,"send":"now"}`},
	} {
		if rec := a.do(r.method, r.path, r.body); rec.Code != http.StatusConflict {
			t.Errorf("%s %s on an approved campaign: want 409, got %d", r.method, r.path, rec.Code)
		}
	}
}

func TestCancel(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	cancel := func(id int64) (int, map[string]any) {
		rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(id)+"/cancel", "")
		return rec.Code, decode[map[string]any](t, rec)
	}

	if code, _ := cancel(d.ID); code != http.StatusConflict {
		t.Fatalf("cancel a draft: want 409, got %d", code)
	}
	a.approve(d.ID, `{"version":2,"send":{"at":"`+at(time.Hour)+`"}}`)
	code, got := cancel(d.ID)
	if code != http.StatusOK || got["status"] != "cancelled" || got["cancelled_by"] != float64(a.marketerID) {
		t.Fatalf("cancel a scheduled campaign: want 200 cancelled, got %d %v", code, got)
	}
	if code, _ := cancel(d.ID); code != http.StatusConflict {
		t.Fatalf("cancel twice: want 409, got %d", code)
	}
}

func TestCancel_WhileSendingOnlyStopsWaitingMessages(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	a.exec(`UPDATE campaigns SET status = 'sending' WHERE id = $1`, d.ID)
	statuses := []string{"pending", "retry_scheduled", "sending", "sent", "failed", "skipped"}
	for i, s := range statuses {
		a.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
			VALUES ($1, 'U', $2, $3, 'Ankara', 'tr', true, true, now())`, 10+i, itoa(int64(10+i))+"@example.com", "+90500100"+itoa(int64(10+i)))
		skip := map[bool]any{true: "character_limit_exceeded", false: nil}[s == "skipped"]
		a.exec(`INSERT INTO messages (campaign_id, user_id, channel, status, skip_reason) VALUES ($1, $2, 'sms', $3, $4)`, d.ID, 10+i, s, skip)
	}

	rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(d.ID)+"/cancel", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("cancel while sending: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct{ from, to string }{
		{"pending", "cancelled"}, {"retry_scheduled", "cancelled"}, // waiting: stopped
		{"sending", "sending"}, {"sent", "sent"}, {"failed", "failed"}, {"skipped", "skipped"}, // in flight or final: untouched
	} {
		uid := 10 + indexOf(statuses, tc.from)
		if n := a.count(`SELECT count(*) FROM messages WHERE user_id = $1 AND status = $2`, uid, tc.to); n != 1 {
			t.Errorf("message that was %s: want %s", tc.from, tc.to)
		}
	}
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func TestEstimate(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()

	rec := a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID)+"/estimate", "")
	got := decode[map[string]any](t, rec)

	aud, _ := got["audience"].(map[string]any)
	over, _ := got["over_limit"].(map[string]any)
	if rec.Code != http.StatusOK || got["estimate"] != true || aud["total"] != float64(1) || over["sms"] != float64(0) {
		t.Fatalf("estimate: %d %s", rec.Code, rec.Body)
	}
}

// parentWithSkips makes a completed SMS campaign: users 1 and 2 were skipped
// for the character limit, user 3 was sent, user 4 failed and user 5 was
// skipped for missing consent (and has opted in since). An earlier, cancelled
// resend already reached user 2. Only user 1 qualifies for a resend.
func (a *app) parentWithSkips() int64 {
	a.t.Helper()
	a.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at) VALUES
		(3, 'C', 'c@example.com', '+905000000003', 'Ankara', 'tr', true, false, now()),
		(4, 'D', 'd@example.com', '+905000000004', 'Ankara', 'tr', true, false, now()),
		(5, 'E', 'e@example.com', '+905000000005', 'Ankara', 'tr', true, false, now())`)
	var parent, oldResend int64
	ctx := context.Background()
	a.pool.QueryRow(ctx, `INSERT INTO campaigns (request_text, channels, rules, status, time_zone, created_by)
		VALUES ('brief', '{sms}', '[{"field":"city","op":"in","value":["Ankara"]}]', 'completed', 'Europe/Istanbul', $1) RETURNING id`, a.marketerID).Scan(&parent)
	a.exec(`INSERT INTO messages (campaign_id, user_id, channel, status, skip_reason) VALUES
		($1, 1, 'sms', 'skipped', 'character_limit_exceeded'), ($1, 2, 'sms', 'skipped', 'character_limit_exceeded'), ($1, 3, 'sms', 'sent', NULL),
		($1, 4, 'sms', 'failed', NULL), ($1, 5, 'sms', 'skipped', 'no_consent')`, parent)
	a.pool.QueryRow(ctx, `INSERT INTO campaigns (request_text, channels, status, time_zone, created_by, parent_campaign_id)
		VALUES ('brief', '{sms}', 'cancelled', 'Europe/Istanbul', $1, $2) RETURNING id`, a.marketerID, parent).Scan(&oldResend)
	a.exec(`INSERT INTO messages (campaign_id, user_id, channel, status) VALUES ($1, 2, 'sms', 'sent')`, oldResend)
	return parent
}

func TestResend_AudienceIsExactlyTheSkippedNotYetSent(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	parent := a.parentWithSkips()

	rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(parent)+"/resend", `{"channel":"sms"}`)
	got := decode[map[string]any](t, rec)

	counts, _ := got["counts"].(map[string]any)
	if rec.Code != http.StatusCreated || got["status"] != "draft" || got["parent_campaign_id"] != float64(parent) || got["rules"] != nil {
		t.Fatalf("want a draft follow-up without rules, got %d %s", rec.Code, rec.Body)
	}
	if counts["total"] != float64(1) || counts["sms"] != float64(1) {
		t.Fatalf("want exactly user 1 (2 was reached by the earlier resend, 3 sent, 4 failed, 5 skipped for consent), got %v", counts)
	}
	id := int64(got["id"].(float64))
	if rec := a.do(http.MethodPut, "/api/campaigns/"+itoa(id)+"/rules", `{"version":1,"rules":[{"field":"city","op":"in","value":["Ankara"]}]}`); rec.Code != http.StatusConflict {
		t.Fatalf("rules of a resend are fixed: want 409, got %d", rec.Code)
	}
}

func TestResend_Conflicts(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	parent := a.parentWithSkips()
	path := "/api/campaigns/" + itoa(parent) + "/resend"

	if rec := a.do(http.MethodPost, path, `{"channel":"email"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("channel the parent did not use: want 422, got %d", rec.Code)
	}

	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() { defer wg.Done(); codes[i] = a.do(http.MethodPost, path, `{"channel":"sms"}`).Code }()
	}
	wg.Wait()
	if !(codes[0] == 201 && codes[1] == 409 || codes[0] == 409 && codes[1] == 201) {
		t.Errorf("two concurrent resends: want one 201 and one 409, got %v", codes)
	}

	a.exec(`UPDATE campaigns SET status = 'sending' WHERE id = $1`, parent)
	a.exec(`DELETE FROM campaigns WHERE parent_campaign_id = $1 AND status = 'draft'`, parent)
	if rec := a.do(http.MethodPost, path, `{"channel":"sms"}`); rec.Code != http.StatusConflict {
		t.Errorf("parent still sending: want 409, got %d", rec.Code)
	}

	a.exec(`UPDATE campaigns SET status = 'completed' WHERE id = $1`, parent)
	a.exec(`UPDATE messages SET status = 'sent', skip_reason = NULL WHERE campaign_id = $1`, parent)
	if rec := a.do(http.MethodPost, path, `{"channel":"sms"}`); rec.Code != http.StatusConflict {
		t.Errorf("no skipped recipients: want 409, got %d", rec.Code)
	}
}

func TestGenerate_ResendAsksForShorterContent(t *testing.T) {
	t.Parallel()
	model := &contentLLM{Mock: llm.NewMock(), outputs: []string{aiAnswer("tr", `{"sms":{"body":"Merhaba {{first_name}}!"}}`), aiAnswer("tr", validContent)}}
	a := newApp(t, model)
	parent := a.parentWithSkips()
	rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(parent)+"/resend", `{"channel":"sms"}`)
	resendID := int64(decode[map[string]any](t, rec)["id"].(float64))

	if evs := a.generate(resendID, 1); names(evs) != "delta,done" {
		t.Fatalf("resend content: %s", names(evs))
	}
	d := a.newDraft()
	if evs := a.generate(d.ID, 1); names(evs) != "delta,done" {
		t.Fatalf("normal content: %s", names(evs))
	}
	if !model.calls[0].Resend || model.calls[1].Resend {
		t.Fatalf("want Resend only for the resend draft, got %v / %v", model.calls[0].Resend, model.calls[1].Resend)
	}
}
