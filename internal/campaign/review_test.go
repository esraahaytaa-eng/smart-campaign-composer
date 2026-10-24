package campaign_test

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"smart-campaign-composer/internal/llm"
)

const twoLanguages = `{"version":1,"default_language":"tr","content":{"tr":` + validContent + `,"en":` + validContent + `}}`

// noContentCalls answers briefs with the mock and fails, recording the call,
// if anything asks it to write content.
func noContentCalls() *contentLLM {
	return &contentLLM{Mock: llm.NewMock(), err: errors.New("content must not be written")}
}

func (a *app) approveLanguage(id int64, lang string, version int) int {
	a.t.Helper()
	return a.do(http.MethodPost, "/api/campaigns/"+itoa(id)+"/languages/"+lang+"/approve", `{"version":`+itoa(int64(version))+`}`).Code
}

// approveLanguages approves each language as the marketer would after reading it.
func (a *app) approveLanguages(id int64, version int, langs ...string) {
	a.t.Helper()
	for _, l := range langs {
		if code := a.approveLanguage(id, l, version); code != http.StatusOK {
			a.t.Fatalf("approve %s: %d", l, code)
		}
	}
}

func approved(d variantDraft, lang string) bool { return d.Languages[lang].ApprovedAt != nil }

func TestLanguageApprove_ApprovesOneLanguage(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.newDraft()
	a.putContent(d.ID, twoLanguages)

	if code := a.approveLanguage(d.ID, "tr", 2); code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	got := a.get(d.ID)
	if !approved(got, "tr") || approved(got, "en") || got.Version != 2 {
		t.Fatalf("want only Turkish approved and the version unchanged, got %+v v%d", got.Languages, got.Version)
	}
}

func TestLanguageApprove_Rejects(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.newDraft()
	a.putContent(d.ID, twoLanguages)
	a.exec(`UPDATE campaign_content SET body = 'Hi {{email}}' WHERE campaign_id = $1 AND language = 'en' AND channel = 'sms'`, d.ID)
	sent := a.readyDraft()
	a.approve(sent.ID, `{"version":2,"send":"now"}`)

	for _, tc := range []struct {
		name, path, body string
		want             int
	}{
		{"invalid text", "/api/campaigns/" + itoa(d.ID) + "/languages/en/approve", `{"version":2}`, http.StatusUnprocessableEntity},
		{"language not written", "/api/campaigns/" + itoa(d.ID) + "/languages/de/approve", `{"version":2}`, http.StatusNotFound},
		{"stale version", "/api/campaigns/" + itoa(d.ID) + "/languages/tr/approve", `{"version":1}`, http.StatusConflict},
		{"no version", "/api/campaigns/" + itoa(d.ID) + "/languages/tr/approve", `{}`, http.StatusUnprocessableEntity},
		{"approved campaign", "/api/campaigns/" + itoa(sent.ID) + "/languages/tr/approve", `{"version":3}`, http.StatusConflict},
		{"unknown campaign", "/api/campaigns/999/languages/tr/approve", `{"version":1}`, http.StatusNotFound},
	} {
		if rec := a.do(http.MethodPost, tc.path, tc.body); rec.Code != tc.want {
			t.Errorf("%s: want %d, got %d %s", tc.name, tc.want, rec.Code, rec.Body)
		}
	}
	if got := a.get(d.ID); approved(got, "tr") || approved(got, "en") {
		t.Fatalf("a rejected request approved a language: %+v", got.Languages)
	}
}

