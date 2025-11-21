package store

import (
	"testing"
)

func TestStore_Dashboard(t *testing.T) {
	s := newTestStore(t)

	// Setup 1 Controller, 1 Bin, 1 Part (Tracked)
	s.CreateController("C1", "1.1.1.1")
	s.CreateBin("B1", 1, 0, 0)

	p := getValidPart("P1")
	p.StockTracking = true
	p.MinStock = 5
	p.ReorderPoint = 10
	s.CreatePart(p)
	s.CreatePartLocation(1, 1, 3) // 3 < 5 (Red)

	// Test GetDashboardBinData
	data, err := s.GetDashboardBinData()
	if err != nil {
		t.Fatalf("GetDashboardBinData failed: %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("Expected 1 dashboard item, got %d", len(data))
	}
	if data[0].BinQuantity != 3 {
		t.Errorf("Expected qty 3, got %d", data[0].BinQuantity)
	}

	// Test Locate
	locs, err := s.GetPartLocationsForLocate(1)
	if err != nil || len(locs) != 1 {
		t.Errorf("Locate failed")
	}
	if locs[0].IP != "1.1.1.1" {
		t.Errorf("Locate IP mismatch")
	}

	// Test Stop (should find it even if qty is 0, though here it is 3)
	stops, err := s.GetPartLocationsForStop(1)
	if err != nil || len(stops) != 1 {
		t.Errorf("Stop failed")
	}

	// Test Stop All
	all, err := s.GetAllBinLocationsForStopAll()
	if err != nil || len(all) != 1 {
		t.Errorf("Stop All failed")
	}
}
