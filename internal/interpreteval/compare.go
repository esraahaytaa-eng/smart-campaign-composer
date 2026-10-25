package interpreteval

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"
)

// Run is one saved evaluation: what was tested, what it cost, and every
// case's result, so two runs can be compared later (D75).
type Run struct {
	Mode          string    `json:"mode"` // real, or mock for a free wiring check
	Model         string    `json:"model"`
	Effort        string    `json:"effort"`
	PromptVersion string    `json:"prompt_version"`
	DatasetSHA256 string    `json:"dataset_sha256"`
	Commit        string    `json:"commit"`
	StartedAt     time.Time `json:"started_at"`
	Seconds       float64   `json:"seconds"`
	InputTokens   int64     `json:"input_tokens"`
	OutputTokens  int64     `json:"output_tokens"`
	CostUSD       float64   `json:"cost_usd"`
	MaxUSD        float64   `json:"max_usd"`
	// The budget reserved this much per case; UnmeteredMaxUSD is the most that
	// failed calls, which report no tokens, may have cost on top of CostUSD.
	WorstCasePerCaseUSD float64  `json:"worst_case_per_case_usd"`
	UnmeteredMaxUSD     float64  `json:"unmetered_max_usd"`
	Skipped             []string `json:"skipped"` // not run: cost cap reached or interrupted
	Summary             Summary  `json:"summary"`
	Results             []Result `json:"results"`
}

// Compare shows how run b differs from run a: settings, metrics and the
// cases whose outcome changed.
func Compare(a, b Run) string {
	var out strings.Builder
	fmt.Fprintf(&out, "A: %s  %s  prompt %s  commit %s\n", a.StartedAt.Format(time.RFC3339), a.Model, a.PromptVersion, a.Commit)
	fmt.Fprintf(&out, "B: %s  %s  prompt %s  commit %s\n", b.StartedAt.Format(time.RFC3339), b.Model, b.PromptVersion, b.Commit)
	for _, d := range []struct{ name, a, b string }{
		{"model", a.Model, b.Model}, {"effort", a.Effort, b.Effort}, {"prompt", a.PromptVersion, b.PromptVersion},
	} {
		if d.a != d.b {
			fmt.Fprintf(&out, "Changed: %s %s → %s\n", d.name, d.a, d.b)
		}
	}
	if a.DatasetSHA256 != b.DatasetSHA256 {
		out.WriteString("Warning: different datasets; case changes may come from the labels, not the model.\n")
	}
	for _, r := range []struct {
		name    string
		skipped int
	}{{"A", len(a.Skipped)}, {"B", len(b.Skipped)}} {
		if r.skipped > 0 {
			fmt.Fprintf(&out, "Warning: %s skipped %d cases (cost cap or interrupted); its metrics cover fewer cases.\n", r.name, r.skipped)
		}
	}

	out.WriteString("\n")
	w := tabwriter.NewWriter(&out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "metric\tA\tB\tchange")
	pct := func(name string, x, y float64) {
		fmt.Fprintf(w, "%s\t%.1f%%\t%.1f%%\t%+.1f pts\n", name, 100*x, 100*y, 100*(y-x))
	}
	pct("accuracy", a.Summary.Accuracy, b.Summary.Accuracy)
	pct("accuracy_answered", a.Summary.AccuracyAnswered, b.Summary.AccuracyAnswered)
	tally := func(t Tally) float64 { return ratio(t.Correct, t.Cases) }
	pct("expected_rules", tally(a.Summary.ExpectedRules), tally(b.Summary.ExpectedRules))
	pct("expected_unsupported", tally(a.Summary.ExpectedUnsupported), tally(b.Summary.ExpectedUnsupported))
	count := func(name string, x, y int) { fmt.Fprintf(w, "%s\t%d\t%d\t%+d\n", name, x, y, y-x) }
	for _, k := range outcomes {
		count(k, a.Summary.Outcomes[k], b.Summary.Outcomes[k])
	}
	for _, k := range ruleErrorKinds {
		count("rule "+k, a.Summary.RuleErrors[k], b.Summary.RuleErrors[k])
	}
	count("retried", a.Summary.Retried, b.Summary.Retried)
	count("wrong_reason_lang", a.Summary.WrongReasonLang, b.Summary.WrongReasonLang)
	fmt.Fprintf(w, "cost_usd\t%.2f\t%.2f\t%+.2f\n", a.CostUSD, b.CostUSD, b.CostUSD-a.CostUSD)
	w.Flush()

	byID := map[string]Result{}
	for _, r := range b.Results {
		byID[r.ID] = r
	}
	var changes []string
	seen := map[string]bool{}
	for _, ra := range a.Results {
		seen[ra.ID] = true
		rb, ok := byID[ra.ID]
		switch {
		case !ok:
			changes = append(changes, "only in A  "+ra.ID)
		case ra.Outcome == rb.Outcome:
		default:
			kind := "changed  "
			if !ra.Correct() && rb.Correct() {
				kind = "fixed    "
			} else if ra.Correct() && !rb.Correct() {
				kind = "regressed"
			}
			changes = append(changes, fmt.Sprintf("%s  %s  %s → %s", kind, ra.ID, ra.Outcome, rb.Outcome))
		}
	}
	for _, rb := range b.Results {
		if !seen[rb.ID] {
			changes = append(changes, "only in B  "+rb.ID)
		}
	}
	if len(changes) == 0 {
		out.WriteString("\nNo case changed its outcome.\n")
		return out.String()
	}
	out.WriteString("\nChanged cases:\n")
	for _, c := range changes {
		out.WriteString("  " + c + "\n")
	}
	out.WriteString("\nEach case ran once per run; rerun a changed case before treating it as a real difference.\n")
	return out.String()
}
