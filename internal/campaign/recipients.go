package campaign

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"smart-campaign-composer/internal/content"
)

const (
	recipientsPageSize = 50
	maxRecipientsPage  = 1000 // more than any campaign here needs; bounds the offset
	maxNameQuery       = 50   // characters
)

// shortcut: offset paging over at most ~6,000 messages per campaign; switch
// to keyset paging on (last attempt, id) if audiences grow far beyond that.

// foldSQL folds Turkish letters and case like audience.foldTurkish, so
// "ISIL" finds "Işıl".
const foldSQL = `lower(translate(%s, 'ÇçĞğİIıÖöŞşÜü', 'ccggiiioossuu'))`

func fold(expr string) string { return strings.Replace(foldSQL, "%s", expr, 1) }

// messageLangSQL is the language whose text a message gets, as the worker
// picks it: its stored language, else the campaign's default (pre-T20 rows),
// else "not recorded" for data with no language at all.
const messageLangSQL = `coalesce(m.language, (SELECT default_language FROM campaigns WHERE id = m.campaign_id), 'und')`

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

type recipientsResponse struct {
	AsOf       time.Time                 `json:"as_of"`
	Groups     map[string]map[string]int `json:"groups"`  // channel → group → count
	Reasons    map[string]int            `json:"reasons"` // for the chosen group and channel
	Total      int                       `json:"total"`   // rows matching every filter
	Page       int                       `json:"page"`
	PageSize   int                       `json:"page_size"`
	Recipients []recipientJSON           `json:"recipients"`
}

// recipientJSON is one message: the least customer data that explains its
// outcome (D47). Contacts are masked; no city, message text or error text.
type recipientJSON struct {
	ID            int64         `json:"id"`
	Name          string        `json:"name"`
	Contact       string        `json:"contact"`
	Channel       string        `json:"channel"`
	Language      content.Lang  `json:"language"` // whose text it got: its own, else the default (D64)
	Variant       *string       `json:"variant"`  // an A/B test's version (D68), else null
	Group         string        `json:"group"`
	Reason        *string       `json:"reason"`
	Detail        *overLimit    `json:"detail"`
	NextAttemptAt *time.Time    `json:"next_attempt_at"` // retrying only
	Attempts      []attemptJSON `json:"attempts"`
}

type overLimit struct {
	Part   string `json:"part"` // sms, subject or body
	Length int    `json:"length"`
	Limit  int    `json:"limit"`
}

