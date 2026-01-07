-- name: GetAllBins :many
SELECT * FROM bins ORDER BY id;

-- name: RestoreBin :exec
INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: CreateBin :one
INSERT INTO bins (name, container_id, led_index, width, grid_x, grid_y)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetBin :one
SELECT * FROM bins WHERE id = ?;

-- name: GetBinsByContainer :many
SELECT * FROM bins 
WHERE container_id = ? 
ORDER BY led_index ASC;

-- name: DeleteBinsByContainer :exec
DELETE FROM bins WHERE container_id = ?;

-- name: UpsertBin :exec
INSERT INTO bins (name, container_id, led_index, width, grid_x, grid_y)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(container_id, led_index) DO UPDATE SET
    name = excluded.name,
    width = excluded.width,
    grid_x = excluded.grid_x,
    grid_y = excluded.grid_y;

-- name: UpdateBinLedIndex :exec
UPDATE bins
SET led_index = ?
WHERE id = ?;

-- name: UpdateBin :exec
UPDATE bins
SET name = ?, led_index = ?, width = ?, grid_x = ?, grid_y = ?
WHERE id = ?;

-- name: DeleteBin :exec
DELETE FROM bins WHERE id = ?;

-- name: DeleteBinByLed :exec
DELETE FROM bins 
WHERE container_id = ? AND led_index = ?;

-- name: GetBinByLocation :one
SELECT b.id 
FROM bins b
JOIN containers c ON b.container_id = c.id
JOIN controllers ct ON c.controller_id = ct.id
WHERE ct.ip_address = ? AND c.segment_id = ? AND b.led_index = ?;

-- name: ClearContainerBinLedIndices :exec
UPDATE bins
SET led_index = NULL
WHERE container_id = ?;
