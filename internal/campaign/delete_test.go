package campaign_test

import (
	"net/http"
	"sync"
	"testing"

	"smart-campaign-composer/internal/llm"
)

func (a *app) deleteCampaign(id int64, body string) int {
	a.t.Helper()
	return a.do(http.MethodDelete, "/api/campaigns/"+itoa(id), body).Code
}

func TestDelete_DraftAndItsContent(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft() // version 2, with content

	if code := a.deleteCampaign(d.ID, `{"version":2}`); code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", code)
	}

	if a.count(`SELECT count(*) FROM campaigns WHERE id = $1`, d.ID)+a.count(`SELECT count(*) FROM campaign_content WHERE campaign_id = $1`, d.ID) != 0 {
		t.Fatal("the draft or its content is still there")
	}
	if rec := a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET after delete: want 404, got %d", rec.Code)
	}
}

func TestDelete_Rejects(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()
	approved := a.readyDraft()
	a.approve(approved.ID, `{"version":2,"send":"now"}`)
	sending := a.readyDraft()
	a.approve(sending.ID, `{"version":2,"send":"now"}`)
	a.exec(`UPDATE campaigns SET status = 'sending' WHERE id = $1`, sending.ID)

	for _, tc := range []struct {
		name string
		id   int64
		body string
		want int
	}{
		{"stale version", d.ID, `{"version":1}`, http.StatusConflict},
		{"no version", d.ID, `{}`, http.StatusUnprocessableEntity},
		{"approved", approved.ID, `{"version":3}`, http.StatusConflict},
		{"sending", sending.ID, `{"version":3}`, http.StatusConflict},
		{"unknown", 999, `{"version":1}`, http.StatusNotFound},
	} {
		if code := a.deleteCampaign(tc.id, tc.body); code != tc.want {
			t.Errorf("%s: want %d, got %d", tc.name, tc.want, code)
		}
	}
	if a.count(`SELECT count(*) FROM campaigns`) != 3 || a.count(`SELECT count(*) FROM campaign_content`) != 6 {
		t.Fatal("a rejected delete removed data")
	}
}

// An abandoned resend draft used to block resends on that channel for good:
// drafts cannot be cancelled, and the one-active-resend index counts them.
func TestDelete_AbandonedResendDraftFreesTheChannel(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	parent := a.parentWithSkips()
	path := "/api/campaigns/" + itoa(parent) + "/resend"
	rec := a.do(http.MethodPost, path, `{"channel":"sms"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("resend: %d %s", rec.Code, rec.Body)
	}
	abandoned := decode[draft](t, rec)
	if rec := a.do(http.MethodPost, path, `{"channel":"sms"}`); rec.Code != http.StatusConflict {
		t.Fatalf("second resend while the draft exists: want 409, got %d", rec.Code)
	}

	if code := a.deleteCampaign(abandoned.ID, `{"version":1}`); code != http.StatusNoContent {
		t.Fatalf("delete the resend draft: want 204, got %d", code)
	}

	if rec := a.do(http.MethodPost, path, `{"channel":"sms"}`); rec.Code != http.StatusCreated {
		t.Fatalf("resend after deleting the abandoned draft: want 201, got %d %s", rec.Code, rec.Body)
	}
	if got := decode[map[string]any](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(parent), "")); got["status"] != "completed" {
		t.Fatalf("the parent must be unchanged, got %v", got["status"])
	}
}

func TestDelete_ConcurrentWithApprove(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.readyDraft()

	var approve, del int
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		approve = a.do(http.MethodPost, "/api/campaigns/"+itoa(d.ID)+"/approve", `{"version":2,"send":"now"}`).Code
	}()
	go func() { defer wg.Done(); del = a.deleteCampaign(d.ID, `{"version":2}`) }()
	wg.Wait()

	exists := a.count(`SELECT count(*) FROM campaigns WHERE id = $1 AND status = 'approved'`, d.ID) == 1
	switch {
	case approve == http.StatusOK && del == http.StatusConflict && exists:
	case del == http.StatusNoContent && approve != http.StatusOK && !exists:
	default:
		t.Fatalf("want exactly one to win: approve %d, delete %d, approved row exists %v", approve, del, exists)
	}
}

// Deleting a draft while its content streams: the stream reports an error and
// nothing is written back.
func TestDelete_DuringContentGeneration(t *testing.T) {
	t.Parallel()
	model := &contentLLM{Mock: llm.NewMock(), outputs: []string{validContent}}
	a := newApp(t, model)
	d := a.newDraft()
	model.between = func() {
		if code := a.deleteCampaign(d.ID, `{"version":1}`); code != http.StatusNoContent {
			t.Errorf("delete mid-stream: want 204, got %d", code)
		}
	}

	evs := a.generate(d.ID, 1)

	if last := evs[len(evs)-1]; last.Name != "error" {
		t.Fatalf("want an error event, got %s", names(evs))
	}
	if a.count(`SELECT count(*) FROM campaigns WHERE id = $1`, d.ID)+a.count(`SELECT count(*) FROM campaign_content WHERE campaign_id = $1`, d.ID) != 0 {
		t.Fatal("the stream wrote the deleted draft back")
	}
}
