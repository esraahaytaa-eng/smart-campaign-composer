package content_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/content"
	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/dbtest"
)

// TestEstimate_MatchesGoRender checks the SQL estimate (D3) against Go's
// Render for every seeded user, with templates whose length lands right
// around the limits, so the two must agree user by user.
func TestEstimate_MatchesGoRender(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.FreshDB(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	if err := db.Seed(ctx, pool, db.DefaultRNGSeed, now, "seed-password"); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Istanbul")
	// Everyone: an order count of at least 0.
	everyone := []audience.Rule{{Field: "order_count", Op: "gte", Value: json.RawMessage(`0`)}}

	c := content.Content{
		// Rendered length is 143 + first, last and city lengths (seeded: about 8 to 25),
		// so roughly half the users go over 160. The subject and body work the same way.
		SMS: &content.Message{Body: "Merhaba {{first_name}} {{last_name}}, " + strings.Repeat("ş", 123) + " {{city}}'da %30!"},
		Email: &content.Message{
			Subject: "{{first_name}} {{last_name}}, " + strings.Repeat("e", 58) + " {{city}}",
			Body:    "Hi {{first_name}},\n\n" + strings.Repeat("b", 1978) + "\n{{city}}",
		},
	}
	if errs := content.ValidateDraft(c, []string{"sms", "email"}); len(errs) != 0 {
		t.Fatalf("templates must be valid drafts: %+v", errs)
	}

	audiences := map[string]content.Audience{}
	want := map[string]int{}
	for _, ch := range []string{"sms", "email"} {
		where, args, err := audience.Build(everyone, ch, now, loc)
		if err != nil {
			t.Fatal(err)
		}
		audiences[ch] = content.Audience{Where: where, Args: args}

		rows, err := pool.Query(ctx, `SELECT u.first_name, coalesce(u.last_name, ''), u.city FROM users u WHERE `+where, args...)
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for rows.Next() {
			var u content.UserVars
			if err := rows.Scan(&u.FirstName, &u.LastName, &u.City); err != nil {
				t.Fatal(err)
			}
			total++
			if ch == "sms" && utf8.RuneCountInString(content.Render(c.SMS.Body, u)) > content.SMSLimit {
				want[ch]++
			}
			if ch == "email" && (utf8.RuneCountInString(content.Render(c.Email.Subject, u)) > content.SubjectLimit ||
				utf8.RuneCountInString(content.Render(c.Email.Body, u)) > content.EmailBodyLimit) {
				want[ch]++
			}
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		// The test only proves something if the limit splits the audience.
		if want[ch] == 0 || want[ch] == total {
			t.Fatalf("%s: %d of %d over the limit; adjust the template so some are over and some are not", ch, want[ch], total)
		}
		t.Logf("%s: %d of %d users over the limit", ch, want[ch], total)
	}

	got, err := content.EstimateOverLimit(ctx, pool, c, audiences)
	if err != nil {
		t.Fatal(err)
	}
	if got.SMS != want["sms"] || got.Email != want["email"] {
		t.Fatalf("SQL estimate %+v, Go render sms=%d email=%d", got, want["sms"], want["email"])
	}
}

func TestEstimate_OnlyChosenChannels(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.FreshDB(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		VALUES ('A', 'a@example.com', '+905000000001', 'Ankara', 'tr', true, true, now())`); err != nil {
		t.Fatal(err)
	}
	c := content.Content{SMS: &content.Message{Body: strings.Repeat("x", 150) + " {{city}} {{city}}"}}
	where, args, _ := audience.Build([]audience.Rule{{Field: "city", Op: "in", Value: json.RawMessage(`["Ankara"]`)}}, "sms", time.Now(), time.UTC)

	got, err := content.EstimateOverLimit(ctx, pool, c, map[string]content.Audience{"sms": {Where: where, Args: args}})
	if err != nil || got.SMS != 1 || got.Email != 0 {
		t.Fatalf("want sms=1 email=0, got %+v (%v)", got, err)
	}
}

// Empty values leave double or edge spaces; tidying them decides whether
// these templates go over 160, so SQL must tidy exactly like Render.
func TestEstimate_TidiesLikeRender(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.FreshDB(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (first_name, last_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		VALUES ('A', NULL, 'a@example.com', '+905000000001', 'Ankara', 'tr', true, true, now())`); err != nil {
		t.Fatal(err)
	}
	where, args, _ := audience.Build([]audience.Rule{{Field: "city", Op: "in", Value: json.RawMessage(`["Ankara"]`)}}, "sms", time.Now(), time.UTC)
	u := content.UserVars{FirstName: "A", City: "Ankara"}

	for name, tpl := range map[string]string{
		"collapse": strings.Repeat("x", 158) + " {{last_name}} y", // 160 after collapsing, 161 without
		"trim":     "{{last_name}} " + strings.Repeat("x", 160),   // 160 after trimming, 161 without
		"over":     strings.Repeat("x", 159) + " {{city}}",        // genuinely over
	} {
		want := 0
		if utf8.RuneCountInString(content.Render(tpl, u)) > content.SMSLimit {
			want = 1
		}
		got, err := content.EstimateOverLimit(ctx, pool, content.Content{SMS: &content.Message{Body: tpl}},
			map[string]content.Audience{"sms": {Where: where, Args: args}})
		if err != nil || got.SMS != want {
			t.Errorf("%s: SQL counted %d, Go render says %d (%v)", name, got.SMS, want, err)
		}
	}
}
