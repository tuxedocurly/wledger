package store

import (
	"log"
	"strconv"
	"wledger/internal/models"

	"modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

// BIN METHODS
func (s *Store) GetBins() ([]models.Bin, error) {
	// Fetch all bins
	query := `
		SELECT b.id, b.name, b.wled_controller_id, b.wled_segment_id, b.led_index, c.name
		FROM bins b
		LEFT JOIN wled_controllers c ON b.wled_controller_id = c.id
		ORDER BY b.wled_segment_id ASC, b.led_index ASC;
	`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bins []models.Bin

	// Map to track overlaps: "CtrlID-SegID-LEDIndex" -> Count
	occurrenceMap := make(map[string]int)

	for rows.Next() {
		var b models.Bin
		err := rows.Scan(&b.ID, &b.Name, &b.WLEDControllerID, &b.WLEDSegmentID, &b.LEDIndex, &b.WLEDControllerName)
		if err != nil {
			log.Println("Error scanning bin row:", err)
			continue
		}

		// DETECT ORPHAN: If the LEFT JOIN returned NULL for the name, the controller doesn't exist
		if !b.WLEDControllerName.Valid {
			b.IsOrphaned = true
		} else {
			// Only count occurrences for valid controllers
			key := strconv.Itoa(b.WLEDControllerID) + "-" + strconv.Itoa(b.WLEDSegmentID) + "-" + strconv.Itoa(b.LEDIndex)
			occurrenceMap[key]++
		}

		bins = append(bins, b)
	}

	// Set flags
	for i := range bins {
		if bins[i].IsOrphaned {
			continue // Orphans don't have overlap warnings, they have orphan warnings
		}
		key := strconv.Itoa(bins[i].WLEDControllerID) + "-" + strconv.Itoa(bins[i].WLEDSegmentID) + "-" + strconv.Itoa(bins[i].LEDIndex)
		if occurrenceMap[key] > 1 {
			bins[i].HasOverlap = true
		}
	}

	return bins, nil
}

func (s *Store) GetAvailableBins(partID int) ([]models.Bin, error) {
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

	availableBins := []models.Bin{}
	for binRows.Next() {
		var b models.Bin
		err := binRows.Scan(&b.ID, &b.Name, &b.WLEDSegmentID, &b.LEDIndex)
		if err != nil {
			log.Println("Error scanning available bin:", err)
			continue
		}
		availableBins = append(availableBins, b)
	}
	return availableBins, nil
}

func (s *Store) GetBinByID(id int) (models.Bin, error) {
	var b models.Bin
	query := `
		SELECT b.id, b.name, b.wled_controller_id, b.wled_segment_id, b.led_index, c.name
		FROM bins b
		LEFT JOIN wled_controllers c ON b.wled_controller_id = c.id
		WHERE b.id = ?;
	`
	row := s.db.QueryRow(query, id)
	err := row.Scan(&b.ID, &b.Name, &b.WLEDControllerID, &b.WLEDSegmentID, &b.LEDIndex, &b.WLEDControllerName)

	// Re-run orphan/overlap logic for single item (simplified)
	if !b.WLEDControllerName.Valid {
		b.IsOrphaned = true
	}
	// Note: Detecting overlap for a single item requires querying all items,
	// so we might skip the overlap check for the single-row return or accept it won't show until refresh.

	return b, err
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

func (s *Store) UpdateBin(b *models.Bin) error {
	_, err := s.db.Exec(
		`UPDATE bins SET name = ?, wled_controller_id = ?, wled_segment_id = ?, led_index = ? WHERE id = ?`,
		b.Name, b.WLEDControllerID, b.WLEDSegmentID, b.LEDIndex, b.ID,
	)
	return err
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

// LOCATION METHODS

func (s *Store) GetPartLocationByID(locationID int) (models.PartLocation, error) {
	var loc models.PartLocation
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

func (s *Store) GetPartLocations(partID int) ([]models.PartLocation, error) {
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

	locations := []models.PartLocation{}
	for rows.Next() {
		var loc models.PartLocation
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
