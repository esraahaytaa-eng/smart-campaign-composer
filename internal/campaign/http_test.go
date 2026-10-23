package campaign_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"smart-campaign-composer/internal/auth"
	"smart-campaign-composer/internal/campaign"
	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/dbtest"
	"smart-campaign-composer/internal/llm"
)

// 21 Oct 2026 12:00 UTC = 15:00 in Istanbul.
var fixedNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

const caseExample = "Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but did not buy anything."

type app struct {
	t          *testing.T
	pool       *pgxpool.Pool
	mux        *http.ServeMux
	cookie     *http.Cookie
	marketerID int64
	stopping   chan struct{} // closed to simulate a server shutdown
}

// failingLLM fails the test if the AI is called.
type failingLLM struct{ t *testing.T }

func (f failingLLM) InterpretRequest(context.Context, llm.InterpretInput) (json.RawMessage, error) {
	f.t.Fatal("the AI must not be called")
	return nil, nil
}

func (f failingLLM) StreamContent(context.Context, llm.ContentInput, func(string)) (json.RawMessage, error) {
	f.t.Fatal("the AI must not be called")
	return nil, nil
}

type garbageLLM struct{}

func (garbageLLM) StreamContent(context.Context, llm.ContentInput, func(string)) (json.RawMessage, error) {
	return json.RawMessage(`not json`), nil
}

func (garbageLLM) InterpretRequest(context.Context, llm.InterpretInput) (json.RawMessage, error) {
	return json.RawMessage(`not json`), nil
}

func newApp(t *testing.T, model llm.LLM) *app {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.FreshDB(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%v: %s", err, sql)
		}
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("password1"), bcrypt.MinCost)
	exec(`INSERT INTO marketers (email, name, password_hash) VALUES ('m@example.com', 'M', $1)`, string(hash))
	// İstanbul: carted 5 days ago, never ordered, both channels. Ankara: SMS only, carted 10 days ago.
	exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at) VALUES
		(1, 'A', 'a@example.com', '+905000000001', 'İstanbul', 'tr', true, true, $1),
		(2, 'B', 'b@example.com', '+905000000002', 'Ankara', 'tr', true, false, $1)`, fixedNow.AddDate(-1, 0, 0))
	exec(`INSERT INTO cart_items (user_id, category, quantity, unit_price, added_at) VALUES
		(1, 'books', 1, 50, $1), (2, 'books', 1, 50, $2)`, fixedNow.AddDate(0, 0, -5), fixedNow.AddDate(0, 0, -10))

	mux := http.NewServeMux()
	api := auth.Routes(mux, pool, "mock")
	stopping := make(chan struct{})
	h := &campaign.Handler{Pool: pool, LLM: model, Timeout: 5 * time.Second, Now: func() time.Time { return fixedNow },
		EventsInterval: 20 * time.Millisecond, Stopping: stopping}
	h.Routes(api)

	a := &app{t: t, pool: pool, mux: mux, stopping: stopping}
	rec := a.do(http.MethodPost, "/api/login", `{"email":"m@example.com","password":"password1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d", rec.Code)
	}
	a.cookie = rec.Result().Cookies()[0]
	a.marketerID = int64(a.count(`SELECT id FROM marketers`))
	return a
}

func (a *app) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if a.cookie != nil {
		req.AddCookie(a.cookie)
	}
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	return rec
}

func (a *app) count(sql string, args ...any) int {
	a.t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}

type draft struct {
	ID           int64             `json:"id"`
	RequestText  string            `json:"request_text"`
	Channels     []string          `json:"channels"`
	Rules        []json.RawMessage `json:"rules"`
	Status       string            `json:"status"`
	Version      int               `json:"version"`
	SendAt       *time.Time        `json:"send_at"`
	ScheduleNote *string           `json:"schedule_note"`
	TimeZone     string            `json:"time_zone"`
	CreatedBy    int64             `json:"created_by"`
	Counts       *struct {
		Total, SMS, Email int
	} `json:"counts"`
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	return v
}

