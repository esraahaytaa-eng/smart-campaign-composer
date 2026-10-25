package interpreteval

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/campaign"
)

// Outcomes of one case. Only ExactMatch and CorrectUnsupported are correct.
const (
	ExactMatch         = "exact_match"
	CorrectUnsupported = "correct_unsupported"
	RuleMismatch       = "rule_mismatch"
	FalseUnsupported   = "false_unsupported" // rules expected, the AI said unsupported
	FalseSupported     = "false_supported"   // unsupported expected, the AI returned rules
	InvalidTwice       = "invalid_twice"     // failed validation on both tries (D15)
	APIError           = "api_error"         // timeout, outage, refusal to answer at all
)

// Rule error kinds.
const (
	Missing    = "missing"
	Extra      = "extra"
	WrongValue = "wrong_value"
	WrongOp    = "wrong_op" // same field, another operator
)

// Result is one scored case, with what the AI actually answered.
type Result struct {
	ID                  string          `json:"id"`
	Lang                string          `json:"lang"`
	Tags                []string        `json:"tags"`
	ExpectedUnsupported bool            `json:"expected_unsupported"`
	Outcome             string          `json:"outcome"`
	RuleErrors          []RuleError     `json:"rule_errors,omitempty"`
	Rules               []audience.Rule `json:"rules,omitempty"`
	Reason              string          `json:"reason,omitempty"`
	Error               string          `json:"error,omitempty"`
	Calls               int             `json:"calls"` // AI calls used; 2 means the answer was retried (D15)
	// WrongReasonLang flags an unsupported reason not written in the brief's
	// language; the marketer reads it, but it does not change the outcome.
	WrongReasonLang bool `json:"wrong_reason_lang,omitempty"`
}

func (r Result) Correct() bool { return r.Outcome == ExactMatch || r.Outcome == CorrectUnsupported }

// RuleError is one difference between expected and actual rules. Field and
// Op name the expected rule (the actual one for Extra); ActualOp is set for
// WrongOp.
type RuleError struct {
	Kind     string          `json:"kind"`
	Field    string          `json:"field"`
	Op       string          `json:"op"`
	ActualOp string          `json:"actual_op,omitempty"`
	Expected json.RawMessage `json:"expected,omitempty"`
	Actual   json.RawMessage `json:"actual,omitempty"`
}

// Score compares one validated interpretation with the case's label. Rules
// match as a set keyed by field and operator, which D14 makes unique; values
// match after sorting their lists, so order never matters.
func Score(c Case, got campaign.Interpretation, err error) Result {
	r := Result{ID: c.ID, Lang: c.Lang, Tags: c.Tags, ExpectedUnsupported: c.Unsupported, Rules: got.Rules, Reason: got.Reason}
	if lang := reasonLanguage(got.Reason); lang != "" && lang != c.Lang {
		r.WrongReasonLang = true
	}
	switch {
	case errors.Is(err, campaign.ErrAIInvalid):
		r.Outcome, r.Error = InvalidTwice, err.Error()
	case err != nil:
		r.Outcome, r.Error = APIError, err.Error()
	case c.Unsupported && got.Unsupported:
		r.Outcome = CorrectUnsupported
	case c.Unsupported:
		r.Outcome = FalseSupported
	case got.Unsupported:
		r.Outcome = FalseUnsupported
	default:
		r.RuleErrors = compareRules(c.Rules, got.Rules)
		r.Outcome = ExactMatch
		if len(r.RuleErrors) > 0 {
			r.Outcome = RuleMismatch
		}
	}
	return r
}

// Common words that tell an English reason from a Turkish one. Quoted brief
// words or city names are outnumbered by the reason's own wording.
var languageWords = map[string]map[string]bool{
	"en": set("the a an and or but is are not no only for with to of in on cannot can does asks audience rule rules customers brief's"),
	"tr": set("ve veya ama bir bu için ile değil yok yalnızca sadece ancak gibi olarak da de hedef kitle kitlesi kural müşteri müşterileri istiyor belirtmiyor içeriyor olan"),
}

func set(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

// reasonLanguage guesses "en" or "tr" from common words, or "" if unclear.
func reasonLanguage(reason string) string {
	var en, tr int
	for _, w := range strings.FieldsFunc(strings.ToLower(reason), func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' }) {
		if languageWords["en"][w] {
			en++
		}
		if languageWords["tr"][w] {
			tr++
		}
	}
	switch {
	case en > tr:
		return "en"
	case tr > en:
		return "tr"
	}
	return ""
}

// compareRules lists differences in the expected rules' order, then extras
// in the actual order. A missing rule and an extra one on the same field
// become one WrongOp.
func compareRules(expected, actual []audience.Rule) []RuleError {
	key := func(r audience.Rule) string { return r.Field + " " + r.Op }
	byKey := map[string]audience.Rule{}
	for _, a := range actual {
		byKey[key(a)] = a
	}
	used := map[string]bool{}
	var errs []RuleError
	var missing []audience.Rule
	for _, e := range expected {
		a, ok := byKey[key(e)]
		if !ok {
			missing = append(missing, e)
			continue
		}
		used[key(e)] = true
		if !sameValue(e.Value, a.Value) {
			errs = append(errs, RuleError{Kind: WrongValue, Field: e.Field, Op: e.Op, Expected: e.Value, Actual: a.Value})
		}
	}
	for _, e := range missing {
		i := slices.IndexFunc(actual, func(a audience.Rule) bool { return a.Field == e.Field && !used[key(a)] })
		if i < 0 {
			errs = append(errs, RuleError{Kind: Missing, Field: e.Field, Op: e.Op, Expected: e.Value})
			continue
		}
		used[key(actual[i])] = true
		errs = append(errs, RuleError{Kind: WrongOp, Field: e.Field, Op: e.Op, ActualOp: actual[i].Op, Expected: e.Value, Actual: actual[i].Value})
	}
	for _, a := range actual {
		if !used[key(a)] {
			errs = append(errs, RuleError{Kind: Extra, Field: a.Field, Op: a.Op, Actual: a.Value})
		}
	}
	return errs
}

// sameValue compares two rule values as JSON: numbers by value, object keys
// in any order, and lists (cities, categories) in any order.
func sameValue(a, b json.RawMessage) bool {
	na, errA := normalize(a)
	nb, errB := normalize(b)
	return errA == nil && errB == nil && bytes.Equal(na, nb)
}

func normalize(raw json.RawMessage) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(sortLists(v)) // Marshal sorts object keys
}

func sortLists(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = sortLists(x)
		}
	case []any:
		for i, x := range t {
			t[i] = sortLists(x)
		}
		slices.SortFunc(t, func(x, y any) int {
			bx, _ := json.Marshal(x)
			by, _ := json.Marshal(y)
			return bytes.Compare(bx, by)
		})
	}
	return v
}
