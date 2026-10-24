package campaign_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smart-campaign-composer/internal/content"
	"smart-campaign-composer/internal/llm"
	"smart-campaign-composer/internal/obstest"
)

// contentLLM interprets briefs with the mock but answers content requests
// from a script, in two chunks, optionally running a hook in between.
type contentLLM struct {
	*llm.Mock
	outputs []string
	err     error
	errs    []error // per call, when set
	between func()
	calls   []llm.ContentInput
}

func (c *contentLLM) StreamContent(ctx context.Context, in llm.ContentInput, onText func(string)) (json.RawMessage, error) {
	c.calls = append(c.calls, in)
	if c.err != nil {
		return nil, c.err
	}
	if i := len(c.calls) - 1; i < len(c.errs) && c.errs[i] != nil {
		return nil, c.errs[i]
	}
	out := c.outputs[len(c.calls)-1]
	onText(out[:len(out)/2])
	if c.between != nil {
		c.between()
	}
	onText(out[len(out)/2:])
	return json.RawMessage(out), nil
}

const (
	validContent   = `{"sms":{"body":"Merhaba {{first_name}}, sepetin seni bekliyor!"},"email":{"subject":"{{first_name}}, %30 indirim","body":"Merhaba {{first_name}},\n\nSepetindeki ürünler seni bekliyor."}}`
	invalidContent = `{"sms":{"body":"Hi {{email}}"},"email":{"subject":"S","body":"B"}}`
)

// aiAnswer is the model's answer: content keyed by language (D64).
func aiAnswer(lang, content string) string { return `{"` + lang + `":` + content + `}` }

type event struct {
	Name string
	Data map[string]any
}

// events parses a text/event-stream body.
func events(t *testing.T, rec *httptest.ResponseRecorder) []event {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("want an event stream, got %d %q: %s", rec.Code, ct, rec.Body)
	}
	var out []event
	for _, block := range strings.Split(strings.TrimSpace(rec.Body.String()), "\n\n") {
		var e event
		for _, line := range strings.Split(block, "\n") {
			if name, ok := strings.CutPrefix(line, "event: "); ok {
				e.Name = name
			} else if data, ok := strings.CutPrefix(line, "data: "); ok {
				if err := json.Unmarshal([]byte(data), &e.Data); err != nil {
					t.Fatalf("bad event data %q: %v", data, err)
				}
			}
		}
		out = append(out, e)
	}
	return out
}

func names(evs []event) string {
	var n []string
	for _, e := range evs {
		if len(n) == 0 || n[len(n)-1] != e.Name {
			n = append(n, e.Name)
		}
	}
	return strings.Join(n, ",")
}

func (a *app) generate(id int64, version int) []event {
	a.t.Helper()
	return events(a.t, a.do(http.MethodPost, "/api/campaigns/"+itoa(id)+"/content", `{"version":`+itoa(int64(version))+`,"languages":["tr"]}`))
}

func TestGenerate_MockStreamsDeltasThenDone(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.newDraft() // case example, sms + email

	evs := a.generate(d.ID, 1)

	if got := names(evs); got != "delta,done" {
		t.Fatalf("want deltas then done, got %s", got)
	}
	streamed := map[string]string{}
	for _, e := range evs[:len(evs)-1] {
		streamed[e.Data["field"].(string)] += e.Data["text"].(string)
	}
	done := evs[len(evs)-1].Data
	c := done["content"].(map[string]any)["tr"].(map[string]any)
	sms, email := c["sms"].(map[string]any), c["email"].(map[string]any)
	if streamed["tr.sms.body"] != sms["body"] || streamed["tr.email.subject"] != email["subject"] || streamed["tr.email.body"] != email["body"] {
		t.Fatalf("deltas do not add up to the saved content:\n%q\n%v", streamed, c)
	}
	if done["version"] != float64(2) || a.count(`SELECT count(*) FROM campaign_content WHERE campaign_id = $1`, d.ID) != 2 {
		t.Fatalf("want content saved and version 2, got %v", done["version"])
	}

	got := decode[map[string]any](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID), ""))
	if got["content"] == nil {
		t.Fatalf("GET does not include the content: %v", got)
	}
}

func TestGenerate_InvalidThenValid_RetryThenDone(t *testing.T) {
	t.Parallel()
	model := &contentLLM{Mock: llm.NewMock(), outputs: []string{aiAnswer("tr", invalidContent), aiAnswer("tr", validContent)}}
	a := newApp(t, model)
	d := a.newDraft()

	evs := a.generate(d.ID, 1)

	if got := names(evs); got != "delta,retry,delta,done" {
		t.Fatalf("want delta,retry,delta,done, got %s", got)
	}
	if len(model.calls) != 2 || !strings.Contains(strings.Join(model.calls[1].Errors, ";"), "tr.sms.body") {
		t.Fatalf("the retry must carry the validation errors, got %+v", model.calls)
	}
	if a.count(`SELECT count(*) FROM campaign_content WHERE campaign_id = $1 AND body LIKE '%sepetin%'`, d.ID) != 1 {
		t.Fatal("valid retry was not saved")
	}
}

