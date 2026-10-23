package obs

import "testing"

// Temporary (T19 step 3): proves that CI fails when a test fails. Removed in
// the next commit.
func TestCIFailsOnAFailingTest(t *testing.T) {
	t.Fatal("deliberate failure: checking that CI reports a failing test")
}