func body(fields map[string]any) string {
	b, _ := json.Marshal(fields)
	return string(b)
}

func (a *app) create(fields map[string]any) (*httptest.ResponseRecorder, draft) {
	a.t.Helper()
	rec := a.do(http.MethodPost, "/api/campaigns", body(fields))
	if rec.Code != http.StatusCreated {
		return rec, draft{}
	}
	return rec, decode[draft](a.t, rec)
}

func TestCreate_FromBrief_DraftWithRulesAndCounts(t *testing.T) {
	a := newApp(t, llm.NewMock())

	rec, d := a.create(map[string]any{"request": caseExample, "channels": []string{"sms", "email"}, "time_zone": "Europe/Istanbul"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d %s", rec.Code, rec.Body)
	}
	if d.Status != "draft" || d.Version != 1 || len(d.Rules) != 3 || d.CreatedBy != a.marketerID || d.TimeZone != "Europe/Istanbul" {
		t.Fatalf("unexpected draft: %+v", d)
	}
	if !strings.Contains(string(d.Rules[0]), "İstanbul") {
		t.Fatalf("rules not normalized: %s", d.Rules[0])
	}
	if d.Counts == nil || d.Counts.Total != 1 || d.Counts.SMS != 1 || d.Counts.Email != 1 {
		t.Fatalf("counts: %+v", d.Counts)
	}
	if n := a.count(`SELECT count(*) FROM campaigns WHERE id = $1 AND created_by = $2 AND status = 'draft'`, d.ID, a.marketerID); n != 1 {
		t.Fatal("draft not stored with created_by")
	}
}

func TestCreate_ScheduleSuggestionIsOnlyAProposal(t *testing.T) {
	a := newApp(t, llm.NewMock())

	rec, d := a.create(map[string]any{
		"request":   "Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00.",
		"channels":  []string{"sms"},
		"time_zone": "Europe/Istanbul",
	})

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d %s", rec.Code, rec.Body)
	}
	if d.SendAt == nil || !d.SendAt.Equal(time.Date(2026, 10, 22, 7, 0, 0, 0, time.UTC)) || d.ScheduleNote == nil {
		t.Fatalf("want proposed send_at 2026-10-22T07:00Z with note, got %v / %v", d.SendAt, d.ScheduleNote)
	}
	if d.Status != "draft" || a.count(`SELECT count(*) FROM messages`) != 0 {
		t.Fatal("a suggestion must not schedule anything")
	}
}

func TestCreate_Unsupported_422NothingSaved(t *testing.T) {
	a := newApp(t, llm.NewMock())

	rec, _ := a.create(map[string]any{"request": "Send something nice to our best customers", "channels": []string{"sms"}, "time_zone": "Europe/Istanbul"})

	got := decode[map[string]any](t, rec)
	if rec.Code != http.StatusUnprocessableEntity || got["unsupported"] != true || got["error"] != llm.MockUnsupportedReason {
		t.Fatalf("want 422 unsupported with reason, got %d %s", rec.Code, rec.Body)
	}
	if a.count(`SELECT count(*) FROM campaigns`) != 0 {
		t.Fatal("unsupported brief created a campaign")
	}
}

func TestCreate_InvalidInput_NothingSavedNoAICall(t *testing.T) {
	a := newApp(t, failingLLM{t})
	ok := map[string]any{"request": caseExample, "channels": []string{"sms"}, "time_zone": "Europe/Istanbul"}
	with := func(k string, v any) string {
		m := map[string]any{}
		for kk, vv := range ok {
			m[kk] = vv
		}
		m[k] = v
		return body(m)
	}

	cases := []struct {
		name, body string
		want       int
	}{
		{"empty channels", with("channels", []string{}), 422},
		{"unknown channel", with("channels", []string{"fax"}), 422},
		{"duplicate channel", with("channels", []string{"sms", "sms"}), 422},
		{"invalid zone", with("time_zone", "Mars/Olympus"), 422},
		{"empty zone", with("time_zone", ""), 422},
		{"Local zone", with("time_zone", "Local"), 422},
		{"empty request", with("request", "  "), 422},
		{"request too long", with("request", strings.Repeat("ş", 1001)), 422},
		{"malformed JSON", `{"request":`, 400},
		{"unknown field", with("sql", "DROP TABLE users"), 400},
	}
	for _, tc := range cases {
		if rec := a.do(http.MethodPost, "/api/campaigns", tc.body); rec.Code != tc.want {
			t.Errorf("%s: want %d, got %d %s", tc.name, tc.want, rec.Code, rec.Body)
		}
	}
	if a.count(`SELECT count(*) FROM campaigns`) != 0 {
		t.Fatal("invalid input created a campaign")
	}
}

