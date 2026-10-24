package content_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/audience"
	"smart-campaign-composer/internal/content"
	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/dbtest"
)

// TestEstimate_MatchesGoRender checks the SQL estimate (D3) against Go's
// Render for every seeded user, with templates whose length lands right
// around the limits, so the two must agree user by user.
func TestEstimate_MatchesGoRender(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.MigratedDB(t)
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
	// English speakers get their own text, around the limits in the same way.
	en := content.Content{
		SMS: &content.Message{Body: "Hello {{first_name}} {{last_name}}, " + strings.Repeat("e", 125) + " in {{city}}!"},
		Email: &content.Message{
			Subject: "{{first_name}} {{last_name}}, " + strings.Repeat("s", 58) + " {{city}}",
			Body:    "Hello {{first_name}},\n\n" + strings.Repeat("b", 1975) + "\n{{city}}",
		},
	}
	for _, x := range []content.Content{c, en} {
		if errs := content.ValidateDraft(x, []string{"sms", "email"}); len(errs) != 0 {
			t.Fatalf("templates must be valid drafts: %+v", errs)
		}
	}

	audiences := map[string]content.Audience{}
	want := map[string]int{}
	wantEn := map[string]int{}
	for _, ch := range []string{"sms", "email"} {
		where, args, err := audience.Build(everyone, ch, now, loc)
		if err != nil {
			t.Fatal(err)
		}
		audiences[ch] = content.Audience{Where: where, Args: args}

		rows, err := pool.Query(ctx, `SELECT u.first_name, coalesce(u.last_name, ''), u.city, u.language FROM users u WHERE `+where, args...)
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for rows.Next() {
			var u content.UserVars
			var lang string
			if err := rows.Scan(&u.FirstName, &u.LastName, &u.City, &lang); err != nil {
				t.Fatal(err)
			}
			total++
			tpl, counter := c, want
			if lang == "en" {
				tpl, counter = en, wantEn
			}
			if ch == "sms" && utf8.RuneCountInString(content.Render(tpl.SMS.Body, u)) > content.SMSLimit {
				counter[ch]++
			}
			if ch == "email" && (utf8.RuneCountInString(content.Render(tpl.Email.Subject, u)) > content.SubjectLimit ||
				utf8.RuneCountInString(content.Render(tpl.Email.Body, u)) > content.EmailBodyLimit) {
				counter[ch]++
			}
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		// The test only proves something if the limit splits the audience.
		if over := want[ch] + wantEn[ch]; want[ch] == 0 || wantEn[ch] == 0 || over == total {
			t.Fatalf("%s: %d+%d of %d over the limit; adjust the templates so some are over and some are not in each language", ch, want[ch], wantEn[ch], total)
		}
		t.Logf("%s: %d Turkish and %d English of %d users over the limit", ch, want[ch], wantEn[ch], total)
	}

	got, err := content.EstimateOverLimit(ctx, pool, content.Variants{"tr": c, "en": en}, "tr", audiences)
	if err != nil {
		t.Fatal(err)
	}
	if got.ByLanguage["tr"].SMS != want["sms"] || got.ByLanguage["tr"].Email != want["email"] ||
		got.ByLanguage["en"].SMS != wantEn["sms"] || got.ByLanguage["en"].Email != wantEn["email"] {
		t.Fatalf("SQL estimate %+v, Go render tr=%v en=%v", got, want, wantEn)
	}
}

func TestEstimate_OnlyChosenChannels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.MigratedDB(t)
	if _, err := pool.Exec(ctx, `INSERT INTO users (first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		VALUES ('A', 'a@example.com', '+905000000001', 'Ankara', 'tr', true, true, now())`); err != nil {
		t.Fatal(err)
	}
	c := content.Content{SMS: &content.Message{Body: strings.Repeat("x", 150) + " {{city}} {{city}}"}}
	where, args, _ := audience.Build([]audience.Rule{{Field: "city", Op: "in", Value: json.RawMessage(`["Ankara"]`)}}, "sms", time.Now(), time.UTC)

	got, err := content.EstimateOverLimit(ctx, pool, content.Variants{"tr": c}, "tr", map[string]content.Audience{"sms": {Where: where, Args: args}})
	if err != nil || got.SMS != 1 || got.Email != 0 {
		t.Fatalf("want sms=1 email=0, got %+v (%v)", got, err)
	}
}

// Empty values leave double or edge spaces; tidying them decides whether
// these templates go over 160, so SQL must tidy exactly like Render.
func TestEstimate_TidiesLikeRender(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.MigratedDB(t)
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
		"punct":    strings.Repeat("x", 159) + " {{last_name}}!",  // 160 without the space left before "!", 161 with it
	} {
		want := 0
		if utf8.RuneCountInString(content.Render(tpl, u)) > content.SMSLimit {
			want = 1
		}
		got, err := content.EstimateOverLimit(ctx, pool, content.Variants{"tr": {SMS: &content.Message{Body: tpl}}}, "tr",
			map[string]content.Audience{"sms": {Where: where, Args: args}})
		if err != nil || got.SMS != want {
			t.Errorf("%s: SQL counted %d, Go render says %d (%v)", name, got.SMS, want, err)
		}
	}
}

