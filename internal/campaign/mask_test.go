package campaign

import "testing"

func TestMaskContacts(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"+905551234517": "+90 555 ••• •• 17",
		"+447700900078": "••• 78", // not Turkish: last two digits only (a UK number reserved for fiction)
		"12345":         "•••",
		"":              "•••",
	} {
		if got := maskPhone(in); got != want {
			t.Errorf("maskPhone(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"ayse.demir.17@example.com": "ays•••@example.com",
		"ab@example.com":            "a•••@example.com", // never more than half
		"a@example.com":             "•••@example.com",
		"şükrü@örnek.com":           "şü•••@örnek.com",
		"no-at-sign":                "•••",
	} {
		if got := maskEmail(in); got != want {
			t.Errorf("maskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
