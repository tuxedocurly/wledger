-- name: CreateProject :one
INSERT INTO projects (name, description, status)
VALUES (?, ?, ?)
RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = ? LIMIT 1;

-- name: ListProjects :many
SELECT
    p.*,
    CAST(COALESCE((SELECT COUNT(*) FROM project_bom_items bom WHERE bom.project_id = p.id), 0) AS INTEGER) AS bom_item_count
FROM projects p
ORDER BY p.updated_at DESC, p.id DESC;

-- name: UpdateProject :exec
UPDATE projects
SET name = ?, description = ?, status = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = ?;

-- name: GetAllProjects :many
SELECT * FROM projects ORDER BY id;

-- name: ClearProjects :exec
DELETE FROM projects;

-- name: RestoreProject :exec
INSERT INTO projects (id, name, description, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- BOM Items
-- name: AddProjectBOMItem :one
INSERT INTO project_bom_items (project_id, part_id, designator, mpn, description, quantity_per_board, notes)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetProjectBOMItem :one
SELECT * FROM project_bom_items WHERE id = ? LIMIT 1;

-- name: ListProjectBOMItems :many
SELECT
    bom.*,
    p.name AS part_name,
    p.part_number AS resolved_part_number,
    CAST(COALESCE((SELECT SUM(pa.quantity) FROM part_assignments pa WHERE pa.part_id = bom.part_id), 0) AS INTEGER) AS available_stock
FROM project_bom_items bom
LEFT JOIN parts p ON bom.part_id = p.id
WHERE bom.project_id = ?
ORDER BY bom.id ASC;

-- name: UpdateProjectBOMItem :exec
UPDATE project_bom_items
SET part_id = ?, designator = ?, mpn = ?, description = ?, quantity_per_board = ?, notes = ?
WHERE id = ?;

-- name: LinkProjectBOMItemPart :exec
UPDATE project_bom_items
SET part_id = ?
WHERE id = ?;

-- name: DeleteProjectBOMItem :exec
DELETE FROM project_bom_items WHERE id = ?;

-- name: GetAllProjectBOMItems :many
SELECT * FROM project_bom_items ORDER BY id;

-- name: ClearProjectBOMItems :exec
DELETE FROM project_bom_items;

-- name: RestoreProjectBOMItem :exec
INSERT INTO project_bom_items (id, project_id, part_id, designator, mpn, description, quantity_per_board, notes, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- Shortage Analysis Queries
-- name: GetProjectBOMWithStock :many
WITH stock_totals AS (
    SELECT part_id, SUM(quantity) AS total_stock
    FROM part_assignments
    GROUP BY part_id
)
SELECT
    bom.*,
    p.name AS part_name,
    p.part_number AS resolved_part_number,
    p.unit_cost AS unit_cost,
    CAST(bom.quantity_per_board * CAST(sqlc.arg('board_count') AS INTEGER) AS INTEGER) AS required_quantity,
    CAST(COALESCE(st.total_stock, 0) AS INTEGER) AS available_stock,
    CAST(MAX(0, (bom.quantity_per_board * CAST(sqlc.arg('board_count') AS INTEGER)) - COALESCE(st.total_stock, 0)) AS INTEGER) AS shortage_quantity
FROM project_bom_items bom
LEFT JOIN parts p ON bom.part_id = p.id
LEFT JOIN stock_totals st ON bom.part_id = st.part_id
WHERE bom.project_id = sqlc.arg('project_id')
ORDER BY bom.id ASC;

-- name: GetProjectShortages :many
WITH stock_totals AS (
    SELECT part_id, SUM(quantity) AS total_stock
    FROM part_assignments
    GROUP BY part_id
),
bom_aggregated AS (
    SELECT
        bom.part_id,
        MIN(bom.id) AS primary_bom_item_id,
        GROUP_CONCAT(bom.designator, ', ') AS designators,
        COALESCE(p.name, MAX(bom.description), MAX(bom.mpn), '') AS display_name,
        COALESCE(p.part_number, MAX(bom.mpn), '') AS part_number,
        SUM(bom.quantity_per_board) AS total_per_board,
        COALESCE(st.total_stock, 0) AS available_stock
    FROM project_bom_items bom
    LEFT JOIN parts p ON bom.part_id = p.id
    LEFT JOIN stock_totals st ON bom.part_id = st.part_id
    WHERE bom.project_id = sqlc.arg('project_id')
    GROUP BY COALESCE(bom.part_id, -bom.id)
)
SELECT
    ba.part_id,
    CAST(ba.primary_bom_item_id AS INTEGER) AS primary_bom_item_id,
    CAST(COALESCE(ba.designators, '') AS TEXT) AS designators,
    CAST(ba.display_name AS TEXT) AS display_name,
    CAST(ba.part_number AS TEXT) AS part_number,
    CAST(ba.total_per_board AS INTEGER) AS quantity_per_board,
    CAST(ba.total_per_board * CAST(sqlc.arg('board_count') AS INTEGER) AS INTEGER) AS required_quantity,
    CAST(ba.available_stock AS INTEGER) AS available_stock,
    CAST((ba.total_per_board * CAST(sqlc.arg('board_count') AS INTEGER)) - ba.available_stock AS INTEGER) AS shortage_quantity
FROM bom_aggregated ba
WHERE (ba.total_per_board * CAST(sqlc.arg('board_count') AS INTEGER)) > ba.available_stock
ORDER BY shortage_quantity DESC, ba.primary_bom_item_id ASC;
