package audience

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Counts are the users an audience reaches per chosen channel; Total counts
// each user once. Channels that were not chosen stay 0.
type Counts struct {
	Total int `json:"total"`
	SMS   int `json:"sms"`
	Email int `json:"email"`
	// The same per user language (D64); languages nobody is reached in are left out.
	ByLanguage map[string]ChannelCounts `json:"by_language,omitempty"`
}

// ChannelCounts are one language's share of Counts.
type ChannelCounts struct {
	Total int `json:"total"`
	SMS   int `json:"sms"`
	Email int `json:"email"`
}

// ScanCounts adds up rows of (language, total, sms, email) counts; channels
// that were not chosen stay 0.
func ScanCounts(rows pgx.Rows, channels []string) (Counts, error) {
	c := Counts{ByLanguage: map[string]ChannelCounts{}}
	var lang string
	var l ChannelCounts
	_, err := pgx.ForEachRow(rows, []any{&lang, &l.Total, &l.SMS, &l.Email}, func() error {
		if !slices.Contains(channels, "sms") {
			l.SMS = 0
		}
		if !slices.Contains(channels, "email") {
			l.Email = 0
		}
		if l.Total > 0 {
			c.ByLanguage[lang] = l
			c.Total, c.SMS, c.Email = c.Total+l.Total, c.SMS+l.SMS, c.Email+l.Email
		}
		return nil
	})
	return c, err
}

// consent is each channel's opt-in column (CASE §3: checked per channel).
var consent = map[string]string{"sms": "u.sms_opt_in", "email": "u.email_opt_in"}

// Build returns a WHERE clause over users aliased "u", selecting users who
// match every rule and allowed the channel, plus its arguments. Relative
// windows count back from now; dates resolve in loc. Rules must already be
// validated; Build still rejects ones it can't parse.
func Build(rules []Rule, channel string, now time.Time, loc *time.Location) (string, []any, error) {
	col, ok := consent[channel]
	if !ok {
		return "", nil, fmt.Errorf("unknown channel %q", channel)
	}
	where, args, err := build(rules, now, loc)
	if err != nil {
		return "", nil, err
	}
	return where + " AND " + col, args, nil
}

// Count returns how many users the audience reaches on each chosen channel.
func Count(ctx context.Context, pool *pgxpool.Pool, rules []Rule, channels []string, now time.Time, loc *time.Location) (Counts, error) {
	if len(channels) == 0 {
		return Counts{}, errors.New("choose at least one channel")
	}
	var anyOf []string
	for _, ch := range channels {
		col, ok := consent[ch]
		if !ok {
			return Counts{}, fmt.Errorf("unknown channel %q", ch)
		}
		anyOf = append(anyOf, col)
	}
	where, args, err := build(rules, now, loc)
	if err != nil {
		return Counts{}, err
	}
	rows, err := pool.Query(ctx, `SELECT u.language, count(*) FILTER (WHERE `+strings.Join(anyOf, " OR ")+`),
		count(*) FILTER (WHERE u.sms_opt_in), count(*) FILTER (WHERE u.email_opt_in)
		FROM users u WHERE `+where+` GROUP BY u.language`, args...)
	if err != nil {
		return Counts{}, fmt.Errorf("count audience: %w", err)
	}
	c, err := ScanCounts(rows, channels)
	if err != nil {
		return Counts{}, fmt.Errorf("count audience: %w", err)
	}
	return c, nil
}

// LoadCatalog reads the cities present in the user data and the categories table.
func LoadCatalog(ctx context.Context, pool *pgxpool.Pool) (Catalog, error) {
	var cat Catalog
	rows, _ := pool.Query(ctx, `SELECT DISTINCT city FROM users ORDER BY 1`)
	cities, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return cat, fmt.Errorf("load cities: %w", err)
	}
	rows, _ = pool.Query(ctx, `SELECT code FROM categories ORDER BY 1`)
	cats, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return cat, fmt.Errorf("load categories: %w", err)
	}
	return Catalog{Cities: cities, Categories: cats}, nil
}

func build(rules []Rule, now time.Time, loc *time.Location) (string, []any, error) {
	if len(rules) == 0 {
		return "", nil, errors.New("no audience rules")
	}
	b := &builder{now: now, loc: loc}
	parts := make([]string, len(rules))
	for i, r := range rules {
		c, err := parse(r, nil)
		if err != nil {
			return "", nil, fmt.Errorf("rule %d: %w", i, err)
		}
		parts[i] = "(" + c.sql(b) + ")"
	}
	return strings.Join(parts, " AND "), b.args, nil
}

// builder collects query arguments; SQL text only ever gets "$n" placeholders.
type builder struct {
	args []any
	now  time.Time
	loc  *time.Location
}

func (b *builder) arg(v any) string {
	b.args = append(b.args, v)
	return fmt.Sprintf("$%d", len(b.args))
}

// inWindow is "col >= start [AND col < end]".
func (b *builder) inWindow(col string, w window) string {
	start, end := w.bounds(b.now, b.loc)
	s := col + " >= " + b.arg(start)
	if end != nil {
		s += " AND " + col + " < " + b.arg(*end)
	}
	return s
}

type cityIn struct{ cities []string }

func (c cityIn) value() any            { return c.cities }
func (c cityIn) sql(b *builder) string { return "u.city = ANY(" + b.arg(c.cities) + ")" }

