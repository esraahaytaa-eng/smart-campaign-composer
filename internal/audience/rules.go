// Package audience validates audience rules and turns them into parameterized
// SQL (D14). Rules come from Claude or the marketer and are untrusted: values
// only ever reach the database as query arguments, never as SQL text.
package audience

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// Rule is one condition. All rules of an audience must match (AND).
type Rule struct {
	Field string          `json:"field"`
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value"`
}

// FieldError describes an invalid rule. Rule is the rule's index, or -1 for
// problems with the list as a whole.
type FieldError struct {
	Rule    int    `json:"rule"`
	Message string `json:"message"`
}

// Catalog holds the values rules may refer to: the seeded cities and the
// categories table.
type Catalog struct {
	Cities     []string
	Categories []string
}

const (
	maxRules  = 20
	maxDays   = 365
	maxMonths = 12
	maxCount  = 10_000
	maxAmount = 1_000_000
)

var minDate = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// fieldOps lists every field and its allowed operators (D14).
var fieldOps = map[string][]string{
	"city":             {"in"},
	"cart_added":       {"within"},
	"no_order":         {"within"},
	"order_count":      {"gte", "lte", "gt", "lt"},
	"total_spend":      {"gte", "lte", "gt", "lt"},
	"last_order":       {"before", "after"},
	"ordered_category": {"in"},
	"carted_category":  {"in"},
	"cart_value":       {"gte", "gt"},
	"signed_up":        {"within", "before"},
	"spend_in_window":  {"gte", "lte", "gt", "lt"}, // D70
	"orders_in_window": {"gte", "lte", "gt", "lt"},
}

// Value types of the rule fields, as the chip editor shows them (D39).
const (
	ValueCities           = "cities"            // ["İstanbul", ...]
	ValueWindow           = "window"            // {"last_days": N}, {"last_months": N} or {"from": "YYYY-MM-DD", "to"?: ...}
	ValueCategoriesWindow = "categories_window" // a window plus "categories": [...]
	ValueCount            = "count"             // whole number
	ValueAmount           = "amount"            // number
	ValueAmountWindow     = "amount_window"     // a window plus "amount": number
	ValueCountWindow      = "count_window"      // a window plus "count": whole number
)

// fieldValues is each field's value type; parse decodes exactly these shapes.
var fieldValues = map[string]string{
	"city":             ValueCities,
	"cart_added":       ValueWindow,
	"no_order":         ValueWindow,
	"signed_up":        ValueWindow, // "before" takes no "to"
	"last_order":       ValueWindow, // "before" takes no "to"
	"ordered_category": ValueCategoriesWindow,
	"carted_category":  ValueCategoriesWindow,
	"order_count":      ValueCount,
	"total_spend":      ValueAmount,
	"cart_value":       ValueAmount,
	"spend_in_window":  ValueAmountWindow,
	"orders_in_window": ValueCountWindow,
}

// FieldSpec describes one rule field for the API catalog (D39).
type FieldSpec struct {
	Field string   `json:"field"`
	Ops   []string `json:"ops"`
	Value string   `json:"value"`
}

// Fields lists every rule field, sorted by name. The slices are copies.
func Fields() []FieldSpec {
	out := make([]FieldSpec, 0, len(fieldOps))
	for f, ops := range fieldOps {
		out = append(out, FieldSpec{f, slices.Clone(ops), fieldValues[f]})
	}
	slices.SortFunc(out, func(a, b FieldSpec) int { return strings.Compare(a.Field, b.Field) })
	return out
}

// Limits are the bounds Validate enforces, as a fresh map: max_count is for
// order counts, max_amount for spend and cart value amounts.
func Limits() map[string]any {
	return map[string]any{
		"max_rules": maxRules, "max_days": maxDays, "max_months": maxMonths, "max_count": maxCount, "max_amount": maxAmount,
		"min_date": minDate.Format(time.DateOnly),
	}
}

