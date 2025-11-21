package store

import (
	"wledger/internal/models"
)

// Dashboard Methods
func (s *Store) GetDashboardBinData() ([]models.DashboardBinData, error) {
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

	var results []models.DashboardBinData
	for rows.Next() {
		var d models.DashboardBinData
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