// Review focus 2: editing an approved language puts only it back to review.
func TestContent_EditResetsOnlyThatLanguage(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.newDraft()
	a.putContent(d.ID, twoLanguages)
	a.approveLanguages(d.ID, 2, "tr", "en")

	edited := strings.Replace(validContent, "sepetin seni bekliyor", "your cart is waiting", 1)
	if code := a.putContent(d.ID, `{"version":2,"default_language":"tr","content":{"tr":`+validContent+`,"en":`+edited+`}}`); code != http.StatusOK {
		t.Fatalf("edit: %d", code)
	}
	got := a.get(d.ID)
	if !approved(got, "tr") || approved(got, "en") {
		t.Fatalf("want Turkish still approved and English back to review, got %+v", got.Languages)
	}
	if code, _ := a.approve(d.ID, `{"version":3,"send":"now"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("campaign approval must wait for English, got %d", code)
	}
}

func TestContent_UnchangedSaveKeepsApproval(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.newDraft()
	a.putContent(d.ID, twoLanguages)
	a.approveLanguages(d.ID, 2, "tr", "en")

	// the same text, a new default
	if code := a.putContent(d.ID, `{"version":2,"default_language":"en","content":{"tr":`+validContent+`,"en":`+validContent+`}}`); code != http.StatusOK {
		t.Fatalf("save: %d", code)
	}
	if got := a.get(d.ID); !approved(got, "tr") || !approved(got, "en") || *got.DefaultLanguage != "en" {
		t.Fatalf("want both still approved with English as default, got %+v %v", got.Languages, *got.DefaultLanguage)
	}
}

// Review focus 3 (D66): a new brief puts every language back to review as
// "may be outdated"; the text stays exactly as it was and no content is written.
func TestRequest_BriefChangeNeedsReviewKeepsText(t *testing.T) {
	t.Parallel()
	model := noContentCalls()
	a := newApp(t, model)
	d := a.readyDraft() // Turkish, approved, version 2
	before := a.get(d.ID).Content

	if code, body := a.putRequest(d.ID, requestBody(ankaraBrief, 2)); code != http.StatusOK {
		t.Fatalf("brief: %d %v", code, body)
	}
	got := a.get(d.ID)
	if approved(got, "tr") || !got.Languages["tr"].Outdated {
		t.Fatalf("want Turkish back to review and marked outdated, got %+v", got.Languages)
	}
	if !reflect.DeepEqual(got.Content, before) || len(model.calls) != 0 {
		t.Fatalf("the text must be unchanged and no content written: %v, %d calls", got.Content, len(model.calls))
	}
	if code, _ := a.approve(d.ID, `{"version":3,"send":"now"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("an outdated language blocks campaign approval, got %d", code)
	}
}

// After a brief change the marketer can approve the existing text as it is,
// without regenerating it or any AI call (user clarification, D66).
func TestLanguageApprove_AfterBriefChangeNoAICall(t *testing.T) {
	t.Parallel()
	model := noContentCalls()
	a := newApp(t, model)
	d := a.readyDraft()
	a.putRequest(d.ID, requestBody(ankaraBrief, 2))

	if code := a.approveLanguage(d.ID, "tr", 3); code != http.StatusOK {
		t.Fatalf("re-approve: %d", code)
	}
	if got := a.get(d.ID); !approved(got, "tr") || got.Languages["tr"].Outdated {
		t.Fatalf("want Turkish approved and no longer outdated, got %+v", got.Languages)
	}
	if code, body := a.approve(d.ID, `{"version":3,"send":"now"}`); code != http.StatusOK || len(model.calls) != 0 {
		t.Fatalf("want the campaign approved with no content call, got %d %v, %d calls", code, body, len(model.calls))
	}
}

func TestApprove_RefusedUntilEveryLanguageApproved(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.newDraft()
	a.putContent(d.ID, twoLanguages)
	a.approveLanguages(d.ID, 2, "tr")

	code, body := a.approve(d.ID, `{"version":2,"send":"now"}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body["error"].(string), "English") || strings.Contains(body["error"].(string), "Türkçe") {
		t.Fatalf("want 422 naming English only, got %d %v", code, body)
	}
	a.approveLanguages(d.ID, 2, "en")
	if code, body := a.approve(d.ID, `{"version":2,"send":"now"}`); code != http.StatusOK {
		t.Fatalf("every language approved: want 200, got %d %v", code, body)
	}
}

// D66: only "Write with AI" writes content; no settings change calls the AI.
func TestNoContentCall_OnSettingsChanges(t *testing.T) {
	t.Parallel()
	model := noContentCalls()
	a := newApp(t, model)
	d := a.newDraft()
	path := "/api/campaigns/" + itoa(d.ID)

	for _, step := range []struct{ method, path, body string }{
		{http.MethodPut, path + "/content", twoLanguages},
		{http.MethodPut, path + "/request", requestBody(ankaraBrief, 2)},
		{http.MethodPut, path + "/rules", `{"version":3,"rules":[{"field":"city","op":"in","value":["İstanbul"]}]}`},
		{http.MethodPut, path + "/channels", `{"version":4,"channels":["sms"]}`},
		{http.MethodPut, path + "/content", `{"version":5,"default_language":"en","content":{"tr":` + validContent + `,"en":` + validContent + `}}`},
		{http.MethodPut, path + "/content", `{"version":6,"default_language":"en","content":{"en":{"sms":{"body":"Hello {{first_name}}"}}}}`},
		{http.MethodPut, path + "/channels", `{"version":7,"channels":["sms","email"]}`},
	} {
		if rec := a.do(step.method, step.path, step.body); rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", step.method, step.path, rec.Code, rec.Body)
		}
	}
	if len(model.calls) != 0 {
		t.Fatalf("settings changes made %d content calls", len(model.calls))
	}
}
