// Package db owns the schema migrations and the deterministic seed (D28, D33).
package db

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies all pending migrations. Already applied ones are skipped.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate(pool, func(p *goose.Provider) error {
		_, err := p.Up(ctx)
		return err
	})
}

func migrate(pool *pgxpool.Pool, run func(*goose.Provider) error) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	dir, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	// An advisory lock makes concurrent migrate runs wait for each other.
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, dir, goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}
	return run(provider)
}

// MigrationsHash identifies the schema the embedded migrations build. Tests
// name their template database after it, so a changed migration gets a new one.
func MigrationsHash() string {
	h := sha256.New()
	fs.WalkDir(migrations, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := migrations.ReadFile(path)
			h.Write([]byte(path))
			h.Write(b)
		}
		return err
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// ProviderRole is the mock provider's database login (D6, D20).
const ProviderRole = "scc_provider"

// Querier runs statements: a pool, or one connection from it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// SetProviderPassword sets the provider role's password; migrate runs it on
// every start, so changing PROVIDER_DB_PASSWORD takes effect.
func SetProviderPassword(ctx context.Context, pool Querier, password string) error {
	var stmt string // ALTER ROLE takes no parameters, so quote the values with format()
	if err := pool.QueryRow(ctx, `SELECT format('ALTER ROLE %I PASSWORD %L', $1::text, $2::text)`,
		ProviderRole, password).Scan(&stmt); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, stmt)
	return err
}
