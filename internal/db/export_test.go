package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

// MigrateTo applies migrations up to version, so a test can add data the way
// an older release stored it.
func MigrateTo(ctx context.Context, pool *pgxpool.Pool, version int64) error {
	return migrate(pool, func(p *goose.Provider) error {
		_, err := p.UpTo(ctx, version)
		return err
	})
}
