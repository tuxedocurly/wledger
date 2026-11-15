-- name: GetAllUsers :many
SELECT * FROM users ORDER BY id;

-- name: RestoreUser :exec
INSERT INTO users (id, email, password_hash, role, change_password_required, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: CreateUser :one
INSERT INTO users (email, password_hash, role, change_password_required) 
VALUES (?, ?, ?, ?)
RETURNING id, email, role, created_at;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, role, change_password_required, created_at 
FROM users 
WHERE email = ? LIMIT 1;

-- name: ListUsers :many
SELECT id, email, role, change_password_required, created_at 
FROM users 
ORDER BY created_at DESC;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: GetUser :one
SELECT * FROM users WHERE id = ? LIMIT 1;

-- name: UpdateUserPassword :exec
UPDATE users 
SET password_hash = ?, change_password_required = 0 
WHERE id = ?;

-- name: SetPasswordResetFlag :exec
UPDATE users 
SET change_password_required = ? 
WHERE id = ?;

-- SESSION QUERIES
-- name: GetSession :one
SELECT token, data, expiry FROM sessions WHERE token = ?;
-- name: CreateSession :exec
INSERT INTO sessions (token, data, expiry) VALUES (?, ?, ?);
-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = ?;
-- name: CleanupSessions :exec
DELETE FROM sessions WHERE expiry < ?;

-- name: ClearSessions :exec
DELETE FROM sessions;