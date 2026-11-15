-- name: GetFlag :one
SELECT value FROM system_flags WHERE key = ?;

-- name: SetFlag :exec
INSERT INTO system_flags (key, value, updated_at)
VALUES (?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(key) DO UPDATE SET
    value = excluded.value,
    updated_at = excluded.updated_at;

-- name: GetAllSystemFlags :many
SELECT * FROM system_flags ORDER BY key;

-- name: ClearSystemFlags :exec
DELETE FROM system_flags;

-- name: RestoreSystemFlag :exec
INSERT INTO system_flags (key, value, updated_at) VALUES (?, ?, ?);