func (a *app) unchanged(id int64) {
	a.t.Helper()
	if n := a.count(`SELECT count(*) FROM campaign_content WHERE campaign_id = $1`, id); n != 0 {
		a.t.Fatalf("content was saved: %d rows", n)
	}
	if v := a.count(`SELECT version FROM campaigns WHERE id = $1`, id); v != 1 {
		a.t.Fatalf("version changed to %d", v)
	}
}

func TestGenerate_InvalidTwice_ErrorNothingSaved(t *testing.T) {
	t.Parallel()
	a := newApp(t, &contentLLM{Mock: llm.NewMock(), outputs: []string{aiAnswer("tr", invalidContent), aiAnswer("tr", invalidContent)}})
	d := a.newDraft()

	evs := a.generate(d.ID, 1)

	if got := names(evs); got != "delta,retry,delta,error" || evs[len(evs)-1].Data["code"] != "ai_invalid" {
		t.Fatalf("want ...error ai_invalid, got %s %+v", got, evs[len(evs)-1])
	}
	a.unchanged(d.ID)
}

func TestGenerate_AIFailure_ErrorNothingSaved(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		err  error
		code any
	}{"refused": {llm.ErrRefused, nil}, "unavailable": {errors.New("503 overloaded"), "ai_unavailable"}} {
		err := tc.err
		t.Run(name, func(t *testing.T) {
			model := &contentLLM{Mock: llm.NewMock(), err: err}
			a := newApp(t, model)
			d := a.newDraft()

			evs := a.generate(d.ID, 1)

			if names(evs) != "error" || strings.Contains(evs[0].Data["message"].(string), "503") || evs[0].Data["code"] != tc.code || len(model.calls) != 1 {
				t.Fatalf("want one error event (code %v) without internals and no retry, got %+v after %d calls", tc.code, evs, len(model.calls))
			}
			a.unchanged(d.ID)
		})
	}
}

// The browser closes the tab mid-stream: nothing may be saved, even if the
// model still returns a valid answer.
func TestGenerate_ClientDisconnect_NothingSaved(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	a := newApp(t, &contentLLM{Mock: llm.NewMock(), outputs: []string{aiAnswer("tr", validContent)}, between: cancel})
	d := a.newDraft()

	req := httptest.NewRequest(http.MethodPost, "/api/campaigns/"+itoa(d.ID)+"/content", strings.NewReader(`{"version":1,"languages":["tr"]}`)).WithContext(ctx)
	req.AddCookie(a.cookie)
	a.mux.ServeHTTP(httptest.NewRecorder(), req)

	a.unchanged(d.ID)
}

