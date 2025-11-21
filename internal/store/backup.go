package store

import (
	"time"
	"wledger/internal/models"
)

func (s *Store) GetAllDataForBackup() (models.BackupData, error) {
	data := models.BackupData{
		Version:     1,
		GeneratedAt: time.Now(),
	}

	// Get all data from each table
	parts, err := s.GetParts()
	if err != nil {
		return data, err
	}
	data.Parts = parts

	ctrls, err := s.GetControllers()
	if err != nil {
		return data, err
	}
	data.Controllers = ctrls

	bins, err := s.GetBins()
	if err != nil {
		return data, err
	}
	data.Bins = bins

	cats, err := s.GetCategories()
	if err != nil {
		return data, err
	}
	data.Categories = cats

	// Manual Queries for things that have no "GetAll" methods

	// Part URLs
	rows, err := s.db.Query("SELECT id, part_id, url, description FROM part_urls")
	if err != nil {
		return data, err
	}
	defer rows.Close()
	for rows.Next() {
		var u models.PartURL
		rows.Scan(&u.ID, &u.PartID, &u.URL, &u.Description)
		data.PartUrls = append(data.PartUrls, u)
	}
	rows.Close()

	// Part Docs
	rows, err = s.db.Query("SELECT id, part_id, filename, filepath, description, mimetype FROM part_documents")
	if err != nil {
		return data, err
	}
	defer rows.Close()
	for rows.Next() {
		var d models.PartDocument
		rows.Scan(&d.ID, &d.PartID, &d.Filename, &d.Filepath, &d.Description, &d.Mimetype)
		data.PartDocs = append(data.PartDocs, d)
	}
	rows.Close()

	// Part Categories (Join Table)
	rows, err = s.db.Query("SELECT part_id, category_id FROM part_categories")
	if err != nil {
		return data, err
	}
	defer rows.Close()
	for rows.Next() {
		var pc models.PartCategory
		rows.Scan(&pc.PartID, &pc.CategoryID)
		data.PartCats = append(data.PartCats, pc)
	}
	rows.Close()

	// Part Locations
	rows, err = s.db.Query("SELECT id, part_id, bin_id, quantity FROM part_locations")
	if err != nil {
		return data, err
	}
	defer rows.Close()
	for rows.Next() {
		var pl models.PartLocation
		// scanning into base fields, ignoring joined names for backup
		rows.Scan(&pl.LocationID, &pl.PartID, &pl.BinID, &pl.Quantity)
		data.PartLocations = append(data.PartLocations, pl)
	}

	return data, nil
}

// TODO: Implement a dry-run mode or validator that checks for data validity before deleting/restoring data
// TODO: Consider wrapping in a transaction for safety
// TODO: Consider backing up existing data before restoring new data
// TODO: Test large data sets for performance, especially on low-end hardware. Might need batching.
// TODO: Handle potential ID conflicts or duplicates. Should probably rework some portions of the WLED controller CRUD as well.
// TODO: Consider logging progress or providing user feedback during long operations (frontend)
// TODO: Consider adding an option to merge data instead of full replacement. Not sure how useful this is though.

// RestoreFromBackup restores the database state from the provided BackupData, deleting existing data first
func (s *Store) RestoreFromBackup(data models.BackupData) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}

	// NUKE EVERYTHING >:)
	// Delete children first, then parents weeeeeee
	tables := []string{
		"part_locations", "part_categories", "part_documents", "part_urls",
		"parts", "bins", "wled_controllers", "categories",
	}
	for _, table := range tables {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			tx.Rollback()
			return err
		}
		// Reset auto-increment counters brrrrrrr
		if _, err := tx.Exec("DELETE FROM sqlite_sequence WHERE name=?", table); err != nil {
			tx.Rollback()
			return err
		}
	}

	// RESTORE EVERYTHING (Parents first)

	// Controllers
	stmt, _ := tx.Prepare("INSERT INTO wled_controllers (id, name, ip_address, status, last_seen) VALUES (?, ?, ?, ?, ?)")
	for _, c := range data.Controllers {
		if _, err := stmt.Exec(c.ID, c.Name, c.IPAddress, c.Status, c.LastSeen); err != nil {
			tx.Rollback()
			return err
		}
	}
	stmt.Close()

	// Bins
	stmt, _ = tx.Prepare("INSERT INTO bins (id, name, wled_controller_id, wled_segment_id, led_index) VALUES (?, ?, ?, ?, ?)")
	for _, b := range data.Bins {
		if _, err := stmt.Exec(b.ID, b.Name, b.WLEDControllerID, b.WLEDSegmentID, b.LEDIndex); err != nil {
			tx.Rollback()
			return err
		}
	}
	stmt.Close()

	// Categories
	stmt, _ = tx.Prepare("INSERT INTO categories (id, name) VALUES (?, ?)")
	for _, c := range data.Categories {
		if _, err := stmt.Exec(c.ID, c.Name); err != nil {
			tx.Rollback()
			return err
		}
	}
	stmt.Close()

	// Parts
	stmt, _ = tx.Prepare(`INSERT INTO parts (id, name, description, part_number, datasheet_url, created_at, updated_at, image_path, manufacturer, supplier, unit_cost, status, stock_tracking_enabled, reorder_point, min_stock) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	for _, p := range data.Parts {
		if _, err := stmt.Exec(p.ID, p.Name, p.Description, p.PartNumber, p.DatasheetURL, p.CreatedAt, p.UpdatedAt, p.ImagePath, p.Manufacturer, p.Supplier, p.UnitCost, p.Status, p.StockTracking, p.ReorderPoint, p.MinStock); err != nil {
			tx.Rollback()
			return err
		}
	}
	stmt.Close()

	// Part URLs
	stmt, _ = tx.Prepare("INSERT INTO part_urls (id, part_id, url, description) VALUES (?, ?, ?, ?)")
	for _, u := range data.PartUrls {
		if _, err := stmt.Exec(u.ID, u.PartID, u.URL, u.Description); err != nil {
			tx.Rollback()
			return err
		}
	}
	stmt.Close()

	// Part Docs
	stmt, _ = tx.Prepare("INSERT INTO part_documents (id, part_id, filename, filepath, description, mimetype) VALUES (?, ?, ?, ?, ?, ?)")
	for _, d := range data.PartDocs {
		if _, err := stmt.Exec(d.ID, d.PartID, d.Filename, d.Filepath, d.Description, d.Mimetype); err != nil {
			tx.Rollback()
			return err
		}
	}
	stmt.Close()

	// Part Categories
	stmt, _ = tx.Prepare("INSERT INTO part_categories (part_id, category_id) VALUES (?, ?)")
	for _, pc := range data.PartCats {
		if _, err := stmt.Exec(pc.PartID, pc.CategoryID); err != nil {
			tx.Rollback()
			return err
		}
	}
	stmt.Close()

	// Part Locations
	stmt, _ = tx.Prepare("INSERT INTO part_locations (id, part_id, bin_id, quantity) VALUES (?, ?, ?, ?)")
	for _, pl := range data.PartLocations {
		if _, err := stmt.Exec(pl.LocationID, pl.PartID, pl.BinID, pl.Quantity); err != nil {
			tx.Rollback()
			return err
		}
	}
	stmt.Close()

	return tx.Commit()
}
