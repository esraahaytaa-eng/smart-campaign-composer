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

const maxFailureReasons = 10 // per channel, most frequent first

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
	Audience       int            `json:"audience"` // messages created when sending started
	Counts         map[string]int `json:"counts"`   // by message status
	Retries        int            `json:"retries"`  // attempts after a message's first
	RateLimited    int            `json:"rate_limited"`
	SkipReasons    map[string]int `json:"skip_reasons"`
	FailureReasons []reasonCount  `json:"failure_reasons"`
	Uncertain      int            `json:"uncertain"`  // may have been delivered without confirmation (D5)
	Resendable     int            `json:"resendable"` // who a resend would reach now (D31)
	ActiveResendID *int64         `json:"active_resend_id"`
}

type reasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// events streams the campaign's status: a snapshot at once, then one on each
// change, polled every EventsInterval. When the campaign is completed or
// cancelled and no message is in flight, it sends "end" and closes (D36).
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
		case <-ticker.C:
		}
		if snap, err = h.snapshot(ctx, id); err != nil {
			if ctx.Err() == nil {
				slog.Error("status snapshot", "campaign", id, "err", err)
				send("error", map[string]string{"message": "internal error"})
			}
			return
		}
		if b, _ := json.Marshal(snap); !bytes.Equal(b, last) {
			last = b
			send("status", json.RawMessage(b))
		}
	}
}

func (h *Handler) snapshot(ctx context.Context, id int64) (statusSnapshot, error) {
	var s statusSnapshot
	var channels []string
	if err := h.Pool.QueryRow(ctx, `SELECT status, channels, send_at, audience_built_at, completed_at, approval_estimate
		FROM campaigns WHERE id = $1`, id).Scan(&s.Status, &channels, &s.SendAt, &s.AudienceBuiltAt, &s.CompletedAt,
		&s.ApprovalEstimate); err != nil {
		return s, err
	}
	s.Channels = map[string]*channelStatus{}
	for _, ch := range channels {
		cs := &channelStatus{Counts: map[string]int{}, SkipReasons: map[string]int{}, FailureReasons: []reasonCount{}}
		for _, st := range messageStatuses {
			cs.Counts[st] = 0
		}
		s.Channels[ch] = cs
	}

	rows, err := h.Pool.Query(ctx, `SELECT channel, status, coalesce(skip_reason, ''), count(*), count(*) FILTER (WHERE delivery_uncertain)
		FROM messages WHERE campaign_id = $1 GROUP BY 1, 2, 3`, id)
	if err != nil {
		return s, err
	}
	var ch, status, reason string
	var n, uncertain int
	_, err = pgx.ForEachRow(rows, []any{&ch, &status, &reason, &n, &uncertain}, func() error {
		cs := s.Channels[ch]
		cs.Audience += n
		cs.Counts[status] += n
		cs.Uncertain += uncertain
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

	rows, err = h.Pool.Query(ctx, `SELECT m.channel, count(*) FILTER (WHERE a.attempt_no > 1), count(*) FILTER (WHERE a.outcome = 'rate_limited')
		FROM delivery_attempts a JOIN messages m ON m.id = a.message_id WHERE m.campaign_id = $1 GROUP BY 1`, id)
	if err != nil {
		return s, err
	}
	var retries, limited int
	if _, err = pgx.ForEachRow(rows, []any{&ch, &retries, &limited}, func() error {
		s.Channels[ch].Retries, s.Channels[ch].RateLimited = retries, limited
		return nil
	}); err != nil {
		return s, err
	}

	rows, err = h.Pool.Query(ctx, `SELECT channel, coalesce(last_error, 'unknown'), count(*) FROM messages
		WHERE campaign_id = $1 AND status = 'failed' GROUP BY 1, 2 ORDER BY 3 DESC, 2`, id)
	if err != nil {
		return s, err
	}
	if _, err = pgx.ForEachRow(rows, []any{&ch, &reason, &n}, func() error {
		if cs := s.Channels[ch]; len(cs.FailureReasons) < maxFailureReasons {
			cs.FailureReasons = append(cs.FailureReasons, reasonCount{reason, n})
		}
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
		if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM users u WHERE `+where, args...).Scan(&cs.Resendable); err != nil {
			return s, err
		}
		err = h.Pool.QueryRow(ctx, `SELECT id FROM campaigns WHERE parent_campaign_id = $1 AND channels = $2
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
