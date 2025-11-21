package store

import (
	"database/sql"
	"errors"
	"path/filepath"
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

func TestStore_NewStore_Physical(t *testing.T) {
	// Create a temporary directory for the test DB
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_inventory.db")

	// Initialize new store (creates file)
	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Verify basic functionality works on physical DB
	if err := s.CreatePart(getValidPart("Real DB Part")); err != nil {
		t.Errorf("Failed to write to physical DB: %v", err)
	}
}
