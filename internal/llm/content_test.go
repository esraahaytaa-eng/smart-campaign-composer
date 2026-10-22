package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"smart-campaign-composer/internal/content"
)

func mockContent(t *testing.T, ctx context.Context, brief string, channels []string) (content.Content, []string, error) {
	t.Helper()
	var chunks []string
	raw, err := NewMock().StreamContent(ctx, ContentInput{Text: brief, Channels: channels}, func(s string) { chunks = append(chunks, s) })
	if err != nil {
		return content.Content{}, chunks, err
	}
	if strings.Join(chunks, "") != string(raw) {
		t.Fatalf("streamed chunks do not add up to the result:\n%q\n%s", strings.Join(chunks, ""), raw)
	}
	var c content.Content
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("mock content does not match the schema: %v\n%s", err, raw)
	}
	return c, chunks, nil
}

func TestMockContent_ReplyInBriefLanguage(t *testing.T) {
	ctx := context.Background()
	both := []string{"sms", "email"}

	tr, chunks, err := mockContent(t, ctx, MockExamples[8].Brief, both) // Turkish example
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tr.SMS.Body, "Merhaba {{first_name}}") || !strings.Contains(tr.Email.Body, "Merhaba {{first_name}}") {
		t.Errorf("Turkish brief: want Turkish content with {{first_name}}, got %+v %+v", tr.SMS, tr.Email)
	}
	if len(chunks) < 5 {
		t.Errorf("want the answer streamed in several chunks, got %d", len(chunks))
	}

	en, _, err := mockContent(t, ctx, MockExamples[0].Brief, both)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(en.SMS.Body, "Hi {{first_name}}") || !strings.Contains(en.Email.Subject, "{{first_name}}") {
		t.Errorf("English brief: want English content with {{first_name}}, got %+v %+v", en.SMS, en.Email)
	}

	for name, c := range map[string]content.Content{"tr": tr, "en": en} {
		if errs := content.ValidateDraft(c, both); len(errs) != 0 {
			t.Errorf("%s mock content must be a valid draft: %+v", name, errs)
		}
	}
}

func TestMockContent_OnlyChosenChannels(t *testing.T) {
	c, _, err := mockContent(t, context.Background(), "Weekend sale for Ankara", []string{"email"})
	if err != nil {
		t.Fatal(err)
	}
	if c.SMS != nil || c.Email == nil {
		t.Fatalf("want email only, got %+v", c)
	}
	if errs := content.ValidateDraft(c, []string{"email"}); len(errs) != 0 {
		t.Fatalf("invalid: %+v", errs)
	}
}

func TestMockContent_StopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := NewMock()
	calls := 0

	_, err := m.StreamContent(ctx, ContentInput{Text: "brief", Channels: []string{"sms", "email"}}, func(string) {
		calls++
		if calls == 2 {
			cancel() // the browser went away
		}
	})

	if !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatalf("want context.Canceled after 2 chunks, got %v after %d", err, calls)
	}
}
