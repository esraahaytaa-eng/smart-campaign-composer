package worker_test

import (
	"context"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the time is up.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(d); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting: %s", what)
		}
	}
}

// lockHolder is the backend holding the sending worker's advisory lock, or 0.
func (f *fixture) lockHolder() int {
	f.t.Helper()
	return f.count(`SELECT coalesce(max(pid), 0) FROM pg_locks WHERE locktype = 'advisory' AND granted AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`)
}

// later is a campaign with content, scheduled an hour ahead so a running
// worker can't build it before the test makes it due.
func (f *fixture) later() int64 {
	f.t.Helper()
	id := f.approved(cartAbandoners, "{sms,email}", time.Now().Add(time.Hour))
	f.addLanguage(id, "tr")
	f.exec(`INSERT INTO campaign_content (campaign_id, channel, language, subject, body) VALUES
		($1, 'sms', 'tr', NULL, 'Tekrar merhaba {{first_name}}'), ($1, 'email', 'tr', 'Merhaba', 'Tekrar merhaba')`, id)
	return id
}

func (f *fixture) allSent(id int64) func() bool {
	return func() bool {
		return f.count(`SELECT count(*) FROM messages WHERE campaign_id = $1 AND status <> 'sent'`, id) == 0 && f.count(`SELECT count(*) FROM messages WHERE campaign_id = $1`, id) > 0
	}
}

// Audit F5: the 100/s limit lives in one process, so a second worker waits
// instead of sending, and takes over when the first stops.
func TestRun_SecondWorkerWaitsThenTakesOver(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	first := f.sendingWithContent("Merhaba {{first_name}}")
	second := f.later()
	pa, pb := &fakeProvider{}, &fakeProvider{}
	a, b := f.newWorker(pa.serve(t)), f.newWorker(pb.serve(t))
	a.Now, a.Poll, b.Now, b.Poll = time.Now, 20*time.Millisecond, time.Now, 20*time.Millisecond

	actx, stopA := context.WithCancel(context.Background())
	aDone := make(chan error)
	go func() { aDone <- a.Run(actx) }()
	waitFor(t, 5*time.Second, "the first worker holds the lock", func() bool { return f.lockHolder() != 0 })
	bctx, stopB := context.WithCancel(context.Background())
	defer stopB()
	bDone := make(chan error)
	go func() { bDone <- b.Run(bctx) }()

	waitFor(t, 10*time.Second, "the first campaign is sent", f.allSent(first))
	if len(pb.requests) != 0 {
		t.Fatalf("the second worker sent %d requests while the first was running", len(pb.requests))
	}

	stopA()
	if err := <-aDone; err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE campaigns SET send_at = now() WHERE id = $1`, second)
	waitFor(t, 10*time.Second, "the second worker takes over", f.allSent(second))
	if len(pb.requests) == 0 {
		t.Fatal("the second campaign must be sent by the second worker")
	}
	stopB()
	<-bDone
}

// Audit F5: if the lock's connection is lost the lock may be gone, so the
// worker stops sending and takes the lock again before it continues.
func TestRun_LostLockIsTakenAgain(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := &fakeProvider{}
	w := f.newWorker(p.serve(t))
	w.Now, w.Poll = time.Now, 20*time.Millisecond
	id := f.later()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- w.Run(ctx) }()
	waitFor(t, 5*time.Second, "the worker holds the lock", func() bool { return f.lockHolder() != 0 })

	old := f.lockHolder()
	f.exec(`SELECT pg_terminate_backend($1)`, old)
	waitFor(t, 10*time.Second, "the lock is taken again", func() bool { h := f.lockHolder(); return h != 0 && h != old })
	f.exec(`UPDATE campaigns SET send_at = now() WHERE id = $1`, id)
	waitFor(t, 10*time.Second, "sending continues", f.allSent(id))
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
