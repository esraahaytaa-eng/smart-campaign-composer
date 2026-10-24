package campaign

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"smart-campaign-composer/internal/auth"
	"smart-campaign-composer/internal/content"
	"smart-campaign-composer/internal/llm"
)

var errStale = errors.New("the campaign changed since you loaded it; reload and try again")

// generateContent streams AI-written content for the selected languages as
// server-sent events (D21, D34): "delta" {field, text} while the model
// writes ("en.sms.body"), "retry" {errors} when the first answer is invalid,
// then "done" {content, version} or "error" {message, code?}. Only a valid answer
// is saved, and only while the client is still connected (D15). It writes
// only the selected languages and keeps the others as they are; a language
// that already has text is rewritten only with replace (D66).
func (h *Handler) generateContent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version         *int           `json:"version"`
		Languages       []content.Lang `json:"languages"`
		DefaultLanguage content.Lang   `json:"default_language"` // optional: the default to set
		Replace         bool           `json:"replace"`          // rewrite languages that have text
		Variant         string         `json:"variant"`          // "b": write an A/B test's version B (D68)
	}
	if !decodeBody(w, r, &in) || !h.editableDraft(w, r.Context(), id, in.Version) {
		return
	}
	if msg := checkLanguages(in.Languages); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	var brief string
	var channels []string
	var version int
	var isResend bool
	var def *string
	var written []content.Lang
	var abTest bool
	if err := h.Pool.QueryRow(r.Context(), `SELECT request_text, channels, version, parent_campaign_id IS NOT NULL, default_language,
			ARRAY(SELECT language FROM campaign_languages l WHERE l.campaign_id = c.id), ab_test
		FROM campaigns c WHERE id = $1`, id).Scan(&brief, &channels, &version, &isResend, &def, &written, &abTest); err != nil {
		serverError(w, "load campaign", err)
		return
	}
	if version != *in.Version {
		writeError(w, http.StatusConflict, errStale.Error())
		return
	}
	// Checked before any AI call, so a refusal costs nothing (D63).
	if slices.Contains(written, content.LegacyLang) {
		writeError(w, http.StatusUnprocessableEntity, "set the language of the existing text before writing another language")
		return
	}
	var versionA content.Variants
	switch in.Variant {
	case "", "a":
	case "b":
		if !abTest {
			writeError(w, http.StatusUnprocessableEntity, "turn on the A/B test to write version B")
			return
		}
		a, err := h.loadContent(r.Context(), id, "a")
		if err != nil {
			serverError(w, "load content", err)
			return
		}
		b, err := h.loadContent(r.Context(), id, "b")
		if err != nil {
			serverError(w, "load content", err)
			return
		}
		versionA = content.Variants{}
		for _, l := range in.Languages {
			c, ok := a[l]
			if !ok {
				writeError(w, http.StatusUnprocessableEntity, "write version A in "+content.LanguageName(l)+" first")
				return
			}
			versionA[l] = content.ForChannels(c, channels)
		}
		written = slices.Collect(maps.Keys(b)) // what replace protects: existing version B
		in.DefaultLanguage = ""                // writing B never changes the default
	default:
		writeError(w, http.StatusUnprocessableEntity, "unknown version: "+in.Variant)
		return
	}
	if !in.Replace {
		var existing []string
		for _, l := range in.Languages {
			if slices.Contains(written, l) {
				existing = append(existing, content.LanguageName(l))
			}
		}
		if len(existing) > 0 {
			writeError(w, http.StatusConflict, "this would replace the existing text in "+strings.Join(existing, ", ")+"; confirm to replace it")
			return
		}
	}
	switch {
	case in.DefaultLanguage != "":
		if !slices.Contains(in.Languages, in.DefaultLanguage) && !slices.Contains(written, in.DefaultLanguage) {
			writeError(w, http.StatusUnprocessableEntity, "the default language must be one of the campaign's languages")
			return
		}
		def = &in.DefaultLanguage
	case def == nil:
		def = &in.Languages[0] // until the marketer picks another
	}

	// The stream can't answer 429 once started, so the limit is checked first (D63).
	if marketer, _ := auth.MarketerID(r.Context()); h.AICalls != nil {
		if wait := h.AICalls.wait(marketer); wait > 0 {
			writeAILimit(w, &aiLimitError{h.AICalls.n, wait})
			return
		}
	}
	send := startSSE(w)

	input := llm.ContentInput{Text: brief, Channels: channels, Languages: in.Languages, Resend: isResend, VersionA: versionA}
	var fields []string
	for _, l := range in.Languages {
		for _, f := range contentFields {
			fields = append(fields, l+"."+f)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
		streamer := llm.NewFieldStreamer(fields, func(d llm.Delta) { send("delta", d) })
		start, gotText := time.Now(), false
		raw, err := h.model().StreamContent(ctx, input, func(text string) {
			if !gotText {
				gotText = true
				llmFirstText.WithLabelValues("content").Observe(time.Since(start).Seconds())
			}
			streamer.Write(text)
		})
		llmDuration.WithLabelValues("content").Observe(time.Since(start).Seconds())
		cancel()
		if r.Context().Err() != nil {
			llmCalls.WithLabelValues("content", "cancelled").Inc()
			return // the client left: nothing is saved
		}
		var v content.Variants
		var problems []string
		switch {
		case errors.Is(err, llm.ErrTruncated):
			problems = []string{"the answer was cut off; keep the messages short"}
		case err != nil:
			llmCalls.WithLabelValues("content", "error").Inc()
			send("error", aiErrorEvent(err))
			return
		default:
			v, problems = parseContent(raw, channels, in.Languages)
		}
		if len(problems) > 0 {
			llmCalls.WithLabelValues("content", "invalid").Inc()
			if attempt == 0 {
				send("retry", map[string]any{"errors": problems})
				input.Errors = problems
				continue
			}
			slog.Warn("AI content invalid twice", "campaign", id, "problems", problems)
			send("error", map[string]string{"message": "The AI wrote invalid content twice. Edit the messages by hand or try again.", "code": "ai_invalid"})
			return
		}
		llmCalls.WithLabelValues("content", "ok").Inc()
		newVersion, err := h.saveContent(r.Context(), id, *in.Version, toSave(versionA != nil, v, *def))
		switch {
		case r.Context().Err() != nil:
			return
		case errors.Is(err, errStale):
			send("error", map[string]string{"message": err.Error()})
		case err != nil:
			slog.Error("save content", "err", err)
			send("error", map[string]string{"message": "internal error"})
		default:
			send("done", map[string]any{"content": v, "version": newVersion})
		}
		return
	}
}

