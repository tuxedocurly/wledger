package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestListParts_BinFilter(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Setup
	// Need Controller -> Container -> Bin
	ctrl, _ := q.CreateController(ctx, db.CreateControllerParams{Name: "C1", IpAddress: "1.2.3.4"})
	cont, _ := q.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont1", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}})

	binA, _ := q.CreateBin(ctx, db.CreateBinParams{Name: "Bin A", ContainerID: cont})
	binB, _ := q.CreateBin(ctx, db.CreateBinParams{Name: "Bin B", ContainerID: cont})

	// Setup Parts
	p1, _ := q.CreatePart(ctx, db.CreatePartParams{Name: "Part 1"})
	p2, _ := q.CreatePart(ctx, db.CreatePartParams{Name: "Part 2"})
	p3, _ := q.CreatePart(ctx, db.CreatePartParams{Name: "Part 3"})
	p4, _ := q.CreatePart(ctx, db.CreatePartParams{Name: "Part 4"}) // Orphaned (no bin) or unassigned

	// Assign Stock
	// P1 -> Bin A
	q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p1, BinID: sql.NullInt64{Int64: binA, Valid: true}, Quantity: 10})
	// P2 -> Bin B
	q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p2, BinID: sql.NullInt64{Int64: binB, Valid: true}, Quantity: 10})
	// P3 -> Bin A AND Bin B
	q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p3, BinID: sql.NullInt64{Int64: binA, Valid: true}, Quantity: 5})
	q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p3, BinID: sql.NullInt64{Int64: binB, Valid: true}, Quantity: 5})
	// P4 -> Orphaned (BinID NULL)
	q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p4, BinID: sql.NullInt64{Valid: false}, Quantity: 1})

	// TEST 1: Filter by Bin A (Expect P1, P3)
	partsA, err := q.ListParts(ctx, db.ListPartsParams{
		BinID:  sql.NullInt64{Int64: binA, Valid: true},
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListParts Bin A failed: %v", err)
	}

	if len(partsA) != 2 {
		t.Errorf("Expected 2 parts in Bin A, got %d", len(partsA))
	}
	// Check IDs
	foundP1 := false
	foundP3 := false
	for _, p := range partsA {
		if p.ID == p1 {
			foundP1 = true
		}
		if p.ID == p3 {
			foundP3 = true
		}
	}
	if !foundP1 || !foundP3 {
		t.Errorf("Expected P1 and P3 in Bin A, got: %v", partsA)
	}

	// TEST 2: Filter by Bin B (Expect P2, P3)
	partsB, err := q.ListParts(ctx, db.ListPartsParams{
		BinID:  sql.NullInt64{Int64: binB, Valid: true},
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListParts Bin B failed: %v", err)
	}
	if len(partsB) != 2 {
		t.Errorf("Expected 2 parts in Bin B, got %d", len(partsB))
	}

	// TEST 3: No Filter (Expect P1, P2, P3, P4)
	partsAll, err := q.ListParts(ctx, db.ListPartsParams{
		BinID:  sql.NullInt64{Valid: false},
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListParts All failed: %v", err)
	}
	if len(partsAll) != 4 {
		t.Errorf("Expected 4 parts total, got %d", len(partsAll))
	}
}

func TestSearchParts_BinFilter(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Setup
	ctrl, _ := q.CreateController(ctx, db.CreateControllerParams{Name: "C1", IpAddress: "1.2.3.4"})
	cont, _ := q.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont1", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}})
	binA, _ := q.CreateBin(ctx, db.CreateBinParams{Name: "Bin A", ContainerID: cont})

	// Setup Parts (Prefix names to ensure FTS match)
	p1, _ := q.CreatePart(ctx, db.CreatePartParams{Name: "Widget A"})
	p2, _ := q.CreatePart(ctx, db.CreatePartParams{Name: "Widget B"})

	// Assign
	q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p1, BinID: sql.NullInt64{Int64: binA, Valid: true}, Quantity: 10})
	q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p2, BinID: sql.NullInt64{Int64: binA, Valid: false}, Quantity: 10}) // Orphaned

	// TEST 1: Search "Widget" + Bin A (Expect P1 only)
	results, err := q.SearchParts(ctx, db.SearchPartsParams{
		Query:  sql.NullString{String: "Widget*", Valid: true},
		BinID:  sql.NullInt64{Int64: binA, Valid: true},
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("SearchParts failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result (Widget A), got %d", len(results))
	}
	if len(results) > 0 && results[0].ID != p1 {
		t.Errorf("Expected Widget A (ID %d), got ID %d", p1, results[0].ID)
	}

	// TEST 2: Search "Widget" + No Bin (Expect P1, P2)
	resultsAll, err := q.SearchParts(ctx, db.SearchPartsParams{
		Query:  sql.NullString{String: "Widget*", Valid: true},
		BinID:  sql.NullInt64{Valid: false},
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("SearchParts All failed: %v", err)
	}
	if len(resultsAll) != 2 {
		t.Errorf("Expected 2 results, got %d", len(resultsAll))
	}
}
