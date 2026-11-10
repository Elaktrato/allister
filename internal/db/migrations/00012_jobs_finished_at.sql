-- +goose Up
-- Done and failed jobs record when they ended, so the queue can prune
-- finished work instead of growing forever.
ALTER TABLE jobs ADD COLUMN finished_at TEXT;

-- +goose Down
ALTER TABLE jobs DROP COLUMN finished_at;
