package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/interpreteval"
	"smart-campaign-composer/internal/llm"
)

// The cost cap depends on the real client's per-call log line (D72); if it
// were renamed or lost its token counts, the cap would silently never trip.
func TestMeter_CountsRealClientTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",
			"content":[{"type":"text","text":"{\"unsupported\":true,\"reason\":\"r\",\"rules\":[],\"suggested_send_at\":null,\"schedule_note\":null}"}],
			"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":2000,"output_tokens":3000}}`))
	}))
	defer srv.Close()
	m := &meter{}
	defer swapLogger(m)()

	client := llm.NewAnthropic("test-key", "claude-opus-5-5", option.WithBaseURL(srv.URL))
	if _, err := client.InterpretRequest(context.Background(), llm.InterpretInput{Text: "b", Now: time.Now(), TimeZone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if m.in.Load() != 2000 || m.out.Load() != 3000 {
		t.Fatalf("want 2000/3000 tokens counted, got %d/%d", m.in.Load(), m.out.Load())
	}
	// Opus 5.5: $4 input, $20 output per million tokens.
	if got := m.usd(prices{4, 20}); got != 0.068 {
		t.Errorf("want $0.068, got %v", got)
	}
}

func TestMeter_IgnoresOtherLogs(t *testing.T) {
	m := &meter{}
	defer swapLogger(m)()
	slog.Info("something else", "input_tokens", 5)
	if m.in.Load() != 0 {
		t.Error("only claude call lines should count")
	}
}

// fake answers per brief; every call logs tokens like the real client does.
type fake struct {
	answers map[string][]string // brief → answer per call
	tokens  int64
}

func (f *fake) InterpretRequest(ctx context.Context, in llm.InterpretInput) (json.RawMessage, error) {
	slog.Info("claude call", "input_tokens", f.tokens, "output_tokens", f.tokens)
	a := f.answers[in.Text]
	if len(a) == 0 {
		return nil, errors.New("no answer")
	}
	i := min(len(in.Errors), len(a)-1) // the retry carries the validation errors
	return json.RawMessage(a[i]), nil
}

func (f *fake) StreamContent(context.Context, llm.ContentInput, func(string)) (json.RawMessage, error) {
	return nil, errors.New("unused")
}

const (
	unsupportedAnswer = `{"unsupported":true,"reason":"r","rules":[],"suggested_send_at":null,"schedule_note":null}`
	ankaraAnswer      = `{"unsupported":false,"reason":"","rules":[{"field":"city","op":"in","value":["Ankara"]}],"suggested_send_at":null,"schedule_note":null}`
	parisAnswer       = `{"unsupported":false,"reason":"","rules":[{"field":"city","op":"in","value":["Paris"]}],"suggested_send_at":null,"schedule_note":null}`
)

func testDataset(briefs ...string) interpreteval.Dataset {
	ds := interpreteval.Dataset{Now: time.Date(2026, 10, 26, 7, 0, 0, 0, time.UTC), TimeZone: "Europe/Istanbul",
		Catalog: audience.Catalog{Cities: []string{"Ankara"}, Categories: []string{"books"}}}
	for _, b := range briefs {
		ds.Cases = append(ds.Cases, interpreteval.Case{ID: b, Lang: "en", Tags: []string{"single"}, Brief: b,
			Rules: []audience.Rule{{Field: "city", Op: "in", Value: json.RawMessage(`["Ankara"]`)}}})
	}
	return ds
}

func unlimited() *budget { return &budget{max: 1e9, perCase: 1, spent: func() float64 { return 0 }} }

func TestEvaluate_ScoresInOrderAndCountsRetries(t *testing.T) {
	f := &fake{answers: map[string][]string{
		"first":  {ankaraAnswer},
		"second": {parisAnswer, ankaraAnswer}, // invalid city, fixed on the retry
		"third":  {unsupportedAnswer},
	}}
	results, skipped := evaluate(context.Background(), testDataset("first", "second", "third"), f, 2, time.Second, unlimited())
	if len(skipped) != 0 || len(results) != 3 {
		t.Fatalf("want 3 results, got %d (skipped %v)", len(results), skipped)
	}
	want := []struct {
		id, outcome string
		calls       int
	}{{"first", interpreteval.ExactMatch, 1}, {"second", interpreteval.ExactMatch, 2}, {"third", interpreteval.FalseUnsupported, 1}}
	for i, w := range want {
		if r := results[i]; r.ID != w.id || r.Outcome != w.outcome || r.Calls != w.calls {
			t.Errorf("result %d: want %+v, got %s %s %d", i, w, r.ID, r.Outcome, r.Calls)
		}
	}
}

func TestBudget_ReservesWorstCaseOfCasesInFlight(t *testing.T) {
	spent := 1.0
	b := &budget{max: 4.5, perCase: 2, spent: func() float64 { return spent }}
	if !b.reserve() || b.reserve() { // 1 + 2 fits; 1 + 2 + 2 would exceed 4.5
		t.Fatal("want one reservation admitted, the second refused")
	}
	spent = 1.5
	b.release(true) // metered: its real spend is in spent now
	if !b.reserve() {
		t.Error("released reservation should make room again")
	}
}

// A timed-out or failed call may be billed without its tokens being logged,
// so its worst case stays counted.
func TestBudget_UnmeteredCasesKeepTheirReservation(t *testing.T) {
	b := &budget{max: 5, perCase: 2, spent: func() float64 { return 0 }}
	b.reserve()
	b.release(false)
	if b.unmetered != 2 {
		t.Errorf("want 2 unmetered, got %v", b.unmetered)
	}
	b.reserve()
	if b.reserve() { // 2 unmetered + 2 in flight + 2 > 5
		t.Error("unmetered spend must still count against the budget")
	}
}

func TestEvaluate_StopsWhenTheNextCaseCannotFit(t *testing.T) {
	f := &fake{answers: map[string][]string{"a": {ankaraAnswer}, "b": {ankaraAnswer}, "c": {ankaraAnswer}}, tokens: 100_000}
	m := &meter{}
	defer swapLogger(m)()
	// Each call really costs $2.40; the worst case reserved per case is $6.
	// After a ($2.40 spent), b needs 2.40 + 6 = 8.40 <= 10; after b, c needs 4.80 + 6 > 10.
	b := &budget{max: 10, perCase: 6, spent: func() float64 { return m.usd(prices{4, 20}) }}
	results, skipped := evaluate(context.Background(), testDataset("a", "b", "c"), f, 4, time.Second, b)
	if len(results) != 2 || len(skipped) != 1 || skipped[0] != "c" {
		t.Fatalf("want a and b run, c skipped; got %d results, skipped %v", len(results), skipped)
	}
	if m.usd(prices{4, 20}) >= 10 {
		t.Errorf("spent %.2f, must stay below the cap", m.usd(prices{4, 20}))
	}
}

// With room for only one case at a time, parallel cases wait for each other
// instead of stopping the run.
func TestEvaluate_WaitsForCasesInFlightBeforeStopping(t *testing.T) {
	f := &fake{answers: map[string][]string{"a": {ankaraAnswer}, "b": {ankaraAnswer}, "c": {ankaraAnswer}}}
	b := &budget{max: 10, perCase: 6, spent: func() float64 { return 0 }}
	results, skipped := evaluate(context.Background(), testDataset("a", "b", "c"), f, 4, time.Second, b)
	if len(results) != 3 || len(skipped) != 0 {
		t.Fatalf("want all 3 run one after another, got %d results, skipped %v", len(results), skipped)
	}
}

func TestEvaluate_APIErrorKeepsItsReservation(t *testing.T) {
	f := &fake{answers: map[string][]string{"b": {ankaraAnswer}, "c": {ankaraAnswer}}} // a fails
	b := &budget{max: 10, perCase: 6, spent: func() float64 { return 0 }}
	results, skipped := evaluate(context.Background(), testDataset("a", "b", "c"), f, 1, time.Second, b)
	if len(results) != 1 || results[0].Outcome != interpreteval.APIError || len(skipped) != 2 {
		t.Fatalf("want a scored as an API error and b, c skipped; got %+v, skipped %v", results, skipped)
	}
}

func TestEvaluate_InterruptedCasesAreSkippedNotErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, skipped := evaluate(ctx, testDataset("a", "b"), &fake{}, 2, time.Second, unlimited())
	if len(results) != 0 || len(skipped) != 2 {
		t.Fatalf("want everything skipped, got %d results, skipped %v", len(results), skipped)
	}
}

// measure counts the exact production request of each case, minus
// max_tokens, which count_tokens does not take and which it reports instead.
func TestMeasure(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" {
			t.Errorf("only count_tokens may reach the API, got %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		brief := len(body["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"input_tokens": %d}`, 1000+brief)
	}))
	defer srv.Close()
	counter := anthropic.NewClient(option.WithAPIKey("k"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0))

	ds := testDataset("short", "a longer brief")
	got, err := measure(context.Background(), ds, "claude-opus-5-5", &counter)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || got.maxOutput != 16000 || got.maxInput <= got.meanInput || got.meanInput < 1000 {
		t.Fatalf("unexpected measurement %+v from %d requests", got, len(bodies))
	}
	b := bodies[0]
	if _, ok := b["max_tokens"]; ok || b["model"] != "claude-opus-5-5" || b["system"] == nil || b["output_config"] == nil || b["thinking"] == nil {
		t.Errorf("want the production request without max_tokens, got keys %v", b)
	}
}

func TestMeasurement_WorstCase(t *testing.T) {
	m := measurement{maxInput: 3000, maxOutput: 16000}
	// Two calls, each (3000 + 1000 retry margin) input and 16000 output at $4/$20.
	if got := m.worstCasePerCase(prices{4, 20}); fmt.Sprintf("%.3f", got) != "0.672" {
		t.Errorf("want $0.672, got %v", got)
	}
}

func TestPricesFor(t *testing.T) {
	if p, err := pricesFor("claude-opus-5-5", false, prices{4, 20}); err != nil || p != (prices{4, 20}) {
		t.Errorf("default model: got %v %v", p, err)
	}
	if _, err := pricesFor("claude-sonnet-5-5", false, prices{4, 20}); err == nil {
		t.Error("another model without explicit prices must be refused: the cap would be wrong")
	}
	if p, err := pricesFor("claude-sonnet-5-5", true, prices{2, 10}); err != nil || p != (prices{2, 10}) {
		t.Errorf("explicit prices: got %v %v", p, err)
	}
}

func swapLogger(h slog.Handler) func() {
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	return func() { slog.SetDefault(old) }
}
