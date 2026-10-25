package llm

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"smart-campaign-composer/internal/audience"
)

var (
	testNow     = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	testCatalog = audience.Catalog{
		Cities:     []string{"İstanbul", "Ankara", "İzmir"},
		Categories: []string{"electronics", "fashion", "home", "beauty", "sports", "books"},
	}
)

type mockOutput struct {
	Unsupported     bool              `json:"unsupported"`
	Reason          string            `json:"reason"`
	Rules           []json.RawMessage `json:"rules"`
	SuggestedSendAt *string           `json:"suggested_send_at"`
	ScheduleNote    *string           `json:"schedule_note"`
}

func runMock(t *testing.T, brief string) mockOutput {
	t.Helper()
	raw, err := NewMock().InterpretRequest(context.Background(), InterpretInput{
		Text: brief, Now: testNow, TimeZone: "Europe/Istanbul", Catalog: testCatalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	var out mockOutput
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("mock output does not match the schema: %v\n%s", err, raw)
	}
	return out
}

// sameJSON compares JSON values regardless of formatting.
func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal([]byte(a), &x); err != nil {
		t.Fatalf("%v: %s", err, a)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return reflect.DeepEqual(x, y)
}

func TestMock_DocumentedPatterns(t *testing.T) {
	for _, ex := range MockExamples {
		t.Run(ex.Brief, func(t *testing.T) {
			out := runMock(t, ex.Brief)
			if out.Unsupported {
				t.Fatalf("unsupported: %s", out.Reason)
			}
			rules, _ := json.Marshal(out.Rules)
			if !sameJSON(t, string(rules), ex.Rules) {
				t.Fatalf("rules\n got: %s\nwant: %s", rules, ex.Rules)
			}
			var parsed []audience.Rule
			json.Unmarshal(rules, &parsed)
			if _, errs := audience.Validate(parsed, testCatalog); len(errs) > 0 {
				t.Fatalf("documented rules do not validate: %v", errs)
			}
		})
	}
}

func TestMock_ExamplesAreInREADME(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, ex := range MockExamples {
		if !strings.Contains(string(readme), ex.Brief) {
			t.Errorf("README is missing mock example %q", ex.Brief)
		}
	}
}

func TestMock_UnknownBrief_Unsupported(t *testing.T) {
	for _, brief := range []string{
		"Send something nice to our best customers",
		"",
		"Send a discount to users in Istanbul who are older than 30.",
		"Send it tomorrow at 10:00.",
		// No audience named: never all_customers (D14).
		"Send a weekend sale announcement.",
		"Hafta sonu indirimi duyurusu gönder.",
	} {
		out := runMock(t, brief)
		if !out.Unsupported || out.Reason != MockUnsupportedReason || len(out.Rules) != 0 {
			t.Errorf("%q: want unsupported with the mock reason and no rules, got %+v", brief, out)
		}
	}
}

// Everyone, said explicitly, is all_customers; "all users in a city" stays a city.
func TestMock_AllCustomers(t *testing.T) {
	all := `[{"field":"all_customers","op":"is","value":true}]`
	for brief, want := range map[string]string{
		"Send a holiday greeting to everyone.":                     all,
		"Send a holiday greeting to all of our customers.":         all,
		"Bütün kullanıcılara bayram tebriği gönder.":               all,
		"Herkese bayram tebriği gönder.":                           all,
		"Send a weekend sale announcement to all users in Ankara.": `[{"field":"city","op":"in","value":["Ankara"]}]`,
	} {
		out := runMock(t, brief)
		rules, _ := json.Marshal(out.Rules)
		if out.Unsupported || !sameJSON(t, string(rules), want) {
			t.Errorf("%q: want %s, got %s (unsupported %v)", brief, want, rules, out.Unsupported)
		}
	}
}

// A brief that matches a pattern but adds a condition must never fall back to
// the pattern's (broader) rules.
func TestMock_ExtraCondition_Unsupported(t *testing.T) {
	for _, brief := range []string{
		"Send a 30% discount reminder to users in Istanbul who like hiking and who added products to their cart in the last 30 days but did not buy anything.",
		"Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but did not buy anything and who like hiking.",
		"Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but did not buy anything. Only women.",
		"Send a coupon for customers who like hiking to all users in Ankara.",
		"Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00. Send it today at 9:00.",
		// Conditions hidden in the free-text offer (Milestone B review).
		"Send a discount to everyone in Izmir and to users in Istanbul.",
		"Ankara'daki tüm kullanıcılara 30 gündür sipariş vermeyenler için indirim gönder.",
		"Ankara'daki tüm kullanıcılara sadece kadınlar için indirim gönder.",
		"Send a 20% discount valid only for women to users in istanbul.",
		"Send a coupon except for VIPs to customers whose last order was more than 90 days ago.",
		"Send a coupon for VIPs to all users in Ankara.",
		"Ankara'daki tüm kullanıcılara VIP hariç indirim gönder.",
	} {
		if out := runMock(t, brief); !out.Unsupported || len(out.Rules) != 0 {
			t.Errorf("%q: want unsupported, got %+v", brief, out)
		}
	}
}

func TestMock_UnknownCityOrCategory_Unsupported(t *testing.T) {
	for _, brief := range []string{
		"Send a weekend sale announcement to all users in Paris.",
		"Send a new arrivals note to customers who bought toys in the last 30 days.",
		"Send a welcome discount to users who signed up in the last 400 days.",
	} {
		out := runMock(t, brief)
		if !out.Unsupported || len(out.Rules) != 0 || !strings.HasPrefix(out.Reason, "Mock AI") {
			t.Errorf("%q: want unsupported, got %+v", brief, out)
		}
	}
}

func TestMock_Schedule(t *testing.T) {
	cases := []struct{ brief, sendAt, note string }{
		// 21 Oct 12:00 UTC is 15:00 in Istanbul (UTC+3).
		{"Ankara'daki tüm kullanıcılara hafta sonu indirimi duyurusu gönder. Yarın saat 10:00'da gönder.", "2026-10-22T10:00:00+03:00", "Yarın saat 10:00'da gönder"},
		{"Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 9.", "2026-10-22T09:00:00+03:00", "Send it tomorrow at 9"},
		{"Send a weekend sale announcement to all users in Ankara. Send it today at 18:30.", "2026-10-21T18:30:00+03:00", "Send it today at 18:30"},
		{"Send a weekend sale announcement to all users in Ankara. Send it in 2 hours.", "2026-10-21T17:00:00+03:00", "Send it in 2 hours"},
		{"Ankara'daki tüm kullanıcılara hafta sonu indirimi duyurusu gönder. 3 saat sonra gönder.", "2026-10-21T18:00:00+03:00", "3 saat sonra gönder"},
		// Already past today: the mock still reports it; Interpret decides it can't be used.
		{"Send a weekend sale announcement to all users in Ankara. Send it today at 9:00.", "2026-10-21T09:00:00+03:00", "Send it today at 9:00"},
	}
	for _, tc := range cases {
		out := runMock(t, tc.brief)
		if out.Unsupported || out.SuggestedSendAt == nil || *out.SuggestedSendAt != tc.sendAt || out.ScheduleNote == nil || *out.ScheduleNote != tc.note {
			t.Errorf("%q: want %s / %q, got %+v", tc.brief, tc.sendAt, tc.note, out)
		}
	}
	for _, zero := range []string{
		"Send a weekend sale announcement to all users in Ankara. Send it in 0 hours.",
		"Ankara'daki tüm kullanıcılara hafta sonu indirimi duyurusu gönder. 0 saat sonra gönder.",
	} {
		if out := runMock(t, zero); !out.Unsupported {
			t.Errorf("%q: zero hours is not a send time, want unsupported, got %+v", zero, out)
		}
	}
	if out := runMock(t, "Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 25:00."); !out.Unsupported {
		t.Errorf("hour 25: want unsupported, got %+v", out)
	}
	if out := runMock(t, "Send a weekend sale announcement to all users in Ankara."); out.SuggestedSendAt != nil || out.ScheduleNote != nil {
		t.Errorf("no timing: want nulls, got %+v", out)
	}
}

func TestMock_InvalidTimeZone(t *testing.T) {
	_, err := NewMock().InterpretRequest(context.Background(), InterpretInput{
		Text: MockExamples[0].Brief, Now: testNow, TimeZone: "Mars/Olympus", Catalog: testCatalog,
	})
	if err == nil {
		t.Fatal("want error for unknown time zone")
	}
}
