package main

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	// Use in memory database
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

	if err := createTables(db); err != nil {
		t.Fatalf("Failed to create tables: %v", err)
	}

	return &Store{db: db}
}

func TestStore_CreatePart(t *testing.T) {
	s := newTestStore(t)

	part := &Part{
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
	if got.PartNumber.String != "R-100" {
		t.Errorf("got part number %q, want %q", got.PartNumber.String, "R-100")
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
