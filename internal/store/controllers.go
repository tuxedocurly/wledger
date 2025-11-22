package store

import (
	"database/sql"
	"errors"
	"log"
	"wledger/internal/models"

	"modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

// Controller Methods
func (s *Store) GetControllers() ([]models.WLEDController, error) {
	// LEFT JOIN to count bins associated with each controller
	query := `
		SELECT c.id, c.name, c.ip_address, c.status, c.last_seen, COUNT(b.id) as bin_count
		FROM wled_controllers c
		LEFT JOIN bins b ON c.id = b.wled_controller_id
		GROUP BY c.id
		ORDER BY c.name ASC;
	`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	controllers := []models.WLEDController{}
	for rows.Next() {
		var c models.WLEDController
		var lastSeenStr sql.NullString

		err := rows.Scan(&c.ID, &c.Name, &c.IPAddress, &c.Status, &c.LastSeen, &c.BinCount)
		if err != nil {
			log.Println("Error scanning controller row:", err)
			continue
		}
		// Manually convert the string to sql.NullTime
		// fixes an issue with SQLite driver crash after .zip restoration
		if lastSeenStr.Valid {
			c.LastSeen.Time = parseTime(lastSeenStr.String)
			c.LastSeen.Valid = true
		} else {
			c.LastSeen.Valid = false
		}

		controllers = append(controllers, c)
	}
	return controllers, nil
}

func (s *Store) GetControllerByID(id int) (models.WLEDController, error) {
	var c models.WLEDController
	var lastSeenStr sql.NullString

	query := `
		SELECT c.id, c.name, c.ip_address, c.status, c.last_seen, COUNT(b.id) as bin_count
		FROM wled_controllers c
		LEFT JOIN bins b ON c.id = b.wled_controller_id
		WHERE c.id = ?
		GROUP BY c.id;
	`
	row := s.db.QueryRow(query, id)

	err := row.Scan(&c.ID, &c.Name, &c.IPAddress, &c.Status, &lastSeenStr, &c.BinCount)
	if err != nil {
		return c, err
	}

	// Manually convert the string to sql.NullTime
	// fixes an issue with SQLite driver crash after .zip restore
	if lastSeenStr.Valid {
		c.LastSeen.Time = parseTime(lastSeenStr.String)
		c.LastSeen.Valid = true
	} else {
		c.LastSeen.Valid = false
	}

	return c, nil
}

func (s *Store) CreateController(name, ipAddress string) error {
	_, err := s.db.Exec(
		`INSERT INTO wled_controllers (name, ip_address, status) VALUES (?, ?, 'unknown')`,
		name, ipAddress,
	)
	return err
}

func (s *Store) UpdateController(c *models.WLEDController) error {
	_, err := s.db.Exec(
		`UPDATE wled_controllers SET name = ?, ip_address = ? WHERE id = ?`,
		c.Name, c.IPAddress, c.ID,
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
		// Return other errors
		return err
	}
	return nil
}

func (s *Store) GetAllControllersForHealthCheck() ([]models.WLEDController, error) {
	rows, err := s.db.Query(`SELECT id, ip_address FROM wled_controllers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	controllers := []models.WLEDController{}
	for rows.Next() {
		var c models.WLEDController
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

func (s *Store) MigrateBins(oldControllerID, newControllerID int) error {
	// We use a transaction to ensure safety
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}

	// Verify the new controller exists (to prevent stranding bins)
	var exists int
	err = tx.QueryRow("SELECT 1 FROM wled_controllers WHERE id = ?", newControllerID).Scan(&exists)
	if err != nil {
		tx.Rollback()
		return errors.New("target controller does not exist")
	}

	// Move the bins
	_, err = tx.Exec(
		`UPDATE bins SET wled_controller_id = ? WHERE wled_controller_id = ?`,
		newControllerID, oldControllerID,
	)
	if err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}
