package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/time/rate"

	"smart-campaign-composer/internal/content"
)

// D19 values.
const (
	ProviderTimeout = 5 * time.Second
	DefaultLease    = 30 * time.Second
	DefaultPoolSize = 20  // concurrent provider requests per channel
	RatePerSecond   = 100 // provider requests per channel, retries included
	maxRetryAfter   = 5 * time.Minute
	// capWindow is a little over 1 s, so a few milliseconds between recording a
	// departure and the request reaching the provider cannot push 101 into a second.
	capWindow        = time.Second + 50*time.Millisecond
	maxBackoff       = 30 * time.Second
	maxProviderReply = 64 << 10
)

// Provider metrics (D26). HTTP 429s are the outcome="rate_limited" series.
var (
	sendAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scc_send_attempts_total", Help: "Provider attempts by channel and outcome.",
	}, []string{"channel", "outcome"})
	sendRetries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scc_send_retries_total", Help: "Messages scheduled for another attempt after a failed one.",
	}, []string{"channel"})
	providerLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "scc_provider_request_duration_seconds", Help: "Provider request duration by channel.",
	}, []string{"channel"})
)

// NewLimiters gives each channel 100 requests per second with a burst of 1,
// so attempts start at least 10 ms apart and no 1 s window exceeds 100 (D19).
func NewLimiters() map[string]*rate.Limiter {
	return map[string]*rate.Limiter{
		"sms":   rate.NewLimiter(RatePerSecond, 1),
		"email": rate.NewLimiter(RatePerSecond, 1),
	}
}

// NewClient is the provider HTTP client: one kept-alive connection per
// sender, so requests don't pay (and vary by) a new TCP connection each time.
func NewClient(timeout time.Duration) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 2 * DefaultPoolSize // both channels share the provider host
	return &http.Client{Timeout: timeout, Transport: t}
}

// Backoff is the wait before retry number attempt+1 (D7): exponential from
// 1 s with jitter (half fixed, half random), capped at 30 s, and never
// earlier than the provider's Retry-After (itself capped at 5 minutes).
func Backoff(attempt int, retryAfter time.Duration, rnd *rand.Rand) time.Duration {
	base := maxBackoff
	if attempt >= 1 && attempt <= 6 {
		base = min(time.Second<<(attempt-1), maxBackoff)
	}
	d := base/2 + time.Duration(rnd.Int64N(int64(base/2)+1))
	return max(d, min(retryAfter, maxRetryAfter))
}

// Worker sends claimed messages to the provider. Fields left zero get the
// D19 defaults in Run.
type Worker struct {
	Queue       *Queue
	ProviderURL string
	Client      *http.Client // its Timeout is the provider timeout
	Limiters    map[string]*rate.Limiter
	MaxAttempts int
	Lease       time.Duration
	PoolSize    int
	Poll        time.Duration // how often idle loops look for work
	Now         func() time.Time
	Rand        *rand.Rand

	mu   sync.Mutex // guards Rand
	caps map[string]*windowCap
}

func (w *Worker) defaults() {
	if w.Client == nil {
		w.Client = NewClient(ProviderTimeout)
	}
	if w.Limiters == nil {
		w.Limiters = NewLimiters()
	}
	if w.MaxAttempts == 0 {
		w.MaxAttempts = 5
	}
	if w.Lease == 0 {
		w.Lease = DefaultLease
	}
	if w.PoolSize == 0 {
		w.PoolSize = DefaultPoolSize
	}
	if w.Poll == 0 {
		w.Poll = time.Second
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	if w.Rand == nil {
		w.Rand = rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0))
	}
	if w.caps == nil {
		w.caps = map[string]*windowCap{}
		for ch := range w.Limiters {
			w.caps[ch] = newWindowCap(RatePerSecond, capWindow)
		}
	}
}

// Run starts due campaigns, sends their messages and completes them, until
// ctx ends. A message interrupted by shutdown keeps its lease and is picked
// up again after it expires (D5).
func (w *Worker) Run(ctx context.Context) error {
	w.defaults()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			if _, err := w.Queue.BuildAudience(ctx, w.Now()); err != nil && ctx.Err() == nil {
				slog.Error("worker: start campaigns", "err", err)
			}
			if _, err := w.Queue.FinishCampaigns(ctx, w.Now()); err != nil && ctx.Err() == nil {
				slog.Error("worker: finish campaigns", "err", err)
			}
			sleep(ctx, w.Poll)
		}
	}()
	for channel := range w.Limiters {
		for range w.PoolSize {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w.sendLoop(ctx, channel)
			}()
		}
	}
	wg.Wait()
	return nil
}

