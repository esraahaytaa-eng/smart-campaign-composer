package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/llm"
)

var (
	now     = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	catalog = audience.Catalog{Cities: []string{"İstanbul", "Ankara"}, Categories: []string{"books", "electronics"}}
)

// scripted returns one prepared answer per call and records the inputs.
type scripted struct {
	outputs []string
	err     error
	errs    []error // per call, when set
	block   bool    // wait for the context to end (simulates a hung API)
	calls   []llm.InterpretInput
}

func (s *scripted) InterpretRequest(ctx context.Context, in llm.InterpretInput) (json.RawMessage, error) {
	s.calls = append(s.calls, in)
	if s.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	if i := len(s.calls) - 1; i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	return json.RawMessage(s.outputs[len(s.calls)-1]), nil
}

func (s *scripted) StreamContent(context.Context, llm.ContentInput, func(string)) (json.RawMessage, error) {
	return nil, errors.New("not used by interpretation tests")
}

func output(rules, sendAt, note string) string {
	return `{"unsupported":false,"reason":"","rules":` + rules +
		`,"suggested_send_at":` + sendAt + `,"schedule_note":` + note + `}`
}

const (
	goodRules = `[{"field":"city","op":"in","value":["istanbul"]},{"field":"cart_added","op":"within","value":{"last_days":30}}]`
	badRules  = `[{"field":"city","op":"in","value":["Paris"]}]`
)

func interpret(t *testing.T, s *scripted, text string) (Interpretation, error) {
	t.Helper()
	return Interpret(context.Background(), s, llm.InterpretInput{
		Text: text, Now: now, TimeZone: "Europe/Istanbul", Catalog: catalog,
	}, time.Second)
}

func TestInterpret_ValidFirstTry(t *testing.T) {
	s := &scripted{outputs: []string{output(goodRules, "null", "null")}}

	got, err := interpret(t, s, "brief")

	if err != nil || len(s.calls) != 1 {
		t.Fatalf("err %v, %d calls", err, len(s.calls))
	}
	if got.Unsupported || len(got.Rules) != 2 || string(got.Rules[0].Value) != `["İstanbul"]` {
		t.Fatalf("want normalized rules, got %+v", got)
	}
}

func TestInterpret_InvalidThenValid_RetriesWithErrors(t *testing.T) {
	s := &scripted{outputs: []string{output(badRules, "null", "null"), output(goodRules, "null", "null")}}

	got, err := interpret(t, s, "brief")

	if err != nil || len(s.calls) != 2 || len(got.Rules) != 2 {
		t.Fatalf("err %v, %d calls, %+v", err, len(s.calls), got)
	}
	if len(s.calls[0].Errors) != 0 {
		t.Fatalf("first call must not carry errors: %v", s.calls[0].Errors)
	}
	if !strings.Contains(strings.Join(s.calls[1].Errors, "\n"), `unknown city "Paris"`) {
		t.Fatalf("retry prompt must carry the validation errors, got %v", s.calls[1].Errors)
	}
}

