package hardware

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestMigrateLegacyLedIndices(t *testing.T) {
	dbConn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()

	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	store := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx := context.Background()

	// Setup Controllers & Containers
	// Controller 1
	ctrl, err := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "Ctrl 1",
		IpAddress: "1.1.1.1",
	})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	// Container 1 (Segment 0, Linear, Total 10)
	cont1, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Cont 1",
		ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
		SegmentID:    0,
		ConfigJson:   sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create container 1: %v", err)
	}

	// Container 2 (Segment 0, Linear, Total 10)
	cont2, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Cont 2",
		ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
		SegmentID:    0,
		ConfigJson:   sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create container 2: %v", err)
	}

	// Setup Bins with RELATIVE indices
	// Cont 1 Bins: 0, 1
	_, err = store.CreateBin(ctx, db.CreateBinParams{
		Name: "C1B1", ContainerID: cont1, LedIndex: sql.NullInt64{Int64: 0, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create bin C1B1: %v", err)
	}
	_, err = store.CreateBin(ctx, db.CreateBinParams{
		Name: "C1B2", ContainerID: cont1, LedIndex: sql.NullInt64{Int64: 1, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create bin C1B2: %v", err)
	}

	// Cont 2 Bins: 0, 1
	_, err = store.CreateBin(ctx, db.CreateBinParams{
		Name: "C2B1", ContainerID: cont2, LedIndex: sql.NullInt64{Int64: 0, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create bin C2B1: %v", err)
	}
	_, err = store.CreateBin(ctx, db.CreateBinParams{
		Name: "C2B2", ContainerID: cont2, LedIndex: sql.NullInt64{Int64: 1, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create bin C2B2: %v", err)
	}

	// Run Migration
	err = MigrateLegacyLedIndices(ctx, store, logger)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Verify results
	// Cont 1 bins should remain 0, 1 (offset 0)
	bins1, err := store.GetBinsByContainer(ctx, cont1)
	if err != nil {
		t.Fatalf("failed to get bins for cont 1: %v", err)
	}
	if len(bins1) != 2 {
		t.Fatalf("expected 2 bins in cont 1, got %d", len(bins1))
	}
	if bins1[0].LedIndex.Int64 != 0 || bins1[1].LedIndex.Int64 != 1 {
		t.Errorf("Cont 1 bins incorrect: %d, %d", bins1[0].LedIndex.Int64, bins1[1].LedIndex.Int64)
	}

	// Cont 2 bins should be 10, 11 (offset 10)
	bins2, err := store.GetBinsByContainer(ctx, cont2)
	if err != nil {
		t.Fatalf("failed to get bins for cont 2: %v", err)
	}
	if len(bins2) != 2 {
		t.Fatalf("expected 2 bins in cont 2, got %d", len(bins2))
	}
	if bins2[0].LedIndex.Int64 != 10 || bins2[1].LedIndex.Int64 != 11 {
		t.Errorf("Cont 2 bins incorrect: %d, %d", bins2[0].LedIndex.Int64, bins2[1].LedIndex.Int64)
	}

	// Verify Flag is set
	flag, err := store.GetFlag(ctx, "migration_005_applied")
	if err != nil || flag != "true" {
		t.Errorf("migration flag not set: %v", err)
	}

	// Run again - should be no-op
	err = MigrateLegacyLedIndices(ctx, store, logger)
	if err != nil {
		t.Fatalf("migration run 2 failed: %v", err)
	}
}