// Validate checks rules against the catalog and returns them normalized
// (canonical city and category names), with every problem found.
func Validate(rules []Rule, cat Catalog) ([]Rule, []FieldError) {
	if len(rules) == 0 {
		return nil, []FieldError{{-1, "add at least one audience rule"}}
	}
	if len(rules) > maxRules {
		return nil, []FieldError{{-1, fmt.Sprintf("at most %d rules are allowed", maxRules)}}
	}
	var errs []FieldError
	out := make([]Rule, len(rules))
	seen := map[string]bool{}
	for i, r := range rules {
		key := r.Field + " " + r.Op
		if seen[key] {
			errs = append(errs, FieldError{i, "duplicate rule: " + key})
			continue
		}
		seen[key] = true
		c, err := parse(r, &cat)
		if err != nil {
			errs = append(errs, FieldError{i, err.Error()})
			continue
		}
		value, err := json.Marshal(c.value())
		if err != nil {
			errs = append(errs, FieldError{i, err.Error()})
			continue
		}
		out[i] = Rule{Field: r.Field, Op: r.Op, Value: value}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

// condition is a parsed rule: it knows its normalized value and its SQL.
type condition interface {
	value() any
	sql(b *builder) string
}

// parse decodes and checks one rule. With a nil catalog (Build, after
// validation) city and category names are taken as they are.
func parse(r Rule, cat *Catalog) (condition, error) {
	if r.Field == "language" {
		return nil, errors.New("language is reserved for a later version")
	}
	ops, ok := fieldOps[r.Field]
	if !ok {
		return nil, fmt.Errorf("unknown field %q", r.Field)
	}
	if !slices.Contains(ops, r.Op) {
		return nil, fmt.Errorf("operator %q is not allowed for %s (use %s)", r.Op, r.Field, strings.Join(ops, " or "))
	}
	if v := bytes.TrimSpace(r.Value); len(v) == 0 || string(v) == "null" {
		return nil, fmt.Errorf("%s needs a value", r.Field)
	}

	switch r.Field {
	case "city":
		var cities []string
		if err := decode(r, &cities); err != nil {
			return nil, err
		}
		cities, err := canonical(cities, catalogCities(cat), "city", foldTurkish)
		return cityIn{cities}, err

	case "cart_added", "no_order", "signed_up", "last_order":
		var w window
		if err := decode(r, &w); err != nil {
			return nil, err
		}
		if err := w.check(); err != nil {
			return nil, err
		}
		if r.Op == "before" && w.To != nil {
			return nil, fmt.Errorf(`%s before takes no "to" date`, r.Field)
		}
		if r.Field == "last_order" {
			return lastOrder{r.Op, w}, nil
		}
		return timeRule{r.Field, r.Op, w}, nil

	case "spend_in_window":
		var v struct {
			Amount *float64 `json:"amount"`
			window
		}
		if err := decode(r, &v); err != nil {
			return nil, err
		}
		if v.Amount == nil {
			return nil, errors.New("spend_in_window needs an amount")
		}
		if *v.Amount < 0 || *v.Amount > maxAmount {
			return nil, fmt.Errorf("%s must be between 0 and %d", r.Field, maxAmount)
		}
		if err := v.check(); err != nil {
			return nil, err
		}
		return windowTotal{r.Field, r.Op, *v.Amount, v.window}, nil

	case "orders_in_window":
		var v struct {
			Count *int64 `json:"count"`
			window
		}
		if err := decode(r, &v); err != nil {
			return nil, err
		}
		if v.Count == nil {
			return nil, errors.New("orders_in_window needs a count")
		}
		if *v.Count < 0 || *v.Count > maxCount {
			return nil, fmt.Errorf("%s must be between 0 and %d", r.Field, maxCount)
		}
		if err := v.check(); err != nil {
			return nil, err
		}
		return windowTotal{r.Field, r.Op, *v.Count, v.window}, nil

	case "ordered_category", "carted_category":
		var v categoryWindow
		if err := decode(r, &v); err != nil {
			return nil, err
		}
		if err := v.check(); err != nil {
			return nil, err
		}
		cats, err := canonical(v.Categories, catalogCategories(cat), "category", strings.ToLower)
		v.Categories = cats
		return categoryRule{r.Field, v}, err

	case "order_count":
		var n int64
		if err := decode(r, &n); err != nil {
			return nil, err
		}
		if n < 0 || n > maxCount {
			return nil, fmt.Errorf("%s must be between 0 and %d", r.Field, maxCount)
		}
		return compare{r.Field, r.Op, n}, nil

	default: // total_spend, cart_value
		var amount float64
		if err := decode(r, &amount); err != nil {
			return nil, err
		}
		if amount < 0 || amount > maxAmount {
			return nil, fmt.Errorf("%s must be between 0 and %d", r.Field, maxAmount)
		}
		return compare{r.Field, r.Op, amount}, nil
	}
}

// decode reads exactly one JSON value and rejects unknown and repeated keys.
func decode(r Rule, v any) error {
	if err := repeatedKey(json.NewDecoder(bytes.NewReader(r.Value))); errors.Is(err, errRepeatedKey) {
		return fmt.Errorf("invalid value for %s %s: %v", r.Field, r.Op, err)
	}
	dec := json.NewDecoder(bytes.NewReader(r.Value))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = errors.New("unexpected data after the value")
	}
	if err != nil {
		return fmt.Errorf("invalid value for %s %s: %v", r.Field, r.Op, err)
	}
	return nil
}

var errRepeatedKey = errors.New("repeated key")

// repeatedKey walks one JSON value and reports a key repeated in an object,
// compared without case like Go's decoding: {"last_days": 30, "last_days": 90}
// would otherwise silently mean 90. Syntax errors are left to the decoder.
func repeatedKey(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch tok {
	case json.Delim('{'):
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			k := strings.ToLower(key.(string))
			if seen[k] {
				return fmt.Errorf("%w %q", errRepeatedKey, key)
			}
			seen[k] = true
			if err := repeatedKey(dec); err != nil {
				return err
			}
		}
	case json.Delim('['):
		for dec.More() {
			if err := repeatedKey(dec); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	_, err = dec.Token() // the closing delimiter
	return err
}

// window is the last N days or calendar months, counted back from when the
// rule is evaluated, or fixed calendar dates in the campaign's time zone
// (D14, D30, D70).
type window struct {
	LastDays   *int    `json:"last_days,omitempty"`
	LastMonths *int    `json:"last_months,omitempty"`
	From       *string `json:"from,omitempty"`
	To         *string `json:"to,omitempty"` // inclusive; null means "until now"
}

func (w window) check() error {
	set := 0
	for _, p := range []bool{w.LastDays != nil, w.LastMonths != nil, w.From != nil} {
		if p {
			set++
		}
	}
	if set != 1 || (w.From == nil && w.To != nil) {
		return errors.New(`use either last_days, last_months or from/to`)
	}
	if w.LastDays != nil && (*w.LastDays < 1 || *w.LastDays > maxDays) {
		return fmt.Errorf("last_days must be between 1 and %d", maxDays)
	}
	if w.LastMonths != nil && (*w.LastMonths < 1 || *w.LastMonths > maxMonths) {
		return fmt.Errorf("last_months must be between 1 and %d", maxMonths)
	}
	if w.From == nil {
		return nil
	}
	from, err := parseDate(*w.From)
	if err != nil {
		return err
	}
	if w.To != nil {
		to, err := parseDate(*w.To)
		if err != nil {
			return err
		}
		if from.After(to) {
			return errors.New("from must not be after to")
		}
	}
	return nil
}

func parseDate(s string) (time.Time, error) {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("dates must be YYYY-MM-DD, got %q", s)
	}
	if d.Before(minDate) {
		return time.Time{}, errors.New("dates must be on or after 2000-01-01")
	}
	return d, nil
}

// bounds resolves the window to [start, end); end is nil for open windows.
// Dates are midnight in loc; "to" includes that whole day.
func (w window) bounds(now time.Time, loc *time.Location) (time.Time, *time.Time) {
	if w.LastDays != nil {
		return now.Add(-time.Duration(*w.LastDays) * 24 * time.Hour), nil
	}
	if w.LastMonths != nil {
		return monthsBack(now.In(loc), *w.LastMonths), nil
	}
	start, _ := time.ParseInLocation(time.DateOnly, *w.From, loc)
	if w.To == nil {
		return start, nil
	}
	end, _ := time.ParseInLocation(time.DateOnly, *w.To, loc)
	end = end.AddDate(0, 0, 1)
	return start, &end
}

// monthsBack is t moved n calendar months back; a day the target month lacks
// becomes its last day (31 March → 28 February), unlike time.AddDate.
func monthsBack(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	lastDay := time.Date(y, m-time.Month(n)+1, 0, 0, 0, 0, 0, t.Location()).Day()
	return time.Date(y, m-time.Month(n), min(d, lastDay), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

type categoryWindow struct {
	Categories []string `json:"categories"`
	window
}

// canonical maps each name to its catalog spelling via fold, dropping
// duplicates. A nil catalog keeps the names unchecked.
func canonical(names, known []string, what string, fold func(string) string) ([]string, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("list at least one %s", what)
	}
	if known == nil {
		return names, nil
	}
	byFold := map[string]string{}
	for _, k := range known {
		byFold[fold(k)] = k
	}
	var out []string
	for _, n := range names {
		c, ok := byFold[fold(strings.TrimSpace(n))]
		if !ok {
			return nil, fmt.Errorf("unknown %s %q", what, n)
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out, nil
}

func catalogCities(c *Catalog) []string {
	if c == nil {
		return nil
	}
	return c.Cities
}

func catalogCategories(c *Catalog) []string {
	if c == nil {
		return nil
	}
	return c.Categories
}

var turkishFold = strings.NewReplacer(
	"Ç", "c", "ç", "c", "Ğ", "g", "ğ", "g", "İ", "i", "I", "i", "ı", "i",
	"Ö", "o", "ö", "o", "Ş", "s", "ş", "s", "Ü", "u", "ü", "u",
)

// foldTurkish makes "Istanbul", "istanbul" and "İSTANBUL" compare equal.
func foldTurkish(s string) string { return strings.ToLower(turkishFold.Replace(s)) }
