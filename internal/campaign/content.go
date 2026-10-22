package campaign

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"

	"smart-campaign-composer/internal/content"
	"smart-campaign-composer/internal/llm"
)

var errStale = errors.New("the campaign changed since you loaded it; reload and try again")

// generateContent streams AI-written content as server-sent events (D21,
// D34): "delta" {field, text} while the model writes, "retry" {errors} when
// the first answer is invalid, then "done" {content, version} or "error"
// {message}. Only a valid answer is saved, and only while the client is
// still connected (D15).
func (h *Handler) generateContent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version *int `json:"version"`
	}
	if !decodeBody(w, r, &in) || !h.editableDraft(w, r.Context(), id, in.Version) {
		return
	}
	var brief string
	var channels []string
	var version int
	var isResend bool
	if err := h.Pool.QueryRow(r.Context(), `SELECT request_text, channels, version, parent_campaign_id IS NOT NULL
		FROM campaigns WHERE id = $1`, id).Scan(&brief, &channels, &version, &isResend); err != nil {
		serverError(w, "load campaign", err)
		return
	}
	if version != *in.Version {
		writeError(w, http.StatusConflict, errStale.Error())
		return
	}

	send := startSSE(w)

	input := llm.ContentInput{Text: brief, Channels: channels, Resend: isResend}
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
		streamer := llm.NewFieldStreamer(contentFields, func(d llm.Delta) { send("delta", d) })
		raw, err := h.LLM.StreamContent(ctx, input, streamer.Write)
		cancel()
		if r.Context().Err() != nil {
			llmCalls.WithLabelValues("content", "cancelled").Inc()
			return // the client left: nothing is saved
		}
		var c content.Content
		var problems []string
		switch {
		case errors.Is(err, llm.ErrTruncated):
			problems = []string{"the answer was cut off; keep the messages short"}
		case err != nil:
			llmCalls.WithLabelValues("content", "error").Inc()
			send("error", map[string]string{"message": aiErrorMessage(err)})
			return
		default:
			c, problems = parseContent(raw, channels)
		}
		if len(problems) > 0 {
			llmCalls.WithLabelValues("content", "invalid").Inc()
			if attempt == 0 {
				send("retry", map[string]any{"errors": problems})
				input.Errors = problems
				continue
			}
			slog.Warn("AI content invalid twice", "campaign", id, "problems", problems)
			send("error", map[string]string{"message": "The AI wrote invalid content twice. Edit the messages by hand or try again."})
			return
		}
		llmCalls.WithLabelValues("content", "ok").Inc()
		newVersion, err := h.saveContent(r.Context(), id, *in.Version, c)
		switch {
		case r.Context().Err() != nil:
			return
		case errors.Is(err, errStale):
			send("error", map[string]string{"message": err.Error()})
		case err != nil:
			slog.Error("save content", "err", err)
			send("error", map[string]string{"message": "internal error"})
		default:
			send("done", map[string]any{"content": c, "version": newVersion})
		}
		return
	}
}

var contentFields = []string{"sms.body", "email.subject", "email.body"}

// parseContent decodes the model's answer strictly and validates it (T7).
func parseContent(raw json.RawMessage, channels []string) (content.Content, []string) {
	var c content.Content
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, []string{"answer is not valid JSON for the schema: " + err.Error()}
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return c, []string{"answer has data after the JSON object"}
	}
	var problems []string
	for _, e := range content.ValidateDraft(c, channels) {
		problems = append(problems, e.Field+": "+e.Message)
	}
	return c, problems
}

func aiErrorMessage(err error) string {
	switch {
	case errors.Is(err, llm.ErrRefused):
		return "The AI declined to write this content. Rephrase the brief or write the messages by hand."
	case errors.Is(err, context.DeadlineExceeded):
		slog.Warn("AI content timed out", "err", err)
		return "The AI did not answer in time. Try again or write the messages by hand."
	default:
		slog.Error("AI content failed", "err", err)
		return "The AI service is unavailable. Try again or write the messages by hand."
	}
}

// updateContent saves content the marketer edited by hand.
func (h *Handler) updateContent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version *int            `json:"version"`
		Content content.Content `json:"content"`
	}
	if !decodeBody(w, r, &in) || !h.editableDraft(w, r.Context(), id, in.Version) {
		return
	}
	var channels []string
	if err := h.Pool.QueryRow(r.Context(), `SELECT channels FROM campaigns WHERE id = $1`, id).Scan(&channels); err != nil {
		serverError(w, "load campaign", err)
		return
	}
	if errs := content.ValidateDraft(in.Content, channels); len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid content", "field_errors": errs})
		return
	}
	_, err := h.saveContent(r.Context(), id, *in.Version, in.Content)
	if errors.Is(err, errStale) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		serverError(w, "save content", err)
		return
	}
	h.respond(w, r.Context(), http.StatusOK, id)
}

// saveContent replaces the draft's content if it still has the expected
// version, and returns the new version.
func (h *Handler) saveContent(ctx context.Context, id int64, version int, c content.Content) (int, error) {
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var newVersion int
	err = tx.QueryRow(ctx, `UPDATE campaigns SET version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 AND status = 'draft' RETURNING version`, id, version).Scan(&newVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errStale
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM campaign_content WHERE campaign_id = $1`, id); err != nil {
		return 0, err
	}
	for ch, m := range map[string]*content.Message{"sms": c.SMS, "email": c.Email} {
		if m == nil {
			continue
		}
		var subject *string
		if ch == "email" {
			subject = &m.Subject
		}
		if _, err := tx.Exec(ctx, `INSERT INTO campaign_content (campaign_id, channel, subject, body) VALUES ($1, $2, $3, $4)`,
			id, ch, subject, m.Body); err != nil {
			return 0, err
		}
	}
	return newVersion, tx.Commit(ctx)
}

// loadContent reads a campaign's saved content; nil if there is none.
func (h *Handler) loadContent(ctx context.Context, id int64) (*content.Content, error) {
	rows, err := h.Pool.Query(ctx, `SELECT channel, coalesce(subject, ''), body FROM campaign_content WHERE campaign_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var c *content.Content
	for rows.Next() {
		var ch string
		var m content.Message
		if err := rows.Scan(&ch, &m.Subject, &m.Body); err != nil {
			return nil, err
		}
		if c == nil {
			c = &content.Content{}
		}
		if ch == "sms" {
			c.SMS = &m
		} else {
			c.Email = &m
		}
	}
	return c, rows.Err()
}
