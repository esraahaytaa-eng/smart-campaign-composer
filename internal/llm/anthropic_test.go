package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/content"
)

// validOutput is the model's text (values as JSON text, D70); decodedOutput what InterpretRequest returns.
const (
	validOutput   = `{"unsupported":false,"reason":"","rules":[{"field":"city","op":"in","value":"[\"İstanbul\"]"}],"suggested_send_at":null,"schedule_note":null}`
	decodedOutput = `{"unsupported":false,"reason":"","rules":[{"field":"city","op":"in","value":["İstanbul"]}],"suggested_send_at":null,"schedule_note":null}`
)

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
	if !sameJSON(t, string(raw), decodedOutput) {
		t.Fatalf("want the model's answer with decoded values, got %s", raw)
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
	for _, f := range audience.Fields() {
		if !strings.Contains(interpretSystemPrompt, f.Field) {
			t.Errorf("system prompt does not describe %s", f.Field)
		}
	}
	for _, want := range []string{"last_months", "gt", "lt", "signed_up / before", "users with no orders", "JSON text"} { // D70
		if !strings.Contains(interpretSystemPrompt, want) {
			t.Errorf("system prompt does not describe %s", want)
		}
	}
}

// D70: a rule's value is JSON text in the answer, so the compiled grammar
// stays small whatever the catalog holds (the API refused per-field value
// shapes as "compiled grammar is too large", 2026-10-24). field and op are
// the catalog's own lists; Go validates the value strictly.
func TestInterpretSchema_OneRuleShape(t *testing.T) {
	b, _ := json.Marshal(interpretSchema["properties"].(map[string]any)["rules"])
	var rules struct {
		Items struct {
			AnyOf      any
			Properties struct {
				Field, Op struct{ Enum []string }
				Value     struct{ Type string }
			}
		}
	}
	if err := json.Unmarshal(b, &rules); err != nil {
		t.Fatal(err)
	}
	var fields, ops []string
	for _, f := range audience.Fields() {
		fields = append(fields, f.Field)
		ops = append(ops, f.Ops...)
	}
	slices.Sort(ops)
	ops = slices.Compact(ops)
	p := rules.Items.Properties
	if rules.Items.AnyOf != nil || p.Value.Type != "string" || !slices.Equal(p.Field.Enum, fields) || !slices.Equal(p.Op.Enum, ops) {
		t.Fatalf("want one rule shape with the catalog's fields %v and operators %v and a text value, got %s", fields, ops, b)
	}
}

// The whole schema has two union-typed parameters (the API's limit is 16):
// suggested_send_at and schedule_note.
func TestInterpretSchema_UnionCount(t *testing.T) {
	var unions func(v any) int
	unions = func(v any) int {
		n := 0
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["anyOf"]; ok {
				n++
			}
			if _, ok := x["type"].([]any); ok {
				n++
			}
			for _, c := range x {
				n += unions(c)
			}
		case []any:
			for _, c := range x {
				n += unions(c)
			}
		}
		return n
	}
	b, _ := json.Marshal(interpretSchema)
	var schema any
	json.Unmarshal(b, &schema)
	if n := unions(schema); n != 2 {
		t.Fatalf("schema has %d union-typed parameters, want 2", n)
	}
}

func answerWith(rules string) string {
	return `{"unsupported":false,"reason":"","rules":[` + rules + `],"suggested_send_at":null,"schedule_note":null}`
}

