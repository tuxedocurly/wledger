package dashboard

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func setupTest(t *testing.T) (Service, db.Store, *sql.DB) {
	dbConn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	store := db.NewStore(dbConn)
	return NewService(store), store, dbConn
}

func TestService_GetStats(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	_, _ = store.CreatePart(ctx, db.CreatePartParams{Name: "Test Part"})

	stats, err := s.GetStats(ctx)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}

	if stats.TotalParts != 1 {
		t.Errorf("expected 1 part, got %d", stats.TotalParts)
	}
}

func TestService_GetGrid(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	// Create Controller
	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl A", IpAddress: "1.1.1.1"})
	
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Cont", ControllerID: sql.NullInt64{Int64: c.ID, Valid: true}, SegmentID: 0,
	})

	// Create Bin
	b, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "Bin 1", ContainerID: cont,
		GridX: sql.NullInt64{Int64: 0, Valid: true}, GridY: sql.NullInt64{Int64: 0, Valid: true},
	})
	// Create Part
	p, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "Part 1", MinStockThreshold: sql.NullInt64{Int64: 10, Valid: true}})
	// Assign
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: p, BinID: sql.NullInt64{Int64: b, Valid: true}, Quantity: 5,
	})

	grid, err := s.GetGrid(ctx)
	if err != nil {
		t.Fatalf("failed to get grid: %v", err)
	}

	if len(grid) != 1 {
		t.Fatalf("expected 1 controller, got %d", len(grid))
	}

	if len(grid[0].Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(grid[0].Containers))
	}

	if len(grid[0].Containers[0].Bins) != 1 {
		t.Fatalf("expected 1 bin, got %d", len(grid[0].Containers[0].Bins))
	}

	if len(grid[0].Containers[0].Bins[0].Statuses) != 1 || grid[0].Containers[0].Bins[0].Statuses[0] != "critical" {
		t.Errorf("expected critical status, got %v", grid[0].Containers[0].Bins[0].Statuses)
	}
}
