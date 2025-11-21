// internal/store/bins_test.go
package store

import (
	"database/sql"
	"testing"
	"time"
	"wledger/internal/models"
)

func createValidPartForBinTest(s *Store) error {
	part := &models.Part{
		Name:          "P1",
		Description:   sql.NullString{String: "Desc", Valid: true},
		PartNumber:    sql.NullString{String: "PN-1", Valid: true},
		Manufacturer:  sql.NullString{String: "Man", Valid: true},
		Supplier:      sql.NullString{String: "Sup", Valid: true},
		UnitCost:      sql.NullFloat64{Float64: 1, Valid: true},
		Status:        sql.NullString{String: "active", Valid: true},
		StockTracking: false,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	return s.CreatePart(part)
}

func TestStore_BinCRUD(t *testing.T) {
	s := newTestStore(t)
	s.CreateController("C1", "1.1.1.1")

	// Create
	if err := s.CreateBin("B1", 1, 0, 0); err != nil {
		t.Fatalf("CreateBin failed: %v", err)
	}

	// Get All
	bins, err := s.GetBins()
	if err != nil || len(bins) != 1 {
		t.Fatalf("GetBins failed")
	}
	if bins[0].Name != "B1" {
		t.Errorf("Expected bin name B1, got %s", bins[0].Name)
	}

	// Get ID
	b, err := s.GetBinByID(bins[0].ID)
	if err != nil || b.Name != "B1" {
		t.Errorf("GetBinByID failed")
	}

	// Update
	b.Name = "B1-Updated"
	if err := s.UpdateBin(&b); err != nil {
		t.Fatalf("UpdateBin failed: %v", err)
	}
	b2, _ := s.GetBinByID(b.ID)
	if b2.Name != "B1-Updated" {
		t.Errorf("Update failed")
	}

	// Delete
	if err := s.DeleteBin(b.ID); err != nil {
		t.Fatalf("DeleteBin failed: %v", err)
	}
	bins, _ = s.GetBins()
	if len(bins) != 0 {
		t.Error("Bin not deleted")
	}
}

func TestStore_CreateBinsBulk(t *testing.T) {
	s := newTestStore(t)
	s.CreateController("C1", "1.1.1.1")

	// Create 5 bins: Test-0 to Test-4
	if err := s.CreateBinsBulk(1, 0, 5, "Test-"); err != nil {
		t.Fatalf("CreateBinsBulk failed: %v", err)
	}

	bins, _ := s.GetBins()
	if len(bins) != 5 {
		t.Errorf("Expected 5 bins, got %d", len(bins))
	}
	if bins[4].Name != "Test-4" || bins[4].LEDIndex != 4 {
		t.Errorf("Bulk bin creation logic error")
	}
}

func TestStore_BinFlags(t *testing.T) {
	s := newTestStore(t)
	s.CreateController("C1", "1.1.1.1")

	// 1. Test Overlap: Create two bins at Segment 0, LED 0
	s.CreateBin("B1", 1, 0, 0)
	s.CreateBin("B2", 1, 0, 0)

	bins, _ := s.GetBins()
	// Check Overlap
	if !bins[0].HasOverlap || !bins[1].HasOverlap {
		t.Error("Bins should be flagged as overlapping")
	}
}

func TestStore_GetAvailableBins(t *testing.T) {
	s := newTestStore(t)
	s.CreateController("C1", "1.1.1.1")
	s.CreateBin("B1", 1, 0, 0)
	s.CreateBin("B2", 1, 0, 1)

	// Use helper to create valid part
	if err := createValidPartForBinTest(s); err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	// Occupy B1
	if err := s.CreatePartLocation(1, 1, 10); err != nil {
		t.Fatalf("CreatePartLocation failed: %v", err)
	}

	// Get Available
	avail, err := s.GetAvailableBins(1)
	if err != nil {
		t.Fatalf("GetAvailableBins failed: %v", err)
	}

	if len(avail) != 1 {
		t.Fatalf("Expected 1 available bin, got %d", len(avail))
	}
	if avail[0].Name != "B2" {
		t.Errorf("Expected B2 to be available")
	}
}

func TestStore_Locations(t *testing.T) {
	s := newTestStore(t)
	s.CreateController("C1", "1.1.1.1")
	s.CreateBin("B1", 1, 0, 0)

	if err := createValidPartForBinTest(s); err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	if err := s.CreatePartLocation(1, 1, 10); err != nil {
		t.Fatalf("CreateLocation failed: %v", err)
	}

	locs, _ := s.GetPartLocations(1)
	if len(locs) != 1 || locs[0].Quantity != 10 {
		t.Errorf("Location fetch failed")
	}

	// Get By ID
	l, err := s.GetPartLocationByID(locs[0].LocationID)
	if err != nil || l.Quantity != 10 {
		t.Errorf("GetPartLocationByID failed")
	}

	if err := s.UpdatePartLocation(locs[0].LocationID, 50); err != nil {
		t.Fatalf("Update failed")
	}

	if err := s.DeletePartLocation(locs[0].LocationID); err != nil {
		t.Fatalf("Delete failed")
	}
}

func TestStore_GetPartNamesInBin(t *testing.T) {
	s := newTestStore(t)
	s.CreateController("C1", "1.1.1.1")
	s.CreateBin("B1", 1, 0, 0)

	// Create 2 parts
	p1 := getValidPart("Resistor")
	s.CreatePart(p1) // ID 1
	p2 := getValidPart("Capacitor")
	s.CreatePart(p2) // ID 2

	// Add stock for both in Bin 1
	s.CreatePartLocation(1, 1, 10)
	s.CreatePartLocation(2, 1, 5)

	// Test
	names, err := s.GetPartNamesInBin(1)
	if err != nil {
		t.Fatalf("GetPartNamesInBin failed: %v", err)
	}

	if len(names) != 2 {
		t.Errorf("Expected 2 part names, got %d", len(names))
	}
	// Check sort order (Alphabetical)
	if names[0] != "Capacitor" || names[1] != "Resistor" {
		t.Errorf("Unexpected names or order: %v", names)
	}
}
