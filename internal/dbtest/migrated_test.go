package dbtest

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// MigratedDB hands every test its own copy of the migrated schema, cloned
// from a template, so tests stay independent of each other.
func TestMigratedDB_IndependentMigratedCopies(t *testing.T) {
	ctx := context.Background()
	a, b := MigratedDB(t), MigratedDB(t)

	var applied int
	if err := a.QueryRow(ctx, `SELECT count(*) FROM goose_db_version WHERE version_id > 0 AND is_applied`).Scan(&applied); err != nil || applied < 5 {
		t.Fatalf("want the migrations applied, got %d (%v)", applied, err)
	}
	if _, err := a.Exec(ctx, `INSERT INTO categories (code, name) VALUES ('only-in-a', 'A')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := b.QueryRow(ctx, `SELECT count(*) FROM categories WHERE code = 'only-in-a'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a write in one test database must not show in another: %d (%v)", n, err)
	}
	var nameA, nameB string
	a.QueryRow(ctx, `SELECT current_database()`).Scan(&nameA)
	b.QueryRow(ctx, `SELECT current_database()`).Scan(&nameB)
	if nameA == nameB {
		t.Fatal("two calls must give two databases")
	}
}

// The provider role belongs to the whole server; test processes setting its
// password at the same moment must not fail ("tuple concurrently updated").
func TestProviderPool_ConcurrentLogins(t *testing.T) {
	admin := MigratedDB(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := &errRecorder{TB: t}
			func() {
				defer func() { recover() }()
				ProviderPool(rec, admin)
			}()
			if rec.err != "" {
				errs <- fmt.Errorf("%s", rec.err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// errRecorder turns Fatal into a recorded error, so goroutines can report.
type errRecorder struct {
	testing.TB
	err string
}

func (r *errRecorder) Fatal(args ...any) { r.err = fmt.Sprint(args...); panic(r) }
func (r *errRecorder) Fatalf(format string, args ...any) {
	r.err = fmt.Sprintf(format, args...)
	panic(r)
}
