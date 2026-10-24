package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/auth"
	"smart-campaign-composer/internal/content"
)

const minScheduleLead = time.Minute // D29: a scheduled send is at least a minute ahead

// channelAudience selects a campaign's recipients on one channel at time now:
// from its rules, or for a resend from the parent's skipped recipients (D31).
// Consent always applies.
func channelAudience(c campaignJSON, channel string, now time.Time) (content.Audience, error) {
	if c.ParentCampaignID == nil {
		loc, err := loadZone(c.TimeZone)
		if err != nil {
			return content.Audience{}, err
		}
		where, args, err := audience.Build(c.Rules, channel, now, loc)
		return content.Audience{Where: where, Args: args}, err
	}
	where, args, err := audience.BuildResend(*c.ParentCampaignID, channel)
	return content.Audience{Where: where, Args: args}, err
}

// countAudience is the current audience per channel.
func (h *Handler) countAudience(ctx context.Context, c campaignJSON, now time.Time) (audience.Counts, error) {
	if c.ParentCampaignID == nil {
		if len(c.Rules) == 0 {
			return audience.Counts{}, nil
		}
		loc, err := loadZone(c.TimeZone)
		if err != nil {
			return audience.Counts{}, err
		}
		return audience.Count(ctx, h.Pool, c.Rules, c.Channels, now, loc)
	}
	var counts audience.Counts
	for _, ch := range c.Channels { // a resend has exactly one channel
		a, err := channelAudience(c, ch, now)
		if err != nil {
			return counts, err
		}
		var n int
		if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM users u WHERE `+a.Where, a.Args...).Scan(&n); err != nil {
			return counts, err
		}
		counts.Total = n
		if ch == "sms" {
			counts.SMS = n
		} else {
			counts.Email = n
		}
	}
	return counts, nil
}

// estimateFor returns the current audience and, if there is content, the
// over-limit counts per channel: both estimates of a future send (D3, D30).
func (h *Handler) estimateFor(ctx context.Context, c campaignJSON, v content.Variants, now time.Time) (audience.Counts, *content.OverLimit, error) {
	counts, err := h.countAudience(ctx, c, now)
	cont := defaultContent(c, v)
	if err != nil || cont == nil {
		return counts, nil, err
	}
	audiences := map[string]content.Audience{}
	for _, ch := range c.Channels {
		if audiences[ch], err = channelAudience(c, ch, now); err != nil {
			return counts, nil, err
		}
	}
	over, err := content.EstimateOverLimit(ctx, h.Pool, *cont, audiences)
	return counts, &over, err
}

// approve freezes a draft and fixes when it is sent (D11, D29, D30). The
// marketer must choose explicitly: "now" or {"at": RFC 3339}; a proposed
// draft time never applies by itself. No messages are created here.
func (h *Handler) approve(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version *int            `json:"version"`
		Send    json.RawMessage `json:"send"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Version == nil {
		writeError(w, http.StatusUnprocessableEntity, "version is required")
		return
	}
	now := h.Now()
	sendAt, err := parseSend(in.Send, now)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		serverError(w, "begin", err)
		return
	}
	defer tx.Rollback(ctx)

	// The row lock makes concurrent approvals and edits wait; the second one
	// then sees the new status or version.
	c, err := scanCampaign(tx.QueryRow(ctx, `SELECT `+campaignColumns+` FROM campaigns WHERE id = $1 FOR UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	case err != nil:
		serverError(w, "load campaign", err)
		return
	case c.Status != "draft":
		writeError(w, http.StatusConflict, "only drafts can be approved")
		return
	case c.Version != *in.Version:
		writeError(w, http.StatusConflict, errStale.Error())
		return
	}

	cont, err := h.loadContent(ctx, id)
	if err != nil {
		serverError(w, "load content", err)
		return
	}
	if cont == nil {
		writeError(w, http.StatusUnprocessableEntity, "write the messages before approving")
		return
	}
	if errs := content.ValidateVariants(cont, c.Channels, *c.DefaultLanguage); len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid content", "field_errors": errs})
		return
	}
	if msg, err := checkWindowsStartBefore(c, sendAt); err != nil {
		serverError(w, "check windows", err)
		return
	} else if msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}

	counts, over, err := h.estimateFor(ctx, c, cont, now)
	if err != nil {
		serverError(w, "estimate audience", err)
		return
	}
	if counts.Total == 0 {
		writeError(w, http.StatusUnprocessableEntity, "the audience is empty: no user matches the rules and allowed the chosen channels")
		return
	}
	estimate, _ := json.Marshal(map[string]any{
		"total": counts.Total, "sms": counts.SMS, "email": counts.Email, "over_limit": over, "calculated_at": now,
	})
	marketer, _ := auth.MarketerID(ctx)
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = 'approved', send_at = $2, approved_by = $3, approved_at = $4,
		approval_estimate = $5, version = version + 1, updated_at = now() WHERE id = $1`, id, sendAt, marketer, now, estimate); err != nil {
		serverError(w, "approve", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, "commit", err)
		return
	}
	h.respond(w, ctx, http.StatusOK, id)
}

// parseSend reads "now" or {"at": RFC 3339} and checks the scheduling window.
func parseSend(raw json.RawMessage, now time.Time) (time.Time, error) {
	errChoose := errors.New(`send is required: "now" or {"at": "<RFC 3339 time>"}`)
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, errChoose
	}
	var word string
	if json.Unmarshal(raw, &word) == nil {
		if word != "now" {
			return time.Time{}, errChoose
		}
		return now, nil
	}
	var at struct {
		At string `json:"at"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&at) != nil {
		return time.Time{}, errChoose
	}
	t, err := time.Parse(time.RFC3339, at.At)
	switch {
	case err != nil:
		return time.Time{}, errors.New("send.at must be an RFC 3339 time with a time zone offset")
	case t.Before(now.Add(minScheduleLead)):
		return time.Time{}, errors.New("a scheduled send must be at least 1 minute from now")
	case t.After(now.Add(maxScheduleAge)):
		return time.Time{}, errors.New("a scheduled send must be within 30 days")
	}
	return t, nil
}

// checkWindowsStartBefore explains why the send time can't work when a
// fixed-date window starts after it: that window would still be empty.
func checkWindowsStartBefore(c campaignJSON, sendAt time.Time) (string, error) {
	if c.ParentCampaignID != nil {
		return "", nil
	}
	loc, err := loadZone(c.TimeZone)
	if err != nil {
		return "", err
	}
	starts, err := audience.FixedWindowStarts(c.Rules, loc)
	if err != nil {
		return "", err
	}
	for _, s := range starts {
		if s.After(sendAt) {
			return fmt.Sprintf("a rule's date window starts on %s, after the send time; choose a later send time or change the rule",
				s.Format(time.DateOnly)), nil
		}
	}
	return "", nil
}

// cancel stops an approved or sending campaign (D11). Messages waiting to be
// sent are cancelled; attempts already in progress finish.
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		serverError(w, "begin", err)
		return
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1 FOR UPDATE`, id).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	case err != nil:
		serverError(w, "load campaign", err)
		return
	case status != "approved" && status != "sending":
		writeError(w, http.StatusConflict, "only approved or sending campaigns can be cancelled")
		return
	}
	marketer, _ := auth.MarketerID(ctx)
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = 'cancelled', cancelled_by = $2, cancelled_at = $3,
		version = version + 1, updated_at = now() WHERE id = $1`, id, marketer, h.Now()); err != nil {
		serverError(w, "cancel", err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE messages SET status = 'cancelled', updated_at = now()
		WHERE campaign_id = $1 AND status IN ('pending', 'retry_scheduled')`, id); err != nil {
		serverError(w, "cancel messages", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, "commit", err)
		return
	}
	h.respond(w, ctx, http.StatusOK, id)
}

