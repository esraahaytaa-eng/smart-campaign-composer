package campaign_test

import (
	"context"
	"strings"
	"encoding/json"
	"net/http"
	"testing"

	"smart-campaign-composer/internal/campaign"
	"smart-campaign-composer/internal/llm"
)

// abDraft is a draft as the A/B test sees it (D68).
type abDraft struct {
	variantDraft
	ABTest   bool                                    `json:"ab_test"`
	ContentB map[string]map[string]map[string]string `json:"content_b"`
}

func (a *app) getAB(id int64) abDraft {
	a.t.Helper()
	return decode[abDraft](a.t, a.do(http.MethodGet, "/api/campaigns/"+itoa(id), ""))
}

func (a *app) putAB(id int64, version int, enabled bool) int {
	a.t.Helper()
	on := "false"
	if enabled {
		on = "true"
	}
	return a.do(http.MethodPut, "/api/campaigns/"+itoa(id)+"/ab", `{"version":`+itoa(int64(version))+`,"enabled":`+on+`}`).Code
}

const validContentB = `{"sms":{"body":"{{first_name}}, sepetindeki fırsat bitmeden gel!"},"email":{"subject":"Sepetin hazır, {{first_name}}","body":"Merhaba {{first_name}},\n\nFırsat bugün bitiyor."}}`

// abReady is a draft with A/B on and both Turkish versions saved and approved, at version 4.
func (a *app) abReady() draft {
	a.t.Helper()
	d := a.readyDraft() // version 2, Turkish A approved
	if code := a.putAB(d.ID, 2, true); code != http.StatusOK {
		a.t.Fatalf("turn on A/B: %d", code)
	}
	if code := a.putContent(d.ID, `{"version":3,"default_language":"tr","content":{"tr":`+validContent+`},"content_b":{"tr":`+validContentB+`}}`); code != http.StatusOK {
		a.t.Fatalf("save B: %d", code)
	}
	if code := a.approveLanguage(d.ID, "tr", 4); code != http.StatusOK {
		a.t.Fatalf("approve: %d", code)
	}
	return d
}

func TestAB_OffByDefault_ResponseUnchanged(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	got := a.getAB(d.ID)
	if got.ABTest || got.ContentB != nil {
		t.Fatalf("want no A/B test and no version B, got %v %v", got.ABTest, got.ContentB)
	}
	if code, body := a.approve(d.ID, `{"version":2,"send":"now"}`); code != http.StatusOK {
		t.Fatalf("a campaign without A/B approves as before, got %d %v", code, body)
	}
}

