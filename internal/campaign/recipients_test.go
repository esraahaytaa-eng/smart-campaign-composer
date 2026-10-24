package campaign_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"smart-campaign-composer/internal/llm"
)

type recipientsPage struct {
	AsOf      string                    `json:"as_of"`
	Groups    map[string]map[string]int `json:"groups"`
	Reasons   map[string]int            `json:"reasons"`
	Total     int                       `json:"total"`
	Page      int                       `json:"page"`
	PageSize  int                       `json:"page_size"`
	Recipient []struct {
		ID            int64          `json:"id"`
		Name          string         `json:"name"`
		Contact       string         `json:"contact"`
		Channel       string         `json:"channel"`
		Language      string         `json:"language"`
		Group         string         `json:"group"`
		Reason        *string        `json:"reason"`
		Detail        map[string]any `json:"detail"`
		NextAttemptAt *string        `json:"next_attempt_at"`
		Attempts      []struct {
			AttemptNo  int     `json:"attempt_no"`
			StartedAt  string  `json:"started_at"`
			Outcome    *string `json:"outcome"`
			HTTPStatus *int    `json:"http_status"`
		} `json:"attempts"`
	} `json:"recipients"`
}

// recipientsCampaign is sendingCampaign with names to search for: a failed
// pair, a skip over the SMS limit (8 + 133 + 24 = 165 characters) and a name
// with LIKE wildcards in it.
func (a *app) recipientsCampaign() int64 {
	a.t.Helper()
	id := a.sendingCampaign()
	a.exec(`UPDATE users u SET first_name = v.f, last_name = v.l FROM (VALUES
		(1, 'Ayşe', 'Demir'), (2, 'Işıl', 'Kaya'), (3, 'Mert', '100%_Ok'), (4, repeat('ş', 133), NULL),
		(5, 'Ece', 'Koç'), (6, 'Tolga', 'Kurt'), (7, 'Okan', 'Demir')) v(id, f, l) WHERE u.id = v.id`)
	return id
}

func (a *app) recipients(id int64, query string) (*recipientsPage, int) {
	a.t.Helper()
	rec := a.do(http.MethodGet, "/api/campaigns/"+itoa(id)+"/recipients?"+query, "")
	if rec.Code != http.StatusOK {
		return nil, rec.Code
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		a.t.Fatalf("customer data must not be cached, Cache-Control %q", cc)
	}
	p := decode[recipientsPage](a.t, rec)
	return &p, rec.Code
}

func recipientNames(p *recipientsPage) []string {
	var out []string
	for _, r := range p.Recipient {
		out = append(out, r.Name)
	}
	return out
}

func TestRecipients_FailedWithReasonsAndAttempts(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	id := a.recipientsCampaign()

	p, _ := a.recipients(id, "group=failed&channel=sms")

	if p.Total != 2 || p.Page != 1 || p.PageSize != 50 || p.AsOf == "" {
		t.Fatalf("page: %+v", p)
	}
	// Same attempt time: the newer message first.
	if got := recipientNames(p); !reflect.DeepEqual(got, []string{"Mert 100%_Ok", "Işıl Kaya"}) {
		t.Fatalf("names: %v", got)
	}
	if !reflect.DeepEqual(p.Reasons, map[string]int{"provider_rejected": 2}) {
		t.Fatalf("reasons: %v", p.Reasons)
	}
	if p.Groups["sms"]["failed"] != 2 || p.Groups["sms"]["sent"] != 1 || p.Groups["email"]["pending"] != 1 {
		t.Fatalf("group counts per channel: %v", p.Groups)
	}
	r := p.Recipient[0]
	if r.Channel != "sms" || r.Group != "failed" || *r.Reason != "provider_rejected" || r.NextAttemptAt != nil ||
		len(r.Attempts) != 1 || *r.Attempts[0].Outcome != "rejected" || *r.Attempts[0].HTTPStatus != 400 || r.Attempts[0].AttemptNo != 1 {
		t.Fatalf("row: %+v", r)
	}
}

