package db_test

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/content"
	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/dbtest"
)

// Content written before T20 has no language: it becomes one approved
// version labelled "und", the campaign's default, so it is sent as before (D64).
func TestMigrate_LegacyContentBecomesApprovedUnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.FreshDB(t)
	if err := db.MigrateTo(ctx, pool, 5); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO marketers (id, email, name, password_hash) VALUES (1, 'm@example.com', 'M', 'x')`,
		`INSERT INTO campaigns (id, request_text, channels, rules, status, time_zone, created_by, approved_by, approved_at)
		 VALUES (1, 'draft', '{sms,email}', '[]', 'draft', 'UTC', 1, NULL, NULL),
		        (2, 'approved', '{sms}', '[]', 'approved', 'UTC', 1, 1, '2026-10-01T10:00:00Z'),
		        (3, 'empty', '{sms}', '[]', 'draft', 'UTC', 1, NULL, NULL)`,
		`INSERT INTO campaign_content (campaign_id, channel, subject, body)
		 VALUES (1, 'sms', NULL, 'Merhaba'), (1, 'email', 'Konu', 'Gövde'), (2, 'sms', NULL, 'Hi')`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%v: %s", err, sql)
		}
	}

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	if n := count(t, pool, `SELECT count(*) FROM campaign_content WHERE language = 'und'`); n != 3 {
		t.Fatalf("want the 3 content rows labelled und, got %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM campaign_languages
		WHERE language = 'und' AND approved_at IS NOT NULL AND approved_by = 1 AND NOT outdated`); n != 2 {
		t.Fatalf("want an approved und version for both campaigns with content, got %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM campaign_languages WHERE campaign_id = 2 AND approved_at = '2026-10-01T10:00:00Z'`); n != 1 {
		t.Fatal("an approved campaign's version keeps its approval time")
	}
	if n := count(t, pool, `SELECT count(*) FROM campaigns WHERE default_language = 'und'`); n != 2 {
		t.Fatalf("want und as default for both campaigns with content, got %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM campaigns WHERE id = 3 AND default_language IS NULL`); n != 1 {
		t.Fatal("a campaign without content has no default language")
	}
}

// Rolling languages back keeps each campaign's default-language text as its
// only text, so a rollback never empties a campaign that had content (D64).
func TestMigrate_DownKeepsDefaultLanguageContent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.FreshDB(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO marketers (id, email, name, password_hash) VALUES (1, 'm@example.com', 'M', 'x')`,
		`INSERT INTO campaigns (id, request_text, channels, rules, status, time_zone, created_by, default_language)
		 VALUES (1, 'bilingual', '{sms}', '[]', 'draft', 'UTC', 1, 'tr'), (2, 'legacy', '{sms}', '[]', 'draft', 'UTC', 1, 'und')`,
		`INSERT INTO campaign_languages (campaign_id, language) VALUES (1, 'tr'), (1, 'en'), (2, 'und')`,
		`INSERT INTO campaign_content (campaign_id, channel, language, body)
		 VALUES (1, 'sms', 'tr', 'Merhaba'), (1, 'sms', 'en', 'Hello'), (2, 'sms', 'und', 'Eski')`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%v: %s", err, sql)
		}
	}

	if err := db.MigrateDownTo(ctx, pool, 5); err != nil {
		t.Fatal(err)
	}

	var bodies []string
	rows, err := pool.Query(ctx, `SELECT body FROM campaign_content ORDER BY campaign_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, b)
	}
	if strings.Join(bodies, ",") != "Merhaba,Eski" {
		t.Fatalf("want each campaign's default-language text kept, got %q", bodies)
	}
}

// The language checks in the database allow exactly the catalog languages
// plus the legacy label, so a language added to one place but not the other
// fails here (D64).
func TestLanguages_ConsistentEverywhere(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	want := []string{content.LegacyLang}
	for _, l := range content.Languages {
		want = append(want, l.Code)
	}
	slices.Sort(want)
	for _, column := range []string{"campaign_languages.language", "campaigns.default_language", "messages.language"} {
		table, col, _ := strings.Cut(column, ".")
		def := str(t, pool, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
			WHERE conrelid = '`+table+`'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%`+col+`%'`)
		var got []string
		for _, m := range regexp.MustCompile(`'([a-z]+)'`).FindAllStringSubmatch(def, -1) {
			got = append(got, m[1])
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s allows %v, the catalog has %v", column, got, want)
		}
	}
}

// D68: content and messages from before A/B testing are version A, and a
// campaign without A/B behaves as before.
func TestMigrate_ExistingContentBecomesVersionA(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.FreshDB(t)
	if err := db.MigrateTo(ctx, pool, 7); err != nil {
		t.Fatal(err)
	}
	seedBilingual(t, pool)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM campaign_content WHERE variant = 'a'`); n != 2 {
		t.Fatalf("want both content rows as version A, got %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM campaigns WHERE NOT ab_test`); n != 1 {
		t.Fatal("an existing campaign has no A/B test")
	}
}