func TestAB_ApprovalWaitsForVersionB(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	if code := a.putAB(d.ID, 2, true); code != http.StatusOK {
		t.Fatalf("turn on A/B: %d", code)
	}
	if code := a.approveLanguage(d.ID, "tr", 3); code != http.StatusUnprocessableEntity {
		t.Fatalf("approving a language without version B: want 422, got %d", code)
	}
	if code, _ := a.approve(d.ID, `{"version":3,"send":"now"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("approving the campaign without version B: want 422, got %d", code)
	}
	a.putContent(d.ID, `{"version":3,"default_language":"tr","content":{"tr":`+validContent+`},"content_b":{"tr":`+validContentB+`}}`)
	if code := a.approveLanguage(d.ID, "tr", 4); code != http.StatusOK {
		t.Fatalf("with version B the language can be approved, got %d", code)
	}
	if code, body := a.approve(d.ID, `{"version":4,"send":"now"}`); code != http.StatusOK {
		t.Fatalf("want approval with both versions, got %d %v", code, body)
	}
}

func TestAB_EditingBNeedsReview(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.abReady()
	before := a.getAB(d.ID)
	edited := `{"sms":{"body":"Yeni B {{first_name}}"},"email":{"subject":"B","body":"B metni"}}`
	if code := a.putContent(d.ID, `{"version":4,"default_language":"tr","content":{"tr":`+validContent+`},"content_b":{"tr":`+edited+`}}`); code != http.StatusOK {
		t.Fatalf("save: %d", code)
	}
	got := a.getAB(d.ID)
	if got.Languages["tr"].ApprovedAt != nil || got.ContentB["tr"]["sms"]["body"] != "Yeni B {{first_name}}" {
		t.Fatalf("want Turkish needing review with the new B, got %+v %v", got.Languages, got.ContentB)
	}
	if got.Content["tr"]["sms"]["body"] != before.Content["tr"]["sms"]["body"] {
		t.Fatal("version A is unchanged")
	}
}

func TestAB_SavingUnchangedBKeepsApproval(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.abReady()
	a.putContent(d.ID, `{"version":4,"default_language":"tr","content":{"tr":`+validContent+`},"content_b":{"tr":`+validContentB+`}}`)
	if a.getAB(d.ID).Languages["tr"].ApprovedAt == nil {
		t.Fatal("saving the same text keeps the approval (D66)")
	}
}

func TestAB_TurningOnWithKeptBNeedsReview(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.abReady()
	if code := a.putAB(d.ID, 4, false); code != http.StatusOK {
		t.Fatalf("turn off: %d", code)
	}
	if a.getAB(d.ID).Languages["tr"].ApprovedAt == nil {
		t.Fatal("turning A/B off keeps the approval of what is still sent")
	}
	if code := a.putAB(d.ID, 5, true); code != http.StatusOK {
		t.Fatalf("turn on: %d", code)
	}
	got := a.getAB(d.ID)
	if !got.ABTest || got.ContentB["tr"] == nil || got.Languages["tr"].ApprovedAt != nil {
		t.Fatalf("want the kept B back and Turkish needing review, got %v %v %+v", got.ABTest, got.ContentB, got.Languages)
	}
}

func TestAB_TurningOffKeepsBNotChecked(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.abReady()
	a.exec(`UPDATE campaign_content SET body = 'Hi {{email}}' WHERE campaign_id = $1 AND variant = 'b' AND channel = 'sms'`, d.ID)
	a.putAB(d.ID, 4, false)
	got := a.getAB(d.ID)
	if got.ABTest || got.ContentB["tr"]["sms"]["body"] != "Hi {{email}}" {
		t.Fatalf("want A/B off with version B kept, got %v %v", got.ABTest, got.ContentB)
	}
	if code, body := a.approve(d.ID, `{"version":5,"send":"now"}`); code != http.StatusOK {
		t.Fatalf("version B isn't checked while A/B is off, got %d %v", code, body)
	}
}

func TestAB_VersionBValidated(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	if code := a.putContent(d.ID, `{"version":2,"default_language":"tr","content":{"tr":`+validContent+`},"content_b":{"tr":`+validContentB+`}}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("version B without an A/B test: want 422, got %d", code)
	}
	a.putAB(d.ID, 2, true)
	for name, b := range map[string]string{
		"invalid text":         `{"tr":` + invalidContent + `}`,
		"a language without A": `{"en":` + validContentB + `}`,
	} {
		if code := a.putContent(d.ID, `{"version":3,"default_language":"tr","content":{"tr":`+validContent+`},"content_b":`+b+`}`); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: want 422, got %d", name, code)
		}
	}
}

func TestAB_RefusedOnResend(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	other := a.newDraft()
	a.exec(`UPDATE campaigns SET parent_campaign_id = $2, rules = NULL, channels = '{sms}' WHERE id = $1`, d.ID, other.ID)
	if code := a.putAB(d.ID, 2, true); code != http.StatusConflict {
		t.Fatalf("A/B on a resend: want 409, got %d", code)
	}
}

func TestAB_ReAddChannelWithKeptB(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.abReady()
	a.exec(`DELETE FROM campaign_content WHERE campaign_id = $1 AND variant = 'a' AND channel = 'email'`, d.ID) // only B kept text for email
	a.putChannels(d.ID, `{"version":4,"channels":["sms"]}`)
	a.approveLanguage(d.ID, "tr", 5)
	a.putChannels(d.ID, `{"version":5,"channels":["sms","email"]}`)
	if a.getAB(d.ID).Languages["tr"].ApprovedAt != nil {
		t.Fatal("kept version B text coming back with its channel needs review (D67)")
	}
}

// Approval fixes the text: neither version can be edited afterwards, so a
// retry sends exactly what was approved (D68; the sending side is pinned in
// the worker tests).
func TestAB_ContentFixedAfterApproval(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.abReady()
	if code, body := a.approve(d.ID, `{"version":4,"send":"now"}`); code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, body)
	}
	if code := a.putContent(d.ID, `{"version":4,"default_language":"tr","content":{"tr":`+validContent+`},"content_b":{"tr":`+validContent+`}}`); code != http.StatusConflict {
		t.Fatalf("editing an approved campaign's version B: want 409, got %d", code)
	}
	if code := a.putAB(d.ID, 4, false); code != http.StatusConflict {
		t.Fatalf("turning A/B off after approval: want 409, got %d", code)
	}
}

// recordingLLM is the mock AI, remembering what each content call asked for.
type recordingLLM struct {
	*llm.Mock
	calls []llm.ContentInput
}

