package audience_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/dbtest"
)

var (
	now      = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	istanbul = mustLoad("Europe/Istanbul")
)

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func daysAgo(d float64) time.Time { return now.Add(-time.Duration(d * 24 * float64(time.Hour))) }

// One user per scenario. Events are relative to now unless an exact time matters.
type event struct {
	at       time.Time
	category string
	qty      int
	price    float64
}

type fixtureUser struct {
	name       string
	city       string
	sms, email bool
	signedUp   time.Time
	orders     []event
	carts      []event
}

var fixture = []fixtureUser{
	{name: "abandoner", city: "İstanbul", sms: true, email: true, signedUp: daysAgo(500),
		orders: []event{{daysAgo(60), "fashion", 1, 200}}, carts: []event{{daysAgo(5), "electronics", 2, 1000}}},
	{name: "recent", city: "Ankara", sms: true, signedUp: daysAgo(500),
		orders: []event{{daysAgo(3), "books", 2, 50}, {daysAgo(100), "home", 1, 300}}},
	{name: "lapsed", city: "İzmir", email: true, signedUp: daysAgo(500),
		orders: []event{{daysAgo(200), "beauty", 1, 500}, {daysAgo(150), "sports", 1, 700}}},
	{name: "vip", city: "İstanbul", sms: true, email: true, signedUp: daysAgo(500),
		orders: []event{{daysAgo(10), "electronics", 1, 6000}, {daysAgo(40), "electronics", 1, 5000}, {daysAgo(80), "fashion", 1, 1500}}},
	{name: "category", city: "Bursa", sms: true, email: true, signedUp: daysAgo(500),
		orders: []event{{daysAgo(20), "books", 1, 80}}, carts: []event{{daysAgo(2), "beauty", 1, 150}}},
	{name: "bigcart", city: "Ankara", sms: true, email: true, signedUp: daysAgo(500),
		carts: []event{{daysAgo(1), "electronics", 3, 2500}}},
	{name: "newbie", city: "Konya", sms: true, signedUp: daysAgo(5),
		carts: []event{{daysAgo(1), "books", 1, 40}}},
	// Matches the case example's rules but allowed no channel.
	{name: "noconsent", city: "İstanbul", signedUp: daysAgo(500),
		carts: []event{{daysAgo(1), "electronics", 1, 100}}},
	{name: "inactive", city: "İzmir", sms: true, email: true, signedUp: daysAgo(400)},
	// Absolute-date edge: 1 Oct 10:00 Istanbul time, and 2 Oct 01:00 Istanbul (still 1 Oct in UTC).
	{name: "oct1", city: "Antalya", sms: true, email: true, signedUp: daysAgo(500),
		orders: []event{{time.Date(2026, 10, 1, 10, 0, 0, 0, istanbul), "electronics", 1, 1000}}},
	{name: "oct2early", city: "Antalya", sms: true, email: true, signedUp: daysAgo(500),
		orders: []event{{time.Date(2026, 10, 2, 1, 0, 0, 0, istanbul), "electronics", 1, 1000}}},
	// D70: 10,001 TL in four orders over the last 12 months, the last 50 days ago.
	{name: "loyal", city: "Antalya", sms: true, email: true, signedUp: daysAgo(500),
		orders: []event{{daysAgo(50), "home", 1, 3000}, {daysAgo(120), "home", 1, 3000}, {daysAgo(200), "home", 1, 3000}, {daysAgo(300), "home", 1, 1001}}},
	// Exactly 10,000 TL in four orders over 12 months, plus an older 5,000 TL order.
	{name: "exact", city: "Antalya", sms: true, email: true, signedUp: daysAgo(500),
		orders: []event{{daysAgo(50), "home", 1, 2500}, {daysAgo(120), "home", 1, 2500}, {daysAgo(200), "home", 1, 2500}, {daysAgo(300), "home", 1, 2500}, {daysAgo(400), "home", 1, 5000}}},
}

func fixtureDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.MigratedDB(t)
	exec := func(sql string, args ...any) int64 {
		var id int64
		if err := pool.QueryRow(ctx, sql+" RETURNING id", args...).Scan(&id); err != nil {
			t.Fatalf("%v: %s", err, sql)
		}
		return id
	}
	for i, u := range fixture {
		uid := exec(`INSERT INTO users (first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
			VALUES ($1, $1 || '@example.com', $2, $3, 'tr', $4, $5, $6)`, u.name, fmt.Sprintf("+90500%07d", i), u.city, u.sms, u.email, u.signedUp)
		for _, o := range u.orders {
			oid := exec(`INSERT INTO orders (user_id, ordered_at, total_amount) VALUES ($1, $2, $3)`, uid, o.at, float64(o.qty)*o.price)
			exec(`INSERT INTO order_items (order_id, category, quantity, unit_price) VALUES ($1, $2, $3, $4)`, oid, o.category, o.qty, o.price)
		}
		for _, c := range u.carts {
			exec(`INSERT INTO cart_items (user_id, category, quantity, unit_price, added_at) VALUES ($1, $2, $3, $4, $5)`,
				uid, c.category, c.qty, c.price, c.at)
		}
	}
	return pool
}