// Rolling A/B back keeps each campaign's version A text (D68).
func TestMigrate_DownKeepsVersionA(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.FreshDB(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	seedBilingual(t, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO campaign_content (campaign_id, channel, language, variant, body)
		VALUES (1, 'sms', 'tr', 'b', 'Merhaba B')`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateDownTo(ctx, pool, 7); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM campaign_content WHERE body IN ('Merhaba', 'Hello')`); n != 2 {
		t.Fatalf("want the version A text kept, got %d rows", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM campaign_content`); n != 2 {
		t.Fatal("version B is removed")
	}
}

func TestVariants_CheckedByDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.FreshDB(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	seedBilingual(t, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		VALUES (1, 'A', 'a@example.com', '+905000000001', 'Ankara', 'tr', true, true, now())`); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO campaign_content (campaign_id, channel, language, variant, body) VALUES (1, 'sms', 'tr', 'c', 'x')`,
		`INSERT INTO messages (campaign_id, user_id, channel, variant) VALUES (1, 1, 'sms', 'c')`,
	} {
		if _, err := pool.Exec(ctx, sql); err == nil || !strings.Contains(err.Error(), "check constraint") {
			t.Fatalf("want a check violation, got %v: %s", err, sql)
		}
	}
}

// seedBilingual adds one draft with Turkish and English SMS text.
func seedBilingual(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, sql := range []string{
		`INSERT INTO marketers (id, email, name, password_hash) VALUES (1, 'm@example.com', 'M', 'x')`,
		`INSERT INTO campaigns (id, request_text, channels, rules, status, time_zone, created_by, default_language)
		 VALUES (1, 'bilingual', '{sms}', '[]', 'draft', 'UTC', 1, 'tr')`,
		`INSERT INTO campaign_languages (campaign_id, language) VALUES (1, 'tr'), (1, 'en')`,
		`INSERT INTO campaign_content (campaign_id, channel, language, body) VALUES (1, 'sms', 'tr', 'Merhaba'), (1, 'sms', 'en', 'Hello')`,
	} {
		if _, err := pool.Exec(context.Background(), sql); err != nil {
			t.Fatalf("%v: %s", err, sql)
		}
	}
}

// Audit F3: messages sent before the column existed get their latest attempt's time.
func TestMigrate_LastAttemptBackfilled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.FreshDB(t)
	if err := db.MigrateTo(ctx, pool, 9); err != nil {
		t.Fatal(err)
	}
	seedBilingual(t, pool)
	for _, sql := range []string{
		`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		 VALUES (1, 'A', 'a@example.com', '+905000000001', 'Ankara', 'tr', true, true, now()),
		        (2, 'B', 'b@example.com', '+905000000002', 'Ankara', 'tr', true, true, now())`,
		`INSERT INTO messages (id, campaign_id, user_id, channel, status) VALUES (1, 1, 1, 'sms', 'sent'), (2, 1, 2, 'sms', 'pending')`,
		`INSERT INTO delivery_attempts (message_id, attempt_no, started_at, outcome) VALUES
		 (1, 1, '2026-10-01 10:00:00Z', 'rate_limited'), (1, 2, '2026-10-01 10:00:05Z', 'accepted')`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%v: %s", err, sql)
		}
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM messages WHERE id = 1 AND last_attempt_at = '2026-10-01 10:00:05Z'`); n != 1 {
		t.Fatal("want the latest attempt's start time")
	}
	if n := count(t, pool, `SELECT count(*) FROM messages WHERE id = 2 AND last_attempt_at IS NULL`); n != 1 {
		t.Fatal("a message never attempted has none")
	}
	if err := db.MigrateDownTo(ctx, pool, 9); err != nil {
		t.Fatal(err)
	}
}
