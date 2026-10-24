package campaign_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"smart-campaign-composer/internal/campaign"
	"smart-campaign-composer/internal/llm"
)

// answers gives one prepared interpretation per call and counts the calls.
type answers struct {
	outputs []string
	calls   int
}

func (m *answers) InterpretRequest(context.Context, llm.InterpretInput) (json.RawMessage, error) {
	m.calls++
	return json.RawMessage(m.outputs[m.calls-1]), nil
}

func (m *answers) StreamContent(context.Context, llm.ContentInput, func(string)) (json.RawMessage, error) {
	return nil, errors.New("not used")
}

const (
	validAnswer   = `{"unsupported":false,"reason":"","rules":[{"field":"city","op":"in","value":["İstanbul"]}],"suggested_send_at":null,"schedule_note":null}`
	invalidAnswer = `{"unsupported":false,"reason":"","rules":[{"field":"city","op":"in","value":["Paris"]}],"suggested_send_at":null,"schedule_note":null}`
)

// D63: real AI calls are limited per marketer, and every provider call
// counts, including the retry inside one operation.
func TestAICallLimit_CountsEveryProviderCall(t *testing.T) {
	t.Parallel()
	model := &answers{outputs: []string{invalidAnswer, validAnswer, invalidAnswer}}
	a := newAppWith(t, model, func(h *campaign.Handler) { h.AICalls = campaign.NewAICallLimit(3) })
	brief := map[string]any{"request": caseExample, "channels": []string{"sms"}, "time_zone": "Europe/Istanbul"}

	// An invalid answer and its retry: two calls.
	if rec, _ := a.create(brief); rec.Code != http.StatusCreated {
		t.Fatalf("first create: %d %s", rec.Code, rec.Body)
	}
	// The third call is invalid again; its retry would be the fourth.
	rec, _ := a.create(brief)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the retry over the limit: want 429, got %d %s", rec.Code, rec.Body)
	}
	if model.calls != 3 {
		t.Fatalf("want exactly 3 provider calls, got %d", model.calls)
	}
	if after, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || after <= 0 || after > 3600 {
		t.Fatalf("want Retry-After in seconds within the hour, got %q", rec.Header().Get("Retry-After"))
	}
	body := decode[map[string]any](t, rec)
	if body["code"] != "ai_call_limit" || !strings.Contains(body["error"].(string), "3 AI calls per hour") || body["retry_after_seconds"] == nil {
		t.Fatalf("want a clear limit answer, got %v", body)
	}
	if n := a.count(`SELECT count(*) FROM campaigns`); n != 1 {
		t.Fatalf("the refused create saved nothing, got %d campaigns", n)
	}
}

// Message generation streams, so the limit is checked before the stream starts.
func TestAICallLimit_GenerationAnswers429BeforeStreaming(t *testing.T) {
	t.Parallel()
	a := newAppWith(t, llm.NewMock(), func(h *campaign.Handler) { h.AICalls = campaign.NewAICallLimit(2) })
	d := a.newDraft()   // call 1
	a.generate(d.ID, 1) // call 2

	rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(d.ID)+"/content", `{"version":2,"languages":["tr"]}`)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("want a 429 JSON answer, not a stream; got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if body := decode[map[string]any](t, rec); body["code"] != "ai_call_limit" {
		t.Fatalf("want the limit code, got %v", body)
	}
}

// A retry refused mid-stream ends the stream with the limit's message.
func TestAICallLimit_RetryRefusedMidStream(t *testing.T) {
	t.Parallel()
	model := &contentLLM{Mock: llm.NewMock(), outputs: []string{invalidContent, validContent}}
	a := newAppWith(t, model, func(h *campaign.Handler) { h.AICalls = campaign.NewAICallLimit(2) })
	d := a.newDraft() // call 1

	evs := a.generate(d.ID, 1) // call 2 is invalid; its retry would be call 3
	last := evs[len(evs)-1]
	if !strings.HasSuffix(names(evs), "retry,error") || !strings.Contains(last.Data["message"].(string), "AI calls per hour") {
		t.Fatalf("want retry then the limit error, got %s %v", names(evs), last.Data)
	}
	if a.count(`SELECT count(*) FROM campaign_content WHERE campaign_id = $1`, d.ID) != 0 {
		t.Fatal("nothing is saved")
	}
}
