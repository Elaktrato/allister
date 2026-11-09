-- +goose Up
ALTER TABLE jobs ADD COLUMN partials INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE jobs DROP COLUMN partials;
