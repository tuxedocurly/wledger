package store

import (
	"database/sql"
	"testing"
	"time"

	"wledger/internal/models"
)

func TestStore_UpdateController(t *testing.T) {
	s := newTestStore(t)

	// Create Controller
	err := s.CreateController("Old Name", "1.1.1.1")
	if err != nil {
		t.Fatalf("CreateController failed: %v", err)
	}

	// Update it (ID is likely 1)
	updated := &models.WLEDController{
		ID:        1,
		Name:      "New Name",
		IPAddress: "2.2.2.2",
	}
	err = s.UpdateController(updated)
	if err != nil {
		t.Fatalf("UpdateController failed: %v", err)
	}

	// Verify
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

func TestStore_DeleteController_InUse(t *testing.T) {
	s := newTestStore(t)

	// 1. Create Controller and Bin that uses it
	s.CreateController("C1", "1.1.1.1")
	s.CreateBin("B1", 1, 0, 0)

	// 2. Attempt Delete
	err := s.DeleteController(1)

	// 3. Assert Foreign Key Error
	if err != ErrForeignKeyConstraint {
		t.Errorf("Expected ErrForeignKeyConstraint, got %v", err)
	}
}

func TestStore_MigrateBins(t *testing.T) {
	s := newTestStore(t)

	// Create Controllers
	s.CreateController("Source", "1.1.1.1")
	s.CreateController("Target", "2.2.2.2")

	// Create Bin on Source
	s.CreateBin("B1", 1, 0, 0)

	// Migrate
	if err := s.MigrateBins(1, 2); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	// Verify Target has the bin
	c2, _ := s.GetControllerByID(2)
	if c2.BinCount != 1 {
		t.Errorf("Target bin count mismatch: got %d, want 1", c2.BinCount)
	}

	// Verify Source is empty
	c1, _ := s.GetControllerByID(1)
	if c1.BinCount != 0 {
		t.Errorf("Source bin count mismatch: got %d, want 0", c1.BinCount)
	}
}

func TestStore_HealthCheck(t *testing.T) {
	s := newTestStore(t)
	s.CreateController("C1", "1.1.1.1")

	// Test GetAll
	ctrls, err := s.GetAllControllersForHealthCheck()
	if err != nil || len(ctrls) != 1 {
		t.Fatalf("GetAllControllersForHealthCheck failed")
	}

	// Test Update Status
	now := sql.NullTime{Time: time.Now(), Valid: true}
	if err := s.UpdateControllerStatus(1, "online", now); err != nil {
		t.Fatalf("UpdateControllerStatus failed: %v", err)
	}

	// Verify Update
	c, _ := s.GetControllerByID(1)
	if c.Status != "online" {
		t.Errorf("Status not updated")
	}
	if !c.LastSeen.Valid {
		t.Errorf("LastSeen not updated")
	}
}
