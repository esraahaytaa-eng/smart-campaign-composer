// Package dbtest gives integration tests a real Postgres connection.
package dbtest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/db"
)

// Pool connects to DATABASE_URL. It fails the test, never skips, when no
// database is available (D27), so a missing database can't hide tests.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL is not set; integration tests need Postgres (use make test)")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect to database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping database: %v", err)
	}
	return pool
}

// FreshDB creates an empty database for one test and drops it afterwards.
func FreshDB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	return create(t, Pool(t), "")
}

// MigratedDB creates a database with the migrated schema for one test and
// drops it afterwards. It is a copy of a template migrated once, which is
// much faster than migrating every test's database.
func MigratedDB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	admin := Pool(t)
	template.once.Do(func() { template.name, template.err = ensureTemplate(admin) })
	if template.err != nil {
		t.Fatalf("template database: %v", template.err)
	}
	return create(t, admin, "TEMPLATE "+template.name)
}

var template struct {
	once sync.Once
	name string
	err  error
}

// ensureTemplate builds the template for the current migrations once per
// Postgres server: test processes of other packages wait on the lock, then
// reuse it. It is built under another name and renamed when done, so a
// half-built template is never copied.
// shortcut: templates of older migrations stay on a long-lived test server
// (make test-local); drop scc_tpl_* databases there to clean up.
func ensureTemplate(admin *pgxpool.Pool) (string, error) {
	ctx := context.Background()
	name := "scc_tpl_" + db.MigrationsHash()
	conn, err := admin.Acquire(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(73100)`); err != nil {
		return "", err
	}
	defer conn.Exec(ctx, `SELECT pg_advisory_unlock(73100)`)

	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil || exists {
		return name, err
	}
	building := name + "_build"
	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+building+" WITH (FORCE)"); err != nil { // left by a crashed run
		return "", err
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+building); err != nil {
		return "", err
	}
	pool, err := connect(ctx, building)
	if err != nil {
		return "", err
	}
	err = db.Migrate(ctx, pool)
	pool.Close()
	if err != nil {
		return "", err
	}
	_, err = conn.Exec(ctx, "ALTER DATABASE "+building+" RENAME TO "+name)
	return name, err
}

// create makes a database for one test (from is "" or "TEMPLATE name") and
// drops it when the test ends.
func create(t testing.TB, admin *pgxpool.Pool, from string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("scc_test_%d", rand.Int64())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" "+from); err != nil {
		t.Fatalf("create database: %v", err)
	}
	pool, err := connect(ctx, name)
	if err != nil {
		t.Fatalf("connect to %s: %v", name, err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
	})
	return pool
}

func connect(ctx context.Context, name string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Database = name
	return pgxpool.NewWithConfig(ctx, cfg)
}

// ProviderPassword is the provider role's password in every test. The role
// belongs to the whole Postgres server, so packages testing in parallel must
// all set the same one, or they lock each other out.
const ProviderPassword = "provider-test-password"

// ProviderPool logs in to admin's database as the mock provider's own role
// (D6), after setting its password.
func ProviderPool(t testing.TB, admin *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	// The role belongs to the whole server: test processes setting its password
	// at once fail with "tuple concurrently updated", so they take turns. The
	// lock is taken in the shared database, since advisory locks are per database.
	conn, err := Pool(t).Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(73101)`); err != nil {
		t.Fatal(err)
	}
	err = db.SetProviderPassword(ctx, conn, ProviderPassword)
	conn.Exec(ctx, `SELECT pg_advisory_unlock(73101)`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := admin.Config().Copy()
	cfg.ConnConfig.User, cfg.ConnConfig.Password = db.ProviderRole, ProviderPassword
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
