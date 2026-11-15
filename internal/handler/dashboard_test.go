package handler

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/dashboard"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestNewDashboardViewModel(t *testing.T) {
	// Setup DB
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	dashService := dashboard.NewService(s)

	h := &Handler{
		Dashboard: dashService,
	}

	ctx := context.Background()

	// Mock Data
	// Controller B: Bin (1,1) - Critical
	c2, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "Controller B", IpAddress: "2.2.2.2"})
	cont2, _ := s.CreateContainer(ctx, db.CreateContainerParams{Name: "C2", ControllerID: sql.NullInt64{Int64: c2.ID, Valid: true}, SegmentID: 0})

	b10Row, _ := s.CreateBin(ctx, db.CreateBinParams{
		Name:        "Bin 10",
		ContainerID: cont2,
		GridX:       sql.NullInt64{Int64: 1, Valid: true},
		GridY:       sql.NullInt64{Int64: 1, Valid: true},
	})
	b10 := b10Row

	p100, _ := s.CreatePart(ctx, db.CreatePartParams{Name: "Part 100", MinStockThreshold: sql.NullInt64{Int64: 5, Valid: true}})
	_ = s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p100, BinID: sql.NullInt64{Int64: b10, Valid: true}, Quantity: 0})

	// Controller A: Bin (0,0) - OK
	c1, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "Controller A", IpAddress: "1.1.1.1"})
	cont1, _ := s.CreateContainer(ctx, db.CreateContainerParams{Name: "C1", ControllerID: sql.NullInt64{Int64: c1.ID, Valid: true}, SegmentID: 0})

	b1, _ := s.CreateBin(ctx, db.CreateBinParams{
		Name:        "Bin 1",
		ContainerID: cont1,
		GridX:       sql.NullInt64{Int64: 0, Valid: true},
		GridY:       sql.NullInt64{Int64: 0, Valid: true},
	})
	p101, _ := s.CreatePart(ctx, db.CreatePartParams{Name: "Part 101", MinStockThreshold: sql.NullInt64{Int64: 5, Valid: true}})
	_ = s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p101, BinID: sql.NullInt64{Int64: b1, Valid: true}, Quantity: 10})

	// Controller A: Bin (0,1) - Low
	b2, _ := s.CreateBin(ctx, db.CreateBinParams{
		Name:        "Bin 2",
		ContainerID: cont1,
		GridX:       sql.NullInt64{Int64: 0, Valid: true},
		GridY:       sql.NullInt64{Int64: 1, Valid: true},
	})
	p102, _ := s.CreatePart(ctx, db.CreatePartParams{Name: "Part 102", MinStockThreshold: sql.NullInt64{Int64: 5, Valid: true}, ReorderLevel: sql.NullInt64{Int64: 10, Valid: true}})
	_ = s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p102, BinID: sql.NullInt64{Int64: b2, Valid: true}, Quantity: 6})

	// Execute via Service
	vm, err := h.Dashboard.GetGrid(ctx)
	if err != nil {
		t.Fatalf("failed to get grid: %v", err)
	}

	// Assertions

	// Controller Sorting (A before B)
	if len(vm) != 2 {
		t.Fatalf("Expected 2 controllers, got %d", len(vm))
	}
	if vm[0].Name != "Controller A" {
		t.Errorf("Expected Controller A first, got %s", vm[0].Name)
	}
	if vm[1].Name != "Controller B" {
		t.Errorf("Expected Controller B second, got %s", vm[1].Name)
	}

	// Bin Sorting for Controller A (GridY 0 before GridY 1)
	if len(vm[0].Containers) != 1 {
		t.Fatalf("Expected 1 container for Controller A, got %d", len(vm[0].Containers))
	}
	binsA := vm[0].Containers[0].Bins
	if len(binsA) != 2 {
		t.Fatalf("Expected 2 bins for Controller A, got %d", len(binsA))
	}
	if binsA[0].GridY != 0 {
		t.Errorf("Expected first bin to be at Y=0, got %d", binsA[0].GridY)
	}
	if binsA[1].GridY != 1 {
		t.Errorf("Expected second bin to be at Y=1, got %d", binsA[1].GridY)
	}

	// Status Calculation
	// Bin 1 (OK)
	if len(binsA[0].Statuses) != 1 || binsA[0].Statuses[0] != "ok" {
		t.Errorf("Expected Bin 1 status 'ok', got %v", binsA[0].Statuses)
	}
	// Bin 2 (Low)
	if len(binsA[1].Statuses) != 1 || binsA[1].Statuses[0] != "low" {
		t.Errorf("Expected Bin 2 status 'low', got %v", binsA[1].Statuses)
	}
	// Bin 10 (Critical) - Controller B
	if len(vm[1].Containers) != 1 {
		t.Fatalf("Expected 1 container for Controller B, got %d", len(vm[1].Containers))
	}
	binsB := vm[1].Containers[0].Bins
	if len(binsB[0].Statuses) != 1 || binsB[0].Statuses[0] != "critical" {
		t.Errorf("Expected Bin 10 status 'critical', got %v", binsB[0].Statuses)
	}
}
