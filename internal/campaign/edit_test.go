package campaign_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"smart-campaign-composer/internal/llm"
)

func (a *app) putChannels(id int64, body string) (int, variantDraft) {
	a.t.Helper()
	rec := a.do(http.MethodPut, "/api/campaigns/"+itoa(id)+"/channels", body)
	if rec.Code != http.StatusOK {
		return rec.Code, variantDraft{}
	}
	return rec.Code, decode[variantDraft](a.t, rec)
}

// D67: a removed channel's text is kept, left out of approval and counts.
func TestChannels_RemoveKeepsText(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft() // sms + email with Turkish content, version 2
	// invalid text on the channel being removed must not block approval
	a.exec(`UPDATE campaign_content SET body = 'Hi {{email}}' WHERE campaign_id = $1 AND channel = 'email'`, d.ID)

	code, got := a.putChannels(d.ID, `{"version":2,"channels":["sms"]}`)

	if code != http.StatusOK || len(got.Channels) != 1 || got.Channels[0] != "sms" || got.Version != 3 {
		t.Fatalf("want sms only at version 3, got %d %+v", code, got.draft)
	}
	if got.Content["tr"]["sms"] == nil || got.Content["tr"]["email"]["body"] != "Hi {{email}}" {
		t.Fatalf("want both channels' text kept, got %v", got.Content)
	}
	if got.Counts == nil || got.Counts.SMS != 1 || got.Counts.Email != 0 || got.Counts.Total != 1 {
		t.Fatalf("want counts for sms only, got %+v", got.Counts)
	}
	if code, body := a.approve(d.ID, `{"version":3,"send":"now"}`); code != http.StatusOK {
		t.Fatalf("the chosen channel's content must be approvable, got %d %v", code, body)
	}
}

func TestChannels_ReAddRestoresText(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	before := a.get(d.ID).Content
	a.putChannels(d.ID, `{"version":2,"channels":["sms"]}`)

	code, got := a.putChannels(d.ID, `{"version":3,"channels":["sms","email"]}`)

	if code != http.StatusOK || len(got.Channels) != 2 || !reflect.DeepEqual(got.Content, before) || got.Counts.Email != 1 {
		t.Fatalf("want both channels with the email text back as it was, got %d %v", code, got.Content)
	}
}

