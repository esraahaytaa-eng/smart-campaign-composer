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
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/llm"
)

const (
	maxRequestChars = 1000 // proposed default (DECISIONS.md Open Questions)
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
	Channels        []string // suggestion only; the marketer chooses (D1)
	Rules           []audience.Rule
	SuggestedSendAt *time.Time // nil unless usable: within now..now+30 days
	ScheduleNote    string
}

// Interpret asks the model to read the brief and validates the answer (D15):
// an invalid answer is retried once with the validation errors; API errors
// and timeouts are not retried. Each call gets its own timeout.
func Interpret(ctx context.Context, model llm.LLM, in llm.InterpretInput, timeout time.Duration) (Interpretation, error) {
	in.Text = strings.TrimSpace(in.Text)
	if n := utf8.RuneCountInString(in.Text); n == 0 || n > maxRequestChars {
		return Interpretation{}, ErrRequestText
	}
	in.Errors = nil
	for range 2 {
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		raw, err := model.InterpretRequest(callCtx, in)
		cancel()
		if err != nil {
			return Interpretation{}, fmt.Errorf("AI request failed: %w", err)
		}
		got, problems := parseInterpretation(raw, in)
		if len(problems) == 0 {
			return got, nil
		}
		in.Errors = problems
	}
	return Interpretation{}, fmt.Errorf("%w: %s", ErrAIInvalid, strings.Join(in.Errors, "; "))
}

// aiAnswer is the JSON contract with the model (see llm.interpretSchema).
type aiAnswer struct {
	Unsupported     bool            `json:"unsupported"`
	Reason          string          `json:"reason"`
	Channels        []string        `json:"channels"`
	Rules           []audience.Rule `json:"rules"`
	SuggestedSendAt *string         `json:"suggested_send_at"`
	ScheduleNote    *string         `json:"schedule_note"`
}

// parseInterpretation validates one answer and returns every problem found.
func parseInterpretation(raw json.RawMessage, in llm.InterpretInput) (Interpretation, []string) {
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
	for _, ch := range out.Channels {
		if ch != "sms" && ch != "email" {
			problems = append(problems, fmt.Sprintf("unknown channel %q", ch))
		} else if !slices.Contains(got.Channels, ch) {
			got.Channels = append(got.Channels, ch)
		}
	}
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
		if err != nil {
			problems = append(problems, fmt.Sprintf("suggested_send_at %q is not an RFC 3339 time with offset", *out.SuggestedSendAt))
		} else {
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
