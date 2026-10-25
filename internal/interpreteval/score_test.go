package interpreteval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/campaign"
)

func rules(t *testing.T, s string) []audience.Rule {
	t.Helper()
	var r []audience.Rule
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func supported(t *testing.T, expected string) Case {
	return Case{ID: "c", Lang: "en", Tags: []string{"multi"}, Rules: rules(t, expected)}
}

func unsupportedCase() Case {
	return Case{ID: "u", Lang: "tr", Tags: []string{"ambiguity"}, Unsupported: true}
}

func got(t *testing.T, s string) campaign.Interpretation {
	return campaign.Interpretation{Rules: rules(t, s)}
}

const threeRules = `[{"field":"city","op":"in","value":["Ankara","İstanbul"]},
	{"field":"cart_added","op":"within","value":{"last_days":7}},
	{"field":"no_order","op":"within","value":{"last_days":7}}]`

func TestScore_ExactMatchIgnoresOrder(t *testing.T) {
	// Rules in another order, cities in another order, keys in another order.
	actual := `[{"field":"no_order","op":"within","value":{"last_days":7}},
		{"field":"city","op":"in","value":["İstanbul","Ankara"]},
		{"field":"cart_added","op":"within","value":{"last_days":7}}]`
	r := Score(supported(t, threeRules), got(t, actual), nil)
	if r.Outcome != ExactMatch || len(r.RuleErrors) != 0 {
		t.Fatalf("want exact match, got %s %+v", r.Outcome, r.RuleErrors)
	}
	if len(r.Rules) != 3 {
		t.Errorf("want the actual rules recorded, got %v", r.Rules)
	}
}

func TestScore_NumbersCompareByValue(t *testing.T) {
	r := Score(supported(t, `[{"field":"total_spend","op":"gt","value":20000}]`),
		got(t, `[{"field":"total_spend","op":"gt","value":20000.0}]`), nil)
	if r.Outcome != ExactMatch {
		t.Fatalf("want exact match, got %s %+v", r.Outcome, r.RuleErrors)
	}
}

func TestScore_RuleErrors(t *testing.T) {
	tests := map[string]struct {
		actual string
		want   []string // kind field op
	}{
		"missing": {`[{"field":"city","op":"in","value":["Ankara","İstanbul"]},
			{"field":"cart_added","op":"within","value":{"last_days":7}}]`,
			[]string{"missing no_order within"}},
		"extra": {`[{"field":"city","op":"in","value":["Ankara","İstanbul"]},
			{"field":"cart_added","op":"within","value":{"last_days":7}},
			{"field":"no_order","op":"within","value":{"last_days":7}},
			{"field":"order_count","op":"gte","value":1}]`,
			[]string{"extra order_count gte"}},
		"wrong value": {`[{"field":"city","op":"in","value":["Ankara"]},
			{"field":"cart_added","op":"within","value":{"last_days":7}},
			{"field":"no_order","op":"within","value":{"last_days":30}}]`,
			[]string{"wrong_value city in", "wrong_value no_order within"}},
		"extra on another field": {`[{"field":"city","op":"in","value":["Ankara","İstanbul"]},
			{"field":"cart_added","op":"within","value":{"last_days":7}},
			{"field":"no_order","op":"within","value":{"last_days":7}},
			{"field":"signed_up","op":"within","value":{"last_days":7}}]`,
			[]string{"extra signed_up within"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := Score(supported(t, threeRules), got(t, tc.actual), nil)
			if r.Outcome != RuleMismatch {
				t.Fatalf("want rule mismatch, got %s", r.Outcome)
			}
			var kinds []string
			for _, e := range r.RuleErrors {
				kinds = append(kinds, e.Kind+" "+e.Field+" "+e.Op)
			}
			if strings.Join(kinds, ",") != strings.Join(tc.want, ",") {
				t.Errorf("want %v, got %v", tc.want, kinds)
			}
		})
	}
}

func TestScore_WrongOperatorPairsMissingAndExtra(t *testing.T) {
	r := Score(supported(t, `[{"field":"total_spend","op":"gt","value":500}]`),
		got(t, `[{"field":"total_spend","op":"gte","value":500}]`), nil)
	if r.Outcome != RuleMismatch || len(r.RuleErrors) != 1 {
		t.Fatalf("want one rule error, got %s %+v", r.Outcome, r.RuleErrors)
	}
	e := r.RuleErrors[0]
	if e.Kind != WrongOp || e.Field != "total_spend" || e.Op != "gt" || e.ActualOp != "gte" {
		t.Errorf("want wrong_op gt→gte, got %+v", e)
	}
	if string(e.Expected) != "500" || string(e.Actual) != "500" {
		t.Errorf("want both values recorded, got %s / %s", e.Expected, e.Actual)
	}
}

// Two rules on one field (order_count gte and lte): only the unmatched one
// is paired, the matching one is left alone.
func TestScore_WrongOperatorWithSameFieldTwice(t *testing.T) {
	expected := `[{"field":"order_count","op":"gte","value":1},{"field":"order_count","op":"lte","value":2}]`
	actual := `[{"field":"order_count","op":"gte","value":1},{"field":"order_count","op":"lt","value":3}]`
	r := Score(supported(t, expected), got(t, actual), nil)
	if len(r.RuleErrors) != 1 || r.RuleErrors[0].Kind != WrongOp || r.RuleErrors[0].Op != "lte" {
		t.Fatalf("want one wrong_op lte→lt, got %+v", r.RuleErrors)
	}
}

func TestScore_Outcomes(t *testing.T) {
	one := `[{"field":"city","op":"in","value":["Ankara"]}]`
	tests := []struct {
		name string
		c    Case
		got  campaign.Interpretation
		err  error
		want string
	}{
		{"correct unsupported", unsupportedCase(), campaign.Interpretation{Unsupported: true, Reason: "r"}, nil, CorrectUnsupported},
		{"false supported", unsupportedCase(), got(t, one), nil, FalseSupported},
		{"false unsupported", supported(t, one), campaign.Interpretation{Unsupported: true, Reason: "r"}, nil, FalseUnsupported},
		{"invalid twice", supported(t, one), campaign.Interpretation{}, fmt.Errorf("%w: rule 0: x", campaign.ErrAIInvalid), InvalidTwice},
		{"invalid twice on unsupported case", unsupportedCase(), campaign.Interpretation{}, campaign.ErrAIInvalid, InvalidTwice},
		{"api error", supported(t, one), campaign.Interpretation{}, errors.New("AI request failed: 529"), APIError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := Score(tc.c, tc.got, tc.err)
			if r.Outcome != tc.want {
				t.Fatalf("want %s, got %s", tc.want, r.Outcome)
			}
			if tc.err != nil && r.Error == "" {
				t.Error("want the error recorded")
			}
		})
	}
}

