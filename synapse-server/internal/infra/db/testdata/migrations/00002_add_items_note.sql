-- +goose Up
ALTER TABLE test_items ADD COLUMN note TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE test_items DROP COLUMN IF EXISTS note;
