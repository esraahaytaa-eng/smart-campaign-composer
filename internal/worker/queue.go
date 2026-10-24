// Package worker sends approved campaigns. The messages table is the queue
// (D18): messages are claimed with a time-limited lease (D5), and results only
// apply while the lease is still held.
package worker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/audience"
)

// ErrLeaseLost means another worker took the message over after this one's
// lease expired; this worker's result must not be applied.
var ErrLeaseLost = errors.New("lease lost: the message was claimed again")

type Queue struct {
	Pool *pgxpool.Pool
}

// BuildAudience starts every approved campaign whose send time has come
// (D29, D30): in one transaction per campaign it evaluates the audience now,
// creates one message per recipient and channel, and marks the campaign
// sending, or completed when nobody matches. It returns how many it started.
func (q *Queue) BuildAudience(ctx context.Context, now time.Time) (int, error) {
	n := 0
	for {
		built, err := q.buildOne(ctx, now)
		if err != nil || !built {
			return n, err
		}
		n++
	}
}

func (q *Queue) buildOne(ctx context.Context, now time.Time) (bool, error) {
	tx, err := q.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var (
		id       int64
		channels []string
		rules    []audience.Rule
		zone     string
		parent   *int64
		def      *string // set on every approved campaign with content
		langs    []string
	)
	// SKIP LOCKED: concurrent builders take different campaigns; a cancel waits for this one.
	err = tx.QueryRow(ctx, `SELECT id, channels, rules, time_zone, parent_campaign_id, default_language,
			ARRAY(SELECT language FROM campaign_languages l WHERE l.campaign_id = c.id)
		FROM campaigns c WHERE status = 'approved' AND send_at <= $1 ORDER BY send_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`, now).
		Scan(&id, &channels, &rules, &zone, &parent, &def, &langs)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return false, fmt.Errorf("campaign %d: time zone: %w", id, err)
	}

	created := int64(0)
	for _, ch := range channels {
		var where string
		var args []any
		if parent != nil {
			where, args, err = audience.BuildResend(*parent, ch)
		} else {
			where, args, err = audience.Build(rules, ch, now, loc)
		}
		if err != nil {
			return false, fmt.Errorf("campaign %d: audience: %w", id, err)
		}
		// Each recipient's language is fixed now (D64): their own if the
		// campaign has it, else the default. Retries reuse it; a resend is a
		// campaign of its own and fixes its recipients' languages here too.
		n := len(args)
		tag, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO messages (campaign_id, user_id, channel, next_attempt_at, language)
			SELECT $%d, u.id, $%d, $%d, CASE WHEN u.language = ANY($%d) THEN u.language ELSE $%d END FROM users u WHERE %s
			ON CONFLICT (campaign_id, user_id, channel) DO NOTHING`, n+1, n+2, n+3, n+4, n+5, where), append(args, id, ch, now, langs, def)...)
		if err != nil {
			return false, fmt.Errorf("campaign %d: create messages: %w", id, err)
		}
		created += tag.RowsAffected()
	}

	status := "sending"
	var completedAt *time.Time
	if created == 0 {
		status, completedAt = "completed", &now
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, audience_built_at = $3, completed_at = $4, updated_at = now()
		WHERE id = $1`, id, status, now, completedAt); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// Job is one claimed message. Attempt is this attempt's number (1-based).
type Job struct {
	MessageID  int64
	CampaignID int64
	UserID     int64
	Channel    string
	Attempt    int
	LeaseToken string
}