// Each value's text becomes the JSON value itself, exactly as written.
func TestDecodeRuleValues(t *testing.T) {
	in := answerWith(`{"field":"spend_in_window","op":"gt","value":"{\"amount\": 10000, \"last_months\": 12}"},` +
		`{"field":"city","op":"in","value":" [\"İstanbul\"] "},{"field":"total_spend","op":"gte","value":"10000"}`)
	want := answerWith(`{"field":"spend_in_window","op":"gt","value":{"amount":10000,"last_months":12}},` +
		`{"field":"city","op":"in","value":["İstanbul"]},{"field":"total_spend","op":"gte","value":10000}`)

	got, err := decodeRuleValues(json.RawMessage(in))
	if err != nil || !sameJSON(t, string(got), want) {
		t.Fatalf("\n got: %s (%v)\nwant: %s", got, err, want)
	}
	unsupported := `{"unsupported":true,"reason":"Age is not in the catalog.","rules":[],"suggested_send_at":null,"schedule_note":null}`
	if got, err := decodeRuleValues(json.RawMessage(unsupported)); err != nil || string(got) != unsupported {
		t.Fatalf("an answer without rules must pass unchanged, got %s (%v)", got, err)
	}
	// An unsupported answer is passed on as it is, rules and all: the caller
	// ignores its rules and shows the reason.
	withRules := `{"unsupported":true,"reason":"Age is not in the catalog.","rules":[{"field":"city","op":"in","value":"İstanbul"}],"suggested_send_at":null,"schedule_note":null}`
	if got, err := decodeRuleValues(json.RawMessage(withRules)); err != nil || string(got) != withRules {
		t.Fatalf("an unsupported answer must pass unchanged, got %s (%v)", got, err)
	}
	// Not the schema at all: left to the caller's strict decoding, which reports it.
	if got, err := decodeRuleValues(json.RawMessage(`not json`)); err != nil || string(got) != `not json` {
		t.Fatalf("got %s (%v)", got, err)
	}
}

