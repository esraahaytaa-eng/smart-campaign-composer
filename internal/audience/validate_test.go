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
		rule("signed_up", "before", `{"last_days": 7}`),
		rule("order_count", "gt", `3`),
		rule("order_count", "lt", `10`),
		rule("total_spend", "gt", `10000`),
		rule("total_spend", "lt", `500`),
		rule("cart_value", "gt", `100`),
		rule("spend_in_window", "gt", `{"amount": 10000, "last_months": 12}`),
		rule("spend_in_window", "lte", `{"amount": 500.5, "from": "2026-01-01", "to": "2026-06-30"}`),
		rule("orders_in_window", "gte", `{"count": 4, "last_days": 365}`),
		rule("orders_in_window", "lt", `{"count": 2, "last_months": 1}`),
		// Explicit null window keys mean "not set".
		rule("no_order", "within", `{"last_days": 45, "last_months": null, "from": null, "to": null}`),
	}

	for _, part := range [][]audience.Rule{rules[:13], rules[13:]} { // at most 20 rules per audience
		if _, errs := audience.Validate(part, catalog); len(errs) != 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
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
		{"months=0", rule("cart_added", "within", `{"last_months": 0}`), "between 1 and 12"},
		{"months=13", rule("cart_added", "within", `{"last_months": 13}`), "between 1 and 12"},
		{"months not integer", rule("cart_added", "within", `{"last_months": 1.5}`), "value"},
		{"months and days", rule("cart_added", "within", `{"last_months": 1, "last_days": 3}`), "either"},
		{"months with to", rule("cart_added", "within", `{"last_months": 1, "to": "2026-10-01"}`), "either"},
		{"signed_up before with to", rule("signed_up", "before", `{"from": "2026-01-01", "to": "2026-02-01"}`), "to"},
		{"cart_value lt", rule("cart_value", "lt", `100`), "operator"},
		{"spend window without amount", rule("spend_in_window", "gte", `{"last_months": 12}`), "amount"},
		{"spend window without window", rule("spend_in_window", "gte", `{"amount": 100}`), "either"},
		{"spend window negative", rule("spend_in_window", "gte", `{"amount": -1, "last_days": 30}`), "between 0 and 1000000"},
		{"spend window as number", rule("spend_in_window", "gte", `100`), "value"},
		{"orders window without count", rule("orders_in_window", "gte", `{"last_days": 30}`), "count"},
		{"orders window count not integer", rule("orders_in_window", "gte", `{"count": 4.5, "last_days": 30}`), "value"},
		{"orders window count too big", rule("orders_in_window", "gte", `{"count": 10001, "last_days": 30}`), "between 0 and 10000"},
		{"spend window null amount", rule("spend_in_window", "gte", `{"amount": null, "last_days": 30}`), "amount"},
		{"months and from", rule("cart_added", "within", `{"last_months": 1, "from": "2026-10-01"}`), "either"},
		{"repeated key", rule("cart_added", "within", `{"last_days": 30, "last_days": 90}`), "repeated key"},
		{"repeated nested key", rule("ordered_category", "in", `{"categories": ["books"], "last_days": 30, "categories": ["home"]}`), "repeated key"},
		{"orders window unknown key", rule("orders_in_window", "gte", `{"count": 1, "amount": 5, "last_days": 30}`), "value"},
		{"all_customers false", rule("all_customers", "is", `false`), "true"},
		{"all_customers not a boolean", rule("all_customers", "is", `"yes"`), "value"},
		{"all_customers bad op", rule("all_customers", "in", `true`), "operator"},
		// Disguised match-all conditions: every customer has at least 0 (D14).
		{"order_count >= 0", rule("order_count", "gte", `0`), "matches every customer"},
		{"total_spend >= 0", rule("total_spend", "gte", `0`), "matches every customer"},
		{"cart_value >= 0", rule("cart_value", "gte", `0`), "matches every customer"},
		{"spend_in_window >= 0", rule("spend_in_window", "gte", `{"amount": 0, "last_months": 12}`), "matches every customer"},
		{"orders_in_window >= 0", rule("orders_in_window", "gte", `{"count": 0, "last_days": 30}`), "matches every customer"},
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

// D14: everyone is chosen explicitly, as the only rule; an empty list stays an error.
func TestValidate_AllCustomers(t *testing.T) {
	got, errs := audience.Validate([]audience.Rule{rule("all_customers", "is", `true`)}, catalog)
	if len(errs) > 0 || len(got) != 1 || string(got[0].Value) != "true" {
		t.Fatalf("want all_customers accepted alone, got %v %v", got, errs)
	}
	_, errs = audience.Validate([]audience.Rule{rule("city", "in", `["Ankara"]`), rule("all_customers", "is", `true`)}, catalog)
	if len(errs) != 1 || errs[0].Rule != 1 || !strings.Contains(errs[0].Message, "only rule") {
		t.Fatalf("want all_customers refused beside another rule, got %v", errs)
	}
	if _, errs := audience.Validate(nil, catalog); len(errs) != 1 || errs[0].Rule != -1 {
		t.Fatalf("an empty list must stay an error, got %v", errs)
	}
}

// Conditions next to the match-all ones are real filters and stay allowed.
func TestValidate_KeepsFiltersNextToMatchAll(t *testing.T) {
	for _, r := range []audience.Rule{
		rule("order_count", "gt", `0`),
		rule("order_count", "lte", `0`),
		rule("order_count", "lt", `1`),
		rule("total_spend", "gt", `0`),
		rule("total_spend", "lte", `0`),
		rule("total_spend", "gte", `0.01`),
		rule("cart_value", "gt", `0`),
		rule("spend_in_window", "gt", `{"amount": 0, "last_days": 30}`),
		rule("spend_in_window", "lte", `{"amount": 0, "last_days": 30}`),
		rule("orders_in_window", "gte", `{"count": 1, "last_days": 30}`),
		rule("orders_in_window", "lt", `{"count": 1, "last_days": 30}`),
	} {
		if _, errs := audience.Validate([]audience.Rule{r}, catalog); len(errs) > 0 {
			t.Errorf("%s %s %s: %v", r.Field, r.Op, r.Value, errs)
		}
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
		rule("spend_in_window", "gte", `{"amount": 100, "from": "2026-09-01"}`),
		rule("orders_in_window", "gte", `{"count": 1, "last_months": 3}`),
		// "before" a future date is everyone so far, not an empty window.
		rule("signed_up", "before", `{"from": "2026-12-01"}`),
		rule("last_order", "before", `{"from": "2026-12-01"}`),
	}

	starts, err := audience.FixedWindowStarts(rules, istanbul)

	want := []time.Time{time.Date(2026, 11, 1, 0, 0, 0, 0, istanbul), time.Date(2026, 10, 1, 0, 0, 0, 0, istanbul), time.Date(2026, 9, 1, 0, 0, 0, 0, istanbul)}
	if err != nil || len(starts) != 3 || !starts[0].Equal(want[0]) || !starts[1].Equal(want[1]) || !starts[2].Equal(want[2]) {
		t.Fatalf("want %v, got %v (%v)", want, starts, err)
	}
}

// Fields is what the chip editor builds rules from (D39): a value of each
// declared type must pass Validate for every operator, so the two can't drift.
func TestFields_DescribeWhatValidateAccepts(t *testing.T) {
	samples := map[string][]string{
		audience.ValueCities:           {`["Ankara"]`},
		audience.ValueWindow:           {`{"last_days":30}`, `{"last_months":12}`, `{"from":"2026-10-01"}`},
		audience.ValueAmountWindow:     {`{"amount":250.5,"last_months":12}`, `{"amount":1,"from":"2026-10-01"}`},
		audience.ValueCountWindow:      {`{"count":4,"last_days":90}`},
		audience.ValueCategoriesWindow: {`{"categories":["books"],"last_days":30}`, `{"categories":["books"],"from":"2026-10-01"}`},
		audience.ValueCount:            {`3`},
		audience.ValueAmount:           {`250.5`},
		audience.ValueAll:              {`true`},
	}
	fields := audience.Fields()
	if len(fields) != 13 {
		t.Fatalf("want the 13 catalog fields, got %d", len(fields))
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

// The catalog hands out copies: a caller changing them cannot change validation.
func TestFieldsAndLimits_AreCopies(t *testing.T) {
	audience.Fields()[0].Ops[0] = "changed"
	limits := audience.Limits()
	limits["max_rules"] = 0

	if f := audience.Fields()[0]; f.Ops[0] == "changed" {
		t.Fatalf("Fields shares its operator slices: %+v", f)
	}
	if l := audience.Limits(); l["max_rules"] != 20 || l["max_months"] != 12 {
		t.Fatal("Limits shares its map")
	}
	if _, errs := audience.Validate([]audience.Rule{rule("cart_added", "within", `{"last_days":30}`)}, catalog); len(errs) > 0 {
		t.Fatalf("validation changed: %v", errs)
	}
}
