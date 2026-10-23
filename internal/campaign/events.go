package campaign

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"smart-campaign-composer/internal/audience"
)

var messageStatuses = []string{"pending", "sending", "retry_scheduled", "sent", "failed", "skipped", "cancelled"}

// Display groups (D46): every message is in exactly one, so they add up to
// the audience. An uncertain delivery is its own group once the message has
// ended without being sent.
var deliveryGroups = []string{"sent", "uncertain", "failed", "skipped", "cancelled", "retrying", "pending"}

// groupsWithReasons are the groups reasonSQL gives a reason code.
var groupsWithReasons = map[string]bool{"uncertain": true, "failed": true, "skipped": true, "retrying": true}

// groupSQL is a message's display group; m is the messages row.
const groupSQL = `CASE
	WHEN m.status = 'sent' THEN 'sent'
	WHEN m.status IN ('failed', 'skipped', 'cancelled') AND m.delivery_uncertain THEN 'uncertain'
	WHEN m.status = 'retry_scheduled' THEN 'retrying'
	WHEN m.status IN ('pending', 'sending') THEN 'pending'
	ELSE m.status END`

// lastAttemptsSQL joins the message's latest attempt (la) and its latest
// attempt without a clear answer (lu), for reasonSQL.
const lastAttemptsSQL = `
	LEFT JOIN LATERAL (SELECT outcome FROM delivery_attempts WHERE message_id = m.id ORDER BY id DESC LIMIT 1) la ON true
	LEFT JOIN LATERAL (SELECT outcome FROM delivery_attempts WHERE message_id = m.id
		AND outcome IN ('timeout', 'network_error', 'interrupted') ORDER BY id DESC LIMIT 1) lu ON true`

// reasonSQL is the reason code within the group (D46), or NULL for groups
// without reasons. Codes, never provider text, so the UI can translate them.
// An uncertain delivery without such an attempt was held by a worker that
// stopped mid-attempt; a retry without an attempt was handed back unsent.
const reasonSQL = `CASE ` + groupSQL + `
	WHEN 'failed' THEN CASE WHEN la.outcome = 'rejected' THEN 'provider_rejected' ELSE 'retries_exhausted' END
	WHEN 'skipped' THEN m.skip_reason
	WHEN 'uncertain' THEN coalesce(lu.outcome, 'no_response_recorded')
	WHEN 'retrying' THEN coalesce(la.outcome, 'requeued')
	END`

// recentWindow is how far back rate_limited_recent counts 429s.
const recentWindow = 5 * time.Minute

// statusSnapshot is what the dashboard shows live (D16). Counts come from the
// database; the approval estimate is kept next to the actual audience.
type statusSnapshot struct {
	Status           string                    `json:"status"`
	SendAt           *time.Time                `json:"send_at"`
	AudienceBuiltAt  *time.Time                `json:"audience_built_at"`
	CompletedAt      *time.Time                `json:"completed_at"`
	ApprovalEstimate json.RawMessage           `json:"approval_estimate"`
	Channels         map[string]*channelStatus `json:"channels"`
	inFlight         bool                      // some message is not final yet
}

type channelStatus struct {
	Audience          int                       `json:"audience"` // messages created when sending started
	Counts            map[string]int            `json:"counts"`   // by message status
	Groups            map[string]int            `json:"groups"`   // by display group (D46)
	Reasons           map[string]map[string]int `json:"reasons"`  // group → reason code → count
	Retries           int                       `json:"retries"`  // attempts after a message's first
	RateLimited       int                       `json:"rate_limited"`
	RateLimitedRecent int                       `json:"rate_limited_recent"` // in the last recentWindow
	SkipReasons       map[string]int            `json:"skip_reasons"`
	Resendable        int                       `json:"resendable"` // who a resend would reach now (D31)
	ActiveResendID    *int64                    `json:"active_resend_id"`
}

// events streams the campaign's status: a snapshot at once, then one on each
// change, polled every EventsInterval. When the campaign is completed or
// cancelled and no message is in flight, it sends "end" and closes (D36).
// On a server shutdown or a database error it just closes, so the browser's
// EventSource reconnects (an "error" event would clash with its own).
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	snap, err := h.snapshot(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	}
	if err != nil {
		serverError(w, "status snapshot", err)
		return
	}
	send := startSSE(w)
	last, _ := json.Marshal(snap)
	send("status", json.RawMessage(last))

	interval := h.EventsInterval
	if interval == 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if (snap.Status == "completed" || snap.Status == "cancelled") && !snap.inFlight {
			send("end", struct{}{})
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-h.Stopping:
			return
		case <-ticker.C:
		}
		if snap, err = h.snapshot(ctx, id); err != nil {
			if ctx.Err() == nil {
				slog.Error("status snapshot", "campaign", id, "err", err)
			}
			return
		}
		if b, _ := json.Marshal(snap); !bytes.Equal(b, last) {
			last = b
			send("status", json.RawMessage(b))
		}
	}
}