type timeRule struct {
	field, op string
	w         window
}

func (t timeRule) value() any { return t.w }
func (t timeRule) sql(b *builder) string {
	switch t.field {
	case "cart_added":
		return "EXISTS (SELECT 1 FROM cart_items c WHERE c.user_id = u.id AND " + b.inWindow("c.added_at", t.w) + ")"
	case "no_order":
		return "NOT EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id AND " + b.inWindow("o.ordered_at", t.w) + ")"
	default: // signed_up
		if t.op == "before" {
			start, _ := t.w.bounds(b.now, b.loc)
			return "u.signed_up_at < " + b.arg(start)
		}
		return b.inWindow("u.signed_up_at", t.w)
	}
}

const lastOrderAt = "(SELECT max(o.ordered_at) FROM orders o WHERE o.user_id = u.id)"

// lastOrder compares the user's most recent order; users without orders never match.
type lastOrder struct {
	op string
	w  window
}

func (l lastOrder) value() any { return l.w }
func (l lastOrder) sql(b *builder) string {
	if l.op == "before" {
		start, _ := l.w.bounds(b.now, b.loc)
		return lastOrderAt + " < " + b.arg(start)
	}
	return b.inWindow(lastOrderAt, l.w)
}

type categoryRule struct {
	field string
	v     categoryWindow
}

func (c categoryRule) value() any { return c.v }
func (c categoryRule) sql(b *builder) string {
	if c.field == "ordered_category" {
		return "EXISTS (SELECT 1 FROM orders o JOIN order_items i ON i.order_id = o.id WHERE o.user_id = u.id AND i.category = ANY(" +
			b.arg(c.v.Categories) + ") AND " + b.inWindow("o.ordered_at", c.v.window) + ")"
	}
	return "EXISTS (SELECT 1 FROM cart_items c WHERE c.user_id = u.id AND c.category = ANY(" +
		b.arg(c.v.Categories) + ") AND " + b.inWindow("c.added_at", c.v.window) + ")"
}

var aggregates = map[string]string{
	"order_count": "(SELECT count(*) FROM orders o WHERE o.user_id = u.id)",
	"total_spend": "(SELECT coalesce(sum(o.total_amount), 0) FROM orders o WHERE o.user_id = u.id)",
	"cart_value":  "(SELECT coalesce(sum(c.quantity * c.unit_price), 0) FROM cart_items c WHERE c.user_id = u.id)",
}

type compare struct {
	field, op string
	n         any // int64 or float64
}

var comparisons = map[string]string{"gte": ">=", "lte": "<=", "gt": ">", "lt": "<"}

func (c compare) value() any { return c.n }
func (c compare) sql(b *builder) string {
	return aggregates[c.field] + " " + comparisons[c.op] + " " + b.arg(c.n)
}

// windowTotal compares the user's spend or order count inside a window (D70).
type windowTotal struct {
	field, op string
	n         any // float64 amount or int64 count
	w         window
}

func (c windowTotal) value() any {
	if n, ok := c.n.(int64); ok {
		return struct {
			Count int64 `json:"count"`
			window
		}{n, c.w}
	}
	return struct {
		Amount float64 `json:"amount"`
		window
	}{c.n.(float64), c.w}
}

func (c windowTotal) sql(b *builder) string {
	total := "count(*)"
	if c.field == "spend_in_window" {
		total = "coalesce(sum(o.total_amount), 0)"
	}
	return "(SELECT " + total + " FROM orders o WHERE o.user_id = u.id AND " + b.inWindow("o.ordered_at", c.w) + ") " +
		comparisons[c.op] + " " + b.arg(c.n)
}

// FixedWindowStarts returns the start of every fixed-date window in the rules,
// resolved in loc. A campaign sent before such a start would evaluate an
// empty window (T9 rejects that at approval).
func FixedWindowStarts(rules []Rule, loc *time.Location) ([]time.Time, error) {
	var starts []time.Time
	for i, r := range rules {
		c, err := parse(r, nil)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		var w window
		switch v := c.(type) {
		case timeRule:
			w = v.w
		case lastOrder:
			w = v.w
		case categoryRule:
			w = v.v.window
		case windowTotal:
			w = v.w
		default:
			continue
		}
		if w.From != nil {
			start, _ := w.bounds(time.Time{}, loc)
			starts = append(starts, start)
		}
	}
	return starts, nil
}

// BuildResend selects a resend's recipients on its channel (D31): users the
// parent campaign skipped for the character limit, minus anyone who may
// already have it on that channel from the parent or any earlier resend:
// sent, still sending, or with an uncertain delivery (D31 history, option A).
// Consent applies.
func BuildResend(parentID int64, channel string) (string, []any, error) {
	col, ok := consent[channel]
	if !ok {
		return "", nil, fmt.Errorf("unknown channel %q", channel)
	}
	where := `u.id IN (SELECT m.user_id FROM messages m WHERE m.campaign_id = $1 AND m.channel = $2
			AND m.skip_reason = 'character_limit_exceeded')
		AND NOT EXISTS (SELECT 1 FROM messages s JOIN campaigns c ON c.id = s.campaign_id
			WHERE (c.id = $1 OR c.parent_campaign_id = $1) AND s.channel = $2 AND s.user_id = u.id
				AND (s.status IN ('sent', 'sending') OR s.delivery_uncertain))
		AND ` + col
	return where, []any{parentID, channel}, nil
}
