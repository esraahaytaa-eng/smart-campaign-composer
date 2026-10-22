package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/auth"
	"smart-campaign-composer/internal/content"
	"smart-campaign-composer/internal/llm"
)

const maxBody = 64 << 10

// Handler serves the campaign draft API (T6). Routes must be mounted behind
// auth.Require so every request has a marketer.
type Handler struct {
	Pool    *pgxpool.Pool
	LLM     llm.LLM
	Timeout time.Duration // per AI call
	Now     func() time.Time
	// EventsInterval is how often the status stream polls; 0 means 1 s (T14).
	EventsInterval time.Duration
	// Stopping is closed when the server shuts down; open status streams then
	// end, since http.Server.Shutdown waits for every handler to return.
	Stopping <-chan struct{}
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/campaigns", h.list)
	mux.HandleFunc("POST /api/campaigns", h.create)
	mux.HandleFunc("GET /api/campaigns/{id}", h.get)
	mux.HandleFunc("PUT /api/campaigns/{id}/rules", h.updateRules)
	mux.HandleFunc("PUT /api/campaigns/{id}/schedule", h.updateSchedule)
	mux.HandleFunc("POST /api/campaigns/{id}/content", h.generateContent)
	mux.HandleFunc("PUT /api/campaigns/{id}/content", h.updateContent)
	mux.HandleFunc("POST /api/campaigns/{id}/approve", h.approve)
	mux.HandleFunc("POST /api/campaigns/{id}/cancel", h.cancel)
	mux.HandleFunc("GET /api/campaigns/{id}/estimate", h.estimate)
	mux.HandleFunc("POST /api/campaigns/{id}/resend", h.resend)
	mux.HandleFunc("GET /api/campaigns/{id}/events", h.events)
}

