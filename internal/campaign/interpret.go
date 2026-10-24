// Package campaign holds the campaign workflow: interpreting a brief, drafts,
// approval and sending (D1, D11).
package campaign

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/llm"
)

const (
	maxRequestChars = 1000 // D15
	maxReasonChars  = 500
	maxNoteChars    = 200
	maxScheduleAge  = 30 * 24 * time.Hour // D29
)

var (
	ErrRequestText = fmt.Errorf("the request must be 1 to %d characters", maxRequestChars)
	// ErrAIInvalid means the AI's answer failed validation twice (D15).
	ErrAIInvalid = errors.New("the AI returned an invalid answer twice")
)

// Interpretation is a validated AI reading of a brief. Rules are only set
// when Unsupported is false, and are already in canonical form.
type Interpretation struct {
	Unsupported     bool
	Reason          string
	Rules           []audience.Rule
	SuggestedSendAt *time.Time // nil unless usable: within now..now+30 days
	ScheduleNote    string
}

// llmCalls counts AI calls (D26). Result is ok, invalid (failed validation,
// retried once), error (API error or timeout), or cancelled (the client left).
var llmCalls = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "scc_llm_calls_total", Help: "AI calls by operation (interpret, content) and result.",
}, []string{"operation", "result"})

// AI call timing (D72): the whole call, and for streamed content the wait
// before the first text, which is what the marketer feels most.
var (
	aiBuckets   = []float64{0.5, 1, 2, 4, 8, 15, 30, 60}
	llmDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "scc_llm_call_duration_seconds", Help: "AI call duration by operation, every result included.", Buckets: aiBuckets,
	}, []string{"operation"})
	llmFirstText = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "scc_llm_first_text_seconds", Help: "Wait from a streamed AI call's start to its first text.", Buckets: aiBuckets,
	}, []string{"operation"})
)

// resultOf labels a failed AI call: cancelled if the client left, else error.
func resultOf(ctx context.Context) string {
	if ctx.Err() != nil {
		return "cancelled"
	}
	return "error"
}

// Interpret asks the model to read the brief and validates the answer (D15):
// an invalid answer is retried once with the validation errors; API errors
// and timeouts are not retried. Each call gets its own timeout.
func Interpret(ctx context.Context, model llm.LLM, in llm.InterpretInput, timeout time.Duration) (Interpretation, error) {
	text, err := checkRequestText(in.Text)
	if err != nil {
		return Interpretation{}, err
	}
	in.Text = text
	loc, err := loadZone(in.TimeZone)
	if err != nil {
		return Interpretation{}, err
	}
	in.Errors = nil
	for range 2 {
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		raw, err := model.InterpretRequest(callCtx, in)
		llmDuration.WithLabelValues("interpret").Observe(time.Since(start).Seconds())
		cancel()
		var got Interpretation
		var problems []string
		var invalid *llm.InvalidAnswerError
		switch {
		case errors.As(err, &invalid):
			problems = invalid.Problems
		case errors.Is(err, llm.ErrTruncated):
			problems = []string{"the answer was cut off; keep it short"}
		case err != nil:
			llmCalls.WithLabelValues("interpret", resultOf(ctx)).Inc()
			return Interpretation{}, fmt.Errorf("AI request failed: %w", err)
		default:
			got, problems = parseInterpretation(raw, in, loc)
		}
		if len(problems) == 0 {
			llmCalls.WithLabelValues("interpret", "ok").Inc()
			return got, nil
		}
		llmCalls.WithLabelValues("interpret", "invalid").Inc()
		in.Errors = problems
	}
	return Interpretation{}, fmt.Errorf("%w: %s", ErrAIInvalid, strings.Join(in.Errors, "; "))
}

// checkRequestText trims the brief and enforces its length (D15), counted
// in characters (code points) like every limit in the app (D2).
func checkRequestText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if n := utf8.RuneCountInString(text); n == 0 || n > maxRequestChars {
		return "", ErrRequestText
	}
	return text, nil
}

// aiAnswer is the JSON contract with the model (see llm.interpretSchema).
type aiAnswer struct {
	Unsupported     bool            `json:"unsupported"`
	Reason          string          `json:"reason"`
	Rules           []audience.Rule `json:"rules"`
	SuggestedSendAt *string         `json:"suggested_send_at"`
	ScheduleNote    *string         `json:"schedule_note"`
}

// parseInterpretation validates one answer and returns every problem found.
func parseInterpretation(raw json.RawMessage, in llm.InterpretInput, loc *time.Location) (Interpretation, []string) {
	var out aiAnswer
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return Interpretation{}, []string{"answer is not valid JSON for the schema: " + err.Error()}
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return Interpretation{}, []string{"answer has data after the JSON object"}
	}

	if out.Unsupported {
		if strings.TrimSpace(out.Reason) == "" {
			return Interpretation{}, []string{"unsupported answers need a reason"}
		}
		if utf8.RuneCountInString(out.Reason) > maxReasonChars {
			return Interpretation{}, []string{fmt.Sprintf("reason must be at most %d characters", maxReasonChars)}
		}
		return Interpretation{Unsupported: true, Reason: out.Reason}, nil
	}

	var problems []string
	got := Interpretation{}
	rules, errs := audience.Validate(out.Rules, in.Catalog)
	for _, e := range errs {
		if e.Rule < 0 {
			problems = append(problems, e.Message)
		} else {
			problems = append(problems, fmt.Sprintf("rule %d: %s", e.Rule, e.Message))
		}
	}
	got.Rules = rules

	if out.ScheduleNote != nil {
		got.ScheduleNote = strings.TrimSpace(*out.ScheduleNote)
		if utf8.RuneCountInString(got.ScheduleNote) > maxNoteChars {
			problems = append(problems, fmt.Sprintf("schedule_note must be at most %d characters", maxNoteChars))
		}
	}
	if out.SuggestedSendAt != nil {
		at, err := time.Parse(time.RFC3339, *out.SuggestedSendAt)
		_, offset := at.Zone()
		_, zoneOffset := at.In(loc).Zone()
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("suggested_send_at %q is not an RFC 3339 time with offset", *out.SuggestedSendAt))
		case offset != zoneOffset:
			// A wrong offset would silently move the time; the retry gets it right.
			problems = append(problems, fmt.Sprintf("suggested_send_at %q must use the %s offset %s",
				*out.SuggestedSendAt, in.TimeZone, at.In(loc).Format("-07:00")))
		default:
			got.SuggestedSendAt, got.ScheduleNote = usableSendAt(at, got.ScheduleNote, in.Now)
		}
	}
	if len(problems) > 0 {
		return Interpretation{}, problems
	}
	return got, nil
}

// usableSendAt keeps a suggestion only inside the allowed scheduling window;
// otherwise it explains in the note why nothing was prefilled (D29).
func usableSendAt(at time.Time, note string, now time.Time) (*time.Time, string) {
	phrase := note
	if phrase == "" {
		phrase = at.Format(time.RFC3339)
	}
	switch {
	case at.Before(now):
		return nil, fmt.Sprintf("%q is in the past, so no send time was suggested", phrase)
	case at.After(now.Add(maxScheduleAge)):
		return nil, fmt.Sprintf("%q is more than 30 days ahead, so no send time was suggested", phrase)
	}
	return &at, note
}