func TestRecipients_SkipDetailAndRetry(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	id := a.recipientsCampaign()

	p, _ := a.recipients(id, "group=skipped")
	if p.Total != 2 || p.Reasons["character_limit_exceeded"] != 1 || p.Reasons["no_consent"] != 1 {
		t.Fatalf("skipped: %+v", p)
	}
	for _, r := range p.Recipient {
		switch *r.Reason {
		case "character_limit_exceeded":
			want := map[string]any{"part": "sms", "length": float64(165), "limit": float64(160)}
			if !reflect.DeepEqual(r.Detail, want) {
				t.Fatalf("detail: %v", r.Detail)
			}
		case "no_consent":
			if r.Detail != nil || len(r.Attempts) != 0 {
				t.Fatalf("a consent skip has no detail or attempts: %+v", r)
			}
		}
	}

	p, _ = a.recipients(id, "group=retrying")
	if p.Total != 1 || *p.Recipient[0].Reason != "server_error" || p.Recipient[0].NextAttemptAt == nil {
		t.Fatalf("retrying: %+v", p.Recipient)
	}
}

func TestRecipients_NameSearch(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	id := a.recipientsCampaign()

	for _, c := range []struct {
		query string
		want  []string
	}{
		{"group=sent&q=AYSE", []string{"Ayşe Demir"}},  // Turkish letters fold
		{"group=failed&q=isil", []string{"Işıl Kaya"}}, // dotless ı and I
		{"group=failed&q=%25", []string{"Mert 100%_Ok"}},
		{"group=failed&q=_", []string{"Mert 100%_Ok"}},
		{"group=failed&q=l+k", []string{"Işıl Kaya"}}, // across first and last name
		{"group=sent&q=+demir+", []string{"Ayşe Demir"}},
		{"group=sent&q=kaya", nil},
	} {
		p, code := a.recipients(id, c.query)
		if code != http.StatusOK || !reflect.DeepEqual(recipientNames(p), c.want) || p.Total != len(c.want) {
			t.Errorf("%s: %d %v", c.query, code, p)
		}
	}
}

