package dashboard

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/components"
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

func TestService_StandaloneContainersOnWallsAndGrid(t *testing.T) {
	dbConn, err := db.Open("file:dash_standalone_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	store := db.NewStore(dbConn)
	s := NewService(store)
	ctx := context.Background()

	// Create one standalone container (controller_id = NULL) with a bin and a stocked part.
	standaloneID, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Travel Case",
		ControllerID: sql.NullInt64{Valid: false},
		SegmentID:    0,
	})
	if err != nil {
		t.Fatalf("create standalone container: %v", err)
	}
	binID, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "T1", ContainerID: standaloneID,
		GridX: sql.NullInt64{Int64: 0, Valid: true}, GridY: sql.NullInt64{Int64: 0, Valid: true},
	})
	partID, _ := store.CreatePart(ctx, db.CreatePartParams{
		Name: "Capacitor", ReorderLevel: sql.NullInt64{Int64: 10, Valid: true}, MinStockThreshold: sql.NullInt64{Int64: 2, Valid: true},
	})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Int64: binID, Valid: true}, Quantity: 5,
	})

	// Legacy GetGrid should include standalone container section
	grid, err := s.GetGrid(ctx)
	if err != nil || len(grid) != 1 || !grid[0].Containers[0].IsStandalone {
		t.Fatalf("expected standalone container in GetGrid, got %+v (err=%v)", grid, err)
	}

	// Mount standalone container on a Wall
	wallID, err := s.CreateWall(ctx, "Portable Shelf", "")
	if err != nil {
		t.Fatalf("CreateWall failed: %v", err)
	}
	if err := s.UpdateWall(ctx, wallID, "Portable Shelf", "", []int64{standaloneID}); err != nil {
		t.Fatalf("UpdateWall failed: %v", err)
	}

	wallContainers, err := s.GetWallWithContainers(ctx, wallID)
	if err != nil || len(wallContainers) != 1 || !wallContainers[0].IsStandalone {
		t.Fatalf("expected standalone container on GetWallWithContainers, got %+v (err=%v)", wallContainers, err)
	}
	if len(wallContainers[0].Bins) != 1 || wallContainers[0].Bins[0].Statuses[0] != "low" {
		t.Errorf("expected low stock status on standalone wall bin, got %+v", wallContainers[0].Bins)
	}

	allWalls, err := s.GetAllWallsWithContainers(ctx)
	if err != nil || len(allWalls) != 1 || len(allWalls[0].Containers) != 1 || !allWalls[0].Containers[0].IsStandalone {
		t.Fatalf("expected standalone container in GetAllWallsWithContainers, got %+v (err=%v)", allWalls, err)
	}
}

func TestDashboardBin_StockColorVarsRendered(t *testing.T) {
	ctrl := components.DashboardController{
		ID:   1,
		Name: "Bench",
		Containers: []components.DashboardContainer{
			{
				ID:   10,
				Name: "Drawer",
				Bins: []components.DashboardBin{
					{ID: 1, Name: "A1", GridX: 0, GridY: 0, Statuses: []string{"ok"}},
					{ID: 2, Name: "B1", GridX: 1, GridY: 0, Statuses: []string{"low"}},
					{ID: 3, Name: "C1", GridX: 2, GridY: 0, Statuses: []string{"critical"}},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := components.DashboardGrid(ctrl).Render(context.Background(), &buf); err != nil {
		t.Fatalf("DashboardGrid render failed: %v", err)
	}
	html := buf.String()
	for _, want := range []string{"var(--color-stock-ok)", "var(--color-stock-low)", "var(--color-stock-critical)"} {
		if !strings.Contains(html, want) {
			t.Errorf("expected DashboardGrid HTML to wire %q, got HTML snippet: %s", want, html)
		}
	}
}
