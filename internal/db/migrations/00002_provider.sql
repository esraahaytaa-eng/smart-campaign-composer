-- +goose Up
-- The mock provider's idempotency records (D6), in their own schema. The
-- provider logs in as scc_provider, which may use this table and nothing else.

CREATE SCHEMA provider;

CREATE TABLE provider.idempotency_keys (
    key text PRIMARY KEY,
    channel text NOT NULL CHECK (channel IN ('sms', 'email')),
    state text NOT NULL CHECK (state IN ('in_progress', 'completed')),
    http_status int,
    response jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

-- Roles belong to the whole Postgres server, so another database (or a
-- concurrent migration) may have created it already.
-- +goose StatementBegin
DO $$
BEGIN
    CREATE ROLE scc_provider LOGIN;
EXCEPTION WHEN duplicate_object OR unique_violation THEN
    NULL;
END
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA provider TO scc_provider;
GRANT SELECT, INSERT, UPDATE, DELETE ON provider.idempotency_keys TO scc_provider;

-- +goose Down
DROP SCHEMA provider CASCADE;