// Claim leases up to n messages of a channel that are due: pending or
// waiting for a retry whose time has come, or still "sending" under an
// expired lease. That last case means a worker died mid-attempt, so the
// message may have been delivered: it is marked uncertain (D5 history).
func (q *Queue) Claim(ctx context.Context, channel string, n int, now time.Time, lease time.Duration) ([]Job, error) {
	rows, _ := q.Pool.Query(ctx, `UPDATE messages SET status = 'sending', lease_token = gen_random_uuid(),
			lease_expires_at = $3, attempt_count = attempt_count + 1, updated_at = now(),
			delivery_uncertain = delivery_uncertain OR status = 'sending'
		WHERE id IN (SELECT id FROM messages WHERE channel = $1
				AND ((status IN ('pending', 'retry_scheduled') AND next_attempt_at <= $2)
					OR (status = 'sending' AND lease_expires_at < $2))
			ORDER BY next_attempt_at, id LIMIT $4 FOR UPDATE SKIP LOCKED)
		RETURNING id, campaign_id, user_id, channel, attempt_count, lease_token::text`, channel, now, now.Add(lease), n)
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Job, error) {
		var j Job
		err := r.Scan(&j.MessageID, &j.CampaignID, &j.UserID, &j.Channel, &j.Attempt, &j.LeaseToken)
		return j, err
	})
}

// Attempt is one provider request, recorded for retry and 429 counts (D16).
type Attempt struct {
	StartedAt, FinishedAt time.Time
	Outcome               string // accepted, rate_limited, server_error, timeout, network_error, rejected, interrupted
	HTTPStatus            int    // 0 when there was no response
	Error                 string
}

// Result is where a claimed message goes next.
type Result struct {
	Status        string // sent, retry_scheduled, failed, skipped or cancelled
	SkipReason    string // with skipped
	LastError     string
	NextAttemptAt time.Time // with retry_scheduled
	Attempt       *Attempt  // nil when no provider request was made (skips)
}

var resultStatuses = []string{"sent", "retry_scheduled", "failed", "skipped", "cancelled"}

// Complete records the attempt and applies the result if this job still holds
// the lease; otherwise it returns ErrLeaseLost and changes nothing else. A
// retry for a campaign cancelled meanwhile becomes cancelled (D11).
func (q *Queue) Complete(ctx context.Context, j Job, r Result) error {
	if !slices.Contains(resultStatuses, r.Status) {
		return fmt.Errorf("invalid result status %q", r.Status)
	}
	tx, err := q.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var sentAt, nextAt *time.Time
	outcome := ""
	if a := r.Attempt; a != nil {
		outcome = a.Outcome
		// Recorded even if the lease was lost: the provider request did happen.
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_attempts (message_id, attempt_no, started_at, finished_at, outcome, http_status, error)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, 0), NULLIF($7, ''))`,
			j.MessageID, j.Attempt, a.StartedAt, a.FinishedAt, a.Outcome, a.HTTPStatus, a.Error); err != nil {
			return fmt.Errorf("record attempt: %w", err)
		}
		sentAt = &a.FinishedAt
	}
	if !r.NextAttemptAt.IsZero() {
		nextAt = &r.NextAttemptAt
	}
	tag, err := tx.Exec(ctx, `UPDATE messages SET
			status = CASE WHEN $2 = 'retry_scheduled' AND (SELECT c.status FROM campaigns c WHERE c.id = messages.campaign_id) = 'cancelled'
				THEN 'cancelled' ELSE $2 END,
			skip_reason = NULLIF($3, ''), last_error = NULLIF($4, ''), next_attempt_at = coalesce($5, next_attempt_at),
			-- No clear answer: the provider may have sent it (D5 history).
			delivery_uncertain = delivery_uncertain OR $8 IN ('timeout', 'network_error', 'interrupted'),
			sent_at = CASE WHEN $2 = 'sent' THEN coalesce($6, now()) ELSE sent_at END,
			lease_token = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = $1 AND lease_token = $7::uuid AND status = 'sending'`,
		j.MessageID, r.Status, r.SkipReason, r.LastError, nextAt, sentAt, j.LeaseToken, outcome)
	if err != nil {
		return fmt.Errorf("complete message: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// FinishCampaigns marks sending campaigns completed once every message is
// final, and returns how many it finished.
func (q *Queue) FinishCampaigns(ctx context.Context, now time.Time) (int, error) {
	tag, err := q.Pool.Exec(ctx, `UPDATE campaigns c SET status = 'completed', completed_at = $1, updated_at = now()
		WHERE status = 'sending' AND NOT EXISTS (SELECT 1 FROM messages m
			WHERE m.campaign_id = c.id AND m.status IN ('pending', 'sending', 'retry_scheduled'))`, now)
	return int(tag.RowsAffected()), err
}
