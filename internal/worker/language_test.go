package worker_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

// bilingual is the cart-abandoner campaign with Turkish (the default) and
// English content on SMS and email; user 2 speaks English. Not built yet.
func (f *fixture) bilingual(enSMS string) int64 {
	f.t.Helper()
	f.exec(`UPDATE users SET language = 'en' WHERE id = 2`)
	id := f.approved(cartAbandoners, "{sms,email}", now)
	f.addLanguage(id, "tr")
	f.addLanguage(id, "en")
	f.exec(`INSERT INTO campaign_content (campaign_id, channel, language, subject, body) VALUES
		($1, 'sms', 'tr', NULL, 'Merhaba {{first_name}}!'), ($1, 'email', 'tr', 'Sepetin', 'Merhaba {{first_name}}.'),
		($1, 'sms', 'en', NULL, $2), ($1, 'email', 'en', 'Your cart', 'Hello {{first_name}}.')`, id, enSMS)
	return id
}

func (f *fixture) languages(id int64) string {
	f.t.Helper()
	return f.str(`SELECT string_agg(user_id || ':' || channel || ':' || coalesce(language, 'NULL'), ' ' ORDER BY user_id, channel)
		FROM messages WHERE campaign_id = $1`, id)
}

func TestBuild_LanguageChosenPerRecipient(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := f.bilingual("Hello {{first_name}}!")
	f.build(now)
	// user 2 allowed SMS only
	if got := f.languages(id); got != "1:email:tr 1:sms:tr 2:sms:en" {
		t.Fatalf("want each recipient in their language, got %s", got)
	}

	g := newFixture(t)
	g.exec(`UPDATE users SET language = 'en' WHERE id = 2`)
	tr := g.sendingWithContent("Merhaba {{first_name}}!") // Turkish only
	if got := g.languages(tr); got != "1:email:tr 1:sms:tr 2:sms:tr" {
		t.Fatalf("want the English speaker given the Turkish default, got %s", got)
	}
}

// Review focus 5: a removed channel's kept text gets no messages.
func TestBuild_NoMessagesForExcludedChannel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := f.bilingual("Hello {{first_name}}!")
	f.exec(`UPDATE campaigns SET channels = '{sms}' WHERE id = $1`, id)
	f.build(now)
	if got := f.languages(id); got != "1:sms:tr 2:sms:en" {
		t.Fatalf("want SMS messages only, got %s", got)
	}
}

func (f *fixture) sentBodies(p *fakeProvider) string {
	var b []string
	for _, r := range p.requests {
		b = append(b, r.body["body"])
	}
	return strings.Join(b, " | ")
}

func TestProcess_SendsChosenLanguage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := &fakeProvider{}
	w := f.newWorker(p.serve(t))
	f.bilingual("Hello {{first_name}}!")
	f.build(now)

	for _, user := range []int64{1, 2} {
		if err := w.Process(context.Background(), f.claimOne("sms", user)); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.sentBodies(p); got != "Merhaba A! | Hello B!" {
		t.Fatalf("want Turkish then English, got %s", got)
	}
}

func TestProcess_RetryKeepsLanguage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := &fakeProvider{handle: status(503)}
	w := f.newWorker(p.serve(t))
	f.bilingual("Hello {{first_name}}!")
	f.build(now)
	job := f.claimOne("sms", 2)
	if err := w.Process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if m := f.message(job.MessageID); m.Status != "retry_scheduled" {
		t.Fatalf("setup: want a retry, got %+v", m)
	}

	p.handle = nil
	f.exec(`UPDATE messages SET next_attempt_at = $2 WHERE id = $1`, job.MessageID, now.Add(-time.Second))
	if err := w.Process(context.Background(), f.claimOne("sms", 2)); err != nil {
		t.Fatal(err)
	}
	if got := f.sentBodies(p); got != "Hello B! | Hello B!" {
		t.Fatalf("want English on both attempts, got %s", got)
	}
}

// The language is fixed when the audience is built: a later change of the
// user's language doesn't switch it mid-send.
func TestProcess_LanguageChangeAfterBuildKeepsChoice(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := &fakeProvider{}
	w := f.newWorker(p.serve(t))
	f.bilingual("Hello {{first_name}}!")
	f.build(now)
	f.exec(`UPDATE users SET language = 'tr' WHERE id = 2`)

	if err := w.Process(context.Background(), f.claimOne("sms", 2)); err != nil {
		t.Fatal(err)
	}
	if got := f.sentBodies(p); got != "Hello B!" {
		t.Fatalf("want the English chosen at build time, got %s", got)
	}
}

func TestProcess_OverLimitUsesChosenLanguage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := &fakeProvider{}
	w := f.newWorker(p.serve(t))
	f.bilingual("{{first_name}} " + strings.Repeat("x", 159)) // over 160 for user 2 only
	f.build(now)
	tr, en := f.claimOne("sms", 1), f.claimOne("sms", 2)

	if err := w.Process(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	if err := w.Process(context.Background(), en); err != nil {
		t.Fatal(err)
	}
	if m := f.message(tr.MessageID); m.Status != "sent" {
		t.Fatalf("the Turkish text fits: want sent, got %+v", m)
	}
	if m := f.message(en.MessageID); m.Status != "skipped" || m.SkipReason != "character_limit_exceeded" {
		t.Fatalf("the English text is too long: want skipped, got %+v", m)
	}
}

// Review focus 1: a message built before T20 (no language) is sent the
// campaign's legacy content, exactly as before.
func TestProcess_PreT20MessageSendsLegacyContent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := &fakeProvider{}
	w := f.newWorker(p.serve(t))
	id := f.approved(cartAbandoners, "{sms}", now)
	f.addLanguage(id, "und")
	f.exec(`INSERT INTO campaign_content (campaign_id, channel, language, body) VALUES ($1, 'sms', 'und', 'Eski {{first_name}}')`, id)
	f.exec(`UPDATE campaigns SET status = 'sending', audience_built_at = $2 WHERE id = $1`, id, now)
	f.exec(`INSERT INTO messages (campaign_id, user_id, channel, next_attempt_at) VALUES ($1, 1, 'sms', $2)`, id, now)

	if err := w.Process(context.Background(), f.claimOne("sms", 1)); err != nil {
		t.Fatal(err)
	}
	if got := f.sentBodies(p); got != "Eski A" {
		t.Fatalf("want the legacy text, got %q", got)
	}
}
