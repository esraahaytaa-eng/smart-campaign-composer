package campaign

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxNameChars     = 100
	defaultNameChars = 60
)

var errName = errors.New("the name must be 1 to 100 characters on one line")

// defaultName is a campaign's name until the marketer renames it (D42): the
// brief's first sentence, cut at a word to at most 60 characters.
func defaultName(brief string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(brief), "\n")
	r := []rune(strings.TrimSpace(line))
	for i, c := range r {
		// A '.' inside "10.000" is not the end of a sentence.
		if (c == '.' || c == '!' || c == '?') && (i+1 == len(r) || unicode.IsSpace(r[i+1])) {
			r = r[:i+1]
			break
		}
	}
	if len(r) == 0 {
		return "Untitled campaign"
	}
	return truncate(r, defaultNameChars)
}

// resendName names a resend after its parent, within the name limit.
func resendName(parent, channel string) string {
	label := map[string]string{"sms": "SMS", "email": "Email"}[channel]
	suffix := " · resend (" + label + ")"
	return truncate([]rune(parent), maxNameChars-utf8.RuneCountInString(suffix)) + suffix
}

// truncate keeps r within max runes, cutting at a word and ending with "…".
func truncate(r []rune, max int) string {
	if len(r) <= max {
		return string(r)
	}
	cut := r[:max-1]
	if !unicode.IsSpace(r[max-1]) {
		if i := lastSpace(cut); i > 0 {
			cut = cut[:i]
		}
	}
	return strings.TrimRightFunc(string(cut), unicode.IsSpace) + "…"
}

func lastSpace(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if unicode.IsSpace(r[i]) {
			return i
		}
	}
	return -1
}

// checkName trims a name and enforces its limits, counted in characters (D2).
func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n == 0 || n > maxNameChars || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errName
	}
	return name, nil
}
