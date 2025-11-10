package main

import (
	"database/sql"
	"errors"
	"log"
	"strconv"

	"modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

var ErrForeignKeyConstraint = errors.New("foreign key constraint violation")
var ErrUniqueConstraint = errors.New("unique constraint violation")

// TODO: The interfaces below should ideally be in their own files,
//       but forsimplicity, they are kept here for now.

// PartStore defines methods for managing the part catalog
type PartStore interface {
	GetPartByID(id int) (Part, error)
	GetParts() ([]Part, error)
	SearchParts(searchTerm string) ([]Part, error)
	CreatePart(p *Part) error
	UpdatePart(p *Part) error
	DeletePart(id int) error
	UpdatePartImagePath(partID int, imagePath string) error
	GetBinLocationCount(partID int) (int, error)
	GetCategories() ([]Category, error)
	GetCategoriesByPartID(partID int) ([]Category, error)
	CreateCategory(name string) (Category, error)
	AssignCategoryToPart(partID int, categoryID int) error
	RemoveCategoryFromPart(partID int, categoryID int) error
	CleanupOrphanedCategories() error
	GetURLsByPartID(partID int) ([]PartURL, error)
	CreatePartURL(partID int, url string, description string) error
	DeletePartURL(urlID int) error
	GetDocumentsByPartID(partID int) ([]PartDocument, error)
	GetDocumentByID(docID int) (PartDocument, error)
	CreatePartDocument(doc *PartDocument) error
	DeletePartDocument(docID int) error
}

// LocationStore defines methods for managing inventory in bins
type LocationStore interface {
	GetPartLocationByID(locationID int) (PartLocation, error)
	GetPartLocations(partID int) ([]PartLocation, error)
	CreatePartLocation(partID, binID, quantity int) error
	UpdatePartLocation(locationID, quantity int) error
	DeletePartLocation(locationID int) error
}

// BinStore defines methods for managing bins
type BinStore interface {
	GetBins() ([]Bin, error)
	GetAvailableBins(partID int) ([]Bin, error)
	CreateBin(name string, controllerID, segmentID, ledIndex int) error
	CreateBinsBulk(controllerID, segmentID, ledCount int, namePrefix string) error
	DeleteBin(id int) error
	GetPartNamesInBin(binID int) ([]string, error) // For delete check
}

// ControllerStore defines methods for managing WLED controllers
type ControllerStore interface {
	GetControllers() ([]WLEDController, error)
	GetControllerByID(id int) (WLEDController, error)
	CreateController(name, ipAddress string) error
	DeleteController(id int) error
	GetAllControllersForHealthCheck() ([]WLEDController, error)
	UpdateControllerStatus(id int, status string, lastSeen sql.NullTime) error
}

// DashboardStore defines methods for the dashboard and locate features
type DashboardStore interface {
	GetDashboardBinData() ([]DashboardBinData, error)
	GetPartLocationsForLocate(partID int) ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
	GetPartLocationsForStop(partID int) ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
	GetAllBinLocationsForStopAll() ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
}

// Store holds the database connection
type Store struct {
	db *sql.DB
}

// Compile-time checks to ensure *Store implements all interfaces
// TODO: Look into whether these are still needed with the current setup
var _ PartStore = (*Store)(nil)
var _ LocationStore = (*Store)(nil)
var _ BinStore = (*Store)(nil)
var _ ControllerStore = (*Store)(nil)
var _ DashboardStore = (*Store)(nil)

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

			-- New fields
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
			FOREIGN KEY (part_id) REFERENCES parts (id) ON DELETE CASCADE,
			FOREIGN KEY (bin_id) REFERENCES bins (id)
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

// Part Methods

func (s *Store) GetPartByID(id int) (Part, error) {
	var p Part
	query := `
		SELECT 
			id, name, description, part_number, datasheet_url, created_at, updated_at,
			image_path, manufacturer, supplier, unit_cost, status,
			stock_tracking_enabled, reorder_point, min_stock
		FROM parts
		WHERE id = ?;
	`
	row := s.db.QueryRow(query, id)
	err := row.Scan(
		&p.ID, &p.Name, &p.Description,
		&p.PartNumber, &p.DatasheetURL,
		&p.CreatedAt, &p.UpdatedAt,
		&p.ImagePath, &p.Manufacturer, &p.Supplier, &p.UnitCost, &p.Status,
		&p.StockTracking, &p.ReorderPoint, &p.MinStock,
	)
	if err != nil {
		return p, err
	}

	qtyQuery := `SELECT IFNULL(SUM(quantity), 0) FROM part_locations WHERE part_id = ?`
	err = s.db.QueryRow(qtyQuery, id).Scan(&p.TotalQuantity)
	return p, err
}

func (s *Store) GetParts() ([]Part, error) {
	query := `
		SELECT 
			p.id, p.name, p.description, p.part_number, p.datasheet_url, p.created_at, p.updated_at,
			p.image_path, p.manufacturer, p.supplier, p.unit_cost, p.status,
			p.stock_tracking_enabled, p.reorder_point, p.min_stock,
			IFNULL(SUM(pl.quantity), 0) AS total_quantity
		FROM parts p
		LEFT JOIN part_locations pl ON p.id = pl.part_id
		GROUP BY p.id
		ORDER BY p.name ASC;
	`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	parts := []Part{}
	for rows.Next() {
		var p Part
		err := rows.Scan(
			&p.ID, &p.Name, &p.Description, &p.PartNumber, &p.DatasheetURL,
			&p.CreatedAt, &p.UpdatedAt,
			&p.ImagePath, &p.Manufacturer, &p.Supplier, &p.UnitCost, &p.Status,
			&p.StockTracking, &p.ReorderPoint, &p.MinStock,
			&p.TotalQuantity,
		)
		if err != nil {
			log.Println("Error scanning part row:", err)
			continue
		}
		parts = append(parts, p)
	}
	return parts, nil
}

func (s *Store) GetBinLocationCount(partID int) (int, error) {
	var count int
	query := `SELECT COUNT(id) FROM part_locations WHERE part_id = ? AND quantity > 0`
	err := s.db.QueryRow(query, partID).Scan(&count)
	return count, err
}

func (s *Store) SearchParts(searchTerm string) ([]Part, error) {
	searchQuery := "%" + searchTerm + "%"

	query := `
		SELECT 
			p.id, p.name, p.description, p.part_number, p.datasheet_url, p.created_at, p.updated_at,
			p.image_path, p.manufacturer, p.supplier, p.unit_cost, p.status,
			p.stock_tracking_enabled, p.reorder_point, p.min_stock,
			IFNULL(SUM(pl.quantity), 0) AS total_quantity
		FROM parts p
		LEFT JOIN part_locations pl ON p.id = pl.part_id
		LEFT JOIN part_categories pc ON p.id = pc.part_id  -- NEW
		LEFT JOIN categories c ON pc.category_id = c.id -- NEW
		WHERE 
			p.name LIKE ? OR 
			p.description LIKE ? OR 
			p.part_number LIKE ? OR
			c.name LIKE ? -- NEW
		GROUP BY p.id
		ORDER BY p.name ASC;
	`
	rows, err := s.db.Query(query, searchQuery, searchQuery, searchQuery, searchQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	parts := []Part{}
	for rows.Next() {
		var p Part
		err := rows.Scan(
			&p.ID, &p.Name, &p.Description, &p.PartNumber, &p.DatasheetURL,
			&p.CreatedAt, &p.UpdatedAt,
			&p.ImagePath, &p.Manufacturer, &p.Supplier, &p.UnitCost, &p.Status,
			&p.StockTracking, &p.ReorderPoint, &p.MinStock,
			&p.TotalQuantity,
		)
		if err != nil {
			log.Println("Error scanning part row:", err)
			continue
		}
		parts = append(parts, p)
	}
	return parts, nil
}

func (s *Store) CreatePart(p *Part) error {
	_, err := s.db.Exec(
		`INSERT INTO parts (
			name, description, part_number, created_at, updated_at,
			manufacturer, supplier, unit_cost, status, 
			stock_tracking_enabled, reorder_point, min_stock
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.Description, p.PartNumber, p.CreatedAt, p.UpdatedAt,
		p.Manufacturer, p.Supplier, p.UnitCost, p.Status,
		p.StockTracking, p.ReorderPoint, p.MinStock,
	)
	return err
}

func (s *Store) UpdatePart(p *Part) error {
	_, err := s.db.Exec(
		`UPDATE parts SET 
			name = ?, description = ?, part_number = ?, updated_at = ?,
			manufacturer = ?, supplier = ?, unit_cost = ?, status = ?,
			stock_tracking_enabled = ?, reorder_point = ?, min_stock = ?
		 WHERE id = ?`,
		p.Name, p.Description, p.PartNumber, p.UpdatedAt,
		p.Manufacturer, p.Supplier, p.UnitCost, p.Status,
		p.StockTracking, p.ReorderPoint, p.MinStock,
		p.ID,
	)
	return err
}

func (s *Store) DeletePart(id int) error {
	_, err := s.db.Exec(`DELETE FROM parts WHERE id = ?`, id)
	return err
}

func (s *Store) UpdatePartImagePath(partID int, imagePath string) error {
	_, err := s.db.Exec(`UPDATE parts SET image_path = ? WHERE id = ?`, imagePath, partID)
	return err
}

// PartLocation Methods

func (s *Store) GetPartLocationByID(locationID int) (PartLocation, error) {
	var loc PartLocation
	query := `
		SELECT pl.id, pl.part_id, pl.bin_id, pl.quantity,
			   b.name, b.wled_segment_id, b.led_index, b.wled_controller_id
		FROM part_locations pl
		JOIN bins b ON pl.bin_id = b.id
		WHERE pl.id = ?;
	`
	row := s.db.QueryRow(query, locationID)
	err := row.Scan(
		&loc.LocationID, &loc.PartID, &loc.BinID, &loc.Quantity,
		&loc.BinName, &loc.SegmentID, &loc.LEDIndex, &loc.ControllerID,
	)
	return loc, err
}

func (s *Store) GetPartLocations(partID int) ([]PartLocation, error) {
	query := `
		SELECT pl.id, pl.part_id, pl.bin_id, pl.quantity,
			   b.name, b.wled_segment_id, b.led_index, b.wled_controller_id
		FROM part_locations pl
		JOIN bins b ON pl.bin_id = b.id
		WHERE pl.part_id = ?
		ORDER BY b.name;
	`
	rows, err := s.db.Query(query, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	locations := []PartLocation{}
	for rows.Next() {
		var loc PartLocation
		err := rows.Scan(
			&loc.LocationID, &loc.PartID, &loc.BinID, &loc.Quantity,
			&loc.BinName, &loc.SegmentID, &loc.LEDIndex, &loc.ControllerID,
		)
		if err != nil {
			log.Println("Error scanning part location:", err)
			continue
		}
		locations = append(locations, loc)
	}
	return locations, nil
}

func (s *Store) CreatePartLocation(partID, binID, quantity int) error {
	_, err := s.db.Exec(
		`INSERT INTO part_locations (part_id, bin_id, quantity) VALUES (?, ?, ?)`,
		partID, binID, quantity,
	)
	return err
}

func (s *Store) UpdatePartLocation(locationID, quantity int) error {
	_, err := s.db.Exec(`UPDATE part_locations SET quantity = ? WHERE id = ?`, quantity, locationID)
	return err
}

func (s *Store) DeletePartLocation(locationID int) error {
	_, err := s.db.Exec(`DELETE FROM part_locations WHERE id = ?`, locationID)
	return err
}

func (s *Store) GetPartLocationsForLocate(partID int) ([]struct {
	IP       string
	SegID    int
	LEDIndex int
}, error) {
	query := `
		SELECT c.ip_address, b.wled_segment_id, b.led_index
		FROM part_locations pl
		JOIN bins b ON pl.bin_id = b.id
		JOIN wled_controllers c ON b.wled_controller_id = c.id
		WHERE pl.part_id = ? AND pl.quantity > 0;
	`
	rows, err := s.db.Query(query, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var locations []struct {
		IP       string
		SegID    int
		LEDIndex int
	}
	for rows.Next() {
		var loc struct {
			IP       string
			SegID    int
			LEDIndex int
		}
		if err := rows.Scan(&loc.IP, &loc.SegID, &loc.LEDIndex); err != nil {
			return nil, err
		}
		locations = append(locations, loc)
	}
	return locations, nil
}

func (s *Store) GetPartLocationsForStop(partID int) ([]struct {
	IP       string
	SegID    int
	LEDIndex int
}, error) {
	query := `
		SELECT c.ip_address, b.wled_segment_id, b.led_index
		FROM part_locations pl
		JOIN bins b ON pl.bin_id = b.id
		JOIN wled_controllers c ON b.wled_controller_id = c.id
		WHERE pl.part_id = ?;
	`
	rows, err := s.db.Query(query, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var locations []struct {
		IP       string
		SegID    int
		LEDIndex int
	}
	for rows.Next() {
		var loc struct {
			IP       string
			SegID    int
			LEDIndex int
		}
		if err := rows.Scan(&loc.IP, &loc.SegID, &loc.LEDIndex); err != nil {
			return nil, err
		}
		locations = append(locations, loc)
	}
	return locations, nil
}

// Controller Methods

func (s *Store) GetControllers() ([]WLEDController, error) {
	rows, err := s.db.Query(`
		SELECT id, name, ip_address, status, last_seen 
		FROM wled_controllers 
		ORDER BY name ASC;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	controllers := []WLEDController{}
	for rows.Next() {
		var c WLEDController
		err := rows.Scan(&c.ID, &c.Name, &c.IPAddress, &c.Status, &c.LastSeen)
		if err != nil {
			log.Println("Error scanning controller row:", err)
			continue
		}
		controllers = append(controllers, c)
	}
	return controllers, nil
}

func (s *Store) GetControllerByID(id int) (WLEDController, error) {
	var c WLEDController
	row := s.db.QueryRow(`
		SELECT id, name, ip_address, status, last_seen 
		FROM wled_controllers 
		WHERE id = ?;
	`, id)
	err := row.Scan(&c.ID, &c.Name, &c.IPAddress, &c.Status, &c.LastSeen)
	return c, err
}

func (s *Store) CreateController(name, ipAddress string) error {
	_, err := s.db.Exec(
		`INSERT INTO wled_controllers (name, ip_address, status) VALUES (?, ?, 'unknown')`,
		name, ipAddress,
	)
	return err
}

func (s *Store) DeleteController(id int) error {
	_, err := s.db.Exec(`DELETE FROM wled_controllers WHERE id = ?`, id)
	if err != nil {
		// Check if the error is a *pointer* to a sqlite.Error
		sqliteErr, ok := err.(*sqlite.Error)
		if ok {
			// Check if the specific error code is a foreign key constraint
			if sqliteErr.Code() == sqlitelib.SQLITE_CONSTRAINT_FOREIGNKEY {
				return ErrForeignKeyConstraint
			}
		}
		// Return other errors normally
		return err
	}
	return nil
}

func (s *Store) GetAllControllersForHealthCheck() ([]WLEDController, error) {
	rows, err := s.db.Query(`SELECT id, ip_address FROM wled_controllers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	controllers := []WLEDController{}
	for rows.Next() {
		var c WLEDController
		if err := rows.Scan(&c.ID, &c.IPAddress); err != nil {
			return nil, err
		}
		controllers = append(controllers, c)
	}
	return controllers, nil
}

func (s *Store) UpdateControllerStatus(id int, status string, lastSeen sql.NullTime) error {
	if status == "online" {
		_, err := s.db.Exec(
			`UPDATE wled_controllers SET status = ?, last_seen = ? WHERE id = ?`,
			status, lastSeen, id,
		)
		return err
	}
	_, err := s.db.Exec(
		`UPDATE wled_controllers SET status = ? WHERE id = ?`,
		status, id,
	)
	return err
}

// Bin Methods

func (s *Store) GetBins() ([]Bin, error) {
	binRows, err := s.db.Query(`
		SELECT b.id, b.name, b.wled_controller_id, b.wled_segment_id, b.led_index, c.name
		FROM bins b
		LEFT JOIN wled_controllers c ON b.wled_controller_id = c.id
		ORDER BY b.wled_segment_id ASC, b.led_index ASC;
	`)
	if err != nil {
		return nil, err
	}
	defer binRows.Close()

	bins := []Bin{}
	for binRows.Next() {
		var b Bin
		err := binRows.Scan(&b.ID, &b.Name, &b.WLEDControllerID, &b.WLEDSegmentID, &b.LEDIndex, &b.WLEDControllerName)
		if err != nil {
			log.Println("Error scanning bin row:", err)
			continue
		}
		bins = append(bins, b)
	}
	return bins, nil
}

func (s *Store) GetAvailableBins(partID int) ([]Bin, error) {
	availBinsQuery := `
		SELECT id, name, wled_segment_id, led_index FROM bins
		WHERE id NOT IN (SELECT bin_id FROM part_locations WHERE part_id = ?)
		ORDER BY wled_segment_id ASC, led_index ASC;
	`
	binRows, err := s.db.Query(availBinsQuery, partID)
	if err != nil {
		return nil, err
	}
	defer binRows.Close()

	availableBins := []Bin{}
	for binRows.Next() {
		var b Bin
		err := binRows.Scan(&b.ID, &b.Name, &b.WLEDSegmentID, &b.LEDIndex)
		if err != nil {
			log.Println("Error scanning available bin:", err)
			continue
		}
		availableBins = append(availableBins, b)
	}
	return availableBins, nil
}

func (s *Store) CreateBin(name string, controllerID, segmentID, ledIndex int) error {
	_, err := s.db.Exec(
		`INSERT INTO bins (name, wled_controller_id, wled_segment_id, led_index) 
		 VALUES (?, ?, ?, ?)`,
		name, controllerID, segmentID, ledIndex,
	)
	if err != nil {
		// Check for specific constraint errors
		sqliteErr, ok := err.(*sqlite.Error)
		if ok {
			if sqliteErr.Code() == sqlitelib.SQLITE_CONSTRAINT_UNIQUE {
				return ErrUniqueConstraint
			}
			if sqliteErr.Code() == sqlitelib.SQLITE_CONSTRAINT_FOREIGNKEY {
				return ErrForeignKeyConstraint
			}
		}
		return err
	}
	return nil
}

func (s *Store) CreateBinsBulk(controllerID, segmentID, ledCount int, namePrefix string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO bins (name, wled_controller_id, wled_segment_id, led_index) 
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for i := 0; i < ledCount; i++ {
		binName := namePrefix + strconv.Itoa(i)
		ledIndex := i
		if _, err := stmt.Exec(binName, controllerID, segmentID, ledIndex); err != nil {
			sqliteErr, ok := err.(*sqlite.Error)
			if ok {
				if sqliteErr.Code() == sqlitelib.SQLITE_CONSTRAINT_UNIQUE {
					tx.Rollback()
					return ErrUniqueConstraint
				}
			}
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) DeleteBin(id int) error {
	_, err := s.db.Exec(`DELETE FROM bins WHERE id = ?`, id)
	return err
}

func (s *Store) GetAllBinLocationsForStopAll() ([]struct {
	IP       string
	SegID    int
	LEDIndex int
}, error) {
	query := `
		SELECT c.ip_address, b.wled_segment_id, b.led_index
		FROM bins b
		JOIN wled_controllers c ON b.wled_controller_id = c.id;
	`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var locations []struct {
		IP       string
		SegID    int
		LEDIndex int
	}
	for rows.Next() {
		var loc struct {
			IP       string
			SegID    int
			LEDIndex int
		}
		if err := rows.Scan(&loc.IP, &loc.SegID, &loc.LEDIndex); err != nil {
			return nil, err
		}
		locations = append(locations, loc)
	}
	return locations, nil
}

func (s *Store) GetPartNamesInBin(binID int) ([]string, error) {
	query := `
        SELECT p.name FROM parts p
        JOIN part_locations pl ON p.id = pl.part_id
        WHERE pl.bin_id = ? AND pl.quantity > 0
        ORDER BY p.name;
    `
	rows, err := s.db.Query(query, binID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

func (s *Store) GetURLsByPartID(partID int) ([]PartURL, error) {
	query := `SELECT id, part_id, url, description FROM part_urls WHERE part_id = ? ORDER BY id`
	rows, err := s.db.Query(query, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var urls []PartURL
	for rows.Next() {
		var u PartURL
		if err := rows.Scan(&u.ID, &u.PartID, &u.URL, &u.Description); err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	return urls, nil
}

func (s *Store) CreatePartURL(partID int, url string, description string) error {
	_, err := s.db.Exec(
		`INSERT INTO part_urls (part_id, url, description) VALUES (?, ?, ?)`,
		partID, url, description,
	)
	return err
}

func (s *Store) DeletePartURL(urlID int) error {
	_, err := s.db.Exec(`DELETE FROM part_urls WHERE id = ?`, urlID)
	return err
}

// Document Methods

func (s *Store) GetDocumentsByPartID(partID int) ([]PartDocument, error) {
	query := `SELECT id, part_id, filename, filepath, description, mimetype FROM part_documents WHERE part_id = ? ORDER BY filename`
	rows, err := s.db.Query(query, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []PartDocument
	for rows.Next() {
		var d PartDocument
		if err := rows.Scan(&d.ID, &d.PartID, &d.Filename, &d.Filepath, &d.Description, &d.Mimetype); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, nil
}

func (s *Store) GetDocumentByID(docID int) (PartDocument, error) {
	var d PartDocument
	query := `SELECT id, part_id, filename, filepath, description, mimetype FROM part_documents WHERE id = ?`
	row := s.db.QueryRow(query, docID)
	err := row.Scan(&d.ID, &d.PartID, &d.Filename, &d.Filepath, &d.Description, &d.Mimetype)
	return d, err
}

func (s *Store) CreatePartDocument(doc *PartDocument) error {
	_, err := s.db.Exec(
		`INSERT INTO part_documents (part_id, filename, filepath, description, mimetype) VALUES (?, ?, ?, ?, ?)`,
		doc.PartID, doc.Filename, doc.Filepath, doc.Description, doc.Mimetype,
	)
	return err
}

func (s *Store) DeletePartDocument(docID int) error {
	_, err := s.db.Exec(`DELETE FROM part_documents WHERE id = ?`, docID)
	return err
}

// Category Methods

func (s *Store) GetCategories() ([]Category, error) {
	query := `SELECT id, name FROM categories ORDER BY name`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cats []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, nil
}

func (s *Store) GetCategoriesByPartID(partID int) ([]Category, error) {
	query := `
		SELECT c.id, c.name FROM categories c
		JOIN part_categories pc ON c.id = pc.category_id
		WHERE pc.part_id = ?
		ORDER BY c.name
	`
	rows, err := s.db.Query(query, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cats []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, nil
}

func (s *Store) CreateCategory(name string) (Category, error) {
	var c Category
	// Check for unique constraint
	err := s.db.QueryRow(`SELECT id, name FROM categories WHERE name = ?`, name).Scan(&c.ID, &c.Name)
	if err == nil {
		// Already exists, just return it
		return c, nil
	}

	res, err := s.db.Exec(`INSERT INTO categories (name) VALUES (?)`, name)
	if err != nil {
		return c, err
	}
	id, _ := res.LastInsertId()
	c.ID = int(id)
	c.Name = name
	return c, nil
}

func (s *Store) AssignCategoryToPart(partID int, categoryID int) error {
	// Ignore errors for "UNIQUE constraint failed"
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO part_categories (part_id, category_id) VALUES (?, ?)`,
		partID, categoryID,
	)
	return err
}

func (s *Store) RemoveCategoryFromPart(partID int, categoryID int) error {
	_, err := s.db.Exec(
		`DELETE FROM part_categories WHERE part_id = ? AND category_id = ?`,
		partID, categoryID,
	)
	return err
}

func (s *Store) CleanupOrphanedCategories() error {
	// This query deletes any category that does not have a matching entry in the part_categories join table.
	// In other words, it removes categories that are not assigned to any part.
	// Used by the category management cleanup feature in health checks to keep the database tidy.
	query := `
        DELETE FROM categories 
        WHERE id NOT IN (SELECT DISTINCT category_id FROM part_categories);
    `
	_, err := s.db.Exec(query)
	return err
}

// Dashboard Method

func (s *Store) GetDashboardBinData() ([]DashboardBinData, error) {
	// This query gets the individual quantity for every bin
	// that belongs to a part with stock tracking enabled.
	// Used by the dashboard to show which bins are below
	// reorder point or minimum stock.
	query := `
		SELECT 
			p.reorder_point,
			p.min_stock,
			pl.quantity,
			c.ip_address,
			b.wled_segment_id,
			b.led_index
		FROM part_locations pl
		JOIN parts p ON pl.part_id = p.id
		JOIN bins b ON pl.bin_id = b.id
		JOIN wled_controllers c ON b.wled_controller_id = c.id
		WHERE 
			p.stock_tracking_enabled = 1;
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []DashboardBinData
	for rows.Next() {
		var d DashboardBinData
		err := rows.Scan(
			&d.ReorderPoint, &d.MinStock, &d.BinQuantity,
			&d.BinIP, &d.BinSegmentID, &d.BinLEDIndex,
		)
		if err != nil {
			return nil, err
		}
		results = append(results, d)
	}
	return results, nil
}
