package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

const validOutput = `{"unsupported":false,"reason":"","channels":[],"rules":[{"field":"city","op":"in","value":["İstanbul"]}],"suggested_send_at":null,"schedule_note":null}`

type captured struct {
	body   map[string]any
	header http.Header
	url    string
}

// fakeAPI answers every request with one Messages API response.
func fakeAPI(t *testing.T, status int, stopReason, text string) (*captured, string) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got.body)
		got.header = r.Header.Clone()
		got.url = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusOK {
			w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`))
			return
		}
		textJSON, _ := json.Marshal(text)
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",
			"content":[{"type":"text","text":` + string(textJSON) + `}],
			"stop_reason":"` + stopReason + `","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)
	return got, srv.URL
}

func interpretVia(t *testing.T, url, model string, in InterpretInput) (json.RawMessage, error) {
	t.Helper()
	a := NewAnthropic("test-key", model, option.WithBaseURL(url), option.WithMaxRetries(0))
	return a.InterpretRequest(context.Background(), in)
}

func testInput() InterpretInput {
	return InterpretInput{Text: "Send a discount to users in Istanbul.", Now: testNow, TimeZone: "Europe/Istanbul", Catalog: testCatalog}
}

func TestAnthropic_RequestShape(t *testing.T) {
	got, url := fakeAPI(t, http.StatusOK, "end_turn", validOutput)

	raw, err := interpretVia(t, url, "claude-opus-5-5", testInput())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != validOutput {
		t.Fatalf("want model text returned as-is, got %s", raw)
	}

	b := got.body
	if b["model"] != "claude-opus-5-5" {
		t.Errorf("model: %v", b["model"])
	}
	oc, _ := b["output_config"].(map[string]any)
	format, _ := oc["format"].(map[string]any)
	if oc["effort"] != "high" || format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("output_config: %v", oc)
	}
	if thinking, _ := b["thinking"].(map[string]any); thinking["type"] != "adaptive" {
		t.Errorf("thinking: %v", b["thinking"])
	}
	// No beta features: stable endpoint, no fallbacks (a refusal becomes "unsupported").
	if _, ok := b["fallbacks"]; ok || got.header.Get("anthropic-beta") != "" || got.url != "/v1/messages" {
		t.Errorf("beta use: fallbacks=%v, beta header=%q, url=%s", b["fallbacks"], got.header.Get("anthropic-beta"), got.url)
	}
	if got.header.Get("X-Api-Key") != "test-key" {
		t.Errorf("api key header missing")
	}
}

func TestInterpretPrompt_ContainsContextNotUserData(t *testing.T) {
	in := testInput()
	in.Errors = []string{`rule 0: unknown city "Paris"`}

	prompt := interpretPrompt(in)

	for _, want := range []string{
		"2026-10-21T15:00:00+03:00", "Europe/Istanbul", // now, shown in the campaign's zone
		"İstanbul", "Ankara", "books", "electronics", // catalog
		"<brief>\nSend a discount to users in Istanbul.\n</brief>",
		`unknown city "Paris"`, // previous validation errors on retry
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "@") || strings.Contains(prompt, "+90") {
		t.Errorf("prompt looks like it contains user records:\n%s", prompt)
	}
	for _, field := range []string{"city", "cart_added", "no_order", "order_count", "total_spend", "last_order", "ordered_category", "carted_category", "cart_value", "signed_up"} {
		if !strings.Contains(interpretSystemPrompt, field) {
			t.Errorf("system prompt does not describe %s", field)
		}
	}
}

func TestAnthropic_RefusalBecomesUnsupported(t *testing.T) {
	_, url := fakeAPI(t, http.StatusOK, "refusal", "")

	raw, err := interpretVia(t, url, "claude-opus-5-5", testInput())
	if err != nil {
		t.Fatal(err)
	}
	var out mockOutput
	if err := json.Unmarshal(raw, &out); err != nil || !out.Unsupported || out.Reason == "" || len(out.Rules) != 0 {
		t.Fatalf("want unsupported output, got %s (%v)", raw, err)
	}
}

func TestAnthropic_TruncatedOrFailedCallsError(t *testing.T) {
	_, url := fakeAPI(t, http.StatusOK, "max_tokens", `{"unsupported":`)
	if _, err := interpretVia(t, url, "claude-opus-5-5", testInput()); err == nil {
		t.Error("max_tokens: want error")
	}

	_, url = fakeAPI(t, http.StatusBadRequest, "", "")
	if _, err := interpretVia(t, url, "claude-opus-5-5", testInput()); err == nil {
		t.Error("HTTP 400: want error")
	}
}

// Structured outputs require additionalProperties: false on every object.
func TestInterpretSchema_AllObjectsClosed(t *testing.T) {
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch n := v.(type) {
		case map[string]any:
			if n["type"] == "object" {
				if n["additionalProperties"] != false {
					t.Errorf("%s: object without additionalProperties: false", path)
				}
				if _, ok := n["required"]; !ok {
					t.Errorf("%s: object without required", path)
				}
			}
			for k, c := range n {
				walk(path+"."+k, c)
			}
		case []any:
			for _, c := range n {
				walk(path+"[]", c)
			}
		}
	}
	// Round-trip so typed slices become []any like the API sees them.
	b, _ := json.Marshal(interpretSchema)
	var schema any
	json.Unmarshal(b, &schema)
	walk("schema", schema)
}
