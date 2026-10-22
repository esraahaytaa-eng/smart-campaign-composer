package content

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Audience selects one channel's recipients: a WHERE clause over users "u"
// with its arguments, e.g. from audience.Build or a resend's fixed list.
type Audience struct {
	Where string
	Args  []any
}

// OverLimit is how many recipients per channel would get a message over the
// limit with the current data: an estimate, shown as a warning (D3).
type OverLimit struct {
	SMS   int `json:"sms"`
	Email int `json:"email"`
}

// EstimateOverLimit counts over-limit messages per channel in one aggregate
// query each, rendering in SQL exactly like Render (same order, same tidying)
// instead of rendering every message in memory (D3).
func EstimateOverLimit(ctx context.Context, pool *pgxpool.Pool, c Content, audiences map[string]Audience) (OverLimit, error) {
	var out OverLimit
	if c.SMS != nil {
		n, err := countOver(ctx, pool, audiences, "sms", []string{c.SMS.Body}, []int{SMSLimit})
		if err != nil {
			return out, err
		}
		out.SMS = n
	}
	if c.Email != nil {
		n, err := countOver(ctx, pool, audiences, "email", []string{c.Email.Subject, c.Email.Body}, []int{SubjectLimit, EmailBodyLimit})
		if err != nil {
			return out, err
		}
		out.Email = n
	}
	return out, nil
}

// countOver counts recipients for whom any template renders over its limit.
func countOver(ctx context.Context, pool *pgxpool.Pool, audiences map[string]Audience, channel string, templates []string, limits []int) (int, error) {
	a, ok := audiences[channel]
	if !ok {
		return 0, fmt.Errorf("no audience given for %s content", channel)
	}
	args := append([]any{}, a.Args...)
	cond := ""
	for i, tpl := range templates {
		args = append(args, tpl)
		if i > 0 {
			cond += " OR "
		}
		cond += fmt.Sprintf("char_length(%s) > %d", renderSQL(fmt.Sprintf("$%d::text", len(args))), limits[i])
	}
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE `+cond+`) FROM users u WHERE `+a.Where, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("estimate %s over-limit messages: %w", channel, err)
	}
	return n, nil
}

// renderSQL is Render as a SQL expression over users "u"; keep the two in step
// (TestEstimate_MatchesGoRender).
func renderSQL(tpl string) string {
	s := tpl
	for _, v := range renderOrder {
		col := "u." + string(v)
		if v == LastName {
			col = "coalesce(u.last_name, '')"
		}
		s = fmt.Sprintf("replace(%s, '{{%s}}', %s)", s, v, col)
	}
	return fmt.Sprintf("btrim(regexp_replace(%s, ' {2,}', ' ', 'g'), ' ')", s)
}