// Text that is not exactly one JSON value is an invalid answer (retried once,
// D15), never guessed at or dropped.
func TestDecodeRuleValues_RejectsInvalidValues(t *testing.T) {
	for name, rule := range map[string]string{
		"not JSON":      `{"field":"cart_added","op":"within","value":"{last_days: 30}"}`,
		"trailing data": `{"field":"cart_added","op":"within","value":"{\"last_days\": 30} {}"}`,
		"empty":         `{"field":"cart_added","op":"within","value":""}`,
		"not text":      `{"field":"cart_added","op":"within","value":{"last_days":30}}`,
		"missing":       `{"field":"cart_added","op":"within"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeRuleValues(json.RawMessage(answerWith(`{"field":"city","op":"in","value":"[\"Ankara\"]"},` + rule)))
			var invalid *InvalidAnswerError
			if !errors.As(err, &invalid) || len(invalid.Problems) != 1 || !strings.HasPrefix(invalid.Problems[0], "rule 1: ") {
				t.Fatalf("want one problem on rule 1, got %v", err)
			}
		})
	}
}

// Decoded values still go through strict validation: conflicting or invalid
// values and pairs outside the catalog are rejected there.
func TestDecodeRuleValues_ValidationStillRejects(t *testing.T) {
	for name, rule := range map[string]string{
		"two window forms": `{"field":"cart_added","op":"within","value":"{\"last_days\": 30, \"last_months\": 1}"}`,
		"13 months":        `{"field":"spend_in_window","op":"gt","value":"{\"amount\": 1, \"last_months\": 13}"}`,
		"unknown key":      `{"field":"orders_in_window","op":"gte","value":"{\"count\": 4, \"last_months\": 12, \"note\": \"x\"}"}`,
		"wrong value type": `{"field":"total_spend","op":"gt","value":"\"10000\""}`,
		"pair not allowed": `{"field":"cart_value","op":"lt","value":"100"}`,
		"before with to":   `{"field":"signed_up","op":"before","value":"{\"from\": \"2026-01-01\", \"to\": \"2026-02-01\"}"}`,
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := decodeRuleValues(json.RawMessage(answerWith(rule)))
			var out struct{ Rules []audience.Rule }
			if err != nil || json.Unmarshal(raw, &out) != nil {
				t.Fatalf("decode: %v", err)
			}
			if _, errs := audience.Validate(out.Rules, testCatalog); len(errs) != 1 {
				t.Fatalf("want one validation error, got %v", errs)
			}
		})
	}
}

// The real client decodes the values, and reports text that is not JSON as
// an invalid answer rather than an API failure.
func TestAnthropic_DecodesRuleValues(t *testing.T) {
	_, url := fakeAPI(t, http.StatusOK, "end_turn", answerWith(`{"field":"no_order","op":"within","value":"{\"last_days\": 45}"}`))
	raw, err := interpretVia(t, url, "claude-opus-5-5", testInput())
	if err != nil || !strings.Contains(string(raw), `"value":{"last_days":45}`) {
		t.Fatalf("want the decoded value, got %s (%v)", raw, err)
	}
	_, url = fakeAPI(t, http.StatusOK, "end_turn", answerWith(`{"field":"no_order","op":"within","value":"45 days"}`))
	var invalid *InvalidAnswerError
	if _, err := interpretVia(t, url, "claude-opus-5-5", testInput()); !errors.As(err, &invalid) {
		t.Fatalf("want an invalid answer, got %v", err)
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
	if _, err := interpretVia(t, url, "claude-opus-5-5", testInput()); !errors.Is(err, ErrTruncated) {
		t.Errorf("max_tokens: want ErrTruncated, got %v", err)
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
	pieces := []string{`{"tr":{"sms":{"bo`, `dy":"Merhaba {{first_`, `name}} İndirim!"}}}`}
	got, url := fakeStream(t, pieces, "end_turn")

	raw, chunks, err := streamVia(t, url, ContentInput{Text: "İndirim gönder", Channels: []string{"sms"}, Languages: []string{"tr"}})

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
	if b["stream"] != true || oc["effort"] != "medium" || len(props) != 1 || props["tr"] == nil {
		t.Errorf("request: stream=%v effort=%v schema properties=%v", b["stream"], oc["effort"], props)
	}
	if thinking, _ := b["thinking"].(map[string]any); thinking["type"] != "adaptive" {
		t.Errorf("thinking: %v", b["thinking"])
	}
	if got.header.Get("anthropic-beta") != "" || got.url != "/v1/messages" {
		t.Errorf("beta use: header %q, url %s", got.header.Get("anthropic-beta"), got.url)
	}
}

// Each real call logs its timing and token use, never the prompt or the
// answer, so a slow generation can be split into waiting and writing (D72).
func TestAnthropicContent_LogsTimingAndTokens(t *testing.T) {
	var logs strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	_, url := fakeStream(t, []string{`{"tr":{"sms":{"body":"Merhaba {{first_name}}"}}}`}, "end_turn")

	if _, _, err := streamVia(t, url, ContentInput{Text: "gizli brief", Channels: []string{"sms"}, Languages: []string{"tr"}}); err != nil {
		t.Fatal(err)
	}

	var line map[string]any
	if err := json.Unmarshal([]byte(logs.String()), &line); err != nil {
		t.Fatalf("want one JSON log line, got %q", logs.String())
	}
	for _, k := range []string{"seconds", "first_text_seconds", "input_tokens", "output_tokens", "text_chars"} {
		if _, ok := line[k].(float64); !ok {
			t.Errorf("log is missing number %s: %v", k, line)
		}
	}
	if line["msg"] != "claude call" || line["operation"] != "content" || line["output_tokens"] != 5.0 || line["stop_reason"] != "end_turn" {
		t.Errorf("log: %v", line)
	}
	if strings.Contains(logs.String(), "gizli") || strings.Contains(logs.String(), "Merhaba") {
		t.Errorf("the log must not contain the brief or the answer: %s", logs.String())
	}
}

func TestAnthropicContent_RefusalAndTruncationAreErrors(t *testing.T) {
	_, url := fakeStream(t, nil, "refusal")
	if _, _, err := streamVia(t, url, ContentInput{Text: "x", Channels: []string{"sms"}, Languages: []string{"tr"}}); !errors.Is(err, ErrRefused) {
		t.Errorf("refusal: want ErrRefused, got %v", err)
	}
	_, url = fakeStream(t, []string{`{"tr":{"sms":{"body":"cut`}, "max_tokens")
	if _, _, err := streamVia(t, url, ContentInput{Text: "x", Channels: []string{"sms"}, Languages: []string{"tr"}}); !errors.Is(err, ErrTruncated) {
		t.Errorf("max_tokens: want ErrTruncated, got %v", err)
	}
	got, url := fakeAPI(t, http.StatusInternalServerError, "", "")
	if _, _, err := streamVia(t, url, ContentInput{Text: "x", Channels: []string{"sms"}, Languages: []string{"tr"}}); err == nil || got.calls != 1 {
		t.Errorf("HTTP 500: want an error after exactly 1 call, got %v after %d", err, got.calls)
	}
}

func TestContentPrompt(t *testing.T) {
	prompt := contentPrompt(ContentInput{Text: "Sepette ürün bırakanlara indirim", Channels: []string{"sms", "email"}, Languages: []string{"tr", "en"},
		Errors: []string{"en.sms.body: the fixed text alone is 172 characters"}})

	for _, want := range []string{"<brief>\nSepette ürün bırakanlara indirim\n</brief>", "sms, email", "172 characters", "tr (Türkçe), en (English)"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	for _, want := range []string{"{{first_name}}", "{{last_name}}", "{{city}}", "160", "80", "2000", "natively", "whatever language the brief is in"} {
		if !strings.Contains(contentSystemPrompt, want) {
			t.Errorf("system prompt is missing %q", want)
		}
	}
	if strings.Contains(prompt, "@") || strings.Contains(prompt, "+90") {
		t.Errorf("prompt looks like it contains user records:\n%s", prompt)
	}
}

// D72: the AI greets each recipient by name, naturally, and never labels
// recipients by their segment or leans on last names that may be missing.
func TestContentSystemPrompt_Personalization(t *testing.T) {
	for _, want := range []string{"Merhaba {{first_name}},", "Hi {{first_name}},", "never as a label", "loyal customers", "Do not use {{last_name}}", `reads "Hi,"`, "never as the first word", "Never attach a suffix or apostrophe"} {
		if !strings.Contains(contentSystemPrompt, want) {
			t.Errorf("system prompt is missing %q", want)
		}
	}
}

// D72: the mock writes like the prompt asks: a greeting word before the name,
// so an empty name never leaves a leading comma.
func TestMockTexts_GreetBeforeTheName(t *testing.T) {
	for name, texts := range map[string]map[string]mockText{"first": mockTexts, "resend": mockResendTexts, "version B": mockVersionBTexts} {
		for lang, m := range texts {
			for field, body := range map[string]string{"sms": m.sms, "email body": m.body} {
				if body != "" && (!strings.Contains(body, "{{first_name}}") || strings.HasPrefix(body, "{{first_name}}")) {
					t.Errorf("%s %s %s: want a greeting word before {{first_name}}: %q", name, lang, field, body)
				}
			}
		}
	}
}

// One closed object per selected language, each with the chosen channels only.
func TestContentSchema_PerLanguage(t *testing.T) {
	schema := contentSchema([]string{"email"}, []string{"tr", "en"})
	props := schema["properties"].(map[string]any)
	if len(props) != 2 || schema["additionalProperties"] != false || len(schema["required"].([]string)) != 2 {
		t.Fatalf("want exactly tr and en, closed and required: %v", schema)
	}
	for _, l := range []string{"tr", "en"} {
		lang := props[l].(map[string]any)
		channels := lang["properties"].(map[string]any)
		if len(channels) != 1 || channels["email"] == nil || lang["additionalProperties"] != false {
			t.Errorf("%s: want email only, closed: %v", l, lang)
		}
	}
}

// D31: a resend exists because the first text was too long for some names.
func TestContentPrompt_ResendAsksForShorterText(t *testing.T) {
	resend := contentPrompt(ContentInput{Text: "brief", Channels: []string{"sms"}, Languages: []string{"tr"}, Resend: true})
	normal := contentPrompt(ContentInput{Text: "brief", Channels: []string{"sms"}, Languages: []string{"tr"}})

	if !strings.Contains(resend, "under 100") || strings.Contains(normal, "under 100") {
		t.Fatalf("resend prompt must ask for shorter text, the normal one must not:\n%s\n---\n%s", resend, normal)
	}
}

// D68: version B is written as an alternative to version A, which is quoted as data.
func TestContentPrompt_VersionBQuotesVersionA(t *testing.T) {
	a := map[string]content.Content{"tr": {SMS: &content.Message{Body: "Merhaba A"}}}
	b := contentPrompt(ContentInput{Text: "brief", Channels: []string{"sms"}, Languages: []string{"tr"}, VersionA: a})
	normal := contentPrompt(ContentInput{Text: "brief", Channels: []string{"sms"}, Languages: []string{"tr"}})

	if !strings.Contains(b, "<version_a>") || !strings.Contains(b, "Merhaba A") || strings.Contains(normal, "version_a") {
		t.Fatalf("only the version B prompt quotes version A:\n%s\n---\n%s", b, normal)
	}
}
