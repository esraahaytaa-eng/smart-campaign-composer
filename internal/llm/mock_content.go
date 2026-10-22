package llm

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"time"
)

// turkish detects a Turkish brief by its letters or common words (D32).
var turkish = regexp.MustCompile(`(?i)[çğıöşüÇĞİÖŞÜ]|\b(?:gonder|sepet|yarin|indirim|kampanya)\b`)

type mockMessage struct {
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

// mockTexts are the mock's fixed templates per language.
type mockText struct{ sms, subject, body string }

var mockTexts = map[bool]mockText{
	true: { // Turkish
		sms:     "Merhaba {{first_name}}! Sana özel bir fırsat seni bekliyor. Kaçırmadan hemen göz at!",
		subject: "{{first_name}}, sana özel bir fırsat var",
		body:    "Merhaba {{first_name}},\n\n{{city}} için hazırladığımız özel fırsatı kaçırma!\n\nSevgiler,\nSmart Campaign Composer",
	},
	false: {
		sms:     "Hi {{first_name}}! A special offer is waiting for you. Take a look before it's gone!",
		subject: "{{first_name}}, a special offer is waiting for you",
		body:    "Hi {{first_name}},\n\nWe have prepared a special offer for you in {{city}}. Don't miss it!\n\nBest regards,\nSmart Campaign Composer",
	},
}

// mockResendTexts are shorter, for recipients the first text was too long for (D31).
var mockResendTexts = map[bool]mockText{
	true:  {sms: "{{first_name}}, fırsatın seni bekliyor!", subject: "Fırsatın seni bekliyor"},
	false: {sms: "{{first_name}}, your offer is waiting!", subject: "Your offer is waiting"},
}

// StreamContent returns fixed, valid templates in the brief's language and
// streams them in small chunks, ChunkDelay apart, like a real model would.
func (m *Mock) StreamContent(ctx context.Context, in ContentInput, onText func(string)) (json.RawMessage, error) {
	isTurkish := turkish.MatchString(in.Text)
	texts := mockTexts[isTurkish]
	if in.Resend {
		short := mockResendTexts[isTurkish]
		texts.sms, texts.subject = short.sms, short.subject
	}
	out := map[string]mockMessage{}
	if slices.Contains(in.Channels, "sms") {
		out["sms"] = mockMessage{Body: texts.sms}
	}
	if slices.Contains(in.Channels, "email") {
		out["email"] = mockMessage{Subject: texts.subject, Body: texts.body}
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
