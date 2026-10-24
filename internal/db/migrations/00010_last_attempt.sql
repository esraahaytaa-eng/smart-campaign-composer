-- +goose Up
-- The latest attempt's start on the message itself, written by the worker
-- with the result (audit F3). The recipient list sorts by it, instead of
-- looking up every message's attempts before it can take a page.
ALTER TABLE messages ADD COLUMN last_attempt_at timestamptz;
UPDATE messages m SET last_attempt_at = a.started_at
FROM (SELECT message_id, max(started_at) AS started_at FROM delivery_attempts GROUP BY message_id) a
WHERE m.id = a.message_id;

-- +goose Down
ALTER TABLE messages DROP COLUMN last_attempt_at;
