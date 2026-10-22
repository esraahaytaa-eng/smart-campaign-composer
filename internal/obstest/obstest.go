// Package obstest reads metric values from the default Prometheus registry in tests.
package obstest

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Value returns a counter's value, or a histogram's sample count, for the
// series with exactly these labels (name, value pairs). A series that was
// never recorded is 0. Tests compare values before and after an action.
func Value(t testing.TB, name string, labels ...string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if !hasLabels(m.GetLabel(), labels) {
				continue
			}
			if h := m.GetHistogram(); h != nil {
				return float64(h.GetSampleCount())
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

func hasLabels(got []*dto.LabelPair, want []string) bool {
	if len(got)*2 != len(want) {
		return false
	}
	for i := 0; i+1 < len(want); i += 2 {
		found := false
		for _, l := range got {
			if l.GetName() == want[i] && l.GetValue() == want[i+1] {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
