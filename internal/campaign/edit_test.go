package campaign_test

import (
	"net/http"
	"testing"

	"smart-campaign-composer/internal/llm"
)

type editedDraft struct {
	draft
	Content map[string]any `json:"content"`
}

func (a *app) putChannels(id int64, body string) (int, editedDraft) {
	a.t.Helper()
	rec := a.do(http.MethodPut, "/api/campaigns/"+itoa(id)+"/channels", body)
	if rec.Code != http.StatusOK {
		return rec.Code, editedDraft{}
	}
	return rec.Code, decode[editedDraft](a.t, rec)
}

func TestChannels_RemovingOneDeletesItsContent(t *testing.T) {
	a := newApp(t, llm.NewMock())
	d := a.readyDraft() // sms + email with content, version 2

	code, got := a.putChannels(d.ID, `{"version":2,"channels":["sms"]}`)

	if code != http.StatusOK || len(got.Channels) != 1 || got.Channels[0] != "sms" || got.Version != 3 {
		t.Fatalf("want sms only at version 3, got %d %+v", code, got.draft)
	}
	if got.Content["sms"] == nil || got.Content["email"] != nil {
		t.Fatalf("want the sms content kept and the email content gone, got %v", got.Content)
	}
	if got.Counts == nil || got.Counts.SMS != 1 || got.Counts.Email != 0 || got.Counts.Total != 1 {
		t.Fatalf("want counts for sms only, got %+v", got.Counts)
	}
	if code, body := a.approve(d.ID, `{"version":3,"send":"now"}`); code != http.StatusOK {
		t.Fatalf("the remaining content must still be approvable, got %d %v", code, body)
	}
}

func TestChannels_AddedOneStartsWithoutContent(t *testing.T) {
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	a.putChannels(d.ID, `{"version":2,"channels":["sms"]}`)

	code, got := a.putChannels(d.ID, `{"version":3,"channels":["sms","email"]}`)

	if code != http.StatusOK || len(got.Channels) != 2 || got.Content["email"] != nil || got.Counts.Email != 1 {
		t.Fatalf("want both channels, no email content yet, got %d %+v %v", code, got.draft, got.Content)
	}
	if code, _ := a.approve(d.ID, `{"version":4,"send":"now"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("approval must wait for the added channel's content, got %d", code)
	}
}

func TestChannels_Rejects(t *testing.T) {
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
	got := decode[editedDraft](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID), ""))
	if got.Version != 2 || len(got.Channels) != 2 || got.Content["email"] == nil {
		t.Fatalf("a rejected change altered the draft: %+v %v", got.draft, got.Content)
	}
	if a.count(`SELECT count(*) FROM campaign_content WHERE campaign_id = $1`, approved.ID) != 2 {
		t.Fatal("the approved campaign's content changed")
	}
}
