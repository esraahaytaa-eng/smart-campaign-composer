package audience_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"smart-campaign-composer/internal/audience"
)

var catalog = audience.Catalog{
	Cities:     []string{"İstanbul", "Ankara", "İzmir", "Eskişehir", "Muğla"},
	Categories: []string{"electronics", "fashion", "home", "beauty", "sports", "books"},
}

func rule(field, op, value string) audience.Rule {
	return audience.Rule{Field: field, Op: op, Value: json.RawMessage(value)}
}

func TestValidate_AcceptsEveryCatalogRule(t *testing.T) {
	rules := []audience.Rule{
		rule("city", "in", `["Ankara"]`),
		rule("cart_added", "within", `{"last_days": 30}`),
		rule("no_order", "within", `{"from": "2026-10-01", "to": null}`),
		rule("order_count", "gte", `2`),
		rule("order_count", "lte", `10`),
		rule("total_spend", "gte", `1000.50`),
		rule("total_spend", "lte", `1000000`),
		rule("last_order", "before", `{"last_days": 90}`),
		rule("last_order", "after", `{"from": "2026-01-01", "to": "2026-03-31"}`),
		rule("ordered_category", "in", `{"categories": ["books"], "last_days": 30}`),
		rule("carted_category", "in", `{"categories": ["electronics", "home"], "from": "2026-10-01"}`),
		rule("cart_value", "gte", `5000`),
		rule("signed_up", "within", `{"last_days": 7}`),
	}

	_, errs := audience.Validate(rules, catalog)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestValidate_Rejects(t *testing.T) {
	cases := []struct {
		name string
		rule audience.Rule
		want string
	}{
		{"unknown field", rule("age", "gte", `30`), "unknown field"},
		{"bad op", rule("city", "eq", `["Ankara"]`), "operator"},
		{"N=0", rule("cart_added", "within", `{"last_days": 0}`), "between 1 and 365"},
		{"N=366", rule("cart_added", "within", `{"last_days": 366}`), "between 1 and 365"},
		{"N not integer", rule("cart_added", "within", `{"last_days": 7.5}`), "value"},
		{"unknown city", rule("city", "in", `["Paris"]`), "unknown city"},
		{"empty city list", rule("city", "in", `[]`), "at least one"},
		{"city not a list", rule("city", "in", `"Ankara"`), "value"},
		{"from after to", rule("signed_up", "within", `{"from": "2026-10-10", "to": "2026-10-01"}`), "from"},
		{"from before 2000", rule("signed_up", "within", `{"from": "1999-12-31"}`), "2000-01-01"},
		{"bad date", rule("signed_up", "within", `{"from": "01.10.2026"}`), "YYYY-MM-DD"},
		{"both window forms", rule("cart_added", "within", `{"last_days": 3, "from": "2026-10-01"}`), "either"},
		{"no window", rule("cart_added", "within", `{}`), "either"},
		{"unknown key", rule("cart_added", "within", `{"last_days": 3, "days": 4}`), "value"},
		{"unknown category", rule("ordered_category", "in", `{"categories": ["toys"], "last_days": 30}`), "unknown category"},
		{"no categories", rule("carted_category", "in", `{"categories": [], "last_days": 30}`), "at least one"},
		{"negative amount", rule("total_spend", "gte", `-1`), "between 0 and 1000000"},
		{"amount too big", rule("cart_value", "gte", `1000001`), "between 0 and 1000000"},
		{"negative count", rule("order_count", "gte", `-1`), "between 0 and 10000"},
		{"last_order before with to", rule("last_order", "before", `{"from": "2026-01-01", "to": "2026-02-01"}`), "to"},
		{"language reserved", rule("language", "in", `["tr"]`), "reserved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := audience.Validate([]audience.Rule{tc.rule}, catalog)
			if len(errs) != 1 || errs[0].Rule != 0 || !strings.Contains(errs[0].Message, tc.want) {
				t.Fatalf("want one error on rule 0 containing %q, got %v", tc.want, errs)
			}
		})
	}
}

func TestValidate_NormalizesCity(t *testing.T) {
	for _, in := range []string{"Istanbul", "istanbul", "ISTANBUL", "İstanbul", " istanbul "} {
		got, errs := audience.Validate([]audience.Rule{rule("city", "in", fmt.Sprintf("[%q]", in))}, catalog)
		if len(errs) != 0 {
			t.Fatalf("%q: %v", in, errs)
		}
		if string(got[0].Value) != `["İstanbul"]` {
			t.Fatalf("%q: want canonical İstanbul, got %s", in, got[0].Value)
		}
	}
	got, _ := audience.Validate([]audience.Rule{rule("city", "in", `["eskisehir", "MUGLA"]`)}, catalog)
	if string(got[0].Value) != `["Eskişehir","Muğla"]` {
		t.Fatalf("got %s", got[0].Value)
	}
}