// D15: the marketer can build the rules by hand when the AI can't help.
func TestCreate_ManualRulesSkipAI(t *testing.T) {
	a := newApp(t, failingLLM{t})

	rec, d := a.create(map[string]any{
		"request": "Weekend sale for Ankara", "channels": []string{"sms"}, "time_zone": "Europe/Istanbul",
		"rules": []map[string]any{{"field": "city", "op": "in", "value": []string{"ankara"}}},
	})
	if rec.Code != http.StatusCreated || len(d.Rules) != 1 || d.Counts.SMS != 1 || d.SendAt != nil {
		t.Fatalf("want 201 with manual rules, got %d %s", rec.Code, rec.Body)
	}

	rec, _ = a.create(map[string]any{
		"request": "Weekend sale", "channels": []string{"sms"}, "time_zone": "Europe/Istanbul",
		"rules": []map[string]any{{"field": "city", "op": "in", "value": []string{"Paris"}}},
	})
	got := decode[map[string]any](t, rec)
	if rec.Code != http.StatusUnprocessableEntity || got["field_errors"] == nil {
		t.Fatalf("invalid manual rules: want 422 with field_errors, got %d %s", rec.Code, rec.Body)
	}
}

func TestCreate_AIInvalidTwice_502NothingSaved(t *testing.T) {
	a := newApp(t, garbageLLM{})

	rec, _ := a.create(map[string]any{"request": caseExample, "channels": []string{"sms"}, "time_zone": "Europe/Istanbul"})

	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "not json") {
		t.Fatalf("want 502 without leaking the raw answer, got %d %s", rec.Code, rec.Body)
	}
	if a.count(`SELECT count(*) FROM campaigns`) != 0 {
		t.Fatal("invalid AI answer created a campaign")
	}
}