func (w *Worker) sendLoop(ctx context.Context, channel string) {
	for ctx.Err() == nil {
		jobs, err := w.Queue.Claim(ctx, channel, 1, w.Now(), w.Lease)
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("worker: claim", "channel", channel, "err", err)
			}
			sleep(ctx, w.Poll)
			continue
		}
		if len(jobs) == 0 {
			sleep(ctx, w.Poll)
			continue
		}
		if err := w.Process(ctx, jobs[0]); err != nil && ctx.Err() == nil {
			slog.Error("worker: process", "message", jobs[0].MessageID, "err", err)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// recipient is what one message needs at send time.
type recipient struct {
	campaignStatus string
	uncertain      bool // an earlier attempt may have been delivered
	attempts       int  // attempts so far that count toward MaxAttempts
	consent        bool
	vars           content.UserVars
	to             string
	tpl            content.Message
}

// Process sends one claimed message: it re-checks the campaign and consent,
// personalizes and length-checks the text, waits for a rate-limit token,
// re-checks again, calls the provider and records the outcome (D3, D7, D19).
func (w *Worker) Process(ctx context.Context, j Job) error {
	w.defaults()
	// Results are written even if ctx ends during the request.
	done := func(r Result) error {
		err := w.Queue.Complete(context.WithoutCancel(ctx), j, r)
		if errors.Is(err, ErrLeaseLost) {
			slog.Warn("worker: lease lost, result dropped", "message", j.MessageID)
			return nil
		}
		return err
	}

	rc, err := w.load(ctx, j)
	if err != nil {
		return err
	}
	if r, stop := skip(rc); stop {
		return done(r) // no token taken, no provider call
	}
	body := content.Render(rc.tpl.Body, rc.vars)
	subject := content.Render(rc.tpl.Subject, rc.vars)
	if overLimit(j.Channel, subject, body) {
		return done(Result{Status: "skipped", SkipReason: "character_limit_exceeded", LastError: uncertainNote(rc)})
	}

	// Shutdown while waiting: nothing was sent, so hand the message back at once.
	release := Result{Status: "retry_scheduled", NextAttemptAt: w.Now()}
	if err := w.Limiters[j.Channel].Wait(ctx); err != nil {
		return errors.Join(err, done(release))
	}
	departure, err := w.caps[j.Channel].wait(ctx) // hard cap: ≤100 departures in any second
	if err != nil {
		return errors.Join(err, done(release))
	}
	// The waits can be long; a cancel or opt-out meanwhile must still stop the
	// send. This is the last check before the request leaves.
	if rc, err = w.load(ctx, j); err != nil {
		return err
	}
	if r, stop := skip(rc); stop {
		return done(r)
	}

	attempt := w.call(ctx, j, departure, rc.to, subject, body)
	result := Result{Attempt: &attempt.Attempt, LastError: attempt.Error}
	counted := rc.attempts + 1 // this one
	switch {
	case attempt.Outcome == "accepted":
		result.Status = "sent"
	case attempt.Outcome == "rejected":
		result.Status = "failed"
	case ctx.Err() != nil:
		// Shutdown mid-request: no answer, maybe delivered. Not a failure and
		// not counted; retried at once with the same key (D5 history).
		attempt.Outcome, attempt.Error = "interrupted", "interrupted by worker shutdown"
		result.LastError, result.Status, result.NextAttemptAt = attempt.Error, "retry_scheduled", w.Now()
	case counted >= w.MaxAttempts:
		result.Status = "failed"
	default: // rate_limited, server_error, timeout, network_error
		w.mu.Lock()
		result.NextAttemptAt = w.Now().Add(Backoff(counted, attempt.retryAfter, w.Rand))
		w.mu.Unlock()
		result.Status = "retry_scheduled"
		sendRetries.WithLabelValues(j.Channel).Inc()
	}
	sendAttempts.WithLabelValues(j.Channel, attempt.Outcome).Inc()
	providerLatency.WithLabelValues(j.Channel).Observe(attempt.FinishedAt.Sub(attempt.StartedAt).Seconds())
	return done(result)
}

func (w *Worker) load(ctx context.Context, j Job) (recipient, error) {
	var rc recipient
	var sms, email bool
	var phone, mail string
	err := w.Queue.Pool.QueryRow(ctx, `SELECT c.status, m.delivery_uncertain,
			(SELECT count(*) FROM delivery_attempts a WHERE a.message_id = m.id AND a.outcome <> 'interrupted'),
			u.sms_opt_in, u.email_opt_in, u.first_name, coalesce(u.last_name, ''),
			u.city, u.phone, u.email, coalesce(cc.subject, ''), cc.body
		FROM messages m JOIN campaigns c ON c.id = m.campaign_id JOIN users u ON u.id = m.user_id
		JOIN campaign_content cc ON cc.campaign_id = m.campaign_id AND cc.channel = m.channel
		WHERE m.id = $1`, j.MessageID).Scan(&rc.campaignStatus, &rc.uncertain, &rc.attempts, &sms, &email,
		&rc.vars.FirstName, &rc.vars.LastName, &rc.vars.City, &phone, &mail, &rc.tpl.Subject, &rc.tpl.Body)
	if err != nil {
		return rc, fmt.Errorf("load message %d: %w", j.MessageID, err)
	}
	rc.consent, rc.to = sms, phone
	if j.Channel == "email" {
		rc.consent, rc.to = email, mail
	}
	return rc, nil
}

// skip decides the outcomes that need no provider call. A cancel or opt-out
// is respected even if an earlier attempt may have been delivered; the record
// then says so instead of claiming nothing was sent.
func skip(rc recipient) (Result, bool) {
	switch {
	case rc.campaignStatus != "sending":
		return Result{Status: "cancelled", LastError: uncertainNote(rc)}, true
	case !rc.consent:
		return Result{Status: "skipped", SkipReason: "no_consent", LastError: uncertainNote(rc)}, true
	}
	return Result{}, false
}

func uncertainNote(rc recipient) string {
	if rc.uncertain {
		return "delivery uncertain: an earlier attempt may have reached the provider"
	}
	return ""
}

// overLimit applies the limits to the personalized text (D2, D3, D13).
func overLimit(channel, subject, body string) bool {
	n := func(s string) int { return len([]rune(s)) }
	if channel == "sms" {
		return n(body) > content.SMSLimit
	}
	return n(subject) > content.SubjectLimit || n(body) > content.EmailBodyLimit
}

// call makes one provider request and classifies it (D7).
func (w *Worker) call(ctx context.Context, j Job, departure time.Time, to, subject, body string) attemptWithRetry {
	payload := map[string]string{"to": to, "body": body}
	if j.Channel == "email" {
		payload["subject"] = subject
	}
	b, _ := json.Marshal(payload)
	a := attemptWithRetry{Attempt: Attempt{StartedAt: departure}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.ProviderURL+"/"+j.Channel+"/send", bytes.NewReader(b))
	if err != nil {
		a.Outcome, a.Error, a.FinishedAt = "network_error", err.Error(), time.Now()
		return a
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", strconv.FormatInt(j.MessageID, 10)) // the same on every attempt (D4, D6)
	resp, err := w.Client.Do(req)
	if err != nil {
		var nerr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &nerr) && nerr.Timeout()) {
			a.Outcome, a.Error = "timeout", "timeout after "+w.Client.Timeout.String()
		} else {
			a.Outcome, a.Error = "network_error", "network error: "+err.Error()
		}
		a.FinishedAt = time.Now()
		return a
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxProviderReply))
	resp.Body.Close()

	a.HTTPStatus = resp.StatusCode
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		a.Outcome = "accepted"
	case resp.StatusCode == http.StatusTooManyRequests:
		a.Outcome, a.Error = "rate_limited", "HTTP 429 rate limited"
	case resp.StatusCode >= 500:
		a.Outcome, a.Error = "server_error", "HTTP "+strconv.Itoa(resp.StatusCode)
	default:
		a.Outcome, a.Error = "rejected", "HTTP "+strconv.Itoa(resp.StatusCode)
	}
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs >= 0 {
		a.retryAfter = time.Duration(secs) * time.Second
	}
	a.FinishedAt = time.Now()
	return a
}

type attemptWithRetry struct {
	Attempt
	retryAfter time.Duration
}
