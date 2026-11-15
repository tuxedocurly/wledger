package hardware

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
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
	wClient := wled.NewClient()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	return NewService(store, wClient, logger), store, dbConn
}

func TestService_ListControllers(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	_, err := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "Test",
		IpAddress: "1.1.1.1",
	})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	list, err := s.ListControllers(ctx)
	if err != nil {
		t.Errorf("failed to list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 controller, got %d", len(list))
	}
}

func TestService_DeleteController(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	c, _ := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "Test",
		IpAddress: "1.1.1.1",
	})

	// Create Container
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Default",
		ControllerID: sql.NullInt64{Int64: c.ID, Valid: true},
		SegmentID:    0,
	})

	// Add a bin
	store.CreateBin(ctx, db.CreateBinParams{
		Name:        "Bin 1",
		ContainerID: cont,
	})

	err := s.DeleteController(ctx, c.ID)
	if err != nil {
		t.Errorf("failed to delete: %v", err)
	}

	// Verify container is orphaned and bin is preserved (v2 ON DELETE SET NULL)
	contRow, err := store.GetContainer(ctx, cont)
	if err != nil {
		t.Fatalf("expected container to remain orphaned: %v", err)
	}
	if contRow.ControllerID.Valid {
		t.Errorf("expected container controller_id to be NULL, got %d", contRow.ControllerID.Int64)
	}

	var count int
	dbConn.QueryRow("SELECT count(*) FROM bins").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 preserved bin, got %d", count)
	}
}
