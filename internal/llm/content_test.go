package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"smart-campaign-composer/internal/content"
)

func mockContent(t *testing.T, ctx context.Context, in ContentInput) (content.Variants, []string, error) {
	t.Helper()
	var chunks []string
	raw, err := NewMock().StreamContent(ctx, in, func(s string) { chunks = append(chunks, s) })
	if err != nil {
		return nil, chunks, err
	}
	if strings.Join(chunks, "") != string(raw) {
		t.Fatalf("streamed chunks do not add up to the result:\n%q\n%s", strings.Join(chunks, ""), raw)
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	out := content.Variants{}
	for lang, msg := range v {
		var c content.Content
		dec := json.NewDecoder(strings.NewReader(string(msg)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			t.Fatalf("mock content does not match the schema: %v\n%s", err, raw)
		}
		out[lang] = c
	}
	return out, chunks, nil
}

// D64: the mock writes exactly the selected languages, whatever the brief's language.
func TestMock_WritesEachSelectedLanguage(t *testing.T) {
	ctx := context.Background()
	both := []string{"sms", "email"}
	turkishBrief := MockExamples[8].Brief

	v, chunks, err := mockContent(t, ctx, ContentInput{Text: turkishBrief, Channels: both, Languages: []string{"tr", "en"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 2 || !strings.Contains(v["tr"].SMS.Body, "Merhaba {{first_name}}") || !strings.Contains(v["en"].Email.Body, "Hi {{first_name}}") {
		t.Fatalf("want Turkish and English versions, got %+v", v)
	}
	if len(chunks) < 5 {
		t.Errorf("want the answer streamed in several chunks, got %d", len(chunks))
	}
	for lang, c := range v {
		if errs := content.ValidateDraft(c, both); len(errs) != 0 {
			t.Errorf("%s mock content must be a valid draft: %+v", lang, errs)
		}
	}

	en, _, err := mockContent(t, ctx, ContentInput{Text: turkishBrief, Channels: both, Languages: []string{"en"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(en) != 1 || !strings.Contains(en["en"].SMS.Body, "Hi {{first_name}}") {
		t.Fatalf("a Turkish brief with English selected: want English only, got %+v", en)
	}
}

// Every catalog language has mock texts (with TestLanguages_ConsistentEverywhere, D64).
func TestMock_HasTextsForEveryCatalogLanguage(t *testing.T) {
	for _, l := range content.Languages {
		if mockTexts[l.Code].sms == "" || mockResendTexts[l.Code].sms == "" {
			t.Errorf("no mock texts for %s", l.Code)
		}
	}
	if len(mockTexts) != len(content.Languages) || len(mockResendTexts) != len(content.Languages) {
		t.Errorf("mock texts for languages outside the catalog: %d, %d", len(mockTexts), len(mockResendTexts))
	}
}

func TestMockContent_OnlyChosenChannels(t *testing.T) {
	v, _, err := mockContent(t, context.Background(), ContentInput{Text: "Weekend sale for Ankara", Channels: []string{"email"}, Languages: []string{"tr"}})
	if err != nil {
		t.Fatal(err)
	}
	if c := v["tr"]; c.SMS != nil || c.Email == nil {
		t.Fatalf("want email only, got %+v", c)
	}
	if errs := content.ValidateDraft(v["tr"], []string{"email"}); len(errs) != 0 {
		t.Fatalf("invalid: %+v", errs)
	}
}

func TestMockContent_StopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := NewMock()
	calls := 0

	_, err := m.StreamContent(ctx, ContentInput{Text: "brief", Channels: []string{"sms", "email"}, Languages: []string{"tr"}}, func(string) {
		calls++
		if calls == 2 {
			cancel() // the browser went away
		}
	})

	if !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatalf("want context.Canceled after 2 chunks, got %v after %d", err, calls)
	}
}

func TestMockContent_ResendIsShorter(t *testing.T) {
	for _, l := range content.Languages {
		var normal, resend map[string]content.Content
		for _, r := range []struct {
			resend bool
			out    *map[string]content.Content
		}{{false, &normal}, {true, &resend}} {
			raw, err := NewMock().StreamContent(context.Background(), ContentInput{Text: "brief", Channels: []string{"sms", "email"}, Languages: []string{l.Code}, Resend: r.resend}, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			json.Unmarshal(raw, r.out)
		}
		if n, o := utf8.RuneCountInString(resend[l.Code].SMS.Body), utf8.RuneCountInString(normal[l.Code].SMS.Body); n >= o-20 {
			t.Errorf("%s: resend SMS (%d) should be clearly shorter than the original (%d)", l.Code, n, o)
		}
		if errs := content.ValidateDraft(resend[l.Code], []string{"sms", "email"}); len(errs) != 0 {
			t.Errorf("%s resend content invalid: %+v", l.Code, errs)
		}
	}
}
