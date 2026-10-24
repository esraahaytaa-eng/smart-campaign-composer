package campaign

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"smart-campaign-composer/internal/auth"
	"smart-campaign-composer/internal/content"
)

// approveLanguage records that the marketer read one language's text and
// approves it as it is (D66). It never writes content, so re-approving after
// a brief change costs no AI call. The version is checked, not bumped: a
// later edit of the text puts the language back to review anyway.
func (h *Handler) approveLanguage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	lang := r.PathValue("lang")
	var in struct {
		Version *int `json:"version"`
	}
	ctx := r.Context()
	if !decodeBody(w, r, &in) || !h.editableDraft(w, ctx, id, in.Version) {
		return
	}
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		serverError(w, "begin", err)
		return
	}
	defer tx.Rollback(ctx)
	// The row lock makes a concurrent content save wait, so the text approved is the text read.
	var channels []string
	err = tx.QueryRow(ctx, `SELECT channels FROM campaigns WHERE id = $1 AND version = $2 AND status = 'draft' FOR UPDATE`,
		id, *in.Version).Scan(&channels)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, errStale.Error())
		return
	}
	if err != nil {
		serverError(w, "lock draft", err)
		return
	}
	v, err := h.loadContent(ctx, id)
	if err != nil {
		serverError(w, "load content", err)
		return
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM campaign_languages WHERE campaign_id = $1 AND language = $2)`,
		id, lang).Scan(&exists); err != nil {
		serverError(w, "load language", err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "this campaign has no content in that language")
		return
	}
	if errs := content.ValidateVariants(content.Variants{lang: v[lang]}, channels, lang); len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid content", "field_errors": errs})
		return
	}
	marketer, _ := auth.MarketerID(ctx)
	if _, err := tx.Exec(ctx, `UPDATE campaign_languages SET approved_at = $3, approved_by = $4, outdated = false
		WHERE campaign_id = $1 AND language = $2`, id, lang, h.Now(), marketer); err != nil {
		serverError(w, "approve language", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, "commit", err)
		return
	}
	h.respond(w, ctx, http.StatusOK, id)
}

// unapprovedLanguages names the languages still waiting for review, for the
// campaign approval error.
func unapprovedLanguages(ctx context.Context, q pgx.Tx, id int64) (string, error) {
	rows, err := q.Query(ctx, `SELECT language FROM campaign_languages WHERE campaign_id = $1 AND approved_at IS NULL ORDER BY language`, id)
	if err != nil {
		return "", err
	}
	langs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	var names []string
	for _, l := range langs {
		names = append(names, content.LanguageName(l))
	}
	return strings.Join(names, ", "), err
}
