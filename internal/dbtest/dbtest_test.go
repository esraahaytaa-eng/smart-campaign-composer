package dbtest

import "testing"

// fatalRecorder records Fatal calls instead of failing the real test.
type fatalRecorder struct {
	testing.TB
	failed bool
}

func (r *fatalRecorder) Fatal(args ...any)                 { r.failed = true; panic(r) }
func (r *fatalRecorder) Fatalf(format string, args ...any) { r.failed = true; panic(r) }
func (r *fatalRecorder) Skip(args ...any)                  { panic("integration tests must fail, not skip") }
func (r *fatalRecorder) Skipf(format string, args ...any) {
	panic("integration tests must fail, not skip")
}
func (r *fatalRecorder) SkipNow() { panic("integration tests must fail, not skip") }

func TestDBRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	rec := &fatalRecorder{TB: t}

	func() {
		defer func() {
			if p := recover(); p != rec {
				t.Fatalf("want Fatal, got %v", p)
			}
		}()
		Pool(rec)
	}()

	if !rec.failed {
		t.Fatal("Pool must fail the test when DATABASE_URL is unset")
	}
}
