package interpreteval

import (
	"encoding/json"
	"strings"
	"testing"
)

func sampleResults() []Result {
	return []Result{
		{ID: "a", Lang: "en", Tags: []string{"single"}, Outcome: ExactMatch, Calls: 1},
		{ID: "b", Lang: "en", Tags: []string{"multi"}, Outcome: RuleMismatch, Calls: 2, RuleErrors: []RuleError{
			{Kind: WrongOp, Field: "total_spend", Op: "gt", ActualOp: "gte"},
			{Kind: Missing, Field: "no_order", Op: "within"},
		}},
		{ID: "c", Lang: "tr", Tags: []string{"ambiguity"}, ExpectedUnsupported: true, Outcome: CorrectUnsupported, Calls: 1,
			Reason: "the brief names no audience", WrongReasonLang: true},
		{ID: "d", Lang: "tr", Tags: []string{"injection"}, ExpectedUnsupported: true, Outcome: FalseSupported, Calls: 1},
		{ID: "e", Lang: "en", Tags: []string{"multi"}, Outcome: FalseUnsupported, Calls: 1, Reason: "cannot express"},
		{ID: "f", Lang: "tr", Tags: []string{"single"}, Outcome: APIError, Calls: 1, Error: "AI request failed: 529"},
	}
}

func TestSummarize(t *testing.T) {
	s := Summarize(sampleResults())
	if s.Cases != 6 || s.Correct != 2 {
		t.Fatalf("want 2 of 6 correct, got %d of %d", s.Correct, s.Cases)
	}
	if s.Accuracy != 2.0/6 || s.AccuracyAnswered != 2.0/5 {
		t.Errorf("want accuracy 2/6 and 2/5 without API errors, got %v %v", s.Accuracy, s.AccuracyAnswered)
	}
	if s.ExpectedRules != (Tally{Cases: 4, Correct: 1}) || s.ExpectedUnsupported != (Tally{Cases: 2, Correct: 1}) {
		t.Errorf("want expectation tallies 1/4 and 1/2, got %+v %+v", s.ExpectedRules, s.ExpectedUnsupported)
	}
	if s.Outcomes[FalseSupported] != 1 || s.Outcomes[APIError] != 1 || s.Outcomes[InvalidTwice] != 0 {
		t.Errorf("unexpected outcomes %v", s.Outcomes)
	}
	if s.RuleErrors[WrongOp] != 1 || s.RuleErrors[Missing] != 1 {
		t.Errorf("unexpected rule errors %v", s.RuleErrors)
	}
	if s.Retried != 1 || s.Calls != 7 {
		t.Errorf("want 1 retried case and 7 calls, got %d %d", s.Retried, s.Calls)
	}
	if s.ByLang["en"] != (Tally{3, 1}) || s.ByLang["tr"] != (Tally{3, 1}) {
		t.Errorf("unexpected by-language %v", s.ByLang)
	}
	if s.ByTag["multi"] != (Tally{2, 0}) || s.ByTag["single"] != (Tally{2, 1}) {
		t.Errorf("unexpected by-tag %v", s.ByTag)
	}
}

// Every outcome and rule error kind appears, zero or not, so two saved runs
// always have the same keys to compare.
func TestSummarize_AllKeysPresent(t *testing.T) {
	s := Summarize(nil)
	b, _ := json.Marshal(s)
	for _, k := range append(outcomes, ruleErrorKinds...) {
		if !strings.Contains(string(b), `"`+k+`":0`) {
			t.Errorf("want %s present with 0 in %s", k, b)
		}
	}
	if s.Accuracy != 0 || s.AccuracyAnswered != 0 {
		t.Errorf("want 0 accuracy for no cases, not NaN: %v %v", s.Accuracy, s.AccuracyAnswered)
	}
}

func TestReport(t *testing.T) {
	out := Report(sampleResults())
	for _, want := range []string{
		"Exact-match accuracy: 2/6 (33.3%)",
		"without API errors: 2/5 (40.0%)",
		"Expected rules:       1/4 (25.0%)",
		"Expected unsupported: 1/2 (50.0%)",
		"false_supported 1",
		"wrong_op 1",
		"Retried: 1 of 6 cases (7 AI calls)",
		"en 1/3 (33.3%)",
		"multi 0/2 (0.0%)",
		"b  rule_mismatch  wrong_op total_spend gt→gte; missing no_order within",
		"e  false_unsupported  cannot express",
		"f  api_error  AI request failed: 529",
		"Reasons not in the brief's language: 1",
		"c  tr brief  the brief names no audience",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "a  exact_match") {
		t.Error("correct cases should not be listed as failures")
	}
}