type attemptJSON struct {
	AttemptNo  int        `json:"attempt_no"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Outcome    *string    `json:"outcome"`
	HTTPStatus *int       `json:"http_status"`
}

// recipients lists a campaign's messages in one display group (D46, D47).
func (h *Handler) recipients(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	qs := r.URL.Query()
	group, channel, q, lang, variant := qs.Get("group"), qs.Get("channel"), strings.TrimSpace(qs.Get("q")), qs.Get("language"), qs.Get("variant")
	page := 1
	if p := qs.Get("page"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > maxRecipientsPage {
			writeError(w, http.StatusUnprocessableEntity, "page must be a number from 1 to "+strconv.Itoa(maxRecipientsPage))
			return
		}
		page = n
	}
	switch {
	case !slices.Contains(deliveryGroups, group):
		writeError(w, http.StatusUnprocessableEntity, "group must be one of "+strings.Join(deliveryGroups, ", "))
		return
	case channel != "" && channel != "sms" && channel != "email":
		writeError(w, http.StatusUnprocessableEntity, "channel must be sms or email")
		return
	case lang != "" && !content.IsLanguage(lang) && lang != content.LegacyLang:
		writeError(w, http.StatusUnprocessableEntity, "language must be a supported language or "+content.LegacyLang)
		return
	case variant != "" && variant != "a" && variant != "b":
		writeError(w, http.StatusUnprocessableEntity, "variant must be a or b")
		return
	case utf8.RuneCountInString(q) > maxNameQuery:
		writeError(w, http.StatusUnprocessableEntity, "the name search is limited to "+strconv.Itoa(maxNameQuery)+" characters")
		return
	}

	out, err := h.listRecipients(r.Context(), id, group, channel, q, lang, variant, page)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	}
	if err != nil {
		serverError(w, "list recipients", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) listRecipients(ctx context.Context, id int64, group, channel, q, lang, variant string, page int) (recipientsResponse, error) {
	out := recipientsResponse{AsOf: h.Now(), Groups: map[string]map[string]int{}, Reasons: map[string]int{},
		Page: page, PageSize: recipientsPageSize, Recipients: []recipientJSON{}}
	v, err := h.loadContent(ctx, id, "a") // fixed after approval (D11)
	if err != nil {
		return out, err
	}
	vb, err := h.loadContent(ctx, id, "b")
	if err != nil {
		return out, err
	}
	// One point in time for counts and rows, as in snapshot.
	tx, err := h.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)

	var channels []string
	if err := tx.QueryRow(ctx, `SELECT channels FROM campaigns WHERE id = $1`, id).Scan(&channels); err != nil {
		return out, err
	}
	for _, ch := range channels {
		out.Groups[ch] = map[string]int{}
		for _, g := range deliveryGroups {
			out.Groups[ch][g] = 0
		}
	}
	rows, err := tx.Query(ctx, `SELECT channel, `+groupSQL+`, count(*) FROM messages m WHERE campaign_id = $1 GROUP BY 1, 2`, id)
	if err != nil {
		return out, err
	}
	var ch, g string
	var n int
	if _, err := pgx.ForEachRow(rows, []any{&ch, &g, &n}, func() error {
		out.Groups[ch][g] = n
		return nil
	}); err != nil {
		return out, err
	}

	// $1 campaign, $2 group, $3 channel ('' for both). The attempt lookups
	// behind reasonSQL run only where a reason is needed: per message for the
	// reason counts, and for the 50 rows of the page, never for the whole group.
	where := ` WHERE m.campaign_id = $1 AND ` + groupSQL + ` = $2 AND ($3 = '' OR m.channel = $3)`
	if groupsWithReasons[group] {
		rows, err = tx.Query(ctx, `SELECT `+reasonSQL+`, count(*) FROM messages m`+lastAttemptsSQL+where+` GROUP BY 1`, id, group, channel)
		if err != nil {
			return out, err
		}
		var reason *string
		if _, err := pgx.ForEachRow(rows, []any{&reason, &n}, func() error {
			if reason != nil {
				out.Reasons[*reason] = n
			}
			return nil
		}); err != nil {
			return out, err
		}
	}

	// The name search (LIKE-escaped) and the language narrow the total and the rows only.
	from, args := ` FROM messages m`, []any{id, group, channel}
	if q != "" {
		from += ` JOIN users u ON u.id = m.user_id`
		args = append(args, likeEscaper.Replace(q))
		where += ` AND ` + fold(`concat_ws(' ', u.first_name, u.last_name)`) + ` LIKE '%' || ` + fold(`$`+strconv.Itoa(len(args))) + ` || '%'`
	}
	if lang != "" {
		args = append(args, lang)
		where += ` AND ` + messageLangSQL + ` = $` + strconv.Itoa(len(args))
	}
	if variant != "" {
		args = append(args, variant)
		where += ` AND m.variant = $` + strconv.Itoa(len(args))
	}
	if err := tx.QueryRow(ctx, `SELECT count(*)`+from+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}

	rows, err = tx.Query(ctx, `WITH page AS (
			SELECT m.id, (SELECT max(started_at) FROM delivery_attempts WHERE message_id = m.id) AS last_at`+from+where+`
			ORDER BY last_at DESC NULLS LAST, m.id DESC
			LIMIT `+strconv.Itoa(recipientsPageSize)+` OFFSET `+strconv.Itoa((page-1)*recipientsPageSize)+`)
		SELECT m.id, concat_ws(' ', u.first_name, u.last_name), u.first_name, coalesce(u.last_name, ''), u.city,
			CASE m.channel WHEN 'sms' THEN u.phone ELSE u.email END, m.channel, `+messageLangSQL+`, m.variant, `+reasonSQL+`,
			CASE WHEN m.status = 'retry_scheduled' THEN m.next_attempt_at END
		FROM page p JOIN messages m ON m.id = p.id JOIN users u ON u.id = m.user_id`+lastAttemptsSQL+`
		ORDER BY p.last_at DESC NULLS LAST, m.id DESC`, args...)
	if err != nil {
		return out, err
	}
	byID := map[int64]int{}
	var first, last, city, contact string
	var rc recipientJSON
	if _, err := pgx.ForEachRow(rows, []any{&rc.ID, &rc.Name, &first, &last, &city, &contact, &rc.Channel, &rc.Language, &rc.Variant, &rc.Reason, &rc.NextAttemptAt}, func() error {
		rc.Group, rc.Attempts, rc.Detail = group, []attemptJSON{}, nil
		if rc.Channel == "sms" {
			rc.Contact = maskPhone(contact)
		} else {
			rc.Contact = maskEmail(contact)
		}
		texts := v
		if rc.Variant != nil && *rc.Variant == "b" {
			texts = vb
		}
		if tpl, ok := texts[rc.Language]; ok && rc.Reason != nil && *rc.Reason == "character_limit_exceeded" {
			rc.Detail = overLimitDetail(tpl, rc.Channel, content.UserVars{FirstName: first, LastName: last, City: city})
		}
		byID[rc.ID] = len(out.Recipients)
		out.Recipients = append(out.Recipients, rc)
		return nil
	}); err != nil {
		return out, err
	}
	if len(byID) == 0 {
		return out, nil
	}

	ids := make([]int64, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	rows, err = tx.Query(ctx, `SELECT message_id, attempt_no, started_at, finished_at, outcome, http_status
		FROM delivery_attempts WHERE message_id = ANY($1) ORDER BY id`, ids)
	if err != nil {
		return out, err
	}
	var mid int64
	var a attemptJSON
	_, err = pgx.ForEachRow(rows, []any{&mid, &a.AttemptNo, &a.StartedAt, &a.FinishedAt, &a.Outcome, &a.HTTPStatus}, func() error {
		i := byID[mid]
		out.Recipients[i].Attempts = append(out.Recipients[i].Attempts, a)
		return nil
	})
	return out, err
}

// overLimitDetail is the first part over its limit once personalized with the
// recipient's current data, as the worker checks it (D3).
func overLimitDetail(c content.Content, channel string, u content.UserVars) *overLimit {
	type part struct {
		name, tpl string
		limit     int
	}
	var parts []part
	switch {
	case channel == "sms" && c.SMS != nil:
		parts = []part{{"sms", c.SMS.Body, content.SMSLimit}}
	case channel == "email" && c.Email != nil:
		parts = []part{{"subject", c.Email.Subject, content.SubjectLimit}, {"body", c.Email.Body, content.EmailBodyLimit}}
	}
	for _, p := range parts {
		if n := utf8.RuneCountInString(content.Render(p.tpl, u)); n > p.limit {
			return &overLimit{p.name, n, p.limit}
		}
	}
	return nil // the data changed since the skip
}

// maskPhone keeps a Turkish number's country and operator code and the last
// two digits: +90 555 ••• •• 17. Other formats keep only the last two.
func maskPhone(p string) string {
	r := []rune(p)
	switch {
	case len(r) == 13 && strings.HasPrefix(p, "+90"):
		return "+90 " + string(r[3:6]) + " ••• •• " + string(r[11:])
	case len(r) >= 6:
		return "••• " + string(r[len(r)-2:])
	}
	return "•••"
}

// maskEmail keeps at most the first 3 characters of the local part, and never
// more than half of it, plus the domain: ays•••@example.com.
func maskEmail(e string) string {
	at := strings.LastIndex(e, "@")
	if at < 0 {
		return "•••"
	}
	local := []rune(e[:at])
	return string(local[:min(3, len(local)/2)]) + "•••" + e[at:]
}
