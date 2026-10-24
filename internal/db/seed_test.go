package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/dbtest"
)

const (
	rngSeed  = 20261021
	password = "test-password"
)

var refTime = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.MigratedDB(t)
	return pool
}

func seeded(t *testing.T, now time.Time) *pgxpool.Pool {
	t.Helper()
	pool := migrated(t)
	if err := db.Seed(context.Background(), pool, rngSeed, now, password); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return pool
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
	return n
}

func str(t *testing.T, pool *pgxpool.Pool, sql string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), sql).Scan(&s); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
	return s
}

// seedFingerprint hashes the seeded data without absolute timestamps or
// generated IDs: every date is an offset from reference_time.
func seedFingerprint(t *testing.T, pool *pgxpool.Pool) string {
	return str(t, pool, `
		WITH m AS (SELECT reference_time AS r, rng_seed FROM seed_meta),
		rows AS (
			SELECT concat_ws('|', 'meta', rng_seed) AS row FROM m
			UNION ALL
			SELECT concat_ws('|', 'u', u.email, u.first_name, coalesce(u.last_name, '<null>'), u.phone,
			                 u.city, u.language, u.sms_opt_in, u.email_opt_in, m.r - u.signed_up_at)
			FROM users u, m
			UNION ALL
			SELECT concat_ws('|', 'o', u.email, m.r - o.ordered_at, o.total_amount)
			FROM orders o JOIN users u ON u.id = o.user_id, m
			UNION ALL
			SELECT concat_ws('|', 'oi', u.email, m.r - o.ordered_at, i.category, i.quantity, i.unit_price)
			FROM order_items i JOIN orders o ON o.id = i.order_id JOIN users u ON u.id = o.user_id, m
			UNION ALL
			SELECT concat_ws('|', 'c', u.email, m.r - c.added_at, c.category, c.quantity, c.unit_price)
			FROM cart_items c JOIN users u ON u.id = c.user_id, m
		)
		SELECT md5(string_agg(row, E'\n' ORDER BY row)) FROM rows`)
}

// rawDump hashes every seeded row as stored, IDs and absolute timestamps included.
func rawDump(t *testing.T, pool *pgxpool.Pool) string {
	return str(t, pool, `
		SELECT md5(concat_ws(E'\n',
			(SELECT string_agg(x::text, E'\n' ORDER BY x.id) FROM users x),
			(SELECT string_agg(x::text, E'\n' ORDER BY x.id) FROM orders x),
			(SELECT string_agg(x::text, E'\n' ORDER BY x.id) FROM order_items x),
			(SELECT string_agg(x::text, E'\n' ORDER BY x.id) FROM cart_items x),
			(SELECT string_agg(x::text, E'\n' ORDER BY x.id) FROM marketers x),
			(SELECT string_agg(x::text, E'\n') FROM seed_meta x)))`)
}

func TestMigrate_RunsTwice(t *testing.T) {
	t.Parallel() // own database; nothing shared with other tests
	pool := migrated(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

// Smoke test: goose's own retries already hide most races, so this passes
// without the session locker too; it guards that the locker never deadlocks.
func TestMigrate_ConcurrentRunsBothSucceed(t *testing.T) {
	t.Parallel() // own database; nothing shared with other tests
	pool := dbtest.FreshDB(t)

	errs := make(chan error, 4)
	for range 4 {
		go func() { errs <- db.Migrate(context.Background(), pool) }()
	}
	for range 4 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
}

func TestSeed_Deterministic(t *testing.T) {
	t.Parallel() // own database; nothing shared with other tests
	a := seeded(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b := seeded(t, refTime)

	if fa, fb := seedFingerprint(t, a), seedFingerprint(t, b); fa != fb {
		t.Fatalf("fingerprints differ: %s vs %s", fa, fb)
	}
}

func TestSeed_RerunChangesNothing(t *testing.T) {
	t.Parallel() // own database; nothing shared with other tests
	pool := seeded(t, refTime)
	if n := count(t, pool, "SELECT count(*) FROM users"); n != 3000 {
		t.Fatalf("want 3000 users, got %d", n)
	}
	before := rawDump(t, pool)

	for _, now := range []time.Time{refTime, refTime.Add(7 * 24 * time.Hour)} {
		if err := db.Seed(context.Background(), pool, rngSeed, now, password); err != nil {
			t.Fatalf("rerun at %v: %v", now, err)
		}
		if after := rawDump(t, pool); after != before {
			t.Fatalf("rerun at %v changed stored rows", now)
		}
	}
}

// TestSeed_Data checks the seeded data. The checks only read, so they share
// one seed (each took 7–9 s with its own).
func TestSeed_Data(t *testing.T) {
	t.Parallel() // own database; nothing shared with other tests
	pool := seeded(t, refTime)
	for name, check := range map[string]func(*testing.T, *pgxpool.Pool){
		"OneDemoMarketer":    seedOneDemoMarketer,
		"NoFutureDates":      seedNoFutureDates,
		"ProfileWindows":     seedProfileWindows,
		"CoversCatalog":      seedCoversCatalog,
		"CaseExampleMatches": seedCaseExampleMatches,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			check(t, pool)
		})
	}
}

func seedOneDemoMarketer(t *testing.T, pool *pgxpool.Pool) {
	rows, err := pool.Query(context.Background(), `SELECT email, password_hash FROM marketers`)
	if err != nil {
		t.Fatal(err)
	}
	type marketer struct{ Email, Hash string }
	got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[marketer])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Email != "marketer@marketer.com" {
		t.Fatalf("want only marketer@marketer.com, got %v", got)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(got[0].Hash), []byte(password)); err != nil {
		t.Fatalf("stored hash does not match the seed password: %v", err)
	}
}

