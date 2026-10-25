package interpreteval

import (
	"fmt"
	"slices"
	"strings"
)

// Fixed orders, so every summary has the same keys and reports read alike.
var (
	outcomes       = []string{ExactMatch, CorrectUnsupported, RuleMismatch, FalseUnsupported, FalseSupported, InvalidTwice, APIError}
	ruleErrorKinds = []string{Missing, Extra, WrongValue, WrongOp}
)

// Tally counts cases and how many of them were correct.
type Tally struct {
	Cases   int `json:"cases"`
	Correct int `json:"correct"`
}

func (t *Tally) add(correct bool) {
	t.Cases++
	if correct {
		t.Correct++
	}
}

func (t Tally) String() string {
	return fmt.Sprintf("%d/%d (%.1f%%)", t.Correct, t.Cases, percent(t.Correct, t.Cases))
}

// Summary is a run's metrics. Correct means exact_match or correct_unsupported.
type Summary struct {
	Cases               int              `json:"cases"`
	Correct             int              `json:"correct"`
	Accuracy            float64          `json:"accuracy"`
	AccuracyAnswered    float64          `json:"accuracy_answered"` // API errors left out
	ExpectedRules       Tally            `json:"expected_rules"`
	ExpectedUnsupported Tally            `json:"expected_unsupported"`
	Outcomes            map[string]int   `json:"outcomes"`
	RuleErrors          map[string]int   `json:"rule_errors"`
	Retried             int              `json:"retried"`
	Calls               int              `json:"calls"`
	WrongReasonLang     int              `json:"wrong_reason_lang"` // reasons not in the brief's language
	ByLang              map[string]Tally `json:"by_lang"`
	ByTag               map[string]Tally `json:"by_tag"`
}

func Summarize(results []Result) Summary {
	s := Summary{Outcomes: map[string]int{}, RuleErrors: map[string]int{}, ByLang: map[string]Tally{}, ByTag: map[string]Tally{}}
	for _, k := range outcomes {
		s.Outcomes[k] = 0
	}
	for _, k := range ruleErrorKinds {
		s.RuleErrors[k] = 0
	}
	for _, r := range results {
		ok := r.Correct()
		s.Cases++
		if ok {
			s.Correct++
		}
		if r.ExpectedUnsupported {
			s.ExpectedUnsupported.add(ok)
		} else {
			s.ExpectedRules.add(ok)
		}
		s.Outcomes[r.Outcome]++
		for _, e := range r.RuleErrors {
			s.RuleErrors[e.Kind]++
		}
		s.Calls += r.Calls
		if r.WrongReasonLang {
			s.WrongReasonLang++
		}
		if r.Calls > 1 {
			s.Retried++
		}
		lang := s.ByLang[r.Lang]
		lang.add(ok)
		s.ByLang[r.Lang] = lang
		for _, tag := range r.Tags {
			t := s.ByTag[tag]
			t.add(ok)
			s.ByTag[tag] = t
		}
	}
	s.Accuracy = ratio(s.Correct, s.Cases)
	s.AccuracyAnswered = ratio(s.Correct, s.Cases-s.Outcomes[APIError])
	return s
}

// Report is the human-readable summary plus one line per incorrect case.
func Report(results []Result) string {
	s := Summarize(results)
	var b strings.Builder
	answered := Tally{Cases: s.Cases - s.Outcomes[APIError], Correct: s.Correct}
	fmt.Fprintf(&b, "Exact-match accuracy: %s, without API errors: %s\n", Tally{s.Cases, s.Correct}, answered)
	fmt.Fprintf(&b, "Expected rules:       %s\n", s.ExpectedRules)
	fmt.Fprintf(&b, "Expected unsupported: %s\n", s.ExpectedUnsupported)
	fmt.Fprintf(&b, "Outcomes: %s\n", counts(outcomes, s.Outcomes))
	fmt.Fprintf(&b, "Rule errors: %s\n", counts(ruleErrorKinds, s.RuleErrors))
	fmt.Fprintf(&b, "Retried: %d of %d cases (%d AI calls)\n", s.Retried, s.Cases, s.Calls)
	fmt.Fprintf(&b, "By language: %s\n", tallies(s.ByLang))
	fmt.Fprintf(&b, "By tag: %s\n", tallies(s.ByTag))
	first := true
	for _, r := range results {
		if r.Correct() {
			continue
		}
		if first {
			b.WriteString("\nIncorrect cases:\n")
			first = false
		}
		fmt.Fprintf(&b, "  %s  %s  %s\n", r.ID, r.Outcome, detail(r))
	}
	if s.WrongReasonLang > 0 {
		fmt.Fprintf(&b, "\nReasons not in the brief's language: %d\n", s.WrongReasonLang)
		for _, r := range results {
			if r.WrongReasonLang {
				fmt.Fprintf(&b, "  %s  %s brief  %s\n", r.ID, r.Lang, r.Reason)
			}
		}
	}
	return b.String()
}

func detail(r Result) string {
	switch {
	case len(r.RuleErrors) > 0:
		var parts []string
		for _, e := range r.RuleErrors {
			op := e.Op
			if e.Kind == WrongOp {
				op += "→" + e.ActualOp
			}
			parts = append(parts, e.Kind+" "+e.Field+" "+op)
		}
		return strings.Join(parts, "; ")
	case r.Error != "":
		return r.Error
	case r.Outcome == FalseSupported:
		return fmt.Sprintf("%d rules returned", len(r.Rules))
	}
	return r.Reason
}

func counts(keys []string, m map[string]int) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, m[k])
	}
	return strings.Join(parts, ", ")
}

func tallies(m map[string]Tally) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + " " + m[k].String()
	}
	return strings.Join(parts, ", ")
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func percent(n, d int) float64 { return 100 * ratio(n, d) }