func TestRecipients_PagesSortedByLastAttempt(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	id := a.recipientsCampaign()
	a.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		SELECT i, 'P' || i, 'p' || i || '@example.com', '+9050000' || lpad(i::text, 5, '0'), 'Ankara', 'tr', true, true, $1
		FROM generate_series(100, 219) i`, fixedNow)
	a.exec(`INSERT INTO messages (campaign_id, user_id, channel, status) SELECT $1, i, 'sms', 'sent' FROM generate_series(100, 219) i`, id)
	a.exec(`INSERT INTO delivery_attempts (message_id, attempt_no, started_at, outcome, http_status)
		SELECT m.id, 1, $2::timestamptz - (m.user_id - 99) * interval '1 second', 'accepted', 200
		FROM messages m WHERE m.campaign_id = $1 AND m.user_id >= 100`, id, fixedNow)

	p1, _ := a.recipients(id, "group=sent&channel=sms")
	p3, _ := a.recipients(id, "group=sent&channel=sms&page=3")
	p4, _ := a.recipients(id, "group=sent&channel=sms&page=4")

	if p1.Total != 121 || len(p1.Recipient) != 50 || p1.Recipient[0].Name != "Ayşe Demir" || p1.Recipient[1].Name != "P100" {
		t.Fatalf("page 1: total %d, %v", p1.Total, recipientNames(p1)[:2])
	}
	if len(p3.Recipient) != 21 || p3.Recipient[20].Name != "P219" || p3.Page != 3 {
		t.Fatalf("page 3: %v", recipientNames(p3))
	}
	if len(p4.Recipient) != 0 || p4.Total != 121 {
		t.Fatalf("past the end: %+v", p4)
	}
}

// No response may carry a full phone number or email, the city, or the
// provider's error text (D47).
func TestRecipients_MasksContacts(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	id := a.recipientsCampaign()
	a.exec(`INSERT INTO messages (campaign_id, user_id, channel, status) VALUES ($1, 2, 'email', 'pending'), ($1, 3, 'email', 'pending')`, id)

	var bodies string
	for _, g := range []string{"sent", "failed", "skipped", "retrying", "pending"} {
		rec := a.do(http.MethodGet, "/api/campaigns/"+itoa(id)+"/recipients?group="+g, "")
		bodies += rec.Body.String()
	}
	for _, secret := range []string{"+90500000000", "a@example.com", "u3@example.com", "İstanbul", "HTTP 400", "HTTP 500"} {
		if strings.Contains(bodies, secret) {
			t.Errorf("response contains %q", secret)
		}
	}
	p, _ := a.recipients(id, "group=sent&channel=sms")
	if c := p.Recipient[0].Contact; c != "+90 500 ••• •• 01" {
		t.Errorf("phone: %q", c)
	}
	p, _ = a.recipients(id, "group=pending&channel=email")
	got := map[string]string{}
	for _, r := range p.Recipient {
		got[r.Name] = r.Contact
	}
	want := map[string]string{"Ayşe Demir": "•••@example.com", "Işıl Kaya": "•••@example.com", "Mert 100%_Ok": "u•••@example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("emails: %v", got)
	}
}

// D64: each row says which language's text the recipient got (their stored
// language, else the default for messages from before T20), the list can be
// filtered by it, and the over-limit detail measures that language's text.
func TestRecipients_IncludeLanguage(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	id := a.recipientsCampaign()
	// English text 3 + 133 + 2 + 30 = 168 characters for user 4; Turkish is 165.
	a.exec(`INSERT INTO campaign_languages (campaign_id, language, approved_at, approved_by) VALUES ($1, 'en', now(), 1)`, id)
	a.exec(`INSERT INTO campaign_content (campaign_id, channel, language, body) VALUES ($1, 'sms', 'en', $2)`, id, "Hi {{first_name}}, "+strings.Repeat("x", 30))
	a.exec(`UPDATE messages SET language = 'en' WHERE campaign_id = $1 AND user_id IN (2, 4)`, id)
	a.exec(`UPDATE messages SET language = 'tr' WHERE campaign_id = $1 AND user_id = 3`, id)

	p, _ := a.recipients(id, "group=failed")
	got := map[string]string{}
	for _, r := range p.Recipient {
		got[r.Name] = r.Language
	}
	if !reflect.DeepEqual(got, map[string]string{"Işıl Kaya": "en", "Mert 100%_Ok": "tr"}) {
		t.Fatalf("languages: %v", got)
	}
	if p, _ := a.recipients(id, "group=failed&language=en"); p.Total != 1 || recipientNames(p)[0] != "Işıl Kaya" || p.Groups["sms"]["failed"] != 2 {
		t.Fatalf("filtered to English: want Işıl only (group counts unfiltered), got %+v", p)
	}
	if p, _ := a.recipients(id, "group=sent&language=tr"); p.Total != 1 || p.Recipient[0].Language != "tr" {
		t.Fatalf("a message without a stored language counts as the default: %+v", p)
	}
	p, _ = a.recipients(id, "group=skipped&language=en")
	want := map[string]any{"part": "sms", "length": float64(168), "limit": float64(160)}
	if p.Total != 1 || !reflect.DeepEqual(p.Recipient[0].Detail, want) {
		t.Fatalf("the detail measures the English text: %+v", p.Recipient)
	}
}

func TestRecipients_RejectsBadInput(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	id := a.recipientsCampaign()

	for _, q := range []string{"", "group=lost", "group=sent&channel=fax", "group=sent&page=0", "group=sent&page=x",
		"group=sent&page=1001", "group=sent&q=" + strings.Repeat("a", 51), "group=sent&language=de"} {
		if _, code := a.recipients(id, q); code != http.StatusUnprocessableEntity {
			t.Errorf("%q: want 422, got %d", q, code)
		}
	}
	if _, code := a.recipients(id, "group=sent&q="+strings.Repeat("ş", 50)); code != http.StatusOK {
		t.Errorf("50 characters are allowed, got %d", code)
	}
	if _, code := a.recipients(999, "group=sent"); code != http.StatusNotFound {
		t.Errorf("unknown campaign: %d", code)
	}
}

func TestRecipients_DraftHasNone(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.newDraft()

	p, _ := a.recipients(d.ID, "group=pending")

	if p.Total != 0 || len(p.Recipient) != 0 || p.Groups["sms"]["pending"] != 0 || p.Groups["email"]["sent"] != 0 || len(p.Reasons) != 0 {
		t.Fatalf("draft: %+v", p)
	}
}