func TestSeed_EmptyPasswordFailsAndLeavesNothing(t *testing.T) {
	t.Parallel() // own database; nothing shared with other tests
	pool := migrated(t)

	if err := db.Seed(context.Background(), pool, rngSeed, refTime, ""); err == nil {
		t.Fatal("want error for empty marketer password")
	}
	if n := count(t, pool, "SELECT (SELECT count(*) FROM seed_meta) + (SELECT count(*) FROM users)"); n != 0 {
		t.Fatalf("failed seed left %d rows", n)
	}
}

func seedNoFutureDates(t *testing.T, pool *pgxpool.Pool) {
	checks := map[string]string{
		"reference time is now":     `SELECT count(*) FROM seed_meta WHERE reference_time <> '2026-10-21 12:00:00Z'`,
		"signup in future":          `SELECT count(*) FROM users, seed_meta m WHERE signed_up_at > m.reference_time`,
		"order in future":           `SELECT count(*) FROM orders, seed_meta m WHERE ordered_at > m.reference_time`,
		"cart in future":            `SELECT count(*) FROM cart_items, seed_meta m WHERE added_at > m.reference_time`,
		"order before signup":       `SELECT count(*) FROM orders o JOIN users u ON u.id = o.user_id WHERE o.ordered_at < u.signed_up_at`,
		"cart before signup":        `SELECT count(*) FROM cart_items c JOIN users u ON u.id = c.user_id WHERE c.added_at < u.signed_up_at`,
		"order total != item sum":   `SELECT count(*) FROM orders o WHERE total_amount <> (SELECT sum(quantity * unit_price) FROM order_items i WHERE i.order_id = o.id)`,
		"order without items":       `SELECT count(*) FROM orders o WHERE NOT EXISTS (SELECT 1 FROM order_items i WHERE i.order_id = o.id)`,
		"marketer password in text": `SELECT count(*) FROM marketers WHERE password_hash = '` + password + `' OR password_hash NOT LIKE '$2%'`,
	}
	for name, sql := range checks {
		if n := count(t, pool, sql); n != 0 {
			t.Errorf("%s: %d rows", name, n)
		}
	}
}

const (
	cartWithin   = `EXISTS (SELECT 1 FROM cart_items c WHERE c.user_id = u.id AND c.added_at >= m.reference_time - make_interval(days => $1))`
	orderWithin  = `EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id AND o.ordered_at >= m.reference_time - make_interval(days => $1))`
	anyOrder     = `EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id)`
	usersFromRef = `SELECT count(*) FROM users u, seed_meta m WHERE `
)

func seedProfileWindows(t *testing.T, pool *pgxpool.Pool) {
	for _, days := range []int{7, 30, 60, 90} {
		rules := map[string]string{
			"cart within, no order within": cartWithin + " AND NOT " + orderWithin,
			"ordered within":               orderWithin,
			"lapsed (orders, none within)": anyOrder + " AND NOT " + orderWithin,
			"signed up within":             `u.signed_up_at >= m.reference_time - make_interval(days => $1)`,
		}
		for name, where := range rules {
			if n := count(t, pool, usersFromRef+where, days); n == 0 {
				t.Errorf("%d days, %s: no users", days, name)
			}
		}
	}

	for _, cat := range []string{"electronics", "fashion", "home", "beauty", "sports", "books"} {
		ordered := `SELECT count(DISTINCT o.user_id) FROM orders o JOIN order_items i ON i.order_id = o.id, seed_meta m
		            WHERE i.category = $1 AND o.ordered_at >= m.reference_time - interval '90 days'`
		carted := `SELECT count(DISTINCT c.user_id) FROM cart_items c, seed_meta m
		           WHERE c.category = $1 AND c.added_at >= m.reference_time - interval '30 days'`
		if count(t, pool, ordered, cat) == 0 || count(t, pool, carted, cat) == 0 {
			t.Errorf("category %s: no ordered (90d) or carted (30d) users", cat)
		}
	}
}

