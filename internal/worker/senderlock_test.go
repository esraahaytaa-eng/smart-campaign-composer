package worker

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"smart-campaign-composer/internal/dbtest"
)

// Review: if the worker's host vanishes without closing its connection, the
// server must drop that session (and free the lock) within seconds, not after
// the OS default of about two hours. The lock connection asks for keepalives.
func TestSenderLock_ServerDropsADeadConnectionQuickly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := &Queue{Pool: dbtest.MigratedDB(t)}
	conn, err := pgx.ConnectConfig(ctx, q.senderLockConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	for setting, want := range map[string]string{"tcp_keepalives_idle": "10", "tcp_keepalives_interval": "5", "tcp_keepalives_count": "3"} {
		var got string
		if err := conn.QueryRow(ctx, "SHOW "+setting).Scan(&got); err != nil || got != want {
			t.Fatalf("%s = %q (%v), want %s", setting, got, err, want)
		}
	}
}