// matching returns the fixture names selected by the rules on the channel, sorted.
func matching(t *testing.T, pool *pgxpool.Pool, rules []audience.Rule, channel string, at time.Time) []string {
	t.Helper()
	where, args, err := audience.Build(rules, channel, at, istanbul)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(context.Background(), `SELECT u.first_name FROM users u WHERE `+where+` ORDER BY 1`, args...)
	if err != nil {
		t.Fatalf("%v\n%s", err, where)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	return names
}

func names(s string) []string {
	out := strings.Fields(s)
	slices.Sort(out)
	return out
}

// Each case uses a channel every expected user allowed; noconsent never matches.
func TestCount_MatchesHandComputed(t *testing.T) {
	pool := fixtureDB(t)

	cases := []struct {
		name    string
		rules   []audience.Rule
		channel string
		want    string
	}{
		{"case example (cart ≤30d, no order ≤30d, İstanbul)", []audience.Rule{
			rule("city", "in", `["İstanbul"]`),
			rule("cart_added", "within", `{"last_days": 30}`),
			rule("no_order", "within", `{"last_days": 30}`)}, "sms", "abandoner"},
		{"win-back (last order >90d)", []audience.Rule{rule("last_order", "before", `{"last_days": 90}`)}, "email", "lapsed"},
		{"recent order (≤7d)", []audience.Rule{rule("last_order", "after", `{"last_days": 7}`)}, "sms", "recent"},
		{"VIP by spend", []audience.Rule{rule("total_spend", "gte", `10000`)}, "sms", "exact loyal vip"},
		{"VIP by order count", []audience.Rule{rule("order_count", "gte", `3`)}, "sms", "exact loyal vip"},
		// D70: strict comparisons, windowed totals, calendar months, signed up before.
		{"spend at least 15,000", []audience.Rule{rule("total_spend", "gte", `15000`)}, "sms", "exact"},
		{"spend over 15,000", []audience.Rule{rule("total_spend", "gt", `15000`)}, "sms", ""},
		{"fewer than 1 order", []audience.Rule{rule("order_count", "lt", `1`)}, "email", "bigcart inactive"},
		{"more than 4 orders", []audience.Rule{rule("order_count", "gt", `4`)}, "sms", "exact"},
		{"spend under 200", []audience.Rule{rule("total_spend", "lt", `200`), rule("order_count", "gte", `1`)}, "sms", "category"},
		{"cart over 7,500", []audience.Rule{rule("cart_value", "gt", `7500`)}, "sms", ""},
		{"spend in 12 months ≥ 12,000 (older orders left out)", []audience.Rule{rule("spend_in_window", "gte", `{"amount": 12000, "last_months": 12}`)}, "sms", "vip"},
		{"spend in 12 months ≥ 10,000", []audience.Rule{rule("spend_in_window", "gte", `{"amount": 10000, "last_months": 12}`)}, "sms", "exact loyal vip"},
		{"spend in 12 months > 10,000", []audience.Rule{rule("spend_in_window", "gt", `{"amount": 10000, "last_months": 12}`)}, "sms", "loyal vip"},
		{"spend in 30 days ≤ 100, with orders", []audience.Rule{rule("spend_in_window", "lte", `{"amount": 100, "last_days": 30}`), rule("order_count", "gte", `1`)}, "sms", "abandoner category exact loyal recent"},
		{"4+ orders in 12 months", []audience.Rule{rule("orders_in_window", "gte", `{"count": 4, "last_months": 12}`)}, "sms", "exact loyal"},
		{"fewer than 2 orders in 12 months, with orders", []audience.Rule{rule("orders_in_window", "lt", `{"count": 2, "last_months": 12}`), rule("order_count", "gte", `1`)}, "sms", "abandoner category oct1 oct2early"},
		{"orders since 1 Oct", []audience.Rule{rule("orders_in_window", "gte", `{"count": 1, "from": "2026-10-01"}`)}, "sms", "category oct1 oct2early recent vip"},
		{"signed up more than 7 days ago, carted ≤7d", []audience.Rule{rule("signed_up", "before", `{"last_days": 7}`), rule("cart_added", "within", `{"last_days": 7}`)}, "sms", "abandoner bigcart category"},
		{"over 10,000 TL in 12 months, 4+ orders, none in 45 days, not new", []audience.Rule{
			rule("spend_in_window", "gt", `{"amount": 10000, "last_months": 12}`),
			rule("orders_in_window", "gte", `{"count": 4, "last_months": 12}`),
			rule("no_order", "within", `{"last_days": 45}`),
			rule("signed_up", "before", `{"last_days": 7}`)}, "sms", "loyal"},
		{"small spenders", []audience.Rule{rule("total_spend", "lte", `300`), rule("order_count", "gte", `1`)}, "sms", "abandoner category"},
		{"never ordered", []audience.Rule{rule("order_count", "lte", `0`)}, "email", "bigcart inactive"},
		{"books ordered ≤30d", []audience.Rule{rule("ordered_category", "in", `{"categories": ["books"], "last_days": 30}`)}, "sms", "category recent"},
		{"beauty carted ≤7d", []audience.Rule{rule("carted_category", "in", `{"categories": ["beauty"], "last_days": 7}`)}, "sms", "category"},
		{"high-value cart", []audience.Rule{rule("cart_value", "gte", `5000`)}, "sms", "bigcart"},
		{"new customers ≤7d", []audience.Rule{rule("signed_up", "within", `{"last_days": 7}`), rule("cart_added", "within", `{"last_days": 7}`)}, "sms", "newbie"},
		{"cities", []audience.Rule{rule("city", "in", `["Ankara", "Bursa"]`)}, "sms", "bigcart category recent"},
		{"electronics on 1 Oct (Istanbul day)", []audience.Rule{
			rule("ordered_category", "in", `{"categories": ["electronics"], "from": "2026-10-01", "to": "2026-10-01"}`)}, "sms", "oct1"},
		{"electronics since 2 Oct", []audience.Rule{
			rule("ordered_category", "in", `{"categories": ["electronics"], "from": "2026-10-02", "to": null}`)}, "email", "oct2early vip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matching(t, pool, tc.rules, tc.channel, now); !slices.Equal(got, names(tc.want)) {
				t.Fatalf("want %v, got %v", names(tc.want), got)
			}
		})
	}
}

