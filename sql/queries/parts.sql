-- name: GetPart :one
SELECT * FROM parts WHERE id = ?;

-- name: ListParts :many
WITH stock_totals AS (
    SELECT
        part_id,
        SUM(quantity) AS total_stock,
        SUM(CASE WHEN bin_id IS NOT NULL THEN quantity ELSE 0 END) AS valid_stock,
        SUM(CASE WHEN bin_id IS NULL THEN quantity ELSE 0 END) AS orphaned_stock
    FROM part_assignments
    GROUP BY part_id
)
SELECT p.*,
    CAST(COALESCE(st.total_stock, 0) AS INTEGER) AS total_stock,
    CAST(COALESCE(st.valid_stock, 0) AS INTEGER) AS valid_stock,
    CAST(COALESCE(st.orphaned_stock, 0) AS INTEGER) AS orphaned_stock
FROM parts p
LEFT JOIN stock_totals st ON p.id = st.part_id
WHERE (
    sqlc.narg('bin_id') IS NULL
    OR EXISTS (
        SELECT 1 FROM part_assignments pa
        WHERE pa.part_id = p.id AND pa.bin_id = sqlc.narg('bin_id')
    )
)
AND (
    sqlc.narg('controller_id') IS NULL
    OR EXISTS (
        SELECT 1 FROM part_assignments pa
        JOIN bins b ON pa.bin_id = b.id
        JOIN containers cont ON b.container_id = cont.id
        WHERE pa.part_id = p.id AND cont.controller_id = sqlc.narg('controller_id')
    )
)
ORDER BY p.name
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: SearchParts :many
WITH stock_totals AS (
    SELECT
        part_id,
        SUM(quantity) AS total_stock,
        SUM(CASE WHEN bin_id IS NOT NULL THEN quantity ELSE 0 END) AS valid_stock,
        SUM(CASE WHEN bin_id IS NULL THEN quantity ELSE 0 END) AS orphaned_stock
    FROM part_assignments
    GROUP BY part_id
)
SELECT p.*,
    CAST(COALESCE(st.total_stock, 0) AS INTEGER) AS total_stock,
    CAST(COALESCE(st.valid_stock, 0) AS INTEGER) AS valid_stock,
    CAST(COALESCE(st.orphaned_stock, 0) AS INTEGER) AS orphaned_stock
FROM parts_fts fts
JOIN parts p ON fts.rowid = p.id
LEFT JOIN stock_totals st ON p.id = st.part_id
WHERE parts_fts MATCH sqlc.arg('query')
  AND (
      sqlc.narg('bin_id') IS NULL
      OR EXISTS (
          SELECT 1 FROM part_assignments pa
          WHERE pa.part_id = p.id AND pa.bin_id = sqlc.narg('bin_id')
      )
  )
  AND (
      sqlc.narg('controller_id') IS NULL
      OR EXISTS (
          SELECT 1 FROM part_assignments pa
          JOIN bins b ON pa.bin_id = b.id
          JOIN containers cont ON b.container_id = cont.id
          WHERE pa.part_id = p.id AND cont.controller_id = sqlc.narg('controller_id')
      )
  )
ORDER BY fts.rank
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetAllParts :many
SELECT * FROM parts ORDER BY id;

-- name: GetAllPartLinks :many
SELECT * FROM part_links ORDER BY id;

-- name: GetAllPartDocs :many
SELECT * FROM part_docs ORDER BY id;

-- name: GetAllPartAiPrompts :many
SELECT * FROM part_ai_prompts ORDER BY id;

-- name: RestorePart :exec
INSERT INTO parts (
    id, name, description, part_number, manufacturer, supplier, 
    unit_cost, reorder_level, min_stock_threshold, 
    image_path, barcode_data, is_favorite, tags, created_at, updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?, 
    ?, ?, ?, 
    ?, ?, ?, ?, ?, ?
);

-- name: RestorePartLink :exec
INSERT INTO part_links (id, part_id, url, label) VALUES (?, ?, ?, ?);

