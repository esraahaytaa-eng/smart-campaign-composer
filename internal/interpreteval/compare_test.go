package interpreteval

import (
	"slices"
	"strings"
	"testing"
)

func runOf(prompt, dataset string, results ...Result) Run {
	return Run{Model: "claude-opus-5-5", PromptVersion: prompt, DatasetSHA256: dataset, Commit: "abc123",
		CostUSD: 2.5, Summary: Summarize(results), Results: results}
}

func res(id, outcome string) Result {
	return Result{ID: id, Lang: "en", Tags: []string{"single"}, Outcome: outcome, Calls: 1}
}

func TestCompare(t *testing.T) {
	a := runOf("p1", "d1", res("same", ExactMatch), res("fixed", RuleMismatch), res("regressed", ExactMatch),
		res("changed", RuleMismatch), res("gone", ExactMatch))
	b := runOf("p2", "d1", res("same", ExactMatch), res("fixed", ExactMatch), res("regressed", FalseSupported),
		res("changed", InvalidTwice), res("new", CorrectUnsupported))
	b.Results[4].WrongReasonLang = true
	b.Summary = Summarize(b.Results)
	b.CostUSD = 3
	out := Compare(a, b)
	for _, want := range []string{
		"prompt p1 → p2",
		"accuracy", "60.0%", "60.0%", "+0.0 pts",
		"false_supported", "0", "1", "+1",
		"cost_usd", "2.50", "3.00", "+0.50",
		"fixed      fixed  rule_mismatch → exact_match",
		"regressed  regressed  exact_match → false_supported",
		"changed    changed  rule_mismatch → invalid_twice",
		"only in A  gone",
		"only in B  new",
		"rerun",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compare misses %q:\n%s", want, out)
		}
	}
	if !slices.ContainsFunc(strings.Split(out, "\n"), func(l string) bool {
		return slices.Equal(strings.Fields(l), []string{"wrong_reason_lang", "0", "1", "+1"})
	}) {
		t.Errorf("want a wrong_reason_lang row 0 → 1:\n%s", out)
	}
	if strings.Contains(out, "  same  ") {
		t.Error("unchanged cases should not be listed")
	}
	if strings.Contains(out, "different datasets") {
		t.Error("same dataset should not warn")
	}
}

func TestCompare_Warnings(t *testing.T) {
	a := runOf("p", "d1", res("x", ExactMatch))
	b := runOf("p", "d2", res("x", ExactMatch))
	b.Skipped = []string{"y", "z"}
	out := Compare(a, b)
	for _, want := range []string{"different datasets", "B skipped 2 cases"} {
		if !strings.Contains(out, want) {
			t.Errorf("compare misses %q:\n%s", want, out)
		}
	}
}
