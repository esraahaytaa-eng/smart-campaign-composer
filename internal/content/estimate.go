package content

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
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
	// The same per language of the text they get (D64).
	ByLanguage map[Lang]OverLimit `json:"by_language,omitempty"`
}

// EstimateOverLimit counts over-limit messages per language and channel in
// one aggregate query each, rendering in SQL exactly like Render (same order,
// same tidying) instead of rendering every message in memory (D3). Each
// language is checked against its own speakers; the default also against
// everyone whose language has no content, as they are sent the default.
// Only the chosen channels' text may be given.
func EstimateOverLimit(ctx context.Context, db Querier, v Variants, def Lang, audiences map[string]Audience) (OverLimit, error) {
	out := OverLimit{ByLanguage: map[Lang]OverLimit{}}
	others := []Lang{} // never nil: ANY(NULL) would match nobody
	for l := range v {
		if l != def {
			others = append(others, l)
		}
	}
	for lang, c := range v {
		speakers := func(a Audience) Audience {
			args := append(append([]any{}, a.Args...), lang)
			where := fmt.Sprintf("(%s) AND u.language = $%d", a.Where, len(args))
			if lang == def {
				args[len(args)-1] = others
				where = fmt.Sprintf("(%s) AND NOT u.language = ANY($%d)", a.Where, len(args))
			}
			return Audience{where, args}
		}
		var l OverLimit
		if c.SMS != nil {
			n, err := countOver(ctx, db, audiences, speakers, "sms", []string{c.SMS.Body}, []int{SMSLimit})
			if err != nil {
				return out, err
			}
			l.SMS = n
		}
		if c.Email != nil {
			n, err := countOver(ctx, db, audiences, speakers, "email", []string{c.Email.Subject, c.Email.Body}, []int{SubjectLimit, EmailBodyLimit})
			if err != nil {
				return out, err
			}
			l.Email = n
		}
		out.ByLanguage[lang] = l
		out.SMS, out.Email = out.SMS+l.SMS, out.Email+l.Email
	}
	return out, nil
}

// Querier is a pool or a transaction.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// countOver counts recipients for whom any template renders over its limit.
func countOver(ctx context.Context, db Querier, audiences map[string]Audience, of func(Audience) Audience, channel string, templates []string, limits []int) (int, error) {
	a, ok := audiences[channel]
	if !ok {
		return 0, fmt.Errorf("no audience given for %s content", channel)
	}
	a = of(a)
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
	err := db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE `+cond+`) FROM users u WHERE `+a.Where, args...).Scan(&n)
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
		col = fmt.Sprintf("btrim(regexp_replace(%s, '%s', ' ', 'g'), ' ')", col, controlClass)
		s = fmt.Sprintf("replace(%s, '{{%s}}', %s)", s, v, col)
	}
	return fmt.Sprintf(`btrim(regexp_replace(regexp_replace(%s, ' {2,}', ' ', 'g'), ' +([,.!?])', '\1', 'g'), ' ')`, s)
}
