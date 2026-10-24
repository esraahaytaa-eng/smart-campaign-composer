package db_test

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"

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
