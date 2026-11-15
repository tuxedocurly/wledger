package parts

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/tags"
)

func TestListParts_WithBinID(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	tagSvc := tags.NewService(database, s)
	docSvc := documents.NewService(s, logger)
	svc := NewService(database, s, logger, tagSvc, docSvc)

	ctx := context.Background()

	// Setup
	ctrl, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "C1", IpAddress: "1.2.3.4"})
	cont, _ := s.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont1", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}})
	binA, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "Bin A", ContainerID: cont})
	binB, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "Bin B", ContainerID: cont})

	// Create Parts
	p1, _ := svc.CreatePart(ctx, CreatePartRequest{Name: "Part A"})
	p2, _ := svc.CreatePart(ctx, CreatePartRequest{Name: "Part B"})

	// Assign
	s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p1, BinID: sql.NullInt64{Int64: binA, Valid: true}, Quantity: 10})
	s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p2, BinID: sql.NullInt64{Int64: binB, Valid: true}, Quantity: 10})

	// Test ListParts with Bin Filter
	binAVal := int64(binA)
	parts, err := svc.ListParts(ctx, "", 1, &binAVal)
	if err != nil {
		t.Fatalf("ListParts failed: %v", err)
	}

	if len(parts) != 1 {
		t.Errorf("Expected 1 part, got %d", len(parts))
	}
	if parts[0].ID != p1 {
		t.Errorf("Expected Part A (ID %d), got ID %d", p1, parts[0].ID)
	}
}
