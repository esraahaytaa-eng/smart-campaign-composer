// Package provider is the mock SMS and email provider (D6, D20). It behaves
// like a real one on the wire: an Idempotency-Key per message, delays,
// 5xx errors, 429 with Retry-After and requests that hang past the client's
// timeout. No message is really sent.
package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/config"
)

const (
	maxKey  = 200
	maxBody = 64 << 10
)

type Server struct {
	pool      *pgxpool.Pool
	behaviour map[string]config.ProviderChannel

	mu  sync.Mutex
	rnd *mrand.Rand // seeded, so a run's outcomes repeat (D20)

	// Hang is how long a "timeout" request takes before it is accepted; it is
	// longer than the worker's 5 s client timeout (D19), so the response is lost.
	Hang time.Duration
	// StaleAfter frees a key left in progress, e.g. by a crash mid-request.
	StaleAfter time.Duration
}

func New(pool *pgxpool.Pool, behaviour map[string]config.ProviderChannel, seed int64) *Server {
	return &Server{
		pool:       pool,
		behaviour:  behaviour,
		rnd:        mrand.New(mrand.NewPCG(uint64(seed), uint64(seed))),
		Hang:       10 * time.Second,
		StaleAfter: time.Minute,
	}
}

// Routes registers POST /sms/send and POST /email/send.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /{channel}/send", s.send)
}

type outcome int

const (
	accept outcome = iota
	serverError
	rateLimited
	hang
)

// draw picks the next simulated outcome and delay from the seeded generator.
func (s *Server) draw(b config.ProviderChannel) (outcome, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rnd.Float64()
	delay := b.DelayMin
	if b.DelayMax > b.DelayMin {
		delay += time.Duration(s.rnd.Int64N(int64(b.DelayMax - b.DelayMin + 1)))
	}
	switch {
	case r < b.TimeoutRate:
		return hang, delay
	case r < b.TimeoutRate+b.ErrorRate:
		return serverError, delay
	case r < b.TimeoutRate+b.ErrorRate+b.RateLimitRate:
		return rateLimited, delay
	}
	return accept, delay
}

type sendRequest struct {
	To      string  `json:"to"`
	Subject *string `json:"subject"`
	Body    string  `json:"body"`
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	channel := r.PathValue("channel")
	b, ok := s.behaviour[channel]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown channel"})
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > maxKey {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Idempotency-Key header of 1 to 200 characters is required"})
		return
	}
	if msg := validate(r, channel); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}

	// The provider finishes what it started even if the caller hangs up:
	// that is exactly the lost-response case idempotency exists for.
	ctx := context.WithoutCancel(r.Context())
	if !s.claim(ctx, w, key, channel) {
		return
	}
	result, delay := s.draw(b)
	if result == hang {
		delay = s.Hang
	}
	time.Sleep(delay)

	switch result {
	case serverError, rateLimited:
		// Release the key so the caller's retry is processed (D7).
		if _, err := s.pool.Exec(ctx, `DELETE FROM provider.idempotency_keys WHERE key = $1 AND state = 'in_progress'`, key); err != nil {
			slog.Error("provider: release key", "err", err)
		}
		if result == rateLimited {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "simulated rate limit"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "simulated provider error"})
		}
		return
	}

	answer, _ := json.Marshal(map[string]string{"id": channel + "_" + rand.Text(), "status": "accepted"})
	// Answer with the stored form (jsonb re-formats), so replays are byte-identical.
	var resp []byte
	if err := s.pool.QueryRow(ctx, `UPDATE provider.idempotency_keys SET state = 'completed', http_status = 200,
		response = $2, completed_at = now() WHERE key = $1 RETURNING response::text`, key, answer).Scan(&resp); err != nil {
		slog.Error("provider: complete key", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(resp)
}

// claim stores the key as in progress before anything is simulated (D6). It
// answers the request itself when the key is already known: the stored
// result for a finished key, a retryable 503 for one still in progress.
func (s *Server) claim(ctx context.Context, w http.ResponseWriter, key, channel string) bool {
	tag, err := s.pool.Exec(ctx, `INSERT INTO provider.idempotency_keys (key, channel, state) VALUES ($1, $2, 'in_progress')
		ON CONFLICT (key) DO NOTHING`, key, channel)
	if err != nil {
		slog.Error("provider: claim key", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return false
	}
	if tag.RowsAffected() == 1 {
		return true
	}

	var stored struct {
		channel, state string
		status         *int
		response       []byte
		stale          bool
	}
	err = s.pool.QueryRow(ctx, `SELECT channel, state, http_status, response, created_at < now() - make_interval(secs => $2)
		FROM provider.idempotency_keys WHERE key = $1`, key, s.StaleAfter.Seconds()).
		Scan(&stored.channel, &stored.state, &stored.status, &stored.response, &stored.stale)
	switch {
	case errors.Is(err, pgx.ErrNoRows): // released by a failed attempt meanwhile
		busy(w)
		return false
	case err != nil:
		slog.Error("provider: load key", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return false
	case stored.channel != channel:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this Idempotency-Key was used for another channel"})
		return false
	case stored.state == "completed":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Idempotent-Replayed", "true")
		w.WriteHeader(*stored.status)
		w.Write(stored.response)
		return false
	case stored.stale:
		tag, err := s.pool.Exec(ctx, `UPDATE provider.idempotency_keys SET created_at = now()
			WHERE key = $1 AND state = 'in_progress' AND created_at < now() - make_interval(secs => $2)`, key, s.StaleAfter.Seconds())
		if err == nil && tag.RowsAffected() == 1 {
			return true
		}
	}
	busy(w)
	return false
}

// busy is retryable for the caller: D7 retries 5xx and honours Retry-After.
func busy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "a request with this Idempotency-Key is in progress"})
}

func validate(r *http.Request, channel string) string {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBody))
	if err != nil {
		return "request body too large"
	}
	var req sendRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return "invalid JSON: " + err.Error()
	}
	switch {
	case req.To == "" || req.Body == "":
		return `"to" and "body" are required`
	case channel == "email" && (req.Subject == nil || *req.Subject == ""):
		return `"subject" is required for email`
	case channel == "sms" && req.Subject != nil:
		return "SMS messages have no subject"
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