// snapshot reads everything at one point in time, so counts never mix moments.
func (h *Handler) snapshot(ctx context.Context, id int64) (statusSnapshot, error) {
	var s statusSnapshot
	tx, err := h.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return s, err
	}
	defer tx.Rollback(ctx)
	var channels []string
	if err := tx.QueryRow(ctx, `SELECT status, channels, send_at, audience_built_at, completed_at, approval_estimate
		FROM campaigns WHERE id = $1`, id).Scan(&s.Status, &channels, &s.SendAt, &s.AudienceBuiltAt, &s.CompletedAt,
		&s.ApprovalEstimate); err != nil {
		return s, err
	}
	s.Channels = map[string]*channelStatus{}
	for _, ch := range channels {
		cs := &channelStatus{Counts: map[string]int{}, Groups: map[string]int{}, Reasons: map[string]map[string]int{}, SkipReasons: map[string]int{}}
		for _, st := range messageStatuses {
			cs.Counts[st] = 0
		}
		for _, g := range deliveryGroups {
			cs.Groups[g] = 0
		}
		s.Channels[ch] = cs
	}

	rows, err := tx.Query(ctx, `SELECT channel, status, coalesce(skip_reason, ''), `+groupSQL+`, count(*)
		FROM messages m WHERE campaign_id = $1 GROUP BY 1, 2, 3, 4`, id)
	if err != nil {
		return s, err
	}
	var ch, status, reason, group string
	var n int
	_, err = pgx.ForEachRow(rows, []any{&ch, &status, &reason, &group, &n}, func() error {
		cs := s.Channels[ch]
		cs.Audience += n
		cs.Counts[status] += n
		cs.Groups[group] += n
		if reason != "" {
			cs.SkipReasons[reason] += n
		}
		if status == "pending" || status == "sending" || status == "retry_scheduled" {
			s.inFlight = true
		}
		return nil
	})
	if err != nil {
		return s, err
	}

	// Per message through the index, so the cost follows this campaign's size,
	// not every attempt ever recorded (a plain join chose a full table scan).
	// attempt_count counts claims, so a sent message claimed once had exactly
	// one attempt, the accepted one: no retries, no 429s. Skipping those
	// leaves the minority that can add to these counts.
	rows, err = tx.Query(ctx, `SELECT m.channel, sum(a.retries), sum(a.limited), sum(a.recent) FROM messages m
		CROSS JOIN LATERAL (SELECT count(*) FILTER (WHERE attempt_no > 1) retries, count(*) FILTER (WHERE outcome = 'rate_limited') limited,
			count(*) FILTER (WHERE outcome = 'rate_limited' AND started_at > $2) recent
			FROM delivery_attempts WHERE message_id = m.id) a
		WHERE m.campaign_id = $1 AND NOT (m.status = 'sent' AND m.attempt_count = 1) GROUP BY 1`, id, h.Now().Add(-recentWindow))
	if err != nil {
		return s, err
	}
	var retries, limited, recent int
	if _, err = pgx.ForEachRow(rows, []any{&ch, &retries, &limited, &recent}, func() error {
		s.Channels[ch].Retries, s.Channels[ch].RateLimited, s.Channels[ch].RateLimitedRecent = retries, limited, recent
		return nil
	}); err != nil {
		return s, err
	}

	// Only messages whose group has reasons, so the lateral joins stay off
	// the sent and pending majority.
	rows, err = tx.Query(ctx, `SELECT m.channel, `+groupSQL+`, `+reasonSQL+`, count(*) FROM messages m`+lastAttemptsSQL+`
		WHERE m.campaign_id = $1 AND m.status IN ('failed', 'skipped', 'cancelled', 'retry_scheduled') GROUP BY 1, 2, 3`, id)
	if err != nil {
		return s, err
	}
	var code *string
	if _, err = pgx.ForEachRow(rows, []any{&ch, &group, &code, &n}, func() error {
		if code == nil {
			return nil // a plain cancelled message has no reason
		}
		cs := s.Channels[ch]
		if cs.Reasons[group] == nil {
			cs.Reasons[group] = map[string]int{}
		}
		cs.Reasons[group][*code] += n
		return nil
	}); err != nil {
		return s, err
	}

	for ch, cs := range s.Channels {
		if cs.SkipReasons["character_limit_exceeded"] == 0 {
			continue // nobody to resend to; skip the queries
		}
		where, args, err := audience.BuildResend(id, ch)
		if err != nil {
			return s, err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users u WHERE `+where, args...).Scan(&cs.Resendable); err != nil {
			return s, err
		}
		err = tx.QueryRow(ctx, `SELECT id FROM campaigns WHERE parent_campaign_id = $1 AND channels = $2
			AND status <> 'cancelled'`, id, []string{ch}).Scan(&cs.ActiveResendID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return s, err
		}
	}
	return s, nil
}

// startSSE sends the event-stream headers and returns a function that writes
// and flushes one event.
func startSSE(w http.ResponseWriter) func(name string, data any) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return func(name string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}
