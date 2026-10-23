-- +goose Up
-- D42: an editable campaign name. Empty means "not set": the API then shows
-- the default derived from the brief, so the rule lives in one place (Go).
ALTER TABLE campaigns ADD COLUMN name text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE campaigns DROP COLUMN name;
