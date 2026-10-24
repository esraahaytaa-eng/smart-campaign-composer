package content

import (
	"slices"
	"strings"
)

// Lang is a content language code (D64).
type Lang = string

// Language is one language content can be written in, named in itself.
type Language struct {
	Code Lang   `json:"code"`
	Name string `json:"name"`
}

// Languages is the single list of content languages (D64). Adding one also
// needs a migration widening the database checks and mock texts;
// TestLanguages_ConsistentEverywhere fails until all three agree.
var Languages = []Language{{"tr", "Türkçe"}, {"en", "English"}}

// LegacyLang labels content written before languages were recorded. It is
// sent to everyone and never offered for new content.
const LegacyLang Lang = "und"

// IsLanguage reports whether l is a catalog language.
func IsLanguage(l Lang) bool {
	return slices.ContainsFunc(Languages, func(x Language) bool { return x.Code == l })
}

// LanguageName is a language's own name; the legacy label has none.
func LanguageName(l Lang) string {
	for _, x := range Languages {
		if x.Code == l {
			return x.Name
		}
	}
	return "language not recorded"
}

// Variants holds a campaign's content per language.
type Variants map[Lang]Content

// ValidateVariants checks a campaign's content before it is saved or
// approved: at least one language, the default among them, catalog languages
// only (or the legacy label alone), and every language complete for the
// chosen channels. Text for channels that aren't chosen is kept but not
// checked (D67). Fields are prefixed with the language: "en.sms.body".
func ValidateVariants(v Variants, channels []string, def Lang) []FieldError {
	if len(v) == 0 {
		return []FieldError{{"content", "write the messages in at least one language"}}
	}
	var errs []FieldError
	if _, ok := v[def]; !ok {
		errs = append(errs, FieldError{"default_language", "the default language must be one of the campaign's languages"})
	}
	for lang, c := range v {
		switch {
		case lang == LegacyLang && len(v) > 1:
			errs = append(errs, FieldError{lang, "content without a recorded language can't be combined with other languages"})
		case lang != LegacyLang && !IsLanguage(lang):
			errs = append(errs, FieldError{lang, "unsupported language"})
		default:
			for _, e := range ValidateDraft(ForChannels(c, channels), channels) {
				errs = append(errs, FieldError{lang + "." + e.Field, e.Message})
			}
		}
	}
	slices.SortFunc(errs, func(a, b FieldError) int { return strings.Compare(a.Field, b.Field) })
	return errs
}

// ForChannels drops the text of channels that aren't chosen (kept, not sent: D67).
func ForChannels(c Content, channels []string) Content {
	if !slices.Contains(channels, "sms") {
		c.SMS = nil
	}
	if !slices.Contains(channels, "email") {
		c.Email = nil
	}
	return c
}

// ValidateVersionB checks an A/B test's version B (D68): only languages that
// have version A, each complete for the chosen channels. complete also
// requires every language of a to have version B, as approval does. Fields
// are prefixed with "b.": "b.en.sms.body".
func ValidateVersionB(a, b Variants, channels []string, complete bool) []FieldError {
	var errs []FieldError
	for lang, c := range b {
		if _, ok := a[lang]; !ok {
			errs = append(errs, FieldError{"b." + lang, "version B needs version A in the same language"})
			continue
		}
		for _, e := range ValidateDraft(ForChannels(c, channels), channels) {
			errs = append(errs, FieldError{"b." + lang + "." + e.Field, e.Message})
		}
	}
	if complete {
		for lang := range a {
			if _, ok := b[lang]; !ok {
				errs = append(errs, FieldError{"b." + lang, "write version B in this language"})
			}
		}
	}
	slices.SortFunc(errs, func(x, y FieldError) int { return strings.Compare(x.Field, y.Field) })
	return errs
}
