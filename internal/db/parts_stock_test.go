package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestPartStockBreakdown(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a Part
	partID, err := q.CreatePart(ctx, db.CreatePartParams{
		Name: "Mixed Stock Part",
	})
	if err != nil {
		t.Fatalf("failed to create part: %v", err)
	}

	// Setup Hierarchy for Valid Stock
	// Create Controller
	controllerRow, err := q.CreateController(ctx, db.CreateControllerParams{
		Name:      "Test Controller",
		IpAddress: "192.168.1.100",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	// Create Container
	containerID, err := q.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Test Container",
		ControllerID: sql.NullInt64{Int64: controllerRow.ID, Valid: true},
		SegmentID:    0,
	})
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	// Create Bin
	binID, err := q.CreateBin(ctx, db.CreateBinParams{
		Name:        "Bin A1",
		ContainerID: containerID,
		LedIndex:    sql.NullInt64{Int64: 0, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create bin: %v", err)
	}

	// Create Valid Stock (Assigned to Bin) -> Quantity 10
	err = q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   partID,
		BinID:    sql.NullInt64{Int64: binID, Valid: true},
		Quantity: 10,
	})
	if err != nil {
		t.Fatalf("failed to create valid assignment: %v", err)
	}

	// Create Orphaned Stock (BinID is NULL) -> Quantity 5
	err = q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   partID,
		BinID:    sql.NullInt64{Valid: false},
		Quantity: 5,
	})
	if err != nil {
		t.Fatalf("failed to create orphaned stock: %v", err)
	}

	// List Parts and check breakdown
	parts, err := q.ListParts(ctx, db.ListPartsParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListParts failed: %v", err)
	}

	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}

	p := parts[0]

	// Total stock should be 15
	if p.TotalStock != 15 {
		t.Errorf("expected total stock 15, got %d", p.TotalStock)
	}

	// Valid Stock should be 10
	if p.ValidStock != 10 {
		t.Errorf("expected valid stock 10, got %d", p.ValidStock)
	}

	// Orphaned Stock should be 5
	if p.OrphanedStock != 5 {
		t.Errorf("expected orphaned stock 5, got %d", p.OrphanedStock)
	}
}
