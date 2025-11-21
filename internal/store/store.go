package store

import (
	"database/sql"
	"errors"
	"log"
)

var ErrForeignKeyConstraint = errors.New("foreign key constraint violation")
var ErrUniqueConstraint = errors.New("unique constraint violation")

// Store holds the database connection
type Store struct {
	db *sql.DB
}

// NewStore initializes the database and returns a new Store
func NewStore(filepath string) (*Store, error) {
	log.Println("Initializing database...")

	db, err := sql.Open("sqlite", filepath)
	if err != nil {
		return nil, err
	}

	// Enable Write-Ahead Logging (WAL) mode
	_, err = db.Exec("PRAGMA journal_mode = WAL;")
	if err != nil {
		return nil, err
	}

	_, err = db.Exec("PRAGMA foreign_keys = ON;")
	if err != nil {
		return nil, err
	}

	// Run Migrations
	if err := createTables(db); err != nil {
		return nil, err
	}

	log.Println("Database initialized successfully.")
	return &Store{db: db}, nil
}

// createTables runs all the CREATE TABLE IF NOT EXISTS queries
func createTables(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS wled_controllers (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT NOT NULL,
			ip_address    TEXT NOT NULL UNIQUE,
			status        TEXT NOT NULL DEFAULT 'unknown',
			last_seen     DATETIME
		);`,
		`CREATE TABLE IF NOT EXISTS bins (
			id                     INTEGER PRIMARY KEY AUTOINCREMENT,
			name                   TEXT NOT NULL UNIQUE,
			wled_controller_id     INTEGER NOT NULL,
			wled_segment_id        INTEGER NOT NULL,
			led_index              INTEGER NOT NULL,
			FOREIGN KEY (wled_controller_id) REFERENCES wled_controllers (id)
		);`,
		`CREATE TABLE IF NOT EXISTS parts (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT NOT NULL,
			description   TEXT,
			part_number   TEXT,
			datasheet_url TEXT,
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			image_path      TEXT,
			manufacturer    TEXT,
			supplier        TEXT,
			unit_cost       REAL NOT NULL DEFAULT 0,
			status          TEXT NOT NULL DEFAULT 'active',
			stock_tracking_enabled BOOLEAN NOT NULL DEFAULT 0,
			reorder_point   INTEGER NOT NULL DEFAULT 0,
			min_stock       INTEGER NOT NULL DEFAULT 0
		);`,

		`CREATE TABLE IF NOT EXISTS part_locations (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			part_id       INTEGER NOT NULL,
			bin_id        INTEGER NOT NULL,
			quantity      INTEGER NOT NULL DEFAULT 0,
			
			-- MUST HAVE 'ON DELETE CASCADE' TO PASS THE TEST
			FOREIGN KEY (part_id) REFERENCES parts (id) ON DELETE CASCADE,
			FOREIGN KEY (bin_id) REFERENCES bins (id) ON DELETE CASCADE
		);`,

		`CREATE TABLE IF NOT EXISTS part_urls (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			part_id       INTEGER NOT NULL,
			url           TEXT NOT NULL,
			description   TEXT,
			FOREIGN KEY (part_id) REFERENCES parts (id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS part_documents (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			part_id       INTEGER NOT NULL,
			filename      TEXT NOT NULL,
			filepath      TEXT NOT NULL,
			description   TEXT,
			mimetype      TEXT,
			FOREIGN KEY (part_id) REFERENCES parts (id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS categories (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT NOT NULL UNIQUE
		);`,
		`CREATE TABLE IF NOT EXISTS part_categories (
			part_id       INTEGER NOT NULL,
			category_id   INTEGER NOT NULL,
			PRIMARY KEY (part_id, category_id),
			FOREIGN KEY (part_id) REFERENCES parts (id) ON DELETE CASCADE,
			FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE CASCADE
		);`,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			return err
		}
	}
	return nil
}