// greetsByName asks the AI's SMS and email body to address the recipient
// with {{first_name}} (D72). Only AI text: the marketer's own text is free.
func greetsByName(lang content.Lang, c content.Content) []string {
	var problems []string
	for field, m := range map[string]*content.Message{"sms.body": c.SMS, "email.body": c.Email} {
		if m != nil && !strings.Contains(m.Body, "{{"+string(content.FirstName)+"}}") {
			problems = append(problems, lang+"."+field+": greet the recipient by name with {{first_name}}")
		}
	}
	slices.Sort(problems)
	return problems
}

var contentFields = []string{"sms.body", "email.subject", "email.body"}

// checkLanguages accepts one or more distinct catalog languages; the legacy
// label is never written by the AI (D64).
func checkLanguages(langs []content.Lang) string {
	if len(langs) == 0 {
		return "choose at least one language to write"
	}
	for i, l := range langs {
		if !content.IsLanguage(l) {
			return "unsupported language: " + l
		}
		if slices.Contains(langs[:i], l) {
			return "a language is listed twice: " + l
		}
	}
	return ""
}

// parseContent decodes the model's answer strictly and validates it (T7):
// exactly the requested languages, each complete for the chosen channels.
func parseContent(raw json.RawMessage, channels []string, langs []content.Lang) (content.Variants, []string) {
	var answer map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&answer); err != nil {
		return nil, []string{"answer is not valid JSON for the schema: " + err.Error()}
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return nil, []string{"answer has data after the JSON object"}
	}
	var problems []string
	v := content.Variants{}
	for key, msg := range answer {
		if !slices.Contains(langs, key) {
			problems = append(problems, key+": a language that was not asked for")
			continue
		}
		var c content.Content
		dec := json.NewDecoder(bytes.NewReader(msg))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			problems = append(problems, key+": not valid JSON for the schema: "+err.Error())
			continue
		}
		for _, e := range content.ValidateDraft(c, channels) {
			problems = append(problems, key+"."+e.Field+": "+e.Message)
		}
		problems = append(problems, greetsByName(key, c)...)
		v[key] = c
	}
	for _, l := range langs {
		if _, ok := answer[l]; !ok {
			problems = append(problems, l+": the version in this language is missing")
		}
	}
	slices.Sort(problems)
	return v, problems
}