func TestInterpret_InvalidTwice_ErrAIInvalid(t *testing.T) {
	for name, bad := range map[string]string{
		"unknown city":         output(badRules, "null", "null"),
		"not JSON":             `Sure! Here are the rules: city = Istanbul`,
		"unknown key":          `{"unsupported":false,"reason":"","rules":` + goodRules + `,"suggested_send_at":null,"schedule_note":null,"sql":"DROP"}`,
		"no rules":             output(`[]`, "null", "null"),
		"channels (not asked)": strings.Replace(output(goodRules, "null", "null"), `"rules":`, `"channels":["sms"],"rules":`, 1),
		"bad time format":      output(goodRules, `"tomorrow 10:00"`, `"tomorrow"`),
		// 10:00 written in UTC for an Istanbul campaign would silently become 13:00 local.
		"wrong zone offset":   output(goodRules, `"2026-10-22T10:00:00Z"`, `"tomorrow at 10:00"`),
		"unsupported, no why": `{"unsupported":true,"reason":"","rules":[],"suggested_send_at":null,"schedule_note":null}`,
		"reason too long":     `{"unsupported":true,"reason":"` + strings.Repeat("x", 501) + `","rules":[],"suggested_send_at":null,"schedule_note":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := &scripted{outputs: []string{bad, bad}}

			_, err := interpret(t, s, "brief")

			if !errors.Is(err, ErrAIInvalid) || len(s.calls) != 2 {
				t.Fatalf("want ErrAIInvalid after 2 calls, got %v after %d", err, len(s.calls))
			}
		})
	}
}

func TestInterpret_UnsupportedPassthrough(t *testing.T) {
	s := &scripted{outputs: []string{`{"unsupported":true,"reason":"Age is not in the catalog.","rules":[{"field":"city","op":"in","value":["Ankara"]}],"suggested_send_at":null,"schedule_note":null}`}}

	got, err := interpret(t, s, "brief")

	if err != nil || len(s.calls) != 1 {
		t.Fatalf("err %v, %d calls", err, len(s.calls))
	}
	if !got.Unsupported || got.Reason != "Age is not in the catalog." || got.Rules != nil {
		t.Fatalf("want unsupported with no rules, got %+v", got)
	}
}

func TestInterpret_Schedule(t *testing.T) {
	cases := []struct {
		name, sendAt, note string
		wantAt             string // "" = not prefilled
		wantNote           string
	}{
		{"valid", `"2026-10-22T10:00:00+03:00"`, `"yarın saat 10:00"`, "2026-10-22T07:00:00Z", "yarın saat 10:00"},
		{"in the past", `"2026-10-20T10:00:00+03:00"`, `"yesterday 10:00"`, "", `"yesterday 10:00" is in the past`},
		{"over 30 days", `"2026-12-01T10:00:00+03:00"`, `"1 December"`, "", `"1 December" is more than 30 days ahead`},
		{"vague", `null`, `"sometime next week"`, "", "sometime next week"},
		{"none", `null`, `null`, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &scripted{outputs: []string{output(goodRules, tc.sendAt, tc.note)}}

			got, err := interpret(t, s, "brief")

			if err != nil || len(got.Rules) != 2 {
				t.Fatalf("err %v, rules must be kept: %+v", err, got)
			}
			at := ""
			if got.SuggestedSendAt != nil {
				at = got.SuggestedSendAt.UTC().Format(time.RFC3339)
			}
			if at != tc.wantAt || !strings.Contains(got.ScheduleNote, tc.wantNote) {
				t.Fatalf("want %q / %q, got %q / %q", tc.wantAt, tc.wantNote, at, got.ScheduleNote)
			}
		})
	}
}

func TestInterpret_TimeoutNoRetry(t *testing.T) {
	s := &scripted{block: true}

	_, err := Interpret(context.Background(), s, llm.InterpretInput{Text: "brief", Now: now, TimeZone: "Europe/Istanbul", Catalog: catalog}, 20*time.Millisecond)

	if !errors.Is(err, context.DeadlineExceeded) || len(s.calls) != 1 {
		t.Fatalf("want deadline error after 1 call, got %v after %d", err, len(s.calls))
	}
}

func TestInterpret_APIErrorNoRetry(t *testing.T) {
	s := &scripted{err: errors.New("503 overloaded")}

	_, err := interpret(t, s, "brief")

	if err == nil || errors.Is(err, ErrAIInvalid) || len(s.calls) != 1 {
		t.Fatalf("want API error after 1 call, got %v after %d", err, len(s.calls))
	}
}

func TestInterpret_RequestTextLimits(t *testing.T) {
	for _, text := range []string{"", "   ", strings.Repeat("ş", 1001)} {
		s := &scripted{}
		if _, err := interpret(t, s, text); !errors.Is(err, ErrRequestText) || len(s.calls) != 0 {
			t.Errorf("%d chars: want ErrRequestText without calling the AI, got %v (%d calls)", len([]rune(text)), err, len(s.calls))
		}
	}
	s := &scripted{outputs: []string{output(goodRules, "null", "null")}}
	if _, err := interpret(t, s, strings.Repeat("ş", 1000)); err != nil {
		t.Errorf("1,000 chars must be accepted: %v", err)
	}
}

// End to end with the real mock: a supported brief gives rules, an unknown one
// gives "unsupported" and never a guessed audience.
func TestInterpret_WithMock(t *testing.T) {
	m := llm.NewMock()
	in := llm.InterpretInput{Now: now, TimeZone: "Europe/Istanbul", Catalog: catalog}

	in.Text = llm.MockExamples[0].Brief
	got, err := Interpret(context.Background(), m, in, time.Second)
	if err != nil || got.Unsupported || len(got.Rules) != 3 {
		t.Fatalf("case example: %v %+v", err, got)
	}

	in.Text = "Send something nice to our best customers"
	got, err = Interpret(context.Background(), m, in, time.Second)
	if err != nil || !got.Unsupported || got.Rules != nil {
		t.Fatalf("unknown brief: %v %+v", err, got)
	}
}

func TestInterpret_InvalidTimeZone_NoAICall(t *testing.T) {
	s := &scripted{}

	_, err := Interpret(context.Background(), s, llm.InterpretInput{Text: "brief", Now: now, TimeZone: "Mars/Olympus", Catalog: catalog}, time.Second)

	if err == nil || errors.Is(err, ErrAIInvalid) || len(s.calls) != 0 {
		t.Fatalf("want a time zone error without calling the AI, got %v after %d calls", err, len(s.calls))
	}
}

// A cut-off answer is an invalid answer (D15): retried once, not "unavailable".
func TestInterpret_TruncatedAnswerIsRetried(t *testing.T) {
	s := &scripted{errs: []error{llm.ErrTruncated}, outputs: []string{"", output(goodRules, "null", "null")}}
	if got, err := interpret(t, s, "brief"); err != nil || len(s.calls) != 2 || len(got.Rules) != 2 {
		t.Fatalf("truncated then valid: want success after 2 calls, got %v after %d", err, len(s.calls))
	}

	s = &scripted{errs: []error{llm.ErrTruncated, llm.ErrTruncated}, outputs: []string{"", ""}}
	if _, err := interpret(t, s, "brief"); !errors.Is(err, ErrAIInvalid) || len(s.calls) != 2 {
		t.Fatalf("truncated twice: want ErrAIInvalid after 2 calls, got %v after %d", err, len(s.calls))
	}
}
