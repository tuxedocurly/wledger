-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;

BEGIN TRANSACTION;

-- 1. Rebuild containers so controller_id is NULLABLE (ON DELETE SET NULL),
--    preserving all existing containers, bins, and wall_cards.
CREATE TABLE containers_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    controller_id INTEGER,
    segment_id INTEGER NOT NULL DEFAULT 0,
    config_json TEXT DEFAULT '{"type":"grid","rows":8,"cols":8}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    position_index INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY(controller_id) REFERENCES controllers(id) ON DELETE SET NULL
);

INSERT INTO containers_new (id, name, controller_id, segment_id, config_json, created_at, updated_at, position_index)
SELECT id, name, controller_id, segment_id, config_json, created_at, updated_at, position_index
FROM containers;

DROP TABLE containers;
ALTER TABLE containers_new RENAME TO containers;
CREATE INDEX IF NOT EXISTS idx_containers_controller_id ON containers(controller_id);

-- 2. Deduplicate existing orphaned part_assignments (part_id, NULL),
--    enforce partial unique index, and coalesce orphaned stock before bin delete.
UPDATE part_assignments
SET quantity = (
    SELECT COALESCE(SUM(pa2.quantity), 0)
    FROM part_assignments pa2
    WHERE pa2.part_id = part_assignments.part_id
      AND pa2.bin_id IS NULL
)
WHERE bin_id IS NULL
  AND id = (
    SELECT MIN(pa3.id)
    FROM part_assignments pa3
    WHERE pa3.part_id = part_assignments.part_id
      AND pa3.bin_id IS NULL
  );

DELETE FROM part_assignments
WHERE bin_id IS NULL
  AND EXISTS (
    SELECT 1
    FROM part_assignments pa2
    WHERE pa2.part_id = part_assignments.part_id
      AND pa2.bin_id IS NULL
      AND pa2.id < part_assignments.id
  );

CREATE UNIQUE INDEX IF NOT EXISTS idx_part_assignments_orphaned
ON part_assignments(part_id) WHERE bin_id IS NULL;

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS coalesce_orphaned_stock_before_bin_delete
BEFORE DELETE ON bins
FOR EACH ROW
BEGIN
    UPDATE part_assignments
    SET quantity = quantity + COALESCE((
        SELECT pa_del.quantity
        FROM part_assignments pa_del
        WHERE pa_del.part_id = part_assignments.part_id
          AND pa_del.bin_id = OLD.id
        LIMIT 1
    ), 0)
    WHERE bin_id IS NULL
      AND part_id IN (
        SELECT part_id FROM part_assignments WHERE bin_id = OLD.id
      );

    DELETE FROM part_assignments
    WHERE bin_id = OLD.id
      AND EXISTS (
        SELECT 1
        FROM part_assignments pa_orph
        WHERE pa_orph.part_id = part_assignments.part_id
          AND pa_orph.bin_id IS NULL
      );
END;
-- +goose StatementEnd

-- 3. Role validation triggers on users.
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS validate_users_role_insert
BEFORE INSERT ON users
FOR EACH ROW
WHEN NEW.role IS NULL OR NEW.role NOT IN ('admin', 'editor', 'viewer')
BEGIN
    SELECT RAISE(ABORT, 'invalid user role');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS validate_users_role_update
BEFORE UPDATE ON users
FOR EACH ROW
WHEN NEW.role IS NULL OR NEW.role NOT IN ('admin', 'editor', 'viewer')
BEGIN
    SELECT RAISE(ABORT, 'invalid user role');
END;
-- +goose StatementEnd

-- 4. Add WLED v2 columns to controllers.
ALTER TABLE controllers ADD COLUMN use_udp BOOLEAN DEFAULT 0;
ALTER TABLE controllers ADD COLUMN udp_port INTEGER DEFAULT 21324;
ALTER TABLE controllers ADD COLUMN restore_state BOOLEAN DEFAULT 1;
ALTER TABLE controllers ADD COLUMN idle_preset_id INTEGER DEFAULT 0;

-- 5. Add locate_color_by_stock to settings.
ALTER TABLE settings ADD COLUMN locate_color_by_stock BOOLEAN DEFAULT 0;

-- 6. Create api_tokens table.
CREATE TABLE IF NOT EXISTS api_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    scopes TEXT NOT NULL DEFAULT 'read',
    last_used_at DATETIME,
    expires_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user_id ON api_tokens(user_id);

-- 7. Create projects and project_bom_items tables.
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'draft',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS project_bom_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    part_id INTEGER,
    designator TEXT,
    mpn TEXT,
    description TEXT,
    quantity_per_board INTEGER NOT NULL DEFAULT 1 CHECK(quantity_per_board > 0),
    notes TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY(part_id) REFERENCES parts(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_project_bom_items_project_id ON project_bom_items(project_id);
CREATE INDEX IF NOT EXISTS idx_project_bom_items_part_id ON project_bom_items(part_id);

PRAGMA foreign_key_check;

COMMIT;

PRAGMA foreign_keys = ON;
