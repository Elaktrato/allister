-- +goose Up
-- A short status line for the page: why an import is waiting or stopped.
ALTER TABLE imports ADD COLUMN note TEXT;

-- +goose Down
ALTER TABLE imports DROP COLUMN note;
