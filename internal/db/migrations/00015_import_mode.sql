-- +goose Up
-- How an import treats entries that are already in the list: skip (leave
-- them alone), newer (update them when the source entry changed later) or
-- always (update them unconditionally).
ALTER TABLE imports ADD COLUMN mode TEXT NOT NULL DEFAULT 'skip';

-- +goose Down
ALTER TABLE imports DROP COLUMN mode;