// estimate shows the current audience and over-limit counts of a campaign
// that has not started sending; the numbers can change until it does (D30).
func (h *Handler) estimate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := scanCampaign(h.Pool.QueryRow(r.Context(), `SELECT `+campaignColumns+` FROM campaigns WHERE id = $1`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	case err != nil:
		serverError(w, "load campaign", err)
		return
	case c.Status != "draft" && c.Status != "approved":
		writeError(w, http.StatusConflict, "the audience is fixed once sending has started")
		return
	}
	cont, err := h.loadContent(r.Context(), id)
	if err != nil {
		serverError(w, "load content", err)
		return
	}
	now := h.Now()
	counts, over, err := h.estimateFor(r.Context(), c, cont, now)
	if err != nil {
		serverError(w, "estimate", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"estimate": true, "audience": counts, "over_limit": over, "calculated_at": now})
}

// resend creates a draft follow-up for one channel, addressed to the
// recipients the parent skipped for the character limit (D31).
func (h *Handler) resend(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Channel string `json:"channel"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	ctx := r.Context()
	parent, err := scanCampaign(h.Pool.QueryRow(ctx, `SELECT `+campaignColumns+` FROM campaigns WHERE id = $1`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	case err != nil:
		serverError(w, "load campaign", err)
		return
	case !slices.Contains(parent.Channels, in.Channel):
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("the campaign did not use the channel %q", in.Channel))
		return
	case parent.Status != "completed" && parent.Status != "cancelled":
		writeError(w, http.StatusConflict, "a resend is possible once the campaign has completed or been cancelled")
		return
	}
	var skipped int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE campaign_id = $1 AND channel = $2
		AND skip_reason = 'character_limit_exceeded'`, id, in.Channel).Scan(&skipped); err != nil {
		serverError(w, "count skipped", err)
		return
	}
	if skipped == 0 {
		writeError(w, http.StatusConflict, "no recipients were skipped for the character limit on this channel")
		return
	}
	marketer, _ := auth.MarketerID(ctx)
	var newID int64
	err = h.Pool.QueryRow(ctx, `INSERT INTO campaigns (request_text, channels, time_zone, created_by, parent_campaign_id)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, parent.RequestText, []string{in.Channel}, parent.TimeZone, marketer, id).Scan(&newID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // campaigns_one_active_resend_idx
		writeError(w, http.StatusConflict, "this campaign already has an active resend on this channel")
		return
	}
	if err != nil {
		serverError(w, "create resend", err)
		return
	}
	h.respond(w, ctx, http.StatusCreated, newID)
}
