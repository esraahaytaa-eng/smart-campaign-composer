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

// The draft shows each language's share of the audience, the recommendation
// and who gets which text; the recommendation never limits the choice (D64).
func TestDraft_RecommendationAndFallback(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	a.exec(`UPDATE users SET language = 'en' WHERE id = 1`) // the case example's only recipient
	d := a.newDraft()

	type view struct {
		Counts struct {
			ByLanguage map[string]struct{ Total, SMS, Email int } `json:"by_language"`
		} `json:"counts"`
		Recommendation struct {
			Languages []string `json:"languages"`
			Default   string   `json:"default"`
		} `json:"recommendation"`
		Delivery         map[string]map[string]struct{ Own, Fallback int } `json:"delivery"`
		ApprovalEstimate struct {
			Delivery map[string]map[string]struct{ Own, Fallback int } `json:"delivery"`
		} `json:"approval_estimate"`
	}
	got := decode[view](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID), ""))
	if got.Counts.ByLanguage["en"].SMS != 1 || len(got.Recommendation.Languages) != 1 || got.Recommendation.Default != "en" {
		t.Fatalf("want English recommended as default, got %+v %+v", got.Counts, got.Recommendation)
	}

	// Turkish only, though nobody in the audience speaks it: allowed.
	if code := a.putContent(d.ID, `{"version":1,"default_language":"tr","content":{"tr":`+validContent+`}}`); code != http.StatusOK {
		t.Fatalf("a language with no speakers can still be chosen: %d", code)
	}
	got = decode[view](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID), ""))
	if d := got.Delivery["sms"]["tr"]; d.Own != 0 || d.Fallback != 1 {
		t.Fatalf("want the English speaker counted as Turkish fallback, got %+v", got.Delivery)
	}
	a.approveLanguages(d.ID, 2, "tr")
	a.approve(d.ID, `{"version":2,"send":"now"}`)
	got = decode[view](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(d.ID), ""))
	if e := got.ApprovalEstimate.Delivery["email"]["tr"]; e.Fallback != 1 {
		t.Fatalf("the approval estimate keeps the split, got %+v", got.ApprovalEstimate)
	}
}

// D31, D64: a resend's languages follow the skipped recipients; it goes
// through the same content and review steps as any draft.
func TestResend_LanguagesOfSkipped(t *testing.T) {
	t.Parallel()
	a := newApp(t, llm.NewMock())
	parent := a.parentWithSkips() // the resend reaches user 1 only
	a.exec(`UPDATE users SET language = 'en' WHERE id = 1`)

	r := decode[draft](t, a.do(http.MethodPost, "/api/campaigns/"+itoa(parent)+"/resend", `{"channel":"sms"}`))
	got := decode[struct {
		Counts struct {
			ByLanguage map[string]struct{ SMS int } `json:"by_language"`
		} `json:"counts"`
		Recommendation struct {
			Languages []string `json:"languages"`
			Default   string   `json:"default"`
		} `json:"recommendation"`
	}](t, a.do(http.MethodGet, "/api/campaigns/"+itoa(r.ID), ""))
	if got.Counts.ByLanguage["en"].SMS != 1 || len(got.Counts.ByLanguage) != 1 || got.Recommendation.Default != "en" {
		t.Fatalf("want the skipped English speaker counted and English recommended, got %+v %+v", got.Counts, got.Recommendation)
	}
	a.putContent(r.ID, `{"version":1,"default_language":"en","content":{"en":{"sms":{"body":"Hi {{first_name}}"}}}}`)
	if code, _ := a.approve(r.ID, `{"version":2,"send":"now"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("the resend's language needs review too, got %d", code)
	}
	a.approveLanguages(r.ID, 2, "en")
	if code, body := a.approve(r.ID, `{"version":2,"send":"now"}`); code != http.StatusOK {
		t.Fatalf("want the resend approved, got %d %v", code, body)
	}
}
