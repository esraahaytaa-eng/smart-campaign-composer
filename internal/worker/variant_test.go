package worker_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const inBursa = `[{"field":"city","op":"in","value":["Bursa"]}]`

// bursa adds tr Turkish and en English speakers in Bursa, ids from 100 (tr)
// and 500 (en); every third one allows SMS only.
func (f *fixture) bursa(tr, en int) {
	f.t.Helper()
	add := func(from, n int, lang string) {
		for i := 0; i < n; i++ {
			id := from + i
			f.exec(`INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
				VALUES ($1, 'U', $2, $3, 'Bursa', $4, true, $5, $6)`,
				id, fmt.Sprintf("u%d@example.com", id), fmt.Sprintf("+90500000%04d", id), lang, i%3 != 2, now.AddDate(-1, 0, 0))
		}
	}
	add(100, tr, "tr")
	add(500, en, "en")
}

// abCampaign is an approved A/B campaign on SMS and email with Turkish (the
// default) and English in both versions; langs limits its languages.
func (f *fixture) abCampaign(rules string, langs ...string) int64 {
	f.t.Helper()
	id := f.approved(rules, "{sms,email}", now)
	f.exec(`UPDATE campaigns SET ab_test = true, default_language = 'tr' WHERE id = $1`, id)
	for _, l := range langs {
		f.exec(`INSERT INTO campaign_languages (campaign_id, language, approved_at, approved_by) VALUES ($1, $2, now(), 1)`, id, l)
		for _, v := range []string{"a", "b"} {
			f.exec(`INSERT INTO campaign_content (campaign_id, channel, language, variant, subject, body) VALUES
				($1, 'sms', $2, $3, NULL, $2 || ' ' || $3 || ' {{first_name}}'), ($1, 'email', $2, $3, 'S', $2 || ' ' || $3)`, id, l, v)
		}
	}
	return id
}

// split counts unique recipients per language and version: "tr:a=2 tr:b=1".
func (f *fixture) split(id int64) string {
	f.t.Helper()
	return f.str(`SELECT coalesce(string_agg(k || '=' || n, ' ' ORDER BY k), '') FROM (
		SELECT language || ':' || coalesce(variant, 'NULL') AS k, count(DISTINCT user_id) AS n
		FROM messages WHERE campaign_id = $1 GROUP BY 1) s`, id)
}

// D68: each language's unique recipients are split 50/50, at most one apart.
func TestBuild_SplitIsBalancedPerLanguage(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		tr, en int
		want   string
	}{
		{1, 2, "en:a=1 en:b=1 tr:a=1"},
		{3, 41, "en:a=21 en:b=20 tr:a=2 tr:b=1"},
	} {
		f := newFixture(t)
		f.bursa(c.tr, c.en)
		id := f.abCampaign(inBursa, "tr", "en")
		f.build(now)
		if got := f.split(id); got != c.want {
			t.Errorf("%d tr + %d en: want %s, got %s", c.tr, c.en, c.want, got)
		}
	}
}

// One version per recipient on every channel, and a recipient with SMS only
// counts once, like everyone else.
func TestBuild_SameVersionOnEveryChannel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.bursa(12, 0) // 4 of them SMS only
	id := f.abCampaign(inBursa, "tr")
	f.build(now)
	if n := f.count(`SELECT count(*) FROM (SELECT user_id FROM messages WHERE campaign_id = $1
		GROUP BY user_id HAVING count(DISTINCT variant) <> 1) s`, id); n != 0 {
		t.Fatalf("want one version per recipient, %d have two", n)
	}
	if n := f.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND channel = 'email'`, id); n != 8 {
		t.Fatalf("setup: want 8 email recipients, got %d", n)
	}
	if got := f.split(id); got != "tr:a=6 tr:b=6" {
		t.Fatalf("want 12 unique recipients split 6/6, got %s", got)
	}
}

// The same campaign and recipients always give the same split.
func TestBuild_SplitIsDeterministic(t *testing.T) {
	t.Parallel()
	assign := func() string {
		f := newFixture(t)
		f.bursa(9, 4)
		id := f.abCampaign(inBursa, "tr", "en")
		f.build(now)
		return f.str(`SELECT string_agg(user_id || channel || variant, ' ' ORDER BY user_id, channel) FROM messages WHERE campaign_id = $1`, id)
	}
	if a, b := assign(), assign(); a != b {
		t.Fatalf("want the same split twice, got\n%s\n%s", a, b)
	}
}

func TestBuild_NoABLeavesVariantNull(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := f.bilingual("Hello {{first_name}}!")
	f.build(now)
	if n := f.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND variant IS NOT NULL`, id); n != 0 {
		t.Fatalf("a campaign without A/B has no versions, %d messages have one", n)
	}
}

// Messages without a version (built before A/B, or without it) send version A.
func TestDispatch_NullVariantSendsA(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := &fakeProvider{}
	w := f.newWorker(p.serve(t))
	f.bursa(1, 0)
	f.abCampaign(inBursa, "tr")
	f.build(now)
	f.exec(`UPDATE messages SET variant = NULL`)
	if err := w.Process(context.Background(), f.claimOne("sms", 100)); err != nil {
		t.Fatal(err)
	}
	if got := f.sentBodies(p); got != "tr a U" {
		t.Fatalf("want version A, got %s", got)
	}
}

// A retry sends the same version's approved text; approval fixed it (D68).
func TestDispatch_RetryKeepsVersion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := &fakeProvider{handle: status(503)}
	w := f.newWorker(p.serve(t))
	f.bursa(2, 0)
	id := f.abCampaign(inBursa, "tr")
	f.build(now)
	user := int64(f.count(`SELECT min(user_id) FROM messages WHERE campaign_id = $1 AND variant = 'b'`, id))
	job := f.claimOne("sms", user)
	if err := w.Process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	p.handle = nil
	f.exec(`UPDATE messages SET next_attempt_at = $2 WHERE id = $1`, job.MessageID, now.Add(-time.Second))
	if err := w.Process(context.Background(), f.claimOne("sms", user)); err != nil {
		t.Fatal(err)
	}
	if got := f.sentBodies(p); got != "tr b U | tr b U" {
		t.Fatalf("want version B on both attempts, got %s", got)
	}
}

// A recipient given the default language gets that language's version.
func TestDispatch_FallbackLanguageUsesItsVersion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := &fakeProvider{}
	w := f.newWorker(p.serve(t))
	f.bursa(0, 2)
	id := f.abCampaign(inBursa, "tr") // no English content
	f.build(now)
	for _, user := range []int64{500, 501} {
		if err := w.Process(context.Background(), f.claimOne("sms", user)); err != nil {
			t.Fatal(err)
		}
	}
	if got, split := f.sentBodies(p), f.split(id); split != "tr:a=1 tr:b=1" || (got != "tr a U | tr b U" && got != "tr b U | tr a U") {
		t.Fatalf("want the Turkish A and B, got %s (%s)", got, split)
	}
}
