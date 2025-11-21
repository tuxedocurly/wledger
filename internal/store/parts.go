package store

import (
	"log"
	"wledger/internal/models"
)

// Part methods
func (s *Store) GetPartByID(id int) (models.Part, error) {
	var p models.Part
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

func (s *Store) GetParts() ([]models.Part, error) {
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

	parts := []models.Part{}
	for rows.Next() {
		var p models.Part
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

func (s *Store) SearchParts(searchTerm string) ([]models.Part, error) {
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

	parts := []models.Part{}
	for rows.Next() {
		var p models.Part
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

func (s *Store) CreatePart(p *models.Part) error {
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

func (s *Store) UpdatePart(p *models.Part) error {
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

func (s *Store) GetBinLocationCount(partID int) (int, error) {
	var count int
	query := `SELECT COUNT(id) FROM part_locations WHERE part_id = ? AND quantity > 0`
	err := s.db.QueryRow(query, partID).Scan(&count)
	return count, err
}

// Category methods
func (s *Store) GetCategories() ([]models.Category, error) {
	query := `SELECT id, name FROM categories ORDER BY name`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cats []models.Category
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, nil
}

func (s *Store) GetCategoriesByPartID(partID int) ([]models.Category, error) {
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

	var cats []models.Category
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, nil
}

func (s *Store) CreateCategory(name string) (models.Category, error) {
	var c models.Category
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
	// remove categories that are not assigned to any part
	// Used by the category management cleanup feature in health checks to keep the database tidy.
	query := `
        DELETE FROM categories 
        WHERE id NOT IN (SELECT DISTINCT category_id FROM part_categories);
    `
	_, err := s.db.Exec(query)
	return err
}

// URL methods
func (s *Store) GetURLsByPartID(partID int) ([]models.PartURL, error) {
	query := `SELECT id, part_id, url, description FROM part_urls WHERE part_id = ? ORDER BY id`
	rows, err := s.db.Query(query, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var urls []models.PartURL
	for rows.Next() {
		var u models.PartURL
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

// Document methods
func (s *Store) GetDocumentsByPartID(partID int) ([]models.PartDocument, error) {
	query := `SELECT id, part_id, filename, filepath, description, mimetype FROM part_documents WHERE part_id = ? ORDER BY filename`
	rows, err := s.db.Query(query, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []models.PartDocument
	for rows.Next() {
		var d models.PartDocument
		if err := rows.Scan(&d.ID, &d.PartID, &d.Filename, &d.Filepath, &d.Description, &d.Mimetype); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, nil
}

func (s *Store) GetDocumentByID(docID int) (models.PartDocument, error) {
	var d models.PartDocument
	query := `SELECT id, part_id, filename, filepath, description, mimetype FROM part_documents WHERE id = ?`
	row := s.db.QueryRow(query, docID)
	err := row.Scan(&d.ID, &d.PartID, &d.Filename, &d.Filepath, &d.Description, &d.Mimetype)
	return d, err
}

func (s *Store) CreatePartDocument(doc *models.PartDocument) error {
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
