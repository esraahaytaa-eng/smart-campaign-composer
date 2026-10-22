-- +goose Up
-- D43 supersedes D42: campaigns are identified by their brief, so the name
-- column added in 00004 goes. 00004 stays, as it may already be applied.
ALTER TABLE campaigns DROP COLUMN name;

-- +goose Down
ALTER TABLE campaigns ADD COLUMN name text NOT NULL DEFAULT '';