func TestGenerate_Preconditions(t *testing.T) {
	t.Parallel()
	a := newApp(t, &contentLLM{Mock: llm.NewMock(), err: errors.New("must not be called")})
	d := a.newDraft()
	path := "/api/campaigns/" + itoa(d.ID) + "/content"

	for name, tc := range map[string]struct {
		path, body string
		want       int
	}{
		"stale version":   {path, `{"version":7,"languages":["tr"]}`, http.StatusConflict},
		"missing version": {path, `{}`, http.StatusUnprocessableEntity},
		"unknown id":      {"/api/campaigns/999/content", `{"version":1,"languages":["tr"]}`, http.StatusNotFound},
	} {
		if rec := a.do(http.MethodPost, tc.path, tc.body); rec.Code != tc.want || strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
			t.Errorf("%s: want %d JSON, got %d %q", name, tc.want, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	if _, err := a.pool.Exec(context.Background(), `UPDATE campaigns SET status = 'approved' WHERE id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	if rec := a.do(http.MethodPost, path, `{"version":1,"languages":["tr"]}`); rec.Code != http.StatusConflict {
		t.Errorf("approved campaign: want 409, got %d", rec.Code)
	}
}

func TestUpdateContent(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.newDraft()
	path := "/api/campaigns/" + itoa(d.ID) + "/content"

	rec := a.do(http.MethodPut, path, `{"version":1,"default_language":"tr","content":{"tr":`+validContent+`}}`)
	got := decode[map[string]any](t, rec)
	if rec.Code != http.StatusOK || got["version"] != float64(2) || got["content"] == nil {
		t.Fatalf("valid edit: want 200 v2 with content, got %d %s", rec.Code, rec.Body)
	}

	rec = a.do(http.MethodPut, path, `{"version":2,"default_language":"tr","content":{"tr":`+invalidContent+`}}`)
	errs := decode[map[string]any](t, rec)
	if rec.Code != http.StatusUnprocessableEntity || errs["field_errors"] == nil {
		t.Fatalf("invalid variable: want 422 with field_errors, got %d %s", rec.Code, rec.Body)
	}
	if rec := a.do(http.MethodPut, path, `{"version":2,"default_language":"tr","content":{"tr":{"sms":{"body":"Hi"}}}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing email content: want 422, got %d", rec.Code)
	}
	// A NUL byte must be a validation error, not a database failure (500).
	if rec := a.do(http.MethodPut, path, `{"version":2,"default_language":"tr","content":{"tr":{"sms":{"body":"Hi\u0000"},"email":{"subject":"S","body":"B"}}}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("NUL in content: want 422, got %d %s", rec.Code, rec.Body)
	}
	if rec := a.do(http.MethodPut, path, `{"version":1,"default_language":"tr","content":{"tr":`+validContent+`}}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale version: want 409, got %d", rec.Code)
	}
	var c content.Content
	json.Unmarshal([]byte(validContent), &c)
	if body := a.count(`SELECT count(*) FROM campaign_content WHERE campaign_id = $1 AND body = $2`, d.ID, c.SMS.Body); body != 1 {
		t.Fatal("content not stored as sent")
	}
}

// The AI must greet each recipient by name in the SMS and the email body
// (D72); an answer without {{first_name}} there is retried once with the reason.
func TestGenerate_RequiresFirstNameInAIText(t *testing.T) {
	t.Parallel()
	generic := `{"sms":{"body":"Değerli müşterilerimize özel %25 indirim!"},"email":{"subject":"Size özel","body":"Değerli müşterimiz,\n\nİndirim sizi bekliyor."}}`
	model := &contentLLM{Mock: llm.NewMock(), outputs: []string{aiAnswer("tr", generic), aiAnswer("tr", validContent)}}
	a := newApp(t, model)
	d := a.newDraft()

	evs := a.generate(d.ID, 1)

	if got := names(evs); !strings.HasPrefix(got, "delta") || !strings.HasSuffix(got, "retry,delta,done") || len(model.calls) != 2 {
		t.Fatalf("want a retry then done after 2 calls, got %s after %d", got, len(model.calls))
	}
	problems := strings.Join(model.calls[1].Errors, "; ")
	if !strings.Contains(problems, "tr.sms.body") || !strings.Contains(problems, "tr.email.body") || !strings.Contains(problems, "{{first_name}}") || strings.Contains(problems, "subject") {
		t.Fatalf("want the SMS and email body named, not the subject: %s", problems)
	}
}

func TestGenerate_TruncatedAnswerIsRetried(t *testing.T) {
	t.Parallel()
	model := &contentLLM{Mock: llm.NewMock(), errs: []error{llm.ErrTruncated}, outputs: []string{"", aiAnswer("tr", validContent)}}
	a := newApp(t, model)
	d := a.newDraft()

	evs := a.generate(d.ID, 1)

	if got := names(evs); got != "retry,delta,done" || len(model.calls) != 2 {
		t.Fatalf("want retry,delta,done after 2 calls, got %s after %d", got, len(model.calls))
	}
}

// Each AI call's duration and its wait for the first text are recorded, so
// slow generations can be split into waiting and writing (D72).
func TestGenerate_RecordsCallDurations(t *testing.T) {
	calls := func() float64 { return obstest.Value(t, "scc_llm_call_duration_seconds", "operation", "content") }
	first := func() float64 { return obstest.Value(t, "scc_llm_first_text_seconds", "operation", "content") }
	c, f := calls(), first()

	a := newApp(t, &contentLLM{Mock: llm.NewMock(), outputs: []string{aiAnswer("tr", invalidContent), aiAnswer("tr", validContent)}})
	a.generate(a.newDraft().ID, 1)
	b := newApp(t, &contentLLM{Mock: llm.NewMock(), err: errors.New("503 overloaded")})
	b.generate(b.newDraft().ID, 1)

	if calls()-c != 3 || first()-f != 2 {
		t.Fatalf("want 3 call durations (failed calls too) and 2 first-text waits, got +%v, +%v", calls()-c, first()-f)
	}
}

func TestGenerate_RecordsCallMetrics(t *testing.T) {
	value := func(result string) float64 {
		return obstest.Value(t, "scc_llm_calls_total", "operation", "content", "result", result)
	}
	ok, invalid, failed := value("ok"), value("invalid"), value("error")

	a := newApp(t, &contentLLM{Mock: llm.NewMock(), outputs: []string{aiAnswer("tr", invalidContent), aiAnswer("tr", validContent)}})
	a.generate(a.newDraft().ID, 1)
	b := newApp(t, &contentLLM{Mock: llm.NewMock(), err: errors.New("503 overloaded")})
	b.generate(b.newDraft().ID, 1)

	if value("ok")-ok != 1 || value("invalid")-invalid != 1 || value("error")-failed != 1 {
		t.Fatalf("want +1 ok, +1 invalid, +1 error; got +%v, +%v, +%v", value("ok")-ok, value("invalid")-invalid, value("error")-failed)
	}
}
