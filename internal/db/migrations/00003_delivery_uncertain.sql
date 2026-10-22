-- +goose Up
-- D5 history (Milestone D review): record when a delivery may have happened
-- without confirmation, and attempts interrupted by a worker shutdown.

ALTER TABLE messages ADD COLUMN delivery_uncertain boolean NOT NULL DEFAULT false;

ALTER TABLE delivery_attempts DROP CONSTRAINT delivery_attempts_outcome_check;
ALTER TABLE delivery_attempts ADD CONSTRAINT delivery_attempts_outcome_check
    CHECK (outcome IN ('accepted', 'rate_limited', 'server_error', 'timeout', 'network_error', 'rejected', 'interrupted'));

-- +goose Down
ALTER TABLE delivery_attempts DROP CONSTRAINT delivery_attempts_outcome_check;
ALTER TABLE delivery_attempts ADD CONSTRAINT delivery_attempts_outcome_check
    CHECK (outcome IN ('accepted', 'rate_limited', 'server_error', 'timeout', 'network_error', 'rejected'));
ALTER TABLE messages DROP COLUMN delivery_uncertain;
