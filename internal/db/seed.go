package db

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// DefaultRNGSeed is the fixed seed used by docker compose (D28).
const DefaultRNGSeed = 20261021

// The one demo marketer login (D10); its password comes from DEMO_MARKETER_PASSWORD.
const demoMarketerEmail = "marketer@marketer.com"

// Seed inserts the demo marketer and 3,000 fictional users with their
// shopping histories, dated relative to now. If the database is already
// seeded it does nothing: existing rows are never updated or deleted.
// Everything is inserted in one transaction, so a crash leaves no partial seed.
func Seed(ctx context.Context, pool *pgxpool.Pool, rngSeed int64, now time.Time, marketerPassword string) error {
	if marketerPassword == "" {
		return errors.New("seed: marketer password is required")
	}
	ref := now.UTC().Truncate(time.Second)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Claiming the single seed_meta row first makes a second (or concurrent) run a no-op.
	tag, err := tx.Exec(ctx, `INSERT INTO seed_meta (id, rng_seed, reference_time) VALUES (1, $1, $2)
		ON CONFLICT (id) DO NOTHING`, rngSeed, ref)
	if err != nil {
		return fmt.Errorf("seed: claim seed_meta: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}

	if err := insertMarketer(ctx, tx, marketerPassword); err != nil {
		return err
	}
	if err := insertDataset(ctx, tx, generate(rngSeed), ref); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertMarketer(ctx context.Context, tx pgx.Tx, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("seed: hash password: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO marketers (email, name, password_hash) VALUES ($1, 'Demo Marketer', $2)`,
		demoMarketerEmail, string(hash)); err != nil {
		return fmt.Errorf("seed: insert marketer: %w", err)
	}
	return nil
}

func insertDataset(ctx context.Context, tx pgx.Tx, data dataset, ref time.Time) error {
	at := func(ago int64) time.Time { return ref.Add(-time.Duration(ago) * time.Second) }

	var users, orders, orderItems, carts [][]any
	for _, u := range data.users {
		var last *string
		if u.lastName != "" {
			last = &u.lastName
		}
		users = append(users, []any{u.id, u.firstName, last, u.email, u.phone, u.city, u.language,
			u.smsOptIn, u.emailOptIn, at(u.signedUpAgo)})
	}
	for _, o := range data.orders {
		var total int64
		for _, it := range o.items {
			total += int64(it.quantity) * it.priceCents
			orderItems = append(orderItems, []any{o.id, it.category, it.quantity, money(it.priceCents)})
		}
		orders = append(orders, []any{o.id, o.userID, at(o.ago), money(total)})
	}
	for _, c := range data.carts {
		carts = append(carts, []any{c.userID, c.item.category, c.item.quantity, money(c.item.priceCents), at(c.ago)})
	}

	copies := []struct {
		table   string
		columns []string
		rows    [][]any
	}{
		{"users", []string{"id", "first_name", "last_name", "email", "phone", "city", "language",
			"sms_opt_in", "email_opt_in", "signed_up_at"}, users},
		{"orders", []string{"id", "user_id", "ordered_at", "total_amount"}, orders},
		{"order_items", []string{"order_id", "category", "quantity", "unit_price"}, orderItems},
		{"cart_items", []string{"user_id", "category", "quantity", "unit_price", "added_at"}, carts},
	}
	for _, c := range copies {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{c.table}, c.columns, pgx.CopyFromRows(c.rows)); err != nil {
			return fmt.Errorf("seed: copy %s: %w", c.table, err)
		}
	}

	// Explicit IDs were inserted, so move the identity sequences past them.
	for _, table := range []string{"users", "orders"} {
		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`SELECT setval(pg_get_serial_sequence('%[1]s', 'id'), (SELECT max(id) FROM %[1]s))`, table)); err != nil {
			return fmt.Errorf("seed: reset %s sequence: %w", table, err)
		}
	}
	return nil
}

func money(cents int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(cents), Exp: -2, Valid: true}
}