func TestChannels_AddedOneStartsWithoutContent(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	_, d := a.create(map[string]any{"request": caseExample, "channels": []string{"sms"}, "time_zone": "Europe/Istanbul"})
	a.putContent(d.ID, `{"version":1,"default_language":"tr","content":{"tr":{"sms":{"body":"Merhaba!"}}}}`)

	code, got := a.putChannels(d.ID, `{"version":2,"channels":["sms","email"]}`)

	if code != http.StatusOK || len(got.Channels) != 2 || got.Content["tr"]["email"] != nil || got.Counts.Email != 1 {
		t.Fatalf("want both channels, no email content yet, got %d %+v %v", code, got.draft, got.Content)
	}
	if code, _ := a.approve(d.ID, `{"version":3,"send":"now"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("approval must wait for the added channel's content, got %d", code)
	}
}

func TestChannels_Rejects(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	approved := a.readyDraft()
	a.approve(approved.ID, `{"version":2,"send":"now"}`)
	parent := a.parentWithSkips()
	resend := decode[draft](t, a.do(http.MethodPost, "/api/campaigns/"+itoa(parent)+"/resend", `{"channel":"sms"}`))

	for _, tc := range []struct {
		name string
		id   int64
		body string
		want int
	}{
		{"no channels", d.ID, `{"version":2,"channels":[]}`, http.StatusUnprocessableEntity},
		{"unknown channel", d.ID, `{"version":2,"channels":["fax"]}`, http.StatusUnprocessableEntity},
		{"listed twice", d.ID, `{"version":2,"channels":["sms","sms"]}`, http.StatusUnprocessableEntity},
		{"no version", d.ID, `{"channels":["sms"]}`, http.StatusUnprocessableEntity},
		{"stale version", d.ID, `{"version":1,"channels":["sms"]}`, http.StatusConflict},
		{"approved", approved.ID, `{"version":3,"channels":["sms"]}`, http.StatusConflict},
		{"resend draft", resend.ID, `{"version":1,"channels":["email"]}`, http.StatusConflict},
		{"unknown", 999, `{"version":1,"channels":["sms"]}`, http.StatusNotFound},
	} {
		if code, _ := a.putChannels(tc.id, tc.body); code != tc.want {
			t.Errorf("%s: want %d, got %d", tc.name, tc.want, code)
		}
	}
	got := a.get(d.ID)
	if got.Version != 2 || len(got.Channels) != 2 || got.Content["tr"]["email"] == nil {
		t.Fatalf("a rejected change altered the draft: %+v %v", got.draft, got.Content)
	}
	if a.count(`SELECT count(*) FROM campaign_content WHERE campaign_id = $1`, approved.ID) != 2 {
		t.Fatal("the approved campaign's content changed")
	}
}

func TestCatalog(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())

	rec := a.do(http.MethodGet, "/api/catalog", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", rec.Code, rec.Body)
	}
	got := decode[struct {
		Fields []struct {
			Field string   `json:"field"`
			Ops   []string `json:"ops"`
			Value string   `json:"value"`
		} `json:"fields"`
		Cities     []string       `json:"cities"`
		Categories []string       `json:"categories"`
		Limits     map[string]any `json:"limits"`
	}](t, rec)
	if len(got.Fields) != 10 || got.Fields[0].Field != "cart_added" || got.Fields[0].Value != "window" {
		t.Fatalf("want the 10 fields sorted by name, got %+v", got.Fields)
	}
	if strings.Join(got.Cities, ",") != "Ankara,İstanbul" || !slices.Contains(got.Categories, "books") {
		t.Fatalf("want cities and categories from the database, got %v %v", got.Cities, got.Categories)
	}
	if got.Limits["max_rules"] != float64(20) || got.Limits["max_days"] != float64(365) || got.Limits["min_date"] != "2000-01-01" {
		t.Fatalf("limits: %v", got.Limits)
	}

	a.cookie = nil
	if rec := a.do(http.MethodGet, "/api/catalog", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without login: want 401, got %d", rec.Code)
	}
}

const ankaraBrief = "Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00."

// hookLLM is the mock with a hook that runs while the AI is reading a brief.
type hookLLM struct {
	*llm.Mock
	during func()
	calls  int
}

func (h *hookLLM) InterpretRequest(ctx context.Context, in llm.InterpretInput) (json.RawMessage, error) {
	h.calls++
	if h.during != nil {
		h.during()
	}
	return h.Mock.InterpretRequest(ctx, in)
}

func (a *app) putRequest(id int64, body string) (int, map[string]any) {
	a.t.Helper()
	rec := a.do(http.MethodPut, "/api/campaigns/"+itoa(id)+"/request", body)
	return rec.Code, decode[map[string]any](a.t, rec)
}

func requestBody(text string, version int) string {
	return body(map[string]any{"request": text, "version": version})
}

func TestRequest_ReinterpretsAndKeepsContent(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft() // the case example: İstanbul cart abandoners, no time, content, version 2
	before := decode[map[string]any](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID), ""))

	code, got := a.putRequest(d.ID, requestBody(ankaraBrief, 2))

	if code != http.StatusOK || got["request_text"] != ankaraBrief || got["version"] != float64(3) || got["status"] != "draft" {
		t.Fatalf("want the new brief at version 3, got %d %v", code, got)
	}
	rules, _ := json.Marshal(got["rules"])
	if string(rules) != `[{"field":"city","op":"in","value":["Ankara"]}]` {
		t.Fatalf("want the new rules replacing the old ones, got %s", rules)
	}
	if got["send_at"] == nil || got["schedule_note"] == nil {
		t.Fatalf("want the brief's send time proposed, got %v %v", got["send_at"], got["schedule_note"])
	}
	counts := got["counts"].(map[string]any)
	if counts["total"] != float64(1) || counts["sms"] != float64(1) || counts["email"] != float64(0) {
		t.Fatalf("want counts for the new rules (user 2 in Ankara, SMS only), got %v", counts)
	}
	if !reflect.DeepEqual(got["content"], before["content"]) {
		t.Fatalf("the content must be kept as it was:\n got %v\nwant %v", got["content"], before["content"])
	}
}

func TestRequest_BriefWithoutTimeClearsTheProposal(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	_, d := a.create(map[string]any{"request": ankaraBrief, "channels": []string{"sms"}, "time_zone": "Europe/Istanbul"})
	if d.SendAt == nil {
		t.Fatal("fixture: want a proposed time")
	}

	code, got := a.putRequest(d.ID, requestBody(caseExample, 1))

	if code != http.StatusOK || got["send_at"] != nil || got["schedule_note"] != nil {
		t.Fatalf("want the old proposal cleared, got %d %v %v", code, got["send_at"], got["schedule_note"])
	}
}

// A failed or unsupported interpretation leaves the draft as it was (D15).
func TestRequest_FailuresLeaveTheDraftUnchanged(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		model llm.LLM
		brief string
		want  int
	}{
		{"unsupported", llm.NewMock(), "Send a birthday note to users older than 40.", http.StatusUnprocessableEntity},
		{"invalid twice", garbageLLM{}, ankaraBrief, http.StatusBadGateway},
		{"empty brief", llm.NewMock(), "   ", http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(t, tc.model)
			_, d := a.create(map[string]any{"request": "manual", "channels": []string{"sms"}, "time_zone": "Europe/Istanbul",
				"rules": []map[string]any{{"field": "city", "op": "in", "value": []string{"Ankara"}}}})

			if code, _ := a.putRequest(d.ID, requestBody(tc.brief, 1)); code != tc.want {
				t.Fatalf("want %d, got %d", tc.want, code)
			}
			got := decode[draft](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID), ""))
			if got.Version != 1 || got.RequestText != "manual" || len(got.Rules) != 1 {
				t.Fatalf("the draft changed: %+v", got)
			}
		})
	}
}

// Stale, approved and resend drafts are refused before the AI is called.
func TestRequest_RejectsWithoutCallingTheAI(t *testing.T) {
	t.Parallel()
	model := &hookLLM{Mock: llm.NewMock()}
	a := newApp(t, model)
	d := a.readyDraft()
	approved := a.readyDraft()
	a.approve(approved.ID, `{"version":2,"send":"now"}`)
	parent := a.parentWithSkips()
	resend := decode[draft](t, a.do(http.MethodPost, "/api/campaigns/"+itoa(parent)+"/resend", `{"channel":"sms"}`))
	model.calls = 0

	for _, tc := range []struct {
		name string
		id   int64
		body string
		want int
	}{
		{"stale version", d.ID, requestBody(ankaraBrief, 1), http.StatusConflict},
		{"no version", d.ID, `{"request":"x"}`, http.StatusUnprocessableEntity},
		{"approved", approved.ID, requestBody(ankaraBrief, 3), http.StatusConflict},
		{"resend draft", resend.ID, requestBody(ankaraBrief, 1), http.StatusConflict},
		{"unknown", 999, requestBody(ankaraBrief, 1), http.StatusNotFound},
	} {
		if code, _ := a.putRequest(tc.id, tc.body); code != tc.want {
			t.Errorf("%s: want %d, got %d", tc.name, tc.want, code)
		}
	}
	if model.calls != 0 {
		t.Fatalf("the AI was called %d times for refused requests", model.calls)
	}
}

// Another edit lands while the AI is reading the brief: the stale answer is
// not saved over it.
func TestRequest_EditDuringInterpretation_409(t *testing.T) {
	t.Parallel()
	model := &hookLLM{Mock: llm.NewMock()}
	a := newApp(t, model)
	d := a.readyDraft()
	model.during = func() {
		a.putChannels(d.ID, `{"version":2,"channels":["sms"]}`)
	}

	if code, _ := a.putRequest(d.ID, requestBody(ankaraBrief, 2)); code != http.StatusConflict {
		t.Fatalf("want 409, got %d", code)
	}
	got := decode[draft](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID), ""))
	if got.Version != 3 || got.RequestText != caseExample {
		t.Fatalf("want the concurrent edit kept and the brief unchanged, got %+v", got)
	}
}

// Approval commits after a channel change has passed its draft check but
// before its update runs: the approved campaign must keep its content. The
// test holds the row lock to put the two in exactly that order.
func TestChannels_ApprovedMeanwhile_KeepsContent(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM campaigns WHERE id = $1 FOR UPDATE`, d.ID); err != nil {
		t.Fatal(err)
	}

	result := make(chan int, 1)
	go func() { code, _ := a.putChannels(d.ID, `{"version":2,"channels":["sms"]}`); result <- code }()
	waitForLockWait(t, a)
	// What approve commits: status and a new version.
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = 'approved', version = 3 WHERE id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if code := <-result; code != http.StatusConflict {
		t.Fatalf("want 409, got %d", code)
	}
	if n := a.count(`SELECT count(*) FROM campaign_content WHERE campaign_id = $1`, d.ID); n != 2 {
		t.Fatalf("the approved campaign lost content: %d rows left", n)
	}
	if got := decode[draft](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID), "")); len(got.Channels) != 2 {
		t.Fatalf("the approved campaign's channels changed: %v", got.Channels)
	}
}

// waitForLockWait returns once another session of the test database is
// blocked on a row lock.
func waitForLockWait(t *testing.T, a *app) {
	t.Helper()
	for give := time.Now().Add(10 * time.Second); a.count(`SELECT count(*) FROM pg_stat_activity
		WHERE datname = current_database() AND wait_event_type = 'Lock'`) == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(give) {
			t.Fatal("the request never blocked on the row lock")
		}
	}
}