-- name: RestorePartDoc :exec
INSERT INTO part_docs (id, part_id, file_path, file_name) VALUES (?, ?, ?, ?);

-- name: RestorePartAssignment :exec
INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (?, ?, ?, ?);

-- name: RestorePartAiPrompt :exec
INSERT INTO part_ai_prompts (id, part_id, prompt_text, ai_response, model_used, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: CreatePart :one
INSERT INTO parts (
    name, description, part_number, manufacturer, supplier, 
    unit_cost, reorder_level, min_stock_threshold, 
    image_path, barcode_data
) VALUES (
    ?, ?, ?, ?, ?, 
    ?, ?, ?, 
    ?, ?
) RETURNING id;

-- name: UpdatePart :exec
UPDATE parts SET 
    name = sqlc.arg('name'), 
    description = sqlc.arg('description'), 
    part_number = sqlc.arg('part_number'), 
    manufacturer = sqlc.arg('manufacturer'), 
    supplier = sqlc.arg('supplier'), 
    unit_cost = sqlc.arg('unit_cost'), 
    reorder_level = sqlc.arg('reorder_level'), 
    min_stock_threshold = sqlc.arg('min_stock_threshold'), 
    barcode_data = sqlc.arg('barcode_data'),
    image_path = sqlc.arg('image_path'),
    updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg('id');

-- name: DeletePart :exec
DELETE FROM parts WHERE id = ?;

-- LINKS
-- name: CreatePartLink :exec
INSERT INTO part_links (part_id, url, label) VALUES (?, ?, ?);

-- name: UpdatePartLink :exec
UPDATE part_links SET url = ?, label = ? WHERE id = ?;

-- name: GetPartLinks :many
SELECT * FROM part_links WHERE part_id = ?;

-- name: DeletePartLink :exec
DELETE FROM part_links WHERE id = ?;

-- DOCUMENTS
-- name: CreatePartDoc :exec
INSERT INTO part_docs (part_id, file_path, file_name) VALUES (?, ?, ?);

-- name: GetPartDocs :many
SELECT * FROM part_docs WHERE part_id = ?;

-- name: GetPartDoc :one
SELECT * FROM part_docs WHERE id = ?;

-- name: DeletePartDoc :exec
DELETE FROM part_docs WHERE id = ?;

-- STOCK ASSIGNMENTS
-- name: GetAllPartAssignments :many
SELECT * FROM part_assignments ORDER BY id;

-- name: GetPartAssignments :many
SELECT 
    pa.id, 
    pa.quantity, 
    b.id as bin_id, 
    b.name as bin_name,
    b.led_index,
    b.width,
    c.name as controller_name,
    c.id as controller_id,
    c.ip_address as controller_ip,
    cont.name as container_name,
    cont.id as container_id
FROM part_assignments pa
LEFT JOIN bins b ON pa.bin_id = b.id
LEFT JOIN containers cont ON b.container_id = cont.id
LEFT JOIN controllers c ON cont.controller_id = c.id
WHERE pa.part_id = ?;

-- name: GetAssignment :one
SELECT * FROM part_assignments WHERE id = ?;

-- name: GetAssignmentID :one
SELECT id FROM part_assignments WHERE part_id = ? AND bin_id = ?;

-- name: CreatePartAssignment :exec
INSERT INTO part_assignments (part_id, bin_id, quantity) VALUES (?, ?, ?);

-- name: ReassignPartAssignment :exec
UPDATE part_assignments 
SET bin_id = ? 
WHERE id = ?;

-- name: UpdatePartAssignmentQuantity :exec
UPDATE part_assignments SET quantity = ? WHERE part_id = ? AND bin_id = ?;

-- name: UpdatePartAssignmentQuantityByID :exec
UPDATE part_assignments SET quantity = ? WHERE id = ?;

-- name: DeletePartAssignment :exec
DELETE FROM part_assignments WHERE part_id = ? AND bin_id = ?;

-- name: DeleteAssignment :exec
DELETE FROM part_assignments WHERE id = ?;
