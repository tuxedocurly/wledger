-- name: RestoreContainer :exec
INSERT INTO containers (id, name, controller_id, segment_id, config_json, position_index, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: CreateContainer :one
INSERT INTO containers (name, controller_id, segment_id, config_json, position_index)
VALUES (?, ?, ?, ?, ?)
RETURNING id;

-- name: GetContainer :one
SELECT * FROM containers WHERE id = ?;

-- name: GetContainersByController :many
SELECT * FROM containers WHERE controller_id = ? ORDER BY position_index ASC, id ASC;

-- name: GetStandaloneContainers :many
SELECT * FROM containers WHERE controller_id IS NULL ORDER BY position_index ASC, id ASC;

-- name: GetAllContainersWithController :many
SELECT
    cont.*,
    c.name AS controller_name,
    c.ip_address AS controller_ip,
    c.is_online AS controller_is_online
FROM containers cont
LEFT JOIN controllers c ON cont.controller_id = c.id
ORDER BY cont.position_index ASC, cont.id ASC;

-- name: UpdateContainerConfig :exec
UPDATE containers
SET name = ?, config_json = ?, segment_id = ?, position_index = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateContainerController :exec
UPDATE containers
SET controller_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteContainer :exec
DELETE FROM containers WHERE id = ?;

-- name: GetAllContainers :many
SELECT * FROM containers ORDER BY name;
