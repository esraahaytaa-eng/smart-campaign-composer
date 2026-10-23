package campaign

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	listPageSize  = 25
	maxListPage   = 10000
	dashboardRows = 6
)

var campaignStatuses = []string{"draft", "approved", "sending", "completed", "cancelled"}

// listJSON is a page of the campaign list (D54): newest first, with the total
// for the filter and the status counts over all campaigns.
type listJSON struct {
	Campaigns []campaignJSON `json:"campaigns"`
	Total     int            `json:"total"`
	Page      int            `json:"page,omitempty"`
	PageSize  int            `json:"page_size,omitempty"`
	Counts    map[string]int `json:"counts"`
}

// A resend shows its original's brief, which may be on another page.
const listColumns = campaignColumns + `,
	(SELECT p.request_text FROM campaigns p WHERE p.id = campaigns.parent_campaign_id)`

// list is GET /api/campaigns?status=&q=&page= (D54).
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	status, q := qs.Get("status"), strings.TrimSpace(qs.Get("q"))
	page := 1
	if p := qs.Get("page"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > maxListPage {
			writeError(w, http.StatusUnprocessableEntity, "page must be a number from 1 to "+strconv.Itoa(maxListPage))
			return
		}
		page = n
	}
	if status == "all" {
		status = ""
	}
	switch {
	case status != "" && !slices.Contains(campaignStatuses, status):
		writeError(w, http.StatusUnprocessableEntity, "status must be all or one of "+strings.Join(campaignStatuses, ", "))
		return
	case utf8.RuneCountInString(q) > maxNameQuery:
		writeError(w, http.StatusUnprocessableEntity, "the search is limited to "+strconv.Itoa(maxNameQuery)+" characters")
		return
	}

	where, args := ` WHERE ($1 = '' OR status = $1)`, []any{status}
	if q != "" {
		where += ` AND ` + fold(`request_text`) + ` LIKE '%' || ` + fold(`$2`) + ` || '%'`
		args = append(args, likeEscaper.Replace(q))
	}
	out := listJSON{Page: page, PageSize: listPageSize}
	if err := h.Pool.QueryRow(r.Context(), `SELECT count(*) FROM campaigns`+where, args...).Scan(&out.Total); err != nil {
		serverError(w, "count campaigns", err)
		return
	}
	var err error
	if out.Campaigns, err = h.campaignRows(r.Context(), `SELECT `+listColumns+` FROM campaigns`+where+
		` ORDER BY id DESC LIMIT `+strconv.Itoa(listPageSize)+` OFFSET `+strconv.Itoa((page-1)*listPageSize), args...); err != nil {
		serverError(w, "list campaigns", err)
		return
	}
	if out.Counts, err = h.statusCounts(r.Context()); err != nil {
		serverError(w, "count statuses", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// dashboard is GET /api/dashboard: the status counts and the campaigns that
// need attention first (D54): sending, drafts, scheduled by send time, then
// the rest, most recently changed first.
func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	var out listJSON
	var err error
	if out.Campaigns, err = h.campaignRows(r.Context(), `SELECT `+listColumns+` FROM campaigns
		ORDER BY CASE status WHEN 'sending' THEN 0 WHEN 'draft' THEN 1 WHEN 'approved' THEN 2 ELSE 3 END,
			CASE WHEN status = 'approved' THEN send_at END, updated_at DESC, id DESC
		LIMIT `+strconv.Itoa(dashboardRows)); err != nil {
		serverError(w, "dashboard campaigns", err)
		return
	}
	if out.Counts, err = h.statusCounts(r.Context()); err != nil {
		serverError(w, "count statuses", err)
		return
	}
	out.Total = out.Counts["all"]
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) campaignRows(ctx context.Context, sql string, args ...any) ([]campaignJSON, error) {
	rows, _ := h.Pool.Query(ctx, sql, args...)
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (campaignJSON, error) {
		var c campaignJSON
		return c, scanInto(row, &c, &c.ParentRequestText)
	})
	if list == nil {
		list = []campaignJSON{}
	}
	return list, err
}

func (h *Handler) statusCounts(ctx context.Context) (map[string]int, error) {
	counts := map[string]int{"all": 0}
	for _, s := range campaignStatuses {
		counts[s] = 0
	}
	rows, _ := h.Pool.Query(ctx, `SELECT status, count(*) FROM campaigns GROUP BY status`)
	var s string
	var n int
	_, err := pgx.ForEachRow(rows, []any{&s, &n}, func() error {
		counts[s] = n
		counts["all"] += n
		return nil
	})
	return counts, err
}
