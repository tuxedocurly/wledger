package store

import (
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"

	"wledger/internal/models"
)

// newTestStore creates an in-memory SQLite database for tests
func newTestStore(t *testing.T) *Store {
	// Use in-memory database
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open in-memory database: %v", err)
	}

	// Enable WAL mode
	_, err = db.Exec("PRAGMA journal_mode = WAL;")
	if err != nil {
		t.Fatalf("Failed to enable WAL mode: %v", err)
	}

	// Enable Foreign Key constraint enforcement
	_, err = db.Exec("PRAGMA foreign_keys = ON;")
	if err != nil {
		t.Fatalf("Failed to enable foreign keys: %v", err)
	}

	// Run migrations
	if err := createTables(db); err != nil {
		t.Fatalf("Failed to create tables: %v", err)
	}

	return &Store{db: db}
}

func TestStore_CreatePart(t *testing.T) {
	s := newTestStore(t)

	part := &models.Part{
		Name:          "Test Resistor",
		Description:   sql.NullString{String: "100 Ohm", Valid: true},
		PartNumber:    sql.NullString{String: "R-100", Valid: true},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Manufacturer:  sql.NullString{String: "TestCo", Valid: true},
		Supplier:      sql.NullString{String: "TestSupply", Valid: true},
		UnitCost:      sql.NullFloat64{Float64: 0.05, Valid: true},
		Status:        sql.NullString{String: "active", Valid: true},
		StockTracking: false,
		ReorderPoint:  0,
		MinStock:      0,
	}

	err := s.CreatePart(part)
	if err != nil {
		t.Fatalf("CreatePart() failed: %v", err)
	}

	got, err := s.GetPartByID(1)
	if err != nil {
		t.Fatalf("GetPartByID() failed: %v", err)
	}

	if got.Name != "Test Resistor" {
		t.Errorf("got name %q, want %q", got.Name, "Test Resistor")
	}
	if got.UnitCost.Float64 != 0.05 {
		t.Errorf("got unit cost %f, want %f", got.UnitCost.Float64, 0.05)
	}
}

func TestStore_DeleteController_InUse(t *testing.T) {
	s := newTestStore(t)

	err := s.CreateController("Test Controller", "1.2.3.4")
	if err != nil {
		t.Fatalf("CreateController() failed: %v", err)
	}

	err = s.CreateBin("Test Bin", 1, 0, 0)
	if err != nil {
		t.Fatalf("CreateBin() failed: %v", err)
	}

	err = s.DeleteController(1)

	if err == nil {
		t.Fatal("DeleteController() did not return an error, but it should have")
	}

	if !errors.Is(err, ErrForeignKeyConstraint) {
		t.Errorf("got error %v, want %v", err, ErrForeignKeyConstraint)
	}
}

func TestStore_CreateBin_Duplicate(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateController("Test C", "1.2.3.4"); err != nil {
		t.Fatalf("CreateController() failed: %v", err)
	}

	err := s.CreateBin("A1-1", 1, 0, 1)
	if err != nil {
		t.Fatalf("CreateBin() (first) failed: %v", err)
	}

	err = s.CreateBin("A1-1", 1, 0, 1)

	if err == nil {
		t.Fatal("CreateBin() (second) did not return an error, but it should have")
	}

	if !errors.Is(err, ErrUniqueConstraint) {
		t.Errorf("got error %v, want %v", err, ErrUniqueConstraint)
	}
}

func TestStore_SearchParts_ByTag(t *testing.T) {
	s := newTestStore(t)

	part := &models.Part{
		Name:          "ESP32 Module",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        sql.NullString{String: "active", Valid: true},
		Manufacturer:  sql.NullString{String: "Espressif", Valid: true},
		Supplier:      sql.NullString{String: "TestSupply", Valid: true},
		UnitCost:      sql.NullFloat64{Float64: 3.50, Valid: true},
		StockTracking: false,
		ReorderPoint:  0,
		MinStock:      0,
	}
	if err := s.CreatePart(part); err != nil {
		t.Fatalf("CreatePart() failed: %v", err)
	}

	cat, err := s.CreateCategory("MCU")
	if err != nil {
		t.Fatalf("CreateCategory() failed: %v", err)
	}
	if err := s.AssignCategoryToPart(1, cat.ID); err != nil {
		t.Fatalf("AssignCategoryToPart() failed: %v", err)
	}

	parts, err := s.SearchParts("MCU")
	if err != nil {
		t.Fatalf("SearchParts() failed: %v", err)
	}

	if len(parts) != 1 {
		t.Fatalf("SearchParts() returned %d results, want 1", len(parts))
	}
	if parts[0].Name != "ESP32 Module" {
		t.Errorf("got part %q, want 'ESP32 Module'", parts[0].Name)
	}
}

func TestStore_UpdateController(t *testing.T) {
	s := newTestStore(t)

	// 1. Create Controller
	err := s.CreateController("Old Name", "1.1.1.1")
	if err != nil {
		t.Fatalf("CreateController failed: %v", err)
	}

	// 2. Update it (ID is likely 1)
	updated := &models.WLEDController{
		ID:        1,
		Name:      "New Name",
		IPAddress: "2.2.2.2",
	}
	err = s.UpdateController(updated)
	if err != nil {
		t.Fatalf("UpdateController failed: %v", err)
	}

	// 3. Verify
	got, err := s.GetControllerByID(1)
	if err != nil {
		t.Fatalf("GetControllerByID failed: %v", err)
	}
	if got.Name != "New Name" {
		t.Errorf("got name %q, want %q", got.Name, "New Name")
	}
	if got.IPAddress != "2.2.2.2" {
		t.Errorf("got ip %q, want %q", got.IPAddress, "2.2.2.2")
	}
}

func TestStore_MigrateBins(t *testing.T) {
	s := newTestStore(t)

	// 1. Create Source Controller (ID 1)
	s.CreateController("Source", "1.1.1.1")
	// 2. Create Target Controller (ID 2)
	s.CreateController("Target", "2.2.2.2")

	// 3. Create 5 bins on Source
	for i := 0; i < 5; i++ {
		s.CreateBin("Bin"+strconv.Itoa(i), 1, 0, i)
	}

	// Verify initial state (Source should have 5 bins)
	ctrls, _ := s.GetControllers()
	for _, c := range ctrls {
		if c.ID == 1 && c.BinCount != 5 {
			t.Errorf("Source controller should have 5 bins, got %d", c.BinCount)
		}
		if c.ID == 2 && c.BinCount != 0 {
			t.Errorf("Target controller should have 0 bins, got %d", c.BinCount)
		}
	}

	// 4. Perform Migration (1 -> 2)
	err := s.MigrateBins(1, 2)
	if err != nil {
		t.Fatalf("MigrateBins failed: %v", err)
	}

	// 5. Verify Final State
	ctrls, _ = s.GetControllers()
	for _, c := range ctrls {
		if c.ID == 1 && c.BinCount != 0 {
			t.Errorf("Source controller should now have 0 bins, got %d", c.BinCount)
		}
		if c.ID == 2 && c.BinCount != 5 {
			t.Errorf("Target controller should now have 5 bins, got %d", c.BinCount)
		}
	}
}
