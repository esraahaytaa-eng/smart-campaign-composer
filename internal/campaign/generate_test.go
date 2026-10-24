package campaign_test

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"smart-campaign-composer/internal/campaign"
	"smart-campaign-composer/internal/llm"
)

func (a *app) generateBody(id int64, body string) []event {
	a.t.Helper()
	return events(a.t, a.do(http.MethodPost, "/api/campaigns/"+itoa(id)+"/content", body))
}

func TestGenerate_RequiresLanguages(t *testing.T) {
	t.Parallel()
	model := noContentCalls()
	a := newApp(t, model)
	d := a.newDraft()
	for name, body := range map[string]string{
		"missing":       `{"version":1}`,
		"empty":         `{"version":1,"languages":[]}`,
		"listed twice":  `{"version":1,"languages":["tr","tr"]}`,
		"unsupported":   `{"version":1,"languages":["de"]}`,
		"wrong default": `{"version":1,"languages":["tr"],"default_language":"en"}`,
	} {
		if rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(d.ID)+"/content", body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: want 422, got %d", name, rec.Code)
		}
	}
	if len(model.calls) != 0 {
		t.Fatalf("a rejected request called the AI %d times", len(model.calls))
	}
}

// The legacy label is never written by the AI (D64).
func TestGenerate_RefusesUnd(t *testing.T) {
	t.Parallel()
	model := noContentCalls()
	a := newApp(t, model)
	d := a.legacyDraft()
	if rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(d.ID)+"/content", `{"version":1,"languages":["und"],"replace":true}`); rec.Code != http.StatusUnprocessableEntity || len(model.calls) != 0 {
		t.Fatalf("want 422 without an AI call, got %d after %d calls", rec.Code, len(model.calls))
	}
}

// The 5% recommendation is a suggestion only: a language nobody in the
// audience speaks can still be written (user clarification, D64).
func TestGenerate_AcceptsLanguageWithNoSpeakers(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock()) // every user speaks Turkish
	d := a.newDraft()
	if evs := a.generateBody(d.ID, `{"version":1,"languages":["en"]}`); names(evs) != "delta,done" {
		t.Fatalf("want English written, got %s", names(evs))
	}
	if got := a.get(d.ID); got.Content["en"] == nil || *got.DefaultLanguage != "en" {
		t.Fatalf("want English saved as the default, got %v %v", got.Content, got.DefaultLanguage)
	}
}

// Review focus 6: a missing, extra or mislabelled language is invalid,
// retried once, then reported; nothing is filled in.
func TestGenerate_MissingLanguageRetriedThenReported(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]string{
		"missing":     aiAnswer("tr", validContent),
		"extra":       `{"tr":` + validContent + `,"en":` + validContent + `,"de":` + validContent + `}`,
		"mislabelled": `{"tr":` + validContent + `,"EN":` + validContent + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			model := &contentLLM{Mock: llm.NewMock(), outputs: []string{answer, answer}}
			a := newApp(t, model)
			d := a.newDraft()

			evs := a.generateBody(d.ID, `{"version":1,"languages":["tr","en"]}`)

			if names(evs) != "delta,retry,delta,error" || len(model.calls) != 2 {
				t.Fatalf("want one retry then an error, got %s after %d calls", names(evs), len(model.calls))
			}
			a.unchanged(d.ID)
		})
	}
}

// Adding a language writes it alone and leaves the others exactly as they
// were: hand-edited text and its approval (D66).
func TestGenerate_NewLanguageLeavesOthersUntouched(t *testing.T) {
	t.Parallel()
	model := &contentLLM{Mock: llm.NewMock(), outputs: []string{aiAnswer("en", validContent)}}
	a := newApp(t, model)
	d := a.readyDraft() // hand-written Turkish, approved, the default
	before := a.get(d.ID)

	if evs := a.generateBody(d.ID, `{"version":2,"languages":["en"]}`); names(evs) != "delta,done" {
		t.Fatalf("want done, got %s", names(evs))
	}
	got := a.get(d.ID)
	if !reflect.DeepEqual(got.Content["tr"], before.Content["tr"]) || !approved(got, "tr") || *got.DefaultLanguage != "tr" {
		t.Fatalf("Turkish must be unchanged, still approved and the default: %v %+v", got.Content["tr"], got.Languages)
	}
	if got.Content["en"] == nil || approved(got, "en") || !slices.Equal(model.calls[0].Languages, []string{"en"}) {
		t.Fatalf("want English written alone and waiting for review, got %+v, asked for %v", got.Languages, model.calls[0].Languages)
	}
}

// Rewriting a language that has content needs an explicit replace; without it
// the server refuses before any AI call, which isn't counted (D63, D66).
func TestGenerate_ExistingLanguageNeedsReplace(t *testing.T) {
	t.Parallel()
	model := &contentLLM{Mock: llm.NewMock(), outputs: []string{aiAnswer("tr", validContent)}}
	a := newAppWith(t, model, func(h *campaign.Handler) { h.AICalls = campaign.NewAICallLimit(2) })
	d := a.readyDraft() // call 1: the brief

	rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(d.ID)+"/content", `{"version":2,"languages":["tr"]}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Türkçe") || len(model.calls) != 0 {
		t.Fatalf("want 409 naming Turkish and no call, got %d %s after %d calls", rec.Code, rec.Body, len(model.calls))
	}
	// call 2 is still available
	if evs := a.generateBody(d.ID, `{"version":2,"languages":["tr"],"replace":true}`); names(evs) != "delta,done" {
		t.Fatalf("with replace: want done, got %s", names(evs))
	}
	if got := a.get(d.ID); approved(got, "tr") {
		t.Fatal("a rewritten language needs review again")
	}
}

func TestGenerate_StreamsLanguageFields(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.newDraft()

	evs := a.generateBody(d.ID, `{"version":1,"languages":["en","tr"],"default_language":"tr"}`)

	fields := map[string]bool{}
	for _, e := range evs {
		if e.Name == "delta" {
			fields[e.Data["field"].(string)] = true
		}
	}
	for _, f := range []string{"tr.sms.body", "tr.email.subject", "tr.email.body", "en.sms.body", "en.email.subject", "en.email.body"} {
		if !fields[f] {
			t.Errorf("no deltas for %s; got %v", f, fields)
		}
	}
	if got := a.get(d.ID); len(got.Content) != 2 || *got.DefaultLanguage != "tr" {
		t.Fatalf("want both saved with Turkish as default, got %v %v", got.Content, *got.DefaultLanguage)
	}
}

// D63: two languages are one generation, so one AI call.
func TestAILimit_OneCallForTwoLanguages(t *testing.T) {
	t.Parallel()
	a := newAppWith(t, llm.NewMock(), func(h *campaign.Handler) { h.AICalls = campaign.NewAICallLimit(2) })
	d := a.newDraft() // call 1

	if evs := a.generateBody(d.ID, `{"version":1,"languages":["tr","en"]}`); names(evs) != "delta,done" { // call 2
		t.Fatalf("want done, got %s", names(evs))
	}
	rec := a.do(http.MethodPost, "/api/campaigns/"+itoa(d.ID)+"/content", `{"version":2,"languages":["tr"],"replace":true}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the limit of 2 is used up by one brief and one two-language generation: want 429, got %d", rec.Code)
	}
}