// aiErrorEvent is the "error" event for a failed call; "code" tells the UI a
// provider failure (bad key, outage) apart from an invalid answer.
func aiErrorEvent(err error) map[string]string {
	var limited *aiLimitError
	switch {
	case errors.As(err, &limited):
		return map[string]string{"message": limited.Error()}
	case errors.Is(err, llm.ErrRefused):
		return map[string]string{"message": "The AI declined to write this content. Rephrase the brief or write the messages by hand."}
	case errors.Is(err, context.DeadlineExceeded):
		slog.Warn("AI content timed out", "err", err)
		return map[string]string{"message": "The AI did not answer in time. Try again or write the messages by hand."}
	default:
		slog.Error("AI content failed", "err", err)
		return map[string]string{"message": "The AI service is unavailable. Try again or write the messages by hand.", "code": "ai_unavailable"}
	}
}

// updateContent saves content the marketer edited by hand: every language
// the campaign keeps, and the default. Languages left out are removed.
func (h *Handler) updateContent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version         *int             `json:"version"`
		DefaultLanguage content.Lang     `json:"default_language"`
		Content         content.Variants `json:"content"`
		ContentB        content.Variants `json:"content_b"` // absent: version B unchanged (D68)
	}
	if !decodeBody(w, r, &in) || !h.editableDraft(w, r.Context(), id, in.Version) {
		return
	}
	var channels []string
	var legacy, abTest bool
	if err := h.Pool.QueryRow(r.Context(), `SELECT channels, EXISTS (SELECT 1 FROM campaign_languages
		WHERE campaign_id = $1 AND language = $2), ab_test FROM campaigns WHERE id = $1`, id, content.LegacyLang).Scan(&channels, &legacy, &abTest); err != nil {
		serverError(w, "load campaign", err)
		return
	}
	errs := content.ValidateVariants(in.Content, channels, in.DefaultLanguage)
	if _, ok := in.Content[content.LegacyLang]; ok && !legacy {
		errs = append(errs, content.FieldError{Field: content.LegacyLang, Message: "content without a recorded language exists only on campaigns written before languages were recorded"})
	}
	if in.ContentB != nil && !abTest {
		errs = append(errs, content.FieldError{Field: "b", Message: "turn on the A/B test to write version B"})
	} else if in.ContentB != nil {
		errs = append(errs, content.ValidateVersionB(in.Content, in.ContentB, channels, false)...)
	}
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid content", "field_errors": errs})
		return
	}
	_, err := h.saveContent(r.Context(), id, *in.Version, contentSave{a: in.Content, b: in.ContentB, replaceB: in.ContentB != nil, def: in.DefaultLanguage})
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

// toSave is the AI's text as saveContent writes it: version B for an A/B
// test (versionB), else version A.
func toSave(versionB bool, v content.Variants, def content.Lang) contentSave {
	if versionB {
		return contentSave{b: v, def: def, generated: true}
	}
	return contentSave{a: v, def: def, generated: true}
}

// contentSave is what saveContent writes. A nil version is left unchanged.
type contentSave struct {
	a, b      content.Variants
	replaceB  bool // b is version B's whole set: other languages' B is removed
	def       content.Lang
	generated bool
}

