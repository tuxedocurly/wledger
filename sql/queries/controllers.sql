-- name: RestoreController :exec
INSERT INTO controllers (
    id, name, ip_address, port, mac_address, is_online, led_count, created_at,
    use_udp, udp_port, restore_state, idle_preset_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CreateController :one
INSERT INTO controllers (name, ip_address, port) 
VALUES (?, ?, ?)
RETURNING id, name, ip_address, is_online, created_at;

-- name: GetControllers :many
SELECT * FROM controllers ORDER BY name;

-- name: GetController :one
SELECT * FROM controllers WHERE id = ?;

-- name: GetControllerByMac :one
SELECT * FROM controllers WHERE mac_address = ? LIMIT 1;

-- name: DeleteController :exec
DELETE FROM controllers WHERE id = ?;

-- name: UpdateControllerStatus :exec
UPDATE controllers
SET is_online = ?
WHERE id = ?;

-- name: UpdateControllerInfo :exec
UPDATE controllers
SET is_online = ?, led_count = ?, mac_address = ?
WHERE id = ?;

-- name: UpdateController :exec
UPDATE controllers
SET name = ?, ip_address = ?, port = ?, use_udp = ?, udp_port = ?, restore_state = ?, idle_preset_id = ?
WHERE id = ?;
