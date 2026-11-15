package handler

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestPartList_StockBreakdown(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	// Create a Part
	p, _ := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "Mixed Part"})

	// Setup Hierarchy
	cRow, _ := h.Queries.CreateController(ctx, db.CreateControllerParams{
		Name:      "C",
		IpAddress: "1.1.1.1",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	contID, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Cont",
		ControllerID: sql.NullInt64{Int64: cRow.ID, Valid: true},
	})
	binID, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{
		Name:        "Bin A",
		ContainerID: contID,
	})

	// Create Valid Stock (10)
	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   p,
		BinID:    sql.NullInt64{Int64: binID, Valid: true},
		Quantity: 10,
	})

	// Create Orphaned Stock (5)
	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   p,
		BinID:    sql.NullInt64{Valid: false},
		Quantity: 5,
	})

	// Test ListParts service method directly
	viewParts, err := h.Parts.ListParts(ctx, "", 1, nil)
	if err != nil {
		t.Fatalf("ListParts failed: %v", err)
	}

	if len(viewParts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(viewParts))
	}

	vp := viewParts[0]
	if vp.TotalStock != 15 {
		t.Errorf("expected TotalStock 15, got %d", vp.TotalStock)
	}
	if vp.ValidStock != 10 {
		t.Errorf("expected ValidStock 10, got %d", vp.ValidStock)
	}
	if vp.OrphanedStock != 5 {
		t.Errorf("expected OrphanedStock 5, got %d", vp.OrphanedStock)
	}
}