// Validate's canonical output is what gets stored and built: it must select
// the same users as the rules it came from (D70's rebuilt values included).
func TestBuild_ValidatedRulesSelectTheSame(t *testing.T) {
	pool := fixtureDB(t)
	rules := []audience.Rule{
		rule("spend_in_window", "gt", `{"last_months": 12, "amount": 10000.0}`),
		rule("orders_in_window", "gte", `{"last_months": 12, "count": 4}`),
		rule("signed_up", "before", `{"last_days": 7}`),
	}
	valid, errs := audience.Validate(rules, audience.Catalog{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if string(valid[0].Value) != `{"amount":10000,"last_months":12}` || string(valid[1].Value) != `{"count":4,"last_months":12}` {
		t.Fatalf("canonical values: %s %s", valid[0].Value, valid[1].Value)
	}
	if got, want := matching(t, pool, valid, "sms", now), matching(t, pool, rules, "sms", now); !slices.Equal(got, want) || !slices.Equal(got, []string{"loyal"}) {
		t.Fatalf("validated rules select %v, raw rules %v, want [loyal]", got, want)
	}
}

func TestCount_RelativeWindowMovesWithNow(t *testing.T) {
	pool := fixtureDB(t)
	rules := []audience.Rule{rule("cart_added", "within", `{"last_days": 3}`)}

	before := matching(t, pool, rules, "sms", now)
	after := matching(t, pool, rules, "sms", now.Add(5*24*time.Hour))

	if !slices.Equal(before, names("bigcart category newbie")) || len(after) != 0 {
		t.Fatalf("want [bigcart category newbie] then [], got %v then %v", before, after)
	}
}

func TestCount_AbsoluteWindowFixed(t *testing.T) {
	pool := fixtureDB(t)
	rules := []audience.Rule{rule("ordered_category", "in", `{"categories": ["electronics"], "from": "2026-10-01", "to": "2026-10-01"}`)}

	before := matching(t, pool, rules, "sms", now)
	after := matching(t, pool, rules, "sms", now.Add(5*24*time.Hour))

	if !slices.Equal(before, []string{"oct1"}) || !slices.Equal(before, after) {
		t.Fatalf("want [oct1] both times, got %v then %v", before, after)
	}
}

func TestCount_ConsentPerChannel(t *testing.T) {
	pool := fixtureDB(t)
	ctx := context.Background()
	// İstanbul: abandoner (both), vip (both), noconsent (none) · Ankara: recent (sms), bigcart (both).
	cases := []struct {
		city     string
		channels []string
		want     audience.Counts
	}{
		{"İstanbul", []string{"sms", "email"}, audience.Counts{Total: 2, SMS: 2, Email: 2}},
		{"Ankara", []string{"sms", "email"}, audience.Counts{Total: 2, SMS: 2, Email: 1}},
		{"Ankara", []string{"sms"}, audience.Counts{Total: 2, SMS: 2}},
		{"Ankara", []string{"email"}, audience.Counts{Total: 1, Email: 1}},
		{"İzmir", []string{"sms"}, audience.Counts{Total: 1, SMS: 1}},
	}
	for _, tc := range cases {
		rules := []audience.Rule{rule("city", "in", `["`+tc.city+`"]`)}
		got, err := audience.Count(ctx, pool, rules, tc.channels, now, istanbul)
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != tc.want.Total || got.SMS != tc.want.SMS || got.Email != tc.want.Email {
			t.Errorf("%s %v: want %+v, got %+v", tc.city, tc.channels, tc.want, got)
		}
	}
	if _, err := audience.Count(ctx, pool, []audience.Rule{rule("city", "in", `["Ankara"]`)}, []string{"fax"}, now, istanbul); err == nil {
		t.Error("unknown channel: want error")
	}
	if _, err := audience.Count(ctx, pool, []audience.Rule{rule("city", "in", `["Ankara"]`)}, nil, now, istanbul); err == nil {
		t.Error("no channels: want error")
	}
}

// The audience per user language and channel, consent applied (D64): the
// basis of the language recommendation and the fallback counts.
func TestCounts_ByLanguage(t *testing.T) {
	pool := fixtureDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE users SET language = 'en' WHERE first_name IN ('vip', 'recent')`); err != nil {
		t.Fatal(err)
	}
	// İstanbul: abandoner (tr, both), vip (en, both), noconsent (tr, none) · Ankara: recent (en, sms), bigcart (tr, both).
	got, err := audience.Count(ctx, pool, []audience.Rule{rule("city", "in", `["İstanbul","Ankara"]`)}, []string{"sms", "email"}, now, istanbul)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]audience.ChannelCounts{"tr": {Total: 2, SMS: 2, Email: 2}, "en": {Total: 2, SMS: 2, Email: 1}}
	if got.Total != 4 || !reflect.DeepEqual(got.ByLanguage, want) {
		t.Fatalf("want %+v of 4, got %+v of %d", want, got.ByLanguage, got.Total)
	}
	sms, err := audience.Count(ctx, pool, []audience.Rule{rule("city", "in", `["Ankara"]`)}, []string{"email"}, now, istanbul)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]audience.ChannelCounts{"tr": {Total: 1, Email: 1}}; !reflect.DeepEqual(sms.ByLanguage, want) {
		t.Fatalf("a language with nobody reachable on the chosen channels is left out: want %+v, got %+v", want, sms.ByLanguage)
	}
}

func TestBuild_NoValueInterpolated(t *testing.T) {
	evil := `'; DROP TABLE users;--`
	quoted, _ := json.Marshal(evil)
	rules := []audience.Rule{
		rule("city", "in", `[`+string(quoted)+`]`),
		rule("ordered_category", "in", `{"categories": [`+string(quoted)+`], "last_days": 30}`),
	}

	where, args, err := audience.Build(rules, "sms", now, istanbul)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(where, "DROP") || strings.Contains(where, "'") {
		t.Fatalf("value leaked into SQL: %s", where)
	}
	if !strings.Contains(fmt.Sprint(args), evil) {
		t.Fatalf("value missing from args: %v", args)
	}
}

func TestBuild_RequiresChannelAndValidRules(t *testing.T) {
	ok := []audience.Rule{rule("city", "in", `["Ankara"]`)}
	if _, _, err := audience.Build(ok, "", now, istanbul); err == nil {
		t.Error("empty channel: want error (consent must always apply)")
	}
	if _, _, err := audience.Build(nil, "sms", now, istanbul); err == nil {
		t.Error("no rules: want error")
	}
	if _, _, err := audience.Build([]audience.Rule{rule("age", "gte", `1`)}, "sms", now, istanbul); err == nil {
		t.Error("unknown field: want error")
	}
}

func TestLoadCatalog(t *testing.T) {
	pool := fixtureDB(t)

	cat, err := audience.LoadCatalog(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(cat.Cities)
	if !slices.Equal(cat.Cities, names("Ankara Antalya Bursa Konya İstanbul İzmir")) {
		t.Errorf("cities: %v", cat.Cities)
	}
	if !slices.Contains(cat.Categories, "electronics") || len(cat.Categories) != 6 {
		t.Errorf("categories: %v", cat.Categories)
	}
}