// saveContent writes the text of the given languages for the chosen channels
// if the draft still has the expected version, and returns the new version.
// Text of channels that aren't chosen is kept as it is (D67).
//   - Hand edits (generated false) replace the whole set: languages not in a
//     are removed, and a language whose text changed in either version needs
//     review again, while one saved unchanged keeps its approval (D66, D68).
//   - AI text (generated true) is merged: other languages stay exactly as
//     they were, and each written language needs review. Writing version A
//     is for the current brief; writing B leaves A's "may be outdated".
func (h *Handler) saveContent(ctx context.Context, id int64, version int, s contentSave) (int, error) {
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var newVersion int
	var channels []string
	err = tx.QueryRow(ctx, `UPDATE campaigns SET version = version + 1, updated_at = now(), default_language = $3
		WHERE id = $1 AND version = $2 AND status = 'draft' RETURNING version, channels`, id, version, s.def).Scan(&newVersion, &channels)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errStale
	}
	if err != nil {
		return 0, err
	}
	if !s.generated && s.a != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM campaign_languages WHERE campaign_id = $1 AND NOT language = ANY($2)`,
			id, slices.Collect(maps.Keys(s.a))); err != nil {
			return 0, err
		}
	}
	changed := map[content.Lang]bool{}
	if s.replaceB {
		rows, err := tx.Query(ctx, `DELETE FROM campaign_content WHERE campaign_id = $1 AND variant = 'b' AND NOT language = ANY($2)
			RETURNING language`, id, slices.Collect(maps.Keys(s.b)))
		if err != nil {
			return 0, err
		}
		langs, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return 0, err
		}
		for _, l := range langs {
			changed[l] = true
		}
	}
	for _, set := range []struct {
		variant string
		v       content.Variants
	}{{"a", s.a}, {"b", s.b}} {
		for lang, c := range set.v {
			if _, err := tx.Exec(ctx, `INSERT INTO campaign_languages (campaign_id, language) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, lang); err != nil {
				return 0, err
			}
			for ch, m := range map[string]*content.Message{"sms": c.SMS, "email": c.Email} {
				if m == nil || !slices.Contains(channels, ch) {
					continue
				}
				var subject *string
				if ch == "email" {
					subject = &m.Subject
				}
				tag, err := tx.Exec(ctx, `INSERT INTO campaign_content (campaign_id, channel, language, variant, subject, body) VALUES ($1, $2, $3, $4, $5, $6)
					ON CONFLICT (campaign_id, channel, language, variant) DO UPDATE SET subject = excluded.subject, body = excluded.body
					WHERE (campaign_content.subject, campaign_content.body) IS DISTINCT FROM (excluded.subject, excluded.body)`,
					id, ch, lang, set.variant, subject, m.Body)
				if err != nil {
					return 0, err
				}
				changed[lang] = changed[lang] || tag.RowsAffected() > 0 || s.generated
			}
		}
	}
	for lang := range changed {
		if !changed[lang] {
			continue
		}
		// Only AI text for version A is written for the current brief.
		fresh := s.generated && s.a != nil
		if _, err := tx.Exec(ctx, `UPDATE campaign_languages SET approved_at = NULL, approved_by = NULL, outdated = outdated AND NOT $3
			WHERE campaign_id = $1 AND language = $2`, id, lang, fresh); err != nil {
			return 0, err
		}
	}
	return newVersion, tx.Commit(ctx)
}

// loadContent reads one version ("a" or "b", D68) of a campaign's saved
// content per language, including channels that aren't chosen; nil if there
// is none.
func (h *Handler) loadContent(ctx context.Context, id int64, variant string) (content.Variants, error) {
	rows, err := h.Pool.Query(ctx, `SELECT language, channel, coalesce(subject, ''), body FROM campaign_content
		WHERE campaign_id = $1 AND variant = $2`, id, variant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var v content.Variants
	for rows.Next() {
		var lang, ch string
		var m content.Message
		if err := rows.Scan(&lang, &ch, &m.Subject, &m.Body); err != nil {
			return nil, err
		}
		if v == nil {
			v = content.Variants{}
		}
		c := v[lang]
		if ch == "sms" {
			c.SMS = &m
		} else {
			c.Email = &m
		}
		v[lang] = c
	}
	return v, rows.Err()
}

// languageState is one language's review state (D66).
type languageState struct {
	ApprovedAt *time.Time `json:"approved_at"`
	Outdated   bool       `json:"outdated"`
}

func (h *Handler) loadLanguages(ctx context.Context, id int64) (map[content.Lang]languageState, error) {
	rows, err := h.Pool.Query(ctx, `SELECT language, approved_at, outdated FROM campaign_languages WHERE campaign_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[content.Lang]languageState{}
	for rows.Next() {
		var lang string
		var s languageState
		if err := rows.Scan(&lang, &s.ApprovedAt, &s.Outdated); err != nil {
			return nil, err
		}
		out[lang] = s
	}
	return out, rows.Err()
}

// defaultContent is the default language's content, sent to everyone until
// recipients get their own language (T20 Task 4); nil if there is none.
func defaultContent(c campaignJSON, v content.Variants) *content.Content {
	if c.DefaultLanguage == nil {
		return nil
	}
	if d, ok := v[*c.DefaultLanguage]; ok {
		d = content.ForChannels(d, c.Channels)
		return &d
	}
	return nil
}
