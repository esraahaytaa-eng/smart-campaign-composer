package content_test

import (
	"slices"
	"testing"

	"smart-campaign-composer/internal/content"
)

var (
	sms   = &content.Message{Body: "Merhaba {{first_name}}!"}
	email = &content.Message{Subject: "İndirim", Body: "Merhaba {{first_name}}."}
	both  = []string{"sms", "email"}
)

func fields(errs []content.FieldError) []string {
	var f []string
	for _, e := range errs {
		f = append(f, e.Field)
	}
	return f
}

func TestIsLanguage_CatalogOnly(t *testing.T) {
	t.Parallel()
	if !content.IsLanguage("tr") || !content.IsLanguage("en") {
		t.Fatal("tr and en are catalog languages")
	}
	for _, l := range []string{"und", "de", "", "TR"} {
		if content.IsLanguage(l) {
			t.Errorf("%q must not be a catalog language", l)
		}
	}
}

func TestValidateVariants(t *testing.T) {
	t.Parallel()
	full := content.Content{SMS: sms, Email: email}
	for _, tc := range []struct {
		name     string
		v        content.Variants
		channels []string
		def      string
		want     []string
	}{
		{"only English valid", content.Variants{"en": full}, both, "en", nil},
		{"two languages valid", content.Variants{"tr": full, "en": full}, both, "tr", nil},
		{"legacy alone valid", content.Variants{"und": full}, both, "und", nil},
		{"none", content.Variants{}, both, "", []string{"content"}},
		{"default missing", content.Variants{"tr": full}, both, "en", []string{"default_language"}},
		{"no default", content.Variants{"tr": full}, both, "", []string{"default_language"}},
		{"unknown language", content.Variants{"tr": full, "de": full}, both, "tr", []string{"de"}},
		{"und mixed", content.Variants{"und": full, "en": full}, both, "en", []string{"und"}},
		{"incomplete second language", content.Variants{"tr": full, "en": {SMS: sms}}, both, "tr", []string{"en.email"}},
		{"invalid text named by language", content.Variants{"en": {SMS: &content.Message{Body: "Hi {{email}}"}}}, []string{"sms"}, "en", []string{"en.sms.body"}},
		{"excluded channel ignored", content.Variants{"tr": {SMS: sms, Email: &content.Message{Body: "{{bad}}"}}}, []string{"sms"}, "tr", nil},
	} {
		if got := fields(content.ValidateVariants(tc.v, tc.channels, tc.def)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: want errors on %v, got %v", tc.name, tc.want, got)
		}
	}
}
