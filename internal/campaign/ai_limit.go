package campaign

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"smart-campaign-composer/internal/auth"
	"smart-campaign-composer/internal/llm"
)

// AICallLimit caps real AI provider calls per marketer: at most n in any
// hour (D63). Every call counts, retries included. In memory, per api process.
type AICallLimit struct {
	n     int
	now   func() time.Time
	mu    sync.Mutex
	calls map[int64][]time.Time // per marketer, oldest first, within the last hour
}

const aiLimitWindow = time.Hour

func NewAICallLimit(n int) *AICallLimit {
	return &AICallLimit{n: n, now: time.Now, calls: map[int64][]time.Time{}}
}

// take records a call for marketer if allowed; otherwise it reports how long
// until the oldest counted call leaves the window.
func (l *AICallLimit) take(marketer int64) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if wait := l.waitLocked(marketer, now); wait > 0 {
		return wait, false
	}
	l.calls[marketer] = append(l.calls[marketer], now)
	return 0, true
}

// wait is how long marketer must wait before the next call (0: now).
func (l *AICallLimit) wait(marketer int64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.waitLocked(marketer, l.now())
}

func (l *AICallLimit) waitLocked(marketer int64, now time.Time) time.Duration {
	calls := l.calls[marketer]
	for len(calls) > 0 && now.Sub(calls[0]) >= aiLimitWindow {
		calls = calls[1:]
	}
	if len(calls) == 0 {
		delete(l.calls, marketer) // the map holds only marketers with recent calls
	} else {
		l.calls[marketer] = calls
	}
	if len(calls) < l.n {
		return 0
	}
	return aiLimitWindow - now.Sub(calls[len(calls)-l.n])
}

// aiLimitError is a provider call refused by the limit.
type aiLimitError struct {
	limit int
	wait  time.Duration
}

func (e *aiLimitError) Error() string {
	return fmt.Sprintf("AI call limit reached: at most %d AI calls per hour per marketer. Try again in %d minutes.",
		e.limit, int(math.Ceil(e.wait.Minutes())))
}

func writeAILimit(w http.ResponseWriter, e *aiLimitError) {
	seconds := int(math.Ceil(e.wait.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	json.NewEncoder(w).Encode(map[string]any{"error": e.Error(), "code": "ai_call_limit", "retry_after_seconds": seconds})
}

// limitedLLM spends one of the marketer's calls on every provider call.
type limitedLLM struct {
	llm.LLM
	limit *AICallLimit
}

func (m limitedLLM) allow(ctx context.Context) error {
	marketer, _ := auth.MarketerID(ctx)
	if wait, ok := m.limit.take(marketer); !ok {
		return &aiLimitError{m.limit.n, wait}
	}
	return nil
}

func (m limitedLLM) InterpretRequest(ctx context.Context, in llm.InterpretInput) (json.RawMessage, error) {
	if err := m.allow(ctx); err != nil {
		return nil, err
	}
	return m.LLM.InterpretRequest(ctx, in)
}

func (m limitedLLM) StreamContent(ctx context.Context, in llm.ContentInput, onText func(string)) (json.RawMessage, error) {
	if err := m.allow(ctx); err != nil {
		return nil, err
	}
	return m.LLM.StreamContent(ctx, in, onText)
}

// model is the AI the handlers call: limited with real AI, as is otherwise.
func (h *Handler) model() llm.LLM {
	if h.AICalls == nil {
		return h.LLM
	}
	return limitedLLM{h.LLM, h.AICalls}
}
