// Package llm talks to the language model: Claude in real mode, a
// pattern-based mock otherwise (D22, D24). Implementations return the model's
// raw JSON; the campaign package validates it (D15), so both go through the
// same checks.
package llm

import (
	"context"
	"encoding/json"
	"time"

	"smart-campaign-composer/internal/audience"
)

type LLM interface {
	// InterpretRequest turns a campaign brief into audience rules and an
	// optional send-time suggestion, as JSON matching interpretSchema.
	InterpretRequest(ctx context.Context, in InterpretInput) (json.RawMessage, error)
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
	Channels        []string        `json:"channels"`
	Rules           []audience.Rule `json:"rules"`
	SuggestedSendAt *string         `json:"suggested_send_at"`
	ScheduleNote    *string         `json:"schedule_note"`
}

func unsupported(reason string) json.RawMessage {
	b, _ := json.Marshal(interpretOutput{Unsupported: true, Reason: reason, Channels: []string{}, Rules: []audience.Rule{}})
	return b
}
