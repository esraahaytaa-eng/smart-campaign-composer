package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

const validOutput = `{"unsupported":false,"reason":"","rules":[{"field":"city","op":"in","value":["İstanbul"]}],"suggested_send_at":null,"schedule_note":null}`

type captured struct {
	body   map[string]any
	header http.Header
	url    string
	calls  int
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
		got.calls++
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
	// Production options apart from the URL: retry behaviour is part of what is tested.
	a := NewAnthropic("test-key", model, option.WithBaseURL(url))
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
	// Validation rejects a repeated field+op; Claude must know, or a valid brief ends in a 502.
	if !strings.Contains(interpretSystemPrompt, "at most once") {
		t.Error("system prompt does not say each field and operator may appear at most once")
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
	// Channels are the marketer's choice (D1), so the AI is not asked for them.
	if _, ok := interpretSchema["properties"].(map[string]any)["channels"]; ok || strings.Contains(interpretSystemPrompt, "Channels") {
		t.Error("schema or prompt still asks the AI for channels")
	}
	// Round-trip so typed slices become []any like the API sees them.
	b, _ := json.Marshal(interpretSchema)
	var schema any
	json.Unmarshal(b, &schema)
	walk("schema", schema)
}

// D15: API errors are not retried, so the SDK's own retries must be off.
func TestAnthropic_NoSDKRetries(t *testing.T) {
	got, url := fakeAPI(t, http.StatusInternalServerError, "", "")

	if _, err := interpretVia(t, url, "claude-opus-5-5", testInput()); err == nil {
		t.Fatal("want error")
	}
	if got.calls != 1 {
		t.Fatalf("want exactly 1 HTTP call for a failing API, got %d", got.calls)
	}
}

// fakeStream answers with Anthropic streaming events carrying the given text
// pieces and stop reason.
func fakeStream(t *testing.T, pieces []string, stopReason string) (*captured, string) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got.body)
		got.header = r.Header.Clone()
		got.url = r.URL.String()
		got.calls++
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(event, data string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data) }
		send("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`)
		send("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		for _, p := range pieces {
			text, _ := json.Marshal(p)
			send("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(text)+`}}`)
		}
		send("content_block_stop", `{"type":"content_block_stop","index":0}`)
		send("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+stopReason+`","stop_sequence":null},"usage":{"output_tokens":5}}`)
		send("message_stop", `{"type":"message_stop"}`)
	}))
	t.Cleanup(srv.Close)
	return got, srv.URL
}

func streamVia(t *testing.T, url string, in ContentInput) (json.RawMessage, []string, error) {
	t.Helper()
	var chunks []string
	raw, err := NewAnthropic("test-key", "claude-opus-5-5", option.WithBaseURL(url)).
		StreamContent(context.Background(), in, func(s string) { chunks = append(chunks, s) })
	return raw, chunks, err
}

func TestAnthropicContent_StreamsTextAndRequestShape(t *testing.T) {
	pieces := []string{`{"sms":{"bo`, `dy":"Merhaba {{first_`, `name}} İndirim!"}}`}
	got, url := fakeStream(t, pieces, "end_turn")

	raw, chunks, err := streamVia(t, url, ContentInput{Text: "İndirim gönder", Channels: []string{"sms"}})

	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(chunks, "") != strings.Join(pieces, "") || string(raw) != strings.Join(pieces, "") {
		t.Fatalf("chunks %q, raw %s", chunks, raw)
	}
	b := got.body
	oc, _ := b["output_config"].(map[string]any)
	format, _ := oc["format"].(map[string]any)
	schema, _ := format["schema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	if b["stream"] != true || oc["effort"] != "medium" || len(props) != 1 || props["sms"] == nil {
		t.Errorf("request: stream=%v effort=%v schema properties=%v", b["stream"], oc["effort"], props)
	}
	if thinking, _ := b["thinking"].(map[string]any); thinking["type"] != "adaptive" {
		t.Errorf("thinking: %v", b["thinking"])
	}
	if got.header.Get("anthropic-beta") != "" || got.url != "/v1/messages" {
		t.Errorf("beta use: header %q, url %s", got.header.Get("anthropic-beta"), got.url)
	}
}

func TestAnthropicContent_RefusalAndTruncationAreErrors(t *testing.T) {
	_, url := fakeStream(t, nil, "refusal")
	if _, _, err := streamVia(t, url, ContentInput{Text: "x", Channels: []string{"sms"}}); !errors.Is(err, ErrRefused) {
		t.Errorf("refusal: want ErrRefused, got %v", err)
	}
	_, url = fakeStream(t, []string{`{"sms":{"body":"cut`}, "max_tokens")
	if _, _, err := streamVia(t, url, ContentInput{Text: "x", Channels: []string{"sms"}}); err == nil {
		t.Error("max_tokens: want error")
	}
	got, url := fakeAPI(t, http.StatusInternalServerError, "", "")
	if _, _, err := streamVia(t, url, ContentInput{Text: "x", Channels: []string{"sms"}}); err == nil || got.calls != 1 {
		t.Errorf("HTTP 500: want an error after exactly 1 call, got %v after %d", err, got.calls)
	}
}

func TestContentPrompt(t *testing.T) {
	prompt := contentPrompt(ContentInput{Text: "Sepette ürün bırakanlara indirim", Channels: []string{"sms", "email"}, Errors: []string{"sms.body: the fixed text alone is 172 characters"}})

	for _, want := range []string{"<brief>\nSepette ürün bırakanlara indirim\n</brief>", "sms, email", "172 characters"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	for _, want := range []string{"{{first_name}}", "{{last_name}}", "{{city}}", "160", "80", "2000", "language of the brief"} {
		if !strings.Contains(contentSystemPrompt, want) {
			t.Errorf("system prompt is missing %q", want)
		}
	}
	if strings.Contains(prompt, "@") || strings.Contains(prompt, "+90") {
		t.Errorf("prompt looks like it contains user records:\n%s", prompt)
	}
	schema := contentSchema([]string{"email"})
	props := schema["properties"].(map[string]any)
	if len(props) != 1 || props["email"] == nil {
		t.Errorf("schema for email only: %v", props)
	}
}