// campaignJSON is a campaign as the API returns it. Counts are the current
// audience per channel, shown for drafts only.
type campaignJSON struct {
	ID           int64           `json:"id"`
	RequestText  string          `json:"request_text"`
	Channels     []string        `json:"channels"`
	Rules        []audience.Rule `json:"rules"`
	Status       string          `json:"status"`
	Version      int             `json:"version"`
	SendAt       *time.Time      `json:"send_at"`
	ScheduleNote *string         `json:"schedule_note"`
	TimeZone     string          `json:"time_zone"`
	CreatedBy    int64           `json:"created_by"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	// Set on a resend: its audience is the parent's skipped recipients (D31).
	ParentCampaignID *int64           `json:"parent_campaign_id"`
	ApprovalEstimate json.RawMessage  `json:"approval_estimate"`
	ApprovedBy       *int64           `json:"approved_by"`
	ApprovedAt       *time.Time       `json:"approved_at"`
	CancelledBy      *int64           `json:"cancelled_by"`
	CancelledAt      *time.Time       `json:"cancelled_at"`
	AudienceBuiltAt  *time.Time       `json:"audience_built_at"`
	CompletedAt      *time.Time       `json:"completed_at"`
	Counts           *audience.Counts `json:"counts,omitempty"`
	Content          *content.Content `json:"content"` // in single-campaign responses
}

const campaignColumns = `id, request_text, channels, rules, status, version, send_at, schedule_note, time_zone, created_by,
	created_at, updated_at, parent_campaign_id, approval_estimate, approved_by, approved_at, cancelled_by, cancelled_at,
	audience_built_at, completed_at`

func scanCampaign(row pgx.Row) (campaignJSON, error) {
	var c campaignJSON
	err := row.Scan(&c.ID, &c.RequestText, &c.Channels, &c.Rules, &c.Status, &c.Version, &c.SendAt,
		&c.ScheduleNote, &c.TimeZone, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt, &c.ParentCampaignID, &c.ApprovalEstimate,
		&c.ApprovedBy, &c.ApprovedAt, &c.CancelledBy, &c.CancelledAt, &c.AudienceBuiltAt, &c.CompletedAt)
	return c, err
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Request  string           `json:"request"`
		Channels []string         `json:"channels"`
		TimeZone string           `json:"time_zone"`
		Rules    *[]audience.Rule `json:"rules"` // optional: rules built by hand skip the AI (D15)
	}
	if !decodeBody(w, r, &in) {
		return
	}
	text, err := checkRequestText(in.Request)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := checkChannels(in.Channels); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if _, err := loadZone(in.TimeZone); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	cat, err := audience.LoadCatalog(r.Context(), h.Pool)
	if err != nil {
		serverError(w, "load catalog", err)
		return
	}

	var rules []audience.Rule
	var sendAt *time.Time
	var note *string
	if in.Rules != nil {
		var errs []audience.FieldError
		if rules, errs = audience.Validate(*in.Rules, cat); len(errs) > 0 {
			writeFieldErrors(w, errs)
			return
		}
	} else {
		got, err := Interpret(r.Context(), h.LLM, llm.InterpretInput{Text: text, Now: h.Now(), TimeZone: in.TimeZone, Catalog: cat}, h.Timeout)
		if err != nil {
			writeAIError(w, err)
			return
		}
		if got.Unsupported {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": got.Reason, "unsupported": true})
			return
		}
		rules, sendAt = got.Rules, got.SuggestedSendAt
		if got.ScheduleNote != "" {
			note = &got.ScheduleNote
		}
	}

	marketer, _ := auth.MarketerID(r.Context())
	rulesJSON, _ := json.Marshal(rules)
	var id int64
	err = h.Pool.QueryRow(r.Context(), `INSERT INTO campaigns (request_text, channels, rules, send_at, schedule_note, time_zone, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, text, in.Channels, rulesJSON, sendAt, note, in.TimeZone, marketer).Scan(&id)
	if err != nil {
		serverError(w, "insert campaign", err)
		return
	}
	h.respond(w, r.Context(), http.StatusCreated, id)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	rows, _ := h.Pool.Query(r.Context(), `SELECT `+campaignColumns+` FROM campaigns ORDER BY id DESC LIMIT 200`)
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (campaignJSON, error) { return scanCampaign(row) })
	if err != nil {
		serverError(w, "list campaigns", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if ok {
		h.respond(w, r.Context(), http.StatusOK, id)
	}
}

func (h *Handler) updateRules(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version *int            `json:"version"`
		Rules   []audience.Rule `json:"rules"`
	}
	if !decodeBody(w, r, &in) || !h.editableDraft(w, r.Context(), id, in.Version) {
		return
	}
	var isResend bool
	if err := h.Pool.QueryRow(r.Context(), `SELECT parent_campaign_id IS NOT NULL FROM campaigns WHERE id = $1`, id).Scan(&isResend); err != nil {
		serverError(w, "load campaign", err)
		return
	}
	if isResend {
		writeError(w, http.StatusConflict, "a resend's audience is fixed: the skipped recipients of the original campaign")
		return
	}
	cat, err := audience.LoadCatalog(r.Context(), h.Pool)
	if err != nil {
		serverError(w, "load catalog", err)
		return
	}
	rules, errs := audience.Validate(in.Rules, cat)
	if len(errs) > 0 {
		writeFieldErrors(w, errs)
		return
	}
	rulesJSON, _ := json.Marshal(rules)
	h.update(w, r.Context(), id, *in.Version, `rules = $4`, rulesJSON)
}

func (h *Handler) updateSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version *int    `json:"version"`
		SendAt  *string `json:"send_at"` // null clears the proposal
	}
	if !decodeBody(w, r, &in) || !h.editableDraft(w, r.Context(), id, in.Version) {
		return
	}
	var sendAt *time.Time
	if in.SendAt != nil {
		at, err := time.Parse(time.RFC3339, *in.SendAt)
		now := h.Now()
		switch {
		case err != nil:
			writeError(w, http.StatusUnprocessableEntity, "send_at must be an RFC 3339 time with a time zone offset")
			return
		case at.Before(now) || at.After(now.Add(maxScheduleAge)):
			writeError(w, http.StatusUnprocessableEntity, "send_at must be between now and 30 days from now")
			return
		}
		sendAt = &at
	}
	// The brief's note no longer describes a time the marketer chose.
	h.update(w, r.Context(), id, *in.Version, `send_at = $4, schedule_note = NULL`, sendAt)
}

