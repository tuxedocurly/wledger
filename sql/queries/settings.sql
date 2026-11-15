-- name: RestoreSettings :exec
INSERT OR REPLACE INTO settings (
    id, require_auth_for_read, locate_timeout_seconds, enable_locate_timeout,
    enable_debug_logs, color_locate, color_stock_ok, color_stock_low, color_stock_critical,
    created_at, updated_at, inspiration_seeds_applied, locate_color_by_stock
) VALUES (
    1, ?, ?, ?,
    ?, ?, ?, ?, ?,
    ?, ?, ?, ?
);

-- name: GetSettings :one
SELECT * FROM settings WHERE id = 1;

-- name: InitSettings :exec
INSERT OR IGNORE INTO settings (
    id, require_auth_for_read, color_locate, color_stock_ok, color_stock_low,
    color_stock_critical, locate_timeout_seconds, enable_locate_timeout,
    enable_debug_logs, inspiration_seeds_applied, locate_color_by_stock
)
VALUES (1, 1, '#0000FF', '#00FF00', '#FFFF00', '#FF0000', 10, 1, 0, 0, 0);

-- name: UpdateGeneralSettings :exec
UPDATE settings 
SET 
    require_auth_for_read = COALESCE(sqlc.narg('require_auth_for_read'), require_auth_for_read), 
    locate_timeout_seconds = COALESCE(sqlc.narg('locate_timeout_seconds'), locate_timeout_seconds), 
    enable_locate_timeout = COALESCE(sqlc.narg('enable_locate_timeout'), enable_locate_timeout), 
    enable_debug_logs = COALESCE(sqlc.narg('enable_debug_logs'), enable_debug_logs),
    locate_color_by_stock = COALESCE(sqlc.narg('locate_color_by_stock'), locate_color_by_stock),
    updated_at = CURRENT_TIMESTAMP
WHERE id = 1;

-- name: UpdateColors :exec
UPDATE settings
SET 
    color_locate = COALESCE(sqlc.narg('color_locate'), color_locate), 
    color_stock_ok = COALESCE(sqlc.narg('color_stock_ok'), color_stock_ok), 
    color_stock_low = COALESCE(sqlc.narg('color_stock_low'), color_stock_low), 
    color_stock_critical = COALESCE(sqlc.narg('color_stock_critical'), color_stock_critical), 
    updated_at = CURRENT_TIMESTAMP
WHERE id = 1;



-- name: MarkInspirationSeedsApplied :exec
UPDATE settings
SET inspiration_seeds_applied = 1, updated_at = CURRENT_TIMESTAMP
WHERE id = 1;

-- name: ClearAuditLogs :exec
DELETE FROM audit_logs;
-- name: ClearPartTags :exec
DELETE FROM part_tags;
-- name: ClearTags :exec
DELETE FROM tags;
-- name: ClearPartAssignments :exec
DELETE FROM part_assignments;
-- name: ClearPartDocs :exec
DELETE FROM part_docs;
-- name: ClearPartLinks :exec
DELETE FROM part_links;
-- name: ClearPartAiPrompts :exec
DELETE FROM part_ai_prompts;
-- name: ClearParts :exec
DELETE FROM parts;
-- name: ClearBins :exec
DELETE FROM bins;
-- name: ClearWallCards :exec
DELETE FROM wall_cards;
-- name: ClearWalls :exec
DELETE FROM walls;
-- name: ClearContainers :exec
DELETE FROM containers;
-- name: ClearControllers :exec
DELETE FROM controllers;
-- name: ClearUsers :exec
DELETE FROM users;
