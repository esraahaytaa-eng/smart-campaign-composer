package campaign_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"smart-campaign-composer/internal/llm"
)

type listPage struct {
	Campaigns []struct {
		ID                int64   `json:"id"`
		RequestText       string  `json:"request_text"`
		Status            string  `json:"status"`
		ParentCampaignID  *int64  `json:"parent_campaign_id"`
		ParentRequestText *string `json:"parent_request_text"`
	} `json:"campaigns"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Counts   map[string]int `json:"counts"`
}

func (a *app) list(query string) (*listPage, int) {
	a.t.Helper()
	rec := a.do(http.MethodGet, "/api/campaigns?"+query, "")
	if rec.Code != http.StatusOK {
		return nil, rec.Code
	}
	p := decode[listPage](a.t, rec)
	return &p, rec.Code
}

// campaignsFixture inserts campaigns directly: n plain drafts plus a few
// named ones to search and filter for.
func (a *app) campaignsFixture(n int) {
	a.t.Helper()
	a.exec(`INSERT INTO campaigns (request_text, channels, rules, status, time_zone, created_by, created_at, updated_at)
		SELECT 'Draft number ' || i, '{sms}', '[]', 'draft', 'Europe/Istanbul', $1, $2::timestamptz + i * interval '1 minute', $2::timestamptz + i * interval '1 minute'
		FROM generate_series(1, $3) i`, a.marketerID, fixedNow.AddDate(0, 0, -30), n)
	a.exec(`INSERT INTO campaigns (request_text, channels, rules, status, time_zone, created_by, send_at, created_at, updated_at) VALUES
		('İSTANBUL sepet hatırlatması', '{sms}', '[]', 'completed', 'Europe/Istanbul', $1, NULL, $2, $2),
		('Ankara 100% indirim_kodu', '{email}', '[]', 'sending', 'Europe/Istanbul', $1, NULL, $2, $2),
		('Işıl için doğum günü', '{sms}', '[]', 'approved', 'Europe/Istanbul', $1, $3, $2, $2)`,
		a.marketerID, fixedNow, fixedNow.AddDate(0, 0, 2))
}

func TestList_PagesNewestFirstWithCounts(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	a.campaignsFixture(247) // 250 in all: more than the old 200-campaign limit

	p, _ := a.list("")
	if p.Total != 250 || p.Page != 1 || p.PageSize != 25 || len(p.Campaigns) != 25 {
		t.Fatalf("first page: total %d page %d size %d rows %d", p.Total, p.Page, p.PageSize, len(p.Campaigns))
	}
	if p.Campaigns[0].ID < p.Campaigns[1].ID {
		t.Fatal("newest first")
	}
	want := map[string]int{"all": 250, "draft": 247, "approved": 1, "sending": 1, "completed": 1, "cancelled": 0}
	if !reflect.DeepEqual(p.Counts, want) {
		t.Fatalf("counts over all campaigns: %v", p.Counts)
	}
	last, _ := a.list("page=10")
	if len(last.Campaigns) != 25 || last.Campaigns[24].RequestText != "Draft number 1" {
		t.Fatalf("page 10 ends with the oldest campaign, got %d rows", len(last.Campaigns))
	}
	past, _ := a.list("page=11")
	if len(past.Campaigns) != 0 || past.Total != 250 {
		t.Fatalf("past the end: %d rows, total %d", len(past.Campaigns), past.Total)
	}
}

func TestList_FilterAndSearch(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	a.campaignsFixture(3)

	for _, c := range []struct {
		query string
		want  []string
	}{
		{"status=completed", []string{"İSTANBUL sepet hatırlatması"}},
		{"q=istanbul", []string{"İSTANBUL sepet hatırlatması"}},           // Turkish İ folds
		{"q=ISIL", []string{"Işıl için doğum günü"}},                      // dotless ı and I
		{"q=SEPET+HATIRLATMASI", []string{"İSTANBUL sepet hatırlatması"}}, // case
		{"q=%25", []string{"Ankara 100% indirim_kodu"}},                   // % is literal
		{"q=_", []string{"Ankara 100% indirim_kodu"}},                     // _ is literal
		{"q=number+2&status=draft", []string{"Draft number 2"}},
		{"q=number&status=sending", nil},
		{"status=all&q=ankara", []string{"Ankara 100% indirim_kodu"}},
	} {
		p, code := a.list(c.query)
		var got []string
		for _, x := range p.Campaigns {
			got = append(got, x.RequestText)
		}
		if code != http.StatusOK || !reflect.DeepEqual(got, c.want) || p.Total != len(c.want) {
			t.Errorf("%s: %d %v (total %d)", c.query, code, got, p.Total)
		}
		if p.Counts["all"] != 6 {
			t.Errorf("%s: counts stay over all campaigns, got %v", c.query, p.Counts)
		}
	}
}

func TestList_ResendNamesItsOriginal(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	a.campaignsFixture(0)
	a.exec(`INSERT INTO campaigns (request_text, channels, status, time_zone, created_by, parent_campaign_id)
		SELECT request_text, '{sms}', 'draft', time_zone, created_by, id FROM campaigns WHERE request_text LIKE 'İSTANBUL%'`)

	p, _ := a.list("status=draft")
	if len(p.Campaigns) != 1 || p.Campaigns[0].ParentRequestText == nil || *p.Campaigns[0].ParentRequestText != "İSTANBUL sepet hatırlatması" {
		t.Fatalf("a resend carries its original's brief: %+v", p.Campaigns)
	}
}

func TestList_RejectsBadInput(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	for _, q := range []string{"status=lost", "page=0", "page=x", "page=10001", "q=" + strings.Repeat("a", 51)} {
		if _, code := a.list(q); code != http.StatusUnprocessableEntity {
			t.Errorf("%q: want 422, got %d", q, code)
		}
	}
}

// The dashboard's rows: what's live and what's waiting first (D54).
func TestDashboard_AttentionOrderAndCounts(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	a.exec(`INSERT INTO campaigns (request_text, channels, rules, status, time_zone, created_by, send_at, updated_at) VALUES
		('done old', '{sms}', '[]', 'completed', 'UTC', $1, NULL, $2::timestamptz - interval '3 days'),
		('done new', '{sms}', '[]', 'cancelled', 'UTC', $1, NULL, $2::timestamptz - interval '1 day'),
		('later', '{sms}', '[]', 'approved', 'UTC', $1, $2::timestamptz + interval '5 days', $2),
		('sooner', '{sms}', '[]', 'approved', 'UTC', $1, $2::timestamptz + interval '1 day', $2),
		('draft old', '{sms}', '[]', 'draft', 'UTC', $1, NULL, $2::timestamptz - interval '2 days'),
		('draft new', '{sms}', '[]', 'draft', 'UTC', $1, NULL, $2),
		('live', '{sms}', '[]', 'sending', 'UTC', $1, NULL, $2::timestamptz - interval '4 days')`, a.marketerID, fixedNow)

	rec := a.do(http.MethodGet, "/api/dashboard", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", rec.Code, rec.Body)
	}
	d := decode[listPage](t, rec)
	var got []string
	for _, c := range d.Campaigns {
		got = append(got, c.RequestText)
	}
	want := []string{"live", "draft new", "draft old", "sooner", "later", "done new"}
	if !reflect.DeepEqual(got, want) || d.Total != 7 || d.Counts["draft"] != 2 || d.Counts["all"] != 7 {
		t.Fatalf("got %v total %d counts %v", got, d.Total, d.Counts)
	}
}
