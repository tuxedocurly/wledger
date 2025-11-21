package store

import (
	"testing"
)

// setupIntegrationDB creates a test scenario:
// - Controller 1 ("Shelf A") with Bin 1 ("Bin A-1")
// - Controller 2 ("Shelf B") with Bin 2 ("Bin B-1")
// - Part 1 ("Generic Resistor")
// - Stock: 100 items in Bin A-1, 50 items in Bin B-1 (Total: 150)
func setupIntegrationDB(t *testing.T) *Store {
	s := newTestStore(t)

	// Create Hardware (Controllers)
	if err := s.CreateController("Shelf A", "192.168.1.10"); err != nil { // ID 1
		t.Fatalf("Setup failed: CreateController A: %v", err)
	}
	if err := s.CreateController("Shelf B", "192.168.1.11"); err != nil { // ID 2
		t.Fatalf("Setup failed: CreateController B: %v", err)
	}

	// Create Containers (Bins)
	// Bin 1 on Controller 1
	if err := s.CreateBin("Bin A-1", 1, 0, 0); err != nil { // ID 1
		t.Fatalf("Setup failed: CreateBin A-1: %v", err)
	}
	// Bin 2 on Controller 2
	if err := s.CreateBin("Bin B-1", 2, 0, 0); err != nil { // ID 2
		t.Fatalf("Setup failed: CreateBin B-1: %v", err)
	}

	// Create Part
	// use the helper from parts_test.go to ensure all constraints are met
	part := getValidPart("Generic Resistor")
	if err := s.CreatePart(part); err != nil { // ID 1
		t.Fatalf("Setup failed: CreatePart: %v", err)
	}

	// Create Inventory (Stock)
	// Add 100 to Bin 1
	if err := s.CreatePartLocation(1, 1, 100); err != nil {
		t.Fatalf("Setup failed: CreateStock 1: %v", err)
	}
	// Add 50 to Bin 2
	if err := s.CreatePartLocation(1, 2, 50); err != nil {
		t.Fatalf("Setup failed: CreateStock 2: %v", err)
	}

	return s
}

// TestIntegration_BinDeletion_UpdatesTotalStock verifies that deleting a physical bin
// automatically removes the associated stock from the total count
func TestIntegration_BinDeletion_UpdatesTotalStock(t *testing.T) {
	// Setup (Total Stock: 150)
	s := setupIntegrationDB(t)

	// Verify initial state
	p, err := s.GetPartByID(1)
	if err != nil {
		t.Fatalf("Failed to fetch part: %v", err)
	}
	if p.TotalQuantity != 150 {
		t.Fatalf("Initial setup invalid. Expected 150 stock, got %d", p.TotalQuantity)
	}

	// Delete Bin 1 (which holds 100 items)
	// This simulates a user removing a physical bin or a shelf collapsing.
	// Because of ON DELETE CASCADE in our schema, the stock record should vanish.
	if err := s.DeleteBin(1); err != nil {
		t.Fatalf("DeleteBin failed: %v", err)
	}

	// Assert: Total stock should now be 50 (only the items in Bin 2 remain)
	pUpdated, err := s.GetPartByID(1)
	if err != nil {
		t.Fatalf("Failed to fetch part after delete: %v", err)
	}

	expectedStock := 50
	if pUpdated.TotalQuantity != expectedStock {
		t.Errorf("Stock calculation mismatch after bin deletion.\nExpected: %d\nGot: %d", expectedStock, pUpdated.TotalQuantity)
	}

	// Verify the specific location record is actually gone
	locs, _ := s.GetPartLocations(1)
	if len(locs) != 1 {
		t.Errorf("Expected 1 location record left, got %d", len(locs))
	}
}

// TestIntegration_ControllerMigration_Lifecycle verifies the workflow of
// replacing a hardware controller and moving its bins to a new one
func TestIntegration_ControllerMigration_Lifecycle(t *testing.T) {
	// Setup
	s := setupIntegrationDB(t)

	// Attempt to Delete Controller 1 (Should Fail)
	// Controller 1 has Bin 1 assigned to it
	err := s.DeleteController(1)
	if err != ErrForeignKeyConstraint {
		t.Errorf("Expected ErrForeignKeyConstraint when deleting active controller, got %v", err)
	}

	// Migrate Bins from Ctrl 1 -> Ctrl 2
	// User replaces bin A's controller with bin B's controller (logically)
	if err := s.MigrateBins(1, 2); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Assert: Controller 1 should now be empty
	c1, _ := s.GetControllerByID(1)
	if c1.BinCount != 0 {
		t.Errorf("Source controller should have 0 bins after migration, got %d", c1.BinCount)
	}

	// Assert: Controller 2 should now have both bins
	c2, _ := s.GetControllerByID(2)
	if c2.BinCount != 2 {
		t.Errorf("Target controller should have 2 bins after migration, got %d", c2.BinCount)
	}

	// Delete Controller 1 (Should Pass)
	// Now that it's empty, remove the hardware record
	if err := s.DeleteController(1); err != nil {
		t.Errorf("Failed to delete empty controller after migration: %v", err)
	}
}
