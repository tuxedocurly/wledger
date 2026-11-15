-- +goose Up
ALTER TABLE settings ADD COLUMN enable_controller_ping BOOLEAN DEFAULT 1;
ALTER TABLE settings ADD COLUMN controller_ping_interval_seconds INTEGER DEFAULT 600;

-- +goose Down
SELECT 1;