// Control characters in a value become spaces, tidied like any space (D72).
func TestEstimate_CleansValuesLikeRender(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.MigratedDB(t)
	if _, err := pool.Exec(ctx, `INSERT INTO users (first_name, last_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		VALUES (E'A\n\nB\t', E'\r', 'a@example.com', '+905000000001', 'Ankara', 'tr', true, true, now())`); err != nil {
		t.Fatal(err)
	}
	where, args, _ := audience.Build([]audience.Rule{{Field: "city", Op: "in", Value: json.RawMessage(`["Ankara"]`)}}, "sms", time.Now(), time.UTC)
	u := content.UserVars{FirstName: "A\n\nB\t", LastName: "\r", City: "Ankara"}

	for name, tpl := range map[string]string{
		"inside": strings.Repeat("x", 156) + " {{first_name}}", // "A B": 160, "A\n\nB\t": 162
		"only":   strings.Repeat("x", 159) + " {{last_name}}!", // 160 once "\r" is a space and tidied
		"over":   strings.Repeat("x", 157) + " {{first_name}}", // 161: genuinely over
	} {
		want := 0
		if utf8.RuneCountInString(content.Render(tpl, u)) > content.SMSLimit {
			want = 1
		}
		got, err := content.EstimateOverLimit(ctx, pool, content.Variants{"tr": {SMS: &content.Message{Body: tpl}}}, "tr",
			map[string]content.Audience{"sms": {Where: where, Args: args}})
		if err != nil || got.SMS != want {
			t.Errorf("%s: SQL counted %d, Go render says %d (%v)", name, got.SMS, want, err)
		}
	}
	if got := content.Render(strings.Repeat("x", 156)+" {{first_name}}", u); utf8.RuneCountInString(got) != 160 {
		t.Errorf("Go render: want 160 characters, got %d (%q)", utf8.RuneCountInString(got), got)
	}
}

// usersIn adds users with the given languages in Ankara, all allowing SMS.
func usersIn(t *testing.T, langs ...string) (*pgxpool.Pool, map[string]content.Audience) {
	t.Helper()
	pool := dbtest.MigratedDB(t)
	for i, l := range langs {
		if _, err := pool.Exec(context.Background(), `INSERT INTO users (first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
			VALUES ('A', $1, $2, 'Ankara', $3, true, false, now())`, fmt.Sprintf("u%d@example.com", i), fmt.Sprintf("+90500000%04d", i), l); err != nil {
			t.Fatal(err)
		}
	}
	where, args, _ := audience.Build([]audience.Rule{{Field: "city", Op: "in", Value: json.RawMessage(`["Ankara"]`)}}, "sms", time.Now(), time.UTC)
	return pool, map[string]content.Audience{"sms": {Where: where, Args: args}}
}

// Review focus 4: the default language's text is estimated over its own
// speakers plus everyone whose language has no content.
func TestEstimate_FallbackRecipientsAgainstDefault(t *testing.T) {
	t.Parallel()
	pool, audiences := usersIn(t, "tr", "tr", "en", "en", "en")
	long := &content.Message{Body: strings.Repeat("x", 161)}
	short := &content.Message{Body: "Kısa"}

	// Turkish only: the English speakers get the (too long) Turkish text.
	got, err := content.EstimateOverLimit(context.Background(), pool, content.Variants{"tr": {SMS: long}}, "tr", audiences)
	if err != nil || got.SMS != 5 || got.ByLanguage["tr"].SMS != 5 {
		t.Fatalf("Turkish only: want all 5 over, got %+v (%v)", got, err)
	}
	// English added and short: only the 2 Turkish speakers get the long text.
	got, err = content.EstimateOverLimit(context.Background(), pool, content.Variants{"tr": {SMS: long}, "en": {SMS: short}}, "tr", audiences)
	if err != nil || got.SMS != 2 || got.ByLanguage["tr"].SMS != 2 || got.ByLanguage["en"].SMS != 0 {
		t.Fatalf("both languages: want 2 Turkish over, got %+v (%v)", got, err)
	}
	// English the default: the long text now only reaches the Turkish speakers, as their own.
	got, err = content.EstimateOverLimit(context.Background(), pool, content.Variants{"tr": {SMS: short}, "en": {SMS: long}}, "en", audiences)
	if err != nil || got.SMS != 3 || got.ByLanguage["en"].SMS != 3 {
		t.Fatalf("English default: want 3 English over, got %+v (%v)", got, err)
	}
}

func TestEstimate_LegacyUndCoversEveryone(t *testing.T) {
	t.Parallel()
	pool, audiences := usersIn(t, "tr", "en", "en")
	got, err := content.EstimateOverLimit(context.Background(), pool,
		content.Variants{"und": {SMS: &content.Message{Body: strings.Repeat("x", 161)}}}, "und", audiences)
	if err != nil || got.SMS != 3 || got.ByLanguage["und"].SMS != 3 {
		t.Fatalf("want all 3 users over with the legacy text, got %+v (%v)", got, err)
	}
}