// editableDraft answers 404/409/422 unless the campaign exists, is a draft
// and a version was sent.
func (h *Handler) editableDraft(w http.ResponseWriter, ctx context.Context, id int64, version *int) bool {
	if version == nil {
		writeError(w, http.StatusUnprocessableEntity, "version is required")
		return false
	}
	var status string
	err := h.Pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, id).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "campaign not found")
		return false
	case err != nil:
		serverError(w, "load campaign", err)
		return false
	case status != "draft":
		writeError(w, http.StatusConflict, "only drafts can be edited")
		return false
	}
	return true
}

// update applies set (with its value as $4) if the draft still has the
// expected version, bumping the version (D10 optimistic locking).
func (h *Handler) update(w http.ResponseWriter, ctx context.Context, id int64, version int, set string, value any) {
	tag, err := h.Pool.Exec(ctx, `UPDATE campaigns SET `+set+`, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 AND status = $3`, id, version, "draft", value)
	if err != nil {
		serverError(w, "update campaign", err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "the campaign changed since you loaded it; reload and try again")
		return
	}
	h.respond(w, ctx, http.StatusOK, id)
}

// respond loads the campaign, adds current counts for drafts, and writes it.
func (h *Handler) respond(w http.ResponseWriter, ctx context.Context, status int, id int64) {
	c, err := scanCampaign(h.Pool.QueryRow(ctx, `SELECT `+campaignColumns+` FROM campaigns WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	}
	if err != nil {
		serverError(w, "load campaign", err)
		return
	}
	if c.Content, err = h.loadContent(ctx, id); err != nil {
		serverError(w, "load content", err)
		return
	}
	if c.Status == "draft" {
		counts, err := h.countAudience(ctx, c, h.Now())
		if err != nil {
			serverError(w, "count audience", err)
			return
		}
		c.Counts = &counts
	}
	writeJSON(w, status, c)
}

func checkChannels(channels []string) error {
	if len(channels) == 0 {
		return errors.New("choose at least one channel")
	}
	for i, ch := range channels {
		if ch != "sms" && ch != "email" {
			return fmt.Errorf("unknown channel %q (use sms or email)", ch)
		}
		if slices.Contains(channels[:i], ch) {
			return fmt.Errorf("channel %q listed twice", ch)
		}
	}
	return nil
}

// loadZone accepts IANA names only ("Europe/Istanbul"); "" and "Local" would
// silently mean the server's zone.
func loadZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, errors.New("time_zone must be an IANA name such as Europe/Istanbul")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown time_zone %q", name)
	}
	return loc, nil
}

func writeAIError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRequestText):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, ErrAIInvalid):
		slog.Warn("AI answer invalid twice", "err", err)
		writeError(w, http.StatusBadGateway, "The AI returned an invalid answer twice. Rephrase the request or build the rules by hand.")
	case errors.Is(err, context.DeadlineExceeded):
		slog.Warn("AI request timed out", "err", err)
		writeError(w, http.StatusGatewayTimeout, "The AI did not answer in time. Try again or build the rules by hand.")
	default:
		slog.Error("AI request failed", "err", err)
		writeError(w, http.StatusBadGateway, "The AI service is unavailable. Try again or build the rules by hand.")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "campaign not found")
		return 0, false
	}
	return id, true
}

// decodeBody reads one JSON object, rejecting unknown fields and oversized bodies.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = errors.New("unexpected data after the JSON object")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeFieldErrors(w http.ResponseWriter, errs []audience.FieldError) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid audience rules", "field_errors": errs})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func serverError(w http.ResponseWriter, what string, err error) {
	slog.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