func (r *recordingLLM) StreamContent(ctx context.Context, in llm.ContentInput, onText func(string)) (json.RawMessage, error) {
	r.calls = append(r.calls, in)
	return r.Mock.StreamContent(ctx, in, onText)
}

// abOn is a draft with Turkish A approved and A/B on, at version 3.
func (a *app) abOn() draft {
	a.t.Helper()
	d := a.readyDraft()
	if code := a.putAB(d.ID, 2, true); code != http.StatusOK {
		a.t.Fatalf("turn on A/B: %d", code)
	}
	return d
}

func TestGenerate_VersionBWritesOnlyB(t *testing.T) {
	t.Parallel()
	model := &recordingLLM{Mock: llm.NewMock()}
	a := newApp(t, model)
	d := a.abOn()
	before := a.getAB(d.ID)

	if evs := a.generateBody(d.ID, `{"version":3,"languages":["tr"],"variant":"b"}`); names(evs) != "delta,done" {
		t.Fatalf("want version B written, got %s", names(evs))
	}
	got := a.getAB(d.ID)
	if len(model.calls) != 1 || model.calls[0].VersionA["tr"].SMS == nil {
		t.Fatalf("want one AI call given Turkish version A, got %+v", model.calls)
	}
	if b := got.ContentB["tr"]["sms"]["body"]; b == "" || b == got.Content["tr"]["sms"]["body"] {
		t.Fatalf("want a different version B, got %q", b)
	}
	if got.Content["tr"]["sms"]["body"] != before.Content["tr"]["sms"]["body"] || *got.DefaultLanguage != "tr" {
		t.Fatal("version A and the default are unchanged")
	}
	if got.Languages["tr"].ApprovedAt != nil {
		t.Fatal("the language needs review once version B is written")
	}
}

func TestGenerate_VersionBRefusedWithoutABOrOnExistingB(t *testing.T) {
	t.Parallel()
	model := noContentCalls()
	a := newApp(t, model)
	off := a.readyDraft()
	on := a.abReady() // version B exists
	for name, c := range map[string]struct {
		id   int64
		body string
		want int
	}{
		"A/B off":         {off.ID, `{"version":2,"languages":["tr"],"variant":"b"}`, http.StatusUnprocessableEntity},
		"B exists":        {on.ID, `{"version":4,"languages":["tr"],"variant":"b"}`, http.StatusConflict},
		"no version A":    {on.ID, `{"version":4,"languages":["en"],"variant":"b"}`, http.StatusUnprocessableEntity},
		"unknown version": {on.ID, `{"version":4,"languages":["tr"],"variant":"c"}`, http.StatusUnprocessableEntity},
	} {
		if rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(c.id)+"/content", c.body); rec.Code != c.want {
			t.Errorf("%s: want %d, got %d %s", name, c.want, rec.Code, rec.Body)
		}
	}
	if len(model.calls) != 0 {
		t.Fatalf("a refusal calls no AI, got %d calls", len(model.calls))
	}
}

func TestGenerate_VersionBCountsTowardLimit(t *testing.T) {
	t.Parallel()
	a := newAppWith(t, llm.NewMock(), func(h *campaign.Handler) { h.AICalls = campaign.NewAICallLimit(2) }) // the brief, then B
	d := a.abOn()
	a.generateBody(d.ID, `{"version":3,"languages":["tr"],"variant":"b","replace":true}`)
	if rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(d.ID)+"/content", `{"version":4,"languages":["tr"],"variant":"b","replace":true}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want the second call limited, got %d", rec.Code)
	}
}

// D68: the review's over-limit estimate for version B measures B's text.
func TestEstimate_OverLimitPerVersion(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.abOn()
	long := `{"sms":{"body":"{{first_name}}{{first_name}}{{first_name}} ` + strings.Repeat("y", 157) + `"},"email":{"subject":"S","body":"B"}}`
	if code := a.putContent(d.ID, `{"version":3,"default_language":"tr","content":{"tr":`+validContent+`},"content_b":{"tr":`+long+`}}`); code != http.StatusOK {
		t.Fatalf("save B: %d", code)
	}
	got := decode[struct {
		Over  struct{ SMS int } `json:"over_limit"`
		OverB *struct{ SMS int } `json:"over_limit_b"`
	}](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID)+"/estimate", ""))
	if got.OverB == nil || got.OverB.SMS == 0 || got.Over.SMS != 0 {
		t.Fatalf("want only version B over the limit, got A %d, B %+v", got.Over.SMS, got.OverB)
	}
}
