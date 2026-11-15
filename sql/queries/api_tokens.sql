-- name: CreateAPIToken :one
INSERT INTO api_tokens (user_id, name, token_hash, token_prefix, scopes, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAPIToken :one
SELECT * FROM api_tokens WHERE id = ? LIMIT 1;

-- name: GetAPITokenByHash :one
SELECT * FROM api_tokens WHERE token_hash = ? LIMIT 1;

-- name: GetAPITokenWithUserByHash :one
SELECT
    t.*,
    u.email AS user_email,
    u.role AS user_role
FROM api_tokens t
JOIN users u ON t.user_id = u.id
WHERE t.token_hash = ?
LIMIT 1;

-- name: ListAPITokensByUser :many
SELECT id, user_id, name, token_prefix, scopes, last_used_at, expires_at, created_at
FROM api_tokens
WHERE user_id = ?
ORDER BY created_at DESC, id DESC;

-- name: UpdateAPITokenLastUsed :exec
UPDATE api_tokens
SET last_used_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteAPIToken :exec
DELETE FROM api_tokens WHERE id = ?;

-- name: DeleteAPITokenForUser :exec
DELETE FROM api_tokens WHERE id = ? AND user_id = ?;

-- name: GetAllAPITokens :many
SELECT * FROM api_tokens ORDER BY id;

-- name: ClearAPITokens :exec
DELETE FROM api_tokens;

-- name: RestoreAPIToken :exec
INSERT INTO api_tokens (id, user_id, name, token_hash, token_prefix, scopes, last_used_at, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);
