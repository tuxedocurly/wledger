package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestOrphanedStock(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Setup Data
	// Create Controller
	ctrl, err := q.CreateController(ctx, db.CreateControllerParams{
		Name:      "TestCtrl",
		IpAddress: "1.2.3.4",
	})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}

	// Create Container
	container, err := q.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "TestContainer",
		ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
		SegmentID:    0,
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}

	// Create Bin
	binID, err := q.CreateBin(ctx, db.CreateBinParams{
		Name:        "A1",
		ContainerID: container,
		LedIndex:    sql.NullInt64{Int64: 0, Valid: true},
	})
	if err != nil {
		t.Fatalf("create bin: %v", err)
	}

	// Create Part
	partID, err := q.CreatePart(ctx, db.CreatePartParams{
		Name: "TestPart",
	})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}

	// Assign Part to Bin
	err = q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   partID,
		BinID:    sql.NullInt64{Int64: binID, Valid: true},
		Quantity: 10,
	})
	if err != nil {
		t.Fatalf("assign part: %v", err)
	}

	// Simulate "Delete Bin"
	err = q.DeleteBinByLed(ctx, db.DeleteBinByLedParams{
		ContainerID: container,
		LedIndex:    sql.NullInt64{Int64: 0, Valid: true}, // Index of Bin A1
	})
	if err != nil {
		t.Fatalf("delete bin: %v", err)
	}

	// Verify Stock Preservation
	// fetch assignments for the part
	assignments, err := q.GetPartAssignments(ctx, partID)
	if err != nil {
		t.Fatalf("get assignments: %v", err)
	}

	if len(assignments) != 1 {
		t.Fatalf("expected 1 assignment (orphaned), got %d", len(assignments))
	}

	stock := assignments[0]

	// Verify ID and Quantity are preserved
	if stock.Quantity != 10 {
		t.Errorf("expected quantity 10, got %d", stock.Quantity)
	}

	// Verify Orphan Status (BinID should be NULL / Invalid)
	if stock.BinID.Valid {
		t.Errorf("expected BinID to be NULL (orphaned), got %d", stock.BinID.Int64)
	}

	if stock.BinName.Valid {
		t.Errorf("expected BinName to be NULL, got %s", stock.BinName.String)
	}
}
