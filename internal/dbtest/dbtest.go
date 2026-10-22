// Package dbtest gives integration tests a real Postgres connection.
package dbtest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
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
	ctx := context.Background()
	admin := Pool(t)
	name := fmt.Sprintf("scc_test_%d", rand.Int64())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
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

// ProviderPassword is the provider role's password in every test. The role
// belongs to the whole Postgres server, so packages testing in parallel must
// all set the same one, or they lock each other out.
const ProviderPassword = "provider-test-password"

// ProviderPool logs in to admin's database as the mock provider's own role
// (D6), after setting its password.
func ProviderPool(t testing.TB, admin *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	if err := db.SetProviderPassword(ctx, admin, ProviderPassword); err != nil {
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