func TestScore_RecordsReason(t *testing.T) {
	r := Score(unsupportedCase(), campaign.Interpretation{Unsupported: true, Reason: "yaş alanı yok"}, nil)
	if r.Reason != "yaş alanı yok" || r.ID != "u" || r.Lang != "tr" || r.Tags[0] != "ambiguity" || !r.ExpectedUnsupported {
		t.Errorf("want case identity and reason recorded, got %+v", r)
	}
}

func TestCorrect(t *testing.T) {
	for outcome, want := range map[string]bool{
		ExactMatch: true, CorrectUnsupported: true,
		RuleMismatch: false, FalseSupported: false, FalseUnsupported: false, InvalidTwice: false, APIError: false,
	} {
		if (Result{Outcome: outcome}).Correct() != want {
			t.Errorf("%s: want correct=%v", outcome, want)
		}
	}
}

// A perfect answer to every committed case scores 100%: the labels and the
// comparison agree on canonical form.
func TestScore_DatasetLabelsScorePerfect(t *testing.T) {
	data, err := os.ReadFile("../../evals/interpret/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	ds, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	var results []Result
	for _, c := range ds.Cases {
		answer := campaign.Interpretation{Unsupported: c.Unsupported, Reason: "r", Rules: c.Rules}
		results = append(results, Score(c, answer, nil))
	}
	if s := Summarize(results); s.Correct != s.Cases {
		t.Errorf("want all correct, got:\n%s", Report(results))
	}
}

func TestReasonLanguage(t *testing.T) {
	for reason, want := range map[string]string{
		// The English injection brief answered in Turkish in the 2026-10-25 run.
		"Brief yalnızca talimatları geçersiz kılma girişimi içeriyor ve veritabanındaki tüm kullanıcıları hedeflemeyi istiyor; pazarlamacının tanımladığı geçerli bir hedef kitle yok.": "tr",
		"Brief bir hedef kitle belirtmiyor; yalnızca yeni sezon ürünlerini tanıtan bir kampanya gönderilmesini istiyor.":                                                                "tr",
		"The brief contains only an attempt to override the instructions and describes no audience.":                                                                                    "en",
		// Quoted Turkish words must not tip an English reason.
		"The brief asks for customers in Diyarbakır who said \"tüm müşteriler\", but age is not a supported field.": "en",
		"Ankara": "",
		"":       "",
	} {
		if got := reasonLanguage(reason); got != want {
			t.Errorf("reasonLanguage(%q) = %q, want %q", reason, got, want)
		}
	}
}

func TestScore_FlagsReasonInAnotherLanguage(t *testing.T) {
	en := Case{ID: "en", Lang: "en", Tags: []string{"injection"}, Unsupported: true}
	tr := Case{ID: "tr", Lang: "tr", Tags: []string{"injection"}, Unsupported: true}
	turkish := "Brief yalnızca talimatları geçersiz kılma girişimi içeriyor; geçerli bir hedef kitle yok."
	english := "The brief only tries to override the instructions and names no audience."
	for _, tc := range []struct {
		c      Case
		reason string
		want   bool
	}{{en, turkish, true}, {tr, english, true}, {en, english, false}, {tr, turkish, false}} {
		r := Score(tc.c, campaign.Interpretation{Unsupported: true, Reason: tc.reason}, nil)
		if r.WrongReasonLang != tc.want || r.Outcome != CorrectUnsupported {
			t.Errorf("%s brief, reason %q: want flagged=%v and still correct, got %v %s", tc.c.Lang, tc.reason, tc.want, r.WrongReasonLang, r.Outcome)
		}
	}
}
