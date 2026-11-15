-- name: GetAllInspirationTemplates :many
SELECT * FROM inspiration_templates ORDER BY id;

-- name: GetInspirationTemplate :one
SELECT * FROM inspiration_templates WHERE id = ?;

-- name: CreateInspirationTemplate :one
INSERT INTO inspiration_templates (title, template_content) VALUES (?, ?) RETURNING *;

-- name: UpdateInspirationTemplate :exec
UPDATE inspiration_templates SET title = ?, template_content = ? WHERE id = ?;

-- name: DeleteInspirationTemplate :exec
DELETE FROM inspiration_templates WHERE id = ?;

-- name: GetPartsForInspirationAll :many
SELECT 
    p.name,
    p.part_number,
    CAST(COALESCE(SUM(pa.quantity), 0) AS INTEGER) as total_quantity
FROM parts p
LEFT JOIN part_assignments pa ON p.id = pa.part_id
GROUP BY p.id
HAVING total_quantity > 0
ORDER BY p.name;

-- name: GetPartsForInspirationFiltered :many
WITH stock_totals AS (
    SELECT
        part_id,
        SUM(quantity) AS total_quantity
    FROM part_assignments
    GROUP BY part_id
)
SELECT
    p.name,
    p.part_number,
    CAST(COALESCE(st.total_quantity, 0) AS INTEGER) AS total_quantity
FROM parts p
JOIN stock_totals st ON p.id = st.part_id
WHERE st.total_quantity > 0
  AND EXISTS (
      SELECT 1
      FROM part_tags pt
      JOIN tags t ON pt.tag_id = t.id
      WHERE pt.part_id = p.id
        AND t.name IN (sqlc.slice('tags'))
  )
ORDER BY p.name;

-- name: ClearInspirationTemplates :exec
DELETE FROM inspiration_templates;

-- name: RestoreInspirationTemplate :exec
INSERT INTO inspiration_templates (id, title, template_content, created_at)
VALUES (?, ?, ?, ?);