func (a *app) newDraft() draft {
	a.t.Helper()
	rec, d := a.create(map[string]any{"request": caseExample, "channels": []string{"sms", "email"}, "time_zone": "Europe/Istanbul"})
	if rec.Code != http.StatusCreated {
		a.t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	return d
}

func TestUpdateRules(t *testing.T) {
	a := newApp(t, llm.NewMock())
	d := a.newDraft()
	path := "/api/campaigns/" + itoa(d.ID) + "/rules"
	ankara := `[{"field":"city","op":"in","value":["Ankara"]}]`

	rec := a.do(http.MethodPut, path, `{"version":1,"rules":`+ankara+`}`)
	got := decode[draft](t, rec)
	if rec.Code != http.StatusOK || got.Version != 2 || got.Counts.SMS != 1 || got.Counts.Email != 0 {
		t.Fatalf("want 200 v2 with recount, got %d %s", rec.Code, rec.Body)
	}

	if rec := a.do(http.MethodPut, path, `{"version":1,"rules":`+ankara+`}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale version: want 409, got %d", rec.Code)
	}

	rec = a.do(http.MethodPut, path, `{"version":2,"rules":[{"field":"age","op":"gte","value":30}]}`)
	errs := decode[map[string]any](t, rec)
	if rec.Code != http.StatusUnprocessableEntity || errs["field_errors"] == nil {
		t.Fatalf("invalid rules: want 422 with field_errors, got %d %s", rec.Code, rec.Body)
	}
	if rec := a.do(http.MethodPut, path, `{"version":2,"rules":[]}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty rules: want 422, got %d", rec.Code)
	}
	if v := a.count(`SELECT version FROM campaigns WHERE id = $1`, d.ID); v != 2 {
		t.Fatalf("rejected edits changed the version: %d", v)
	}
}

func TestUpdateSchedule(t *testing.T) {
	a := newApp(t, llm.NewMock())
	d := a.newDraft()
	path := "/api/campaigns/" + itoa(d.ID) + "/schedule"

	rec := a.do(http.MethodPut, path, `{"version":1,"send_at":"2026-10-25T09:00:00+03:00"}`)
	got := decode[draft](t, rec)
	if rec.Code != http.StatusOK || got.Version != 2 || got.SendAt == nil || !got.SendAt.Equal(time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("want 200 v2 with send_at, got %d %s", rec.Code, rec.Body)
	}

	rec = a.do(http.MethodPut, path, `{"version":2,"send_at":null}`)
	if got := decode[draft](t, rec); rec.Code != http.StatusOK || got.SendAt != nil || got.ScheduleNote != nil {
		t.Fatalf("clear: want 200 with no send_at or note, got %d %s", rec.Code, rec.Body)
	}

	for name, at := range map[string]string{
		"past":         `"2026-10-21T11:00:00Z"`,
		"over 30 days": `"2026-11-21T12:00:01Z"`,
		"no offset":    `"2026-10-25T09:00:00"`,
		"not a time":   `"tomorrow"`,
	} {
		if rec := a.do(http.MethodPut, path, `{"version":3,"send_at":`+at+`}`); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: want 422, got %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := a.do(http.MethodPut, path, `{"version":1,"send_at":null}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale version: want 409, got %d", rec.Code)
	}
}

func TestEdit_NonDraft_409(t *testing.T) {
	a := newApp(t, llm.NewMock())
	d := a.newDraft()
	if _, err := a.pool.Exec(context.Background(), `UPDATE campaigns SET status = 'approved' WHERE id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	id := itoa(d.ID)

	if rec := a.do(http.MethodPut, "/api/campaigns/"+id+"/rules", `{"version":1,"rules":[{"field":"city","op":"in","value":["Ankara"]}]}`); rec.Code != http.StatusConflict {
		t.Errorf("rules on approved: want 409, got %d", rec.Code)
	}
	if rec := a.do(http.MethodPut, "/api/campaigns/"+id+"/schedule", `{"version":1,"send_at":null}`); rec.Code != http.StatusConflict {
		t.Errorf("schedule on approved: want 409, got %d", rec.Code)
	}
}

func TestGet(t *testing.T) {
	a := newApp(t, llm.NewMock())
	d := a.newDraft()

	rec := a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID), "")
	if got := decode[draft](t, rec); rec.Code != http.StatusOK || got.ID != d.ID || got.Counts == nil || got.Counts.Total != 1 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	rec = a.do(http.MethodGet, "/api/campaigns", "")
	list := decode[[]draft](t, rec)
	if rec.Code != http.StatusOK || len(list) != 1 || list[0].ID != d.ID {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	for _, path := range []string{"/api/campaigns/999", "/api/campaigns/abc", "/api/campaigns/999/rules"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/rules") {
			method = http.MethodPut
		}
		if rec := a.do(method, path, `{"version":1,"rules":[{"field":"city","op":"in","value":["Ankara"]}]}`); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: want 404, got %d", method, path, rec.Code)
		}
	}
}

func TestCampaigns_RequireLogin(t *testing.T) {
	a := newApp(t, llm.NewMock())
	a.cookie = nil

	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/campaigns"}, {http.MethodPost, "/api/campaigns"}, {http.MethodGet, "/api/campaigns/1"},
		{http.MethodGet, "/api/campaigns/1/recipients?group=sent"},
	} {
		if rec := a.do(r.method, r.path, "{}"); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: want 401, got %d", r.method, r.path, rec.Code)
		}
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
