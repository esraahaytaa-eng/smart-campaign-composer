-- +goose Up
-- The claim reads due messages in (next_attempt_at, id) order. With id in the
-- index that order comes straight from it: a claim reads one entry instead of
-- sorting every due message of the channel (audit F1).
CREATE INDEX messages_claim_idx ON messages (channel, next_attempt_at, id)
    WHERE status IN ('pending', 'retry_scheduled');
DROP INDEX messages_due_idx;

-- +goose Down
CREATE INDEX messages_due_idx ON messages (channel, next_attempt_at)
    WHERE status IN ('pending', 'retry_scheduled');
DROP INDEX messages_claim_idx;