func seedCoversCatalog(t *testing.T, pool *pgxpool.Pool) {
	rules := map[string]string{
		"İstanbul":                 `SELECT count(*) FROM users WHERE city = 'İstanbul'`,
		"at least 10 cities":       `SELECT (count(DISTINCT city) >= 10)::int FROM users`,
		"sms only":                 `SELECT count(*) FROM users WHERE sms_opt_in AND NOT email_opt_in`,
		"email only":               `SELECT count(*) FROM users WHERE email_opt_in AND NOT sms_opt_in`,
		"no consent":               `SELECT count(*) FROM users WHERE NOT sms_opt_in AND NOT email_opt_in`,
		"both channels":            `SELECT count(*) FROM users WHERE sms_opt_in AND email_opt_in`,
		"zero orders":              `SELECT count(*) FROM users u WHERE NOT EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id)`,
		"lapsed buyer (>90d)":      `SELECT count(*) FROM users u, seed_meta m WHERE ` + anyOrder + ` AND NOT EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id AND o.ordered_at >= m.reference_time - interval '90 days')`,
		"high spender (≥10,000)":   `SELECT count(*) FROM (SELECT user_id FROM orders GROUP BY user_id HAVING sum(total_amount) >= 10000) s`,
		"frequent buyer (≥8)":      `SELECT count(*) FROM (SELECT user_id FROM orders GROUP BY user_id HAVING count(*) >= 8) s`,
		"high-value cart (≥5,000)": `SELECT count(*) FROM (SELECT user_id FROM cart_items GROUP BY user_id HAVING sum(quantity * unit_price) >= 5000) s`,
		"NULL last_name":           `SELECT count(*) FROM users WHERE last_name IS NULL`,
		"English speakers":         `SELECT count(*) FROM users WHERE language = 'en'`,
		"cart ≤30d, no order ≤30d": usersFromRef + strings.ReplaceAll(cartWithin+" AND NOT "+orderWithin, "$1", "30"),
	}
	for name, sql := range rules {
		if n := count(t, pool, sql); n <= 0 {
			t.Errorf("%s: no users", name)
		}
	}
}

func seedCaseExampleMatches(t *testing.T, pool *pgxpool.Pool) {
	// CASE §2: users in Istanbul with a cart in the last 30 days and no order since.
	n := count(t, pool, usersFromRef+`u.city = 'İstanbul' AND `+cartWithin+` AND NOT `+orderWithin, 30)
	if n == 0 {
		t.Fatal("case example audience is empty")
	}
	t.Logf("case example audience: %d users", n)
}

func TestMessagesUniqueConstraint(t *testing.T) {
	t.Parallel()        // own database; nothing shared with other tests
	pool := migrated(t) // one marketer and one customer are enough
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO marketers (email, name, password_hash) VALUES ('m@example.com', 'M', 'x');
		INSERT INTO users (id, first_name, email, phone, city, language, sms_opt_in, email_opt_in, signed_up_at)
		VALUES (1, 'A', 'a@example.com', '+905000000001', 'Ankara', 'tr', true, true, now())`); err != nil {
		t.Fatal(err)
	}

	var campaignID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (request_text, channels, time_zone, created_by)
		VALUES ('test', '{sms}', 'Europe/Istanbul', (SELECT min(id) FROM marketers))
		RETURNING id`).Scan(&campaignID)
	if err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO messages (campaign_id, user_id, channel) VALUES ($1, 1, 'sms')`
	if _, err := pool.Exec(ctx, insert, campaignID); err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(ctx, insert, campaignID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("want unique violation, got %v", err)
	}
}

func TestCharLengthCountsCodePoints(t *testing.T) {
	t.Parallel() // own database; nothing shared with other tests
	pool := migrated(t)

	if n := count(t, pool, `SELECT char_length('şğİ')`); n != 3 {
		t.Fatalf("want 3 code points, got %d", n)
	}
}
