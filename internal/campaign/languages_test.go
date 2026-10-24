package campaign_test

import (
	"net/http"
	"testing"

	"smart-campaign-composer/internal/llm"
)

// variantDraft is a campaign response with its content per language.
type variantDraft struct {
	draft
	DefaultLanguage *string                                 `json:"default_language"`
	Content         map[string]map[string]map[string]string `json:"content"`
	Languages       map[string]struct {
		ApprovedAt *string `json:"approved_at"`
		Outdated   bool    `json:"outdated"`
	} `json:"languages"`
}

func (a *app) get(id int64) variantDraft {
	a.t.Helper()
	return decode[variantDraft](a.t, a.do(http.MethodGet, "/api/campaigns/"+itoa(id), ""))
}

func (a *app) putContent(id int64, body string) int {
	a.t.Helper()
	return a.do(http.MethodPut, "/api/campaigns/"+itoa(id)+"/content", body).Code
}

// legacyDraft stores content the way it was migrated from before T20: one
// approved version labelled "und", the default (D64).
func (a *app) legacyDraft() draft {
	a.t.Helper()
	d := a.newDraft()
	a.exec(`UPDATE campaigns SET default_language = 'und' WHERE id = $1`, d.ID)
	a.exec(`INSERT INTO campaign_languages (campaign_id, language, approved_at, approved_by) VALUES ($1, 'und', now(), 1)`, d.ID)
	a.exec(`INSERT INTO campaign_content (campaign_id, channel, language, subject, body)
		VALUES ($1, 'sms', 'und', NULL, 'Eski SMS'), ($1, 'email', 'und', 'Eski', 'Eski e-posta')`, d.ID)
	return d
}

func TestCatalog_ListsLanguages(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	got := decode[struct {
		Languages []struct{ Code, Name string } `json:"languages"`
	}](t, a.do(http.MethodGet, "/api/catalog", ""))
	if len(got.Languages) != 2 || got.Languages[0].Code != "tr" || got.Languages[0].Name != "Türkçe" ||
		got.Languages[1].Code != "en" || got.Languages[1].Name != "English" {
		t.Fatalf("want Türkçe and English from the catalog, got %+v", got.Languages)
	}
}

func TestContent_SaveLoadVariants(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.newDraft()

	if code := a.putContent(d.ID, `{"version":1,"default_language":"tr","content":{"tr":`+validContent+`,"en":`+validContent+`}}`); code != http.StatusOK {
		t.Fatalf("two languages: want 200, got %d", code)
	}
	got := a.get(d.ID)
	if got.DefaultLanguage == nil || *got.DefaultLanguage != "tr" || got.Content["en"]["sms"]["body"] == "" || got.Content["tr"]["email"]["subject"] == "" {
		t.Fatalf("want both languages with Turkish as default, got %v %v", got.DefaultLanguage, got.Content)
	}
	if len(got.Languages) != 2 || got.Languages["en"].ApprovedAt != nil || got.Languages["en"].Outdated {
		t.Fatalf("want a review state per language, new ones not approved: %+v", got.Languages)
	}

	// Removing a language: only the languages sent are kept.
	if code := a.putContent(d.ID, `{"version":2,"default_language":"tr","content":{"tr":`+validContent+`}}`); code != http.StatusOK {
		t.Fatalf("one language: want 200, got %d", code)
	}
	if got := a.get(d.ID); len(got.Content) != 1 || len(got.Languages) != 1 || got.Content["tr"] == nil {
		t.Fatalf("want English removed, got %v %v", got.Content, got.Languages)
	}

	for name, body := range map[string]string{
		"legacy label on new content": `{"version":3,"default_language":"und","content":{"und":` + validContent + `}}`,
		"unsupported language":        `{"version":3,"default_language":"de","content":{"de":` + validContent + `}}`,
		"default not among them":      `{"version":3,"default_language":"en","content":{"tr":` + validContent + `}}`,
		"no default":                  `{"version":3,"content":{"tr":` + validContent + `}}`,
		"no language":                 `{"version":3,"default_language":"tr","content":{}}`,
	} {
		if code := a.putContent(d.ID, body); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: want 422, got %d", name, code)
		}
	}
	if got := a.get(d.ID); got.Version != 3 {
		t.Fatalf("a rejected save changed the draft: version %d", got.Version)
	}
}

// A draft from before T20 keeps its unlabelled content until the marketer
// names its language; it can still be edited as it is.
func TestContent_RelabelLegacyDraft(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.legacyDraft()

	if got := a.get(d.ID); *got.DefaultLanguage != "und" || got.Content["und"]["sms"]["body"] != "Eski SMS" || got.Languages["und"].ApprovedAt == nil {
		t.Fatalf("want the legacy version approved and the default, got %v %v %+v", got.DefaultLanguage, got.Content, got.Languages)
	}
	if code := a.putContent(d.ID, `{"version":1,"default_language":"und","content":{"und":`+validContent+`}}`); code != http.StatusOK {
		t.Fatalf("editing legacy content as it is: want 200, got %d", code)
	}
	if code := a.putContent(d.ID, `{"version":2,"default_language":"en","content":{"en":`+validContent+`}}`); code != http.StatusOK {
		t.Fatalf("relabel: want 200, got %d", code)
	}
	got := a.get(d.ID)
	if *got.DefaultLanguage != "en" || len(got.Content) != 1 || got.Content["en"] == nil || a.count(`SELECT count(*) FROM campaign_languages WHERE campaign_id = $1 AND language = 'und'`, d.ID) != 0 {
		t.Fatalf("want only English left, the default, got %v %v", got.DefaultLanguage, got.Content)
	}
	if code := a.putContent(d.ID, `{"version":3,"default_language":"und","content":{"und":`+validContent+`}}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("once relabelled, the legacy label can't come back: want 422, got %d", code)
	}
}

// Review focus 1: a draft written before T20 can still be approved as it is.
func TestApprove_LegacyDraft(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	d := a.legacyDraft()

	if code, body := a.approve(d.ID, `{"version":1,"send":"now"}`); code != http.StatusOK {
		t.Fatalf("want the legacy draft approved, got %d %v", code, body)
	}
}