func TestValidate_NormalizesCategoryCase(t *testing.T) {
	got, errs := audience.Validate([]audience.Rule{rule("ordered_category", "in", `{"categories": ["Books"], "last_days": 30}`)}, catalog)
	if len(errs) != 0 || string(got[0].Value) != `{"categories":["books"],"last_days":30}` {
		t.Fatalf("got %s, %v", got[0].Value, errs)
	}
}

func TestValidate_ListRules(t *testing.T) {
	if _, errs := audience.Validate(nil, catalog); len(errs) != 1 || errs[0].Rule != -1 || !strings.Contains(errs[0].Message, "at least one") {
		t.Fatalf("empty list: want list error, got %v", errs)
	}

	var many []audience.Rule
	for range 21 {
		many = append(many, rule("city", "in", `["Ankara"]`))
	}
	if _, errs := audience.Validate(many, catalog); len(errs) == 0 || errs[0].Rule != -1 || !strings.Contains(errs[0].Message, "20") {
		t.Fatalf("21 rules: want list error, got %v", errs)
	}

	dup := []audience.Rule{rule("order_count", "gte", `1`), rule("order_count", "lte", `5`), rule("order_count", "gte", `2`)}
	if _, errs := audience.Validate(dup, catalog); len(errs) != 1 || errs[0].Rule != 2 || !strings.Contains(errs[0].Message, "duplicate") {
		t.Fatalf("duplicate: want error on rule 2, got %v", errs)
	}
}

func TestValidate_ReportsEveryBadRule(t *testing.T) {
	rules := []audience.Rule{rule("city", "in", `["Paris"]`), rule("cart_added", "within", `{"last_days": 30}`), rule("age", "gte", `1`)}

	_, errs := audience.Validate(rules, catalog)
	if len(errs) != 2 || errs[0].Rule != 0 || errs[1].Rule != 2 {
		t.Fatalf("want errors on rules 0 and 2, got %v", errs)
	}
}

func TestFixedWindowStarts(t *testing.T) {
	istanbul, _ := time.LoadLocation("Europe/Istanbul")
	rules := []audience.Rule{
		rule("city", "in", `["Ankara"]`),
		rule("cart_added", "within", `{"last_days": 30}`),
		rule("signed_up", "within", `{"from": "2026-11-01", "to": null}`),
		rule("ordered_category", "in", `{"categories": ["books"], "from": "2026-10-01", "to": "2026-10-05"}`),
	}

	starts, err := audience.FixedWindowStarts(rules, istanbul)

	want := []time.Time{time.Date(2026, 11, 1, 0, 0, 0, 0, istanbul), time.Date(2026, 10, 1, 0, 0, 0, 0, istanbul)}
	if err != nil || len(starts) != 2 || !starts[0].Equal(want[0]) || !starts[1].Equal(want[1]) {
		t.Fatalf("want %v, got %v (%v)", want, starts, err)
	}
}

// Fields is what the chip editor builds rules from (D39): a value of each
// declared type must pass Validate for every operator, so the two can't drift.
func TestFields_DescribeWhatValidateAccepts(t *testing.T) {
	samples := map[string][]string{
		audience.ValueCities:           {`["Ankara"]`},
		audience.ValueWindow:           {`{"last_days":30}`, `{"from":"2026-10-01"}`},
		audience.ValueCategoriesWindow: {`{"categories":["books"],"last_days":30}`, `{"categories":["books"],"from":"2026-10-01"}`},
		audience.ValueCount:            {`3`},
		audience.ValueAmount:           {`250.5`},
	}
	fields := audience.Fields()
	if len(fields) != 10 {
		t.Fatalf("want the 10 catalog fields, got %d", len(fields))
	}
	for i, f := range fields {
		if i > 0 && fields[i-1].Field >= f.Field {
			t.Errorf("fields must be sorted: %s before %s", fields[i-1].Field, f.Field)
		}
		values, ok := samples[f.Value]
		if !ok || len(f.Ops) == 0 {
			t.Errorf("%s: unknown value type %q or no operators", f.Field, f.Value)
			continue
		}
		for _, op := range f.Ops {
			for _, v := range values {
				if _, errs := audience.Validate([]audience.Rule{rule(f.Field, op, v)}, catalog); len(errs) > 0 {
					t.Errorf("%s %s %s: %v", f.Field, op, v, errs)
				}
			}
		}
	}
}
