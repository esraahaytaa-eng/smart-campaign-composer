// Package llm talks to the language model: Claude in real mode, a
// pattern-based mock otherwise (D22, D24). Implementations return the model's
// raw JSON; the campaign package validates it (D15), so both go through the
// same checks.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/content"
)

type LLM interface {
	// InterpretRequest turns a campaign brief into audience rules and an
	// optional send-time suggestion, as JSON matching interpretSchema.
	InterpretRequest(ctx context.Context, in InterpretInput) (json.RawMessage, error)
	// StreamContent writes message templates for the chosen channels in each
	// selected language as JSON matching contentSchema, keyed by language,
	// passing each piece of raw JSON text to onText as it arrives (D34), and
	// returns the complete JSON.
	StreamContent(ctx context.Context, in ContentInput, onText func(string)) (json.RawMessage, error)
}

// ContentInput, like InterpretInput, never contains user records (D15).
type ContentInput struct {
	Text      string   // the brief, in any language (D32)
	Channels  []string // "sms" and/or "email"
	Languages []string // catalog languages to write, one version each (D64)
	Errors    []string // validation errors of the previous answer, on the retry
	Resend    bool     // the first text was too long for some recipients (D31): write shorter
	// VersionA is set when writing an A/B test's version B (D68): the text it
	// is an alternative to, per language. The marketer's text, sent as data.
	VersionA map[string]content.Content
}

// InterpretInput never contains user records (D15): only the brief, the
// clock, the campaign's time zone and the rule catalog.
type InterpretInput struct {
	Text     string
	Now      time.Time
	TimeZone string // IANA name from the browser
	Catalog  audience.Catalog
	Errors   []string // validation errors of the previous answer, on the retry
}

// interpretOutput is the JSON shape both implementations produce.
type interpretOutput struct {
	Unsupported     bool            `json:"unsupported"`
	Reason          string          `json:"reason"`
	Rules           []audience.Rule `json:"rules"`
	SuggestedSendAt *string         `json:"suggested_send_at"`
	ScheduleNote    *string         `json:"schedule_note"`
}

// ErrTruncated means the answer hit the token limit and was cut off; callers
// treat it like an invalid answer (one retry, D15), not as an outage.
var ErrTruncated = errors.New("the AI answer was cut off at the token limit")

func unsupported(reason string) json.RawMessage {
	b, _ := json.Marshal(interpretOutput{Unsupported: true, Reason: reason, Rules: []audience.Rule{}})
	return b
}
