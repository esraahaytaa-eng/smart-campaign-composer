package campaign

import (
	"slices"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/content"
)

// recommendShare is the audience share from which a language is recommended
// (D64): a starting suggestion only; any catalog language can be chosen.
const recommendShare = 0.05

// recommend suggests the catalog languages with at least 5% of the audience,
// in catalog order, and the largest of them as the default (a tie keeps the
// catalog order).
func recommend(c audience.Counts) (langs []content.Lang, def content.Lang) {
	total := 0
	for _, l := range c.ByLanguage {
		total += l.Total
	}
	best := 0
	for _, l := range content.Languages {
		n := c.ByLanguage[l.Code].Total
		if total == 0 || float64(n) < recommendShare*float64(total) {
			continue
		}
		langs = append(langs, l.Code)
		if n > best {
			best, def = n, l.Code
		}
	}
	return langs, def
}

// recommendation is recommend's answer as the API returns it.
type recommendation struct {
	Languages []content.Lang `json:"languages"`
	Default   content.Lang   `json:"default"`
}

func newRecommendation(c audience.Counts) *recommendation {
	langs, def := recommend(c)
	return &recommendation{Languages: append([]content.Lang{}, langs...), Default: def}
}

// deliveryCount is how many recipients get one language's text: in their own
// language, or as the default because theirs has no content.
type deliveryCount struct {
	Own      int `json:"own"`
	Fallback int `json:"fallback"`
}

// delivery splits each chosen channel's audience by the language of the text
// they will get, as the worker chooses it (D64).
func delivery(c audience.Counts, channels []string, langs []content.Lang, def content.Lang) map[string]map[content.Lang]deliveryCount {
	out := map[string]map[content.Lang]deliveryCount{}
	for _, ch := range channels {
		out[ch] = map[content.Lang]deliveryCount{}
		for _, l := range langs {
			out[ch][l] = deliveryCount{}
		}
		for userLang, n := range c.ByLanguage {
			k := n.SMS
			if ch == "email" {
				k = n.Email
			}
			if k == 0 {
				continue
			}
			got, own := userLang, slices.Contains(langs, userLang)
			if !own {
				got = def
			}
			d := out[ch][got]
			if own {
				d.Own += k
			} else {
				d.Fallback += k
			}
			out[ch][got] = d
		}
	}
	return out
}
