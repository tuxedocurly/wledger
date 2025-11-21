// internal/store/parts_test.go
package store

import (
	"database/sql"
	"testing"
	"time"

	"wledger/internal/models"
)

// Helper to generate a valid part
func getValidPart(name string) *models.Part {
	return &models.Part{
		Name:          name,
		Description:   sql.NullString{String: "Desc", Valid: true},
		PartNumber:    sql.NullString{String: "PN-123", Valid: true},
		Manufacturer:  sql.NullString{String: "TestCo", Valid: true},
		Supplier:      sql.NullString{String: "DigiKey", Valid: true},
		UnitCost:      sql.NullFloat64{Float64: 0.10, Valid: true},
		Status:        sql.NullString{String: "active", Valid: true},
		StockTracking: false,
		ReorderPoint:  0,
		MinStock:      0,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
}

func TestStore_PartCRUD(t *testing.T) {
	s := newTestStore(t)

	// Create
	part := getValidPart("Test Part")
	if err := s.CreatePart(part); err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	// Get By ID
	p, err := s.GetPartByID(1)
	if err != nil {
		t.Fatalf("GetPartByID failed: %v", err)
	}
	if p.Name != "Test Part" {
		t.Errorf("Expected 'Test Part', got %q", p.Name)
	}

	// Update
	p.Name = "Updated Part"
	// Ensure we keep valid data for update
	if err := s.UpdatePart(&p); err != nil {
		t.Fatalf("UpdatePart failed: %v", err)
	}

	p2, _ := s.GetPartByID(1)
	if p2.Name != "Updated Part" {
		t.Errorf("Update failed, got %q", p2.Name)
	}

	// Update Image Path
	if err := s.UpdatePartImagePath(1, "images/test.jpg"); err != nil {
		t.Fatalf("UpdatePartImagePath failed: %v", err)
	}
	p3, _ := s.GetPartByID(1)
	if p3.ImagePath.String != "images/test.jpg" {
		t.Errorf("Image update failed")
	}

	// Delete
	if err := s.DeletePart(1); err != nil {
		t.Fatalf("DeletePart failed: %v", err)
	}
	_, err = s.GetPartByID(1)
	if err == nil {
		t.Error("Expected error getting deleted part, got nil")
	}
}

func TestStore_GetParts(t *testing.T) {
	s := newTestStore(t)

	// Create 3 parts
	if err := s.CreatePart(getValidPart("C Part")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePart(getValidPart("A Part")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePart(getValidPart("B Part")); err != nil {
		t.Fatal(err)
	}

	parts, err := s.GetParts()
	if err != nil {
		t.Fatalf("GetParts failed: %v", err)
	}

	if len(parts) != 3 {
		t.Errorf("Expected 3 parts, got %d", len(parts))
	}

	// Verify Sorting (Alphabetical by Name)
	if parts[0].Name != "A Part" || parts[1].Name != "B Part" || parts[2].Name != "C Part" {
		t.Error("GetParts is not sorted alphabetically")
	}
}

func TestStore_GetBinLocationCount(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreatePart(getValidPart("P1")); err != nil {
		t.Fatal(err)
	}

	s.CreateController("C1", "1.1.1.1")
	s.CreateBin("B1", 1, 0, 0)
	s.CreateBin("B2", 1, 0, 1)

	// Add stock to 2 bins
	s.CreatePartLocation(1, 1, 10)
	s.CreatePartLocation(1, 2, 5)

	count, err := s.GetBinLocationCount(1)
	if err != nil {
		t.Fatalf("GetBinLocationCount failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected count 2, got %d", count)
	}
}

func TestStore_PartURLs(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreatePart(getValidPart("P1")); err != nil {
		t.Fatal(err)
	}

	// Create URL
	if err := s.CreatePartURL(1, "http://google.com", "Search"); err != nil {
		t.Fatalf("CreatePartURL failed: %v", err)
	}

	// Get URLs
	urls, err := s.GetURLsByPartID(1)
	if err != nil {
		t.Fatalf("GetURLsByPartID failed: %v", err)
	}
	if len(urls) != 1 || urls[0].URL != "http://google.com" {
		t.Errorf("URL mismatch")
	}

	// Delete URL
	if err := s.DeletePartURL(urls[0].ID); err != nil {
		t.Fatalf("DeletePartURL failed: %v", err)
	}
	urls, _ = s.GetURLsByPartID(1)
	if len(urls) != 0 {
		t.Errorf("URL not deleted")
	}
}

func TestStore_PartDocs(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreatePart(getValidPart("P1")); err != nil {
		t.Fatal(err)
	}

	doc := &models.PartDocument{
		PartID: 1, Filename: "test.pdf", Filepath: "docs/test.pdf",
	}
	if err := s.CreatePartDocument(doc); err != nil {
		t.Fatalf("CreatePartDocument failed: %v", err)
	}

	docs, err := s.GetDocumentsByPartID(1)
	if err != nil || len(docs) != 1 {
		t.Fatalf("Failed to get docs")
	}

	// Test Get Single
	d, err := s.GetDocumentByID(docs[0].ID)
	if err != nil || d.Filename != "test.pdf" {
		t.Errorf("GetDocumentByID failed")
	}

	if err := s.DeletePartDocument(docs[0].ID); err != nil {
		t.Fatalf("Delete failed")
	}
}

func TestStore_Categories(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreatePart(getValidPart("P1")); err != nil {
		t.Fatal(err)
	}

	// Create Category
	cat, err := s.CreateCategory("Resistors")
	if err != nil {
		t.Fatalf("CreateCategory failed: %v", err)
	}
	// Ensure creating same category returns existing ID
	cat2, _ := s.CreateCategory("Resistors")
	if cat.ID != cat2.ID {
		t.Error("Duplicate category creation should return existing ID")
	}

	// Assign
	if err := s.AssignCategoryToPart(1, cat.ID); err != nil {
		t.Fatalf("Assign failed: %v", err)
	}

	// Get
	cats, err := s.GetCategoriesByPartID(1)
	if err != nil || len(cats) != 1 || cats[0].Name != "Resistors" {
		t.Errorf("GetCategoriesByPartID failed")
	}

	allCats, _ := s.GetCategories()
	if len(allCats) != 1 {
		t.Errorf("GetCategories failed")
	}

	// Remove
	if err := s.RemoveCategoryFromPart(1, cat.ID); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	cats, _ = s.GetCategoriesByPartID(1)
	if len(cats) != 0 {
		t.Errorf("Category not removed")
	}

	// Cleanup Orphaned
	if err := s.CleanupOrphanedCategories(); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}
	allCats, _ = s.GetCategories()
	if len(allCats) != 0 {
		t.Errorf("Orphaned category was not cleaned up")
	}
}
