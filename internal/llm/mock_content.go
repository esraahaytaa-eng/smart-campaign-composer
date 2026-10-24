package llm

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

type mockMessage struct {
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

// mockTexts are the mock's fixed templates per catalog language.
type mockText struct{ sms, subject, body string }

var mockTexts = map[string]mockText{
	"tr": {
		sms:     "Merhaba {{first_name}}! Sana özel bir fırsat seni bekliyor. Kaçırmadan hemen göz at!",
		subject: "{{first_name}}, sana özel bir fırsat var",
		body:    "Merhaba {{first_name}},\n\n{{city}} için hazırladığımız özel fırsatı kaçırma!\n\nSevgiler,\nSmart Campaign Composer",
	},
	"en": {
		sms:     "Hi {{first_name}}! A special offer is waiting for you. Take a look before it's gone!",
		subject: "{{first_name}}, a special offer is waiting for you",
		body:    "Hi {{first_name}},\n\nWe have prepared a special offer for you in {{city}}. Don't miss it!\n\nBest regards,\nSmart Campaign Composer",
	},
}

// mockResendTexts are shorter, for recipients the first text was too long for (D31).
var mockResendTexts = map[string]mockText{
	"tr": {sms: "{{first_name}}, fırsatın seni bekliyor!", subject: "Fırsatın seni bekliyor"},
	"en": {sms: "{{first_name}}, your offer is waiting!", subject: "Your offer is waiting"},
}

// StreamContent returns fixed, valid templates in each selected language and
// streams them in small chunks, ChunkDelay apart, like a real model would.
func (m *Mock) StreamContent(ctx context.Context, in ContentInput, onText func(string)) (json.RawMessage, error) {
	out := map[string]map[string]mockMessage{}
	for _, lang := range in.Languages {
		texts := mockTexts[lang]
		if in.Resend {
			short := mockResendTexts[lang]
			texts.sms, texts.subject = short.sms, short.subject
		}
		out[lang] = map[string]mockMessage{}
		if slices.Contains(in.Channels, "sms") {
			out[lang]["sms"] = mockMessage{Body: texts.sms}
		}
		if slices.Contains(in.Channels, "email") {
			out[lang]["email"] = mockMessage{Subject: texts.subject, Body: texts.body}
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}

	runes := []rune(string(raw))
	for start := 0; start < len(runes); start += 12 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		onText(string(runes[start:min(start+12, len(runes))]))
		if m.ChunkDelay > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(m.ChunkDelay):
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}
