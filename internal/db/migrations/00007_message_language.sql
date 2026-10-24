-- +goose Up
-- D64: the language of the text each recipient gets, fixed when the audience
-- is built. NULL on messages built before T20: they get the default ('und').
ALTER TABLE messages ADD COLUMN language text CHECK (language IN ('tr', 'en', 'und'));

-- +goose Down
ALTER TABLE messages DROP COLUMN language;
