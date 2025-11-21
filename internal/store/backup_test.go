package store

import (
	"testing"
)

func TestStore_BackupRestore(t *testing.T) {
	s := newTestStore(t)

	// Populate Data
	s.CreateController("C1", "1.1.1.1")
	s.CreateBin("B1", 1, 0, 0)
	s.CreatePart(getValidPart("P1"))
	s.CreatePartLocation(1, 1, 10)
	cat, _ := s.CreateCategory("Cat1")
	s.AssignCategoryToPart(1, cat.ID)
	s.CreatePartURL(1, "http://test", "test")

	// Export
	backup, err := s.GetAllDataForBackup()
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	if len(backup.Parts) != 1 || len(backup.PartLocations) != 1 {
		t.Errorf("Backup data missing")
	}

	// Nuke DB (Simulate by creating a fresh store)
	s2 := newTestStore(t)

	// Import
	if err := s2.RestoreFromBackup(backup); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify
	parts, _ := s2.GetParts()
	if len(parts) != 1 {
		t.Errorf("Restore failed: parts missing")
	}
	locs, _ := s2.GetPartLocations(parts[0].ID)
	if len(locs) != 1 {
		t.Errorf("Restore failed: locations missing")
	}
}
