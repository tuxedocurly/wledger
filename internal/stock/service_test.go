package stock

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
)

// setupTestDB creates an in memory DB and applies the schema using db.Migrate
func setupTestDB(t *testing.T) (*sql.DB, db.Store, func()) {
	// Open in-memory DB
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Apply migrations automatically
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	// Create store helper
	s := db.NewStore(conn)

	// return cleanup function
	return conn, s, func() {
		conn.Close()
	}
}

func TestService(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(s, logger)
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Setup: User, Controller, Bins, Part
	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	ctrl, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl1", IpAddress: "1.2.3.4"})
	
	cont, _ := s.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Container1",
		ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
		SegmentID:    0,
	})

	bin1ID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
	bin2ID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A2", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 1, Valid: true}})
	
	// Create Part directly via DB store to avoid dependency on parts service
	partID, err := s.CreatePart(ctx, db.CreatePartParams{
		Name: "Stock Part",
	})
	if err != nil {
		t.Fatalf("failed to create part: %v", err)
	}

	// Clear logs from setup
	database.ExecContext(ctx, "DELETE FROM audit_logs")

	// Assign Stock (Create Assignment)
	err = svc.AssignStock(ctx, AssignStockRequest{PartID: partID, BinID: bin1ID, Quantity: 10})
	if err != nil {
		t.Fatalf("AssignStock failed: %v", err)
	}

	logs, _ := s.GetAllAuditLogs(ctx)
	if len(logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(logs))
	}
	assignLog := logs[0]
	var assignNew map[string]any
	json.Unmarshal(assignLog.NewValue, &assignNew)
	if assignNew["quantity"] != float64(10) || assignNew["bin_id"] != float64(bin1ID) {
		t.Errorf("expected assignment details in new_value, got: %s", string(assignLog.NewValue))
	}

	// Adjust Stock
	assignments, _ := s.GetPartAssignments(ctx, partID)
	assignmentID := assignments[0].ID
	err = svc.AdjustStock(ctx, assignmentID, 5)
	if err != nil {
		t.Fatalf("AdjustStock failed: %v", err)
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 2 {
		t.Fatalf("expected 2 audit logs, got %d", len(logs))
	}
	adjustLog := logs[1]
	var adjustOld, adjustNew map[string]any
	json.Unmarshal(adjustLog.OldValue, &adjustOld)
	json.Unmarshal(adjustLog.NewValue, &adjustNew)

	if adjustOld["quantity"] != float64(10) {
		t.Errorf("expected old qty 10, got %v", adjustOld["quantity"])
	}
	if adjustNew["quantity"] != float64(15) {
		t.Errorf("expected new qty 15, got %v", adjustNew["quantity"])
	}

	// Move Stock
	err = svc.MoveStock(ctx, MoveStockRequest{PartID: partID, AssignmentID: assignmentID, TargetBinID: bin2ID})
	if err != nil {
		t.Fatalf("MoveStock failed: %v", err)
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 3 {
		t.Fatalf("expected 3 audit logs, got %d", len(logs))
	}
	moveLog := logs[2]
	var moveOld, moveNew map[string]any
	json.Unmarshal(moveLog.OldValue, &moveOld)
	json.Unmarshal(moveLog.NewValue, &moveNew)

	if moveOld["bin_id"] != float64(bin1ID) {
		t.Errorf("expected old bin %d, got %v", bin1ID, moveOld["bin_id"])
	}
	if moveNew["bin_id"] != float64(bin2ID) {
		t.Errorf("expected new bin %d, got %v", bin2ID, moveNew["bin_id"])
	}

	// Remove Stock
	assignments, _ = s.GetPartAssignments(ctx, partID)
	newAssignmentID := assignments[0].ID

	err = svc.RemoveStock(ctx, RemoveStockRequest{PartID: partID, AssignmentID: newAssignmentID})
	if err != nil {
		t.Fatalf("RemoveStock failed: %v", err)
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 4 {
		t.Fatalf("expected 4 audit logs, got %d", len(logs))
	}
	removeLog := logs[3]
	var removeOld map[string]any
	json.Unmarshal(removeLog.OldValue, &removeOld)
	if removeOld["quantity"] != float64(15) {
		t.Errorf("expected removed qty 15 in old_value, got %v", removeOld["quantity"])
	}
}

func TestMoveStock_SameBin(t *testing.T) {
	_, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(s, logger)
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Setup
	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	ctrl, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl1", IpAddress: "1.2.3.4"})
	cont, _ := s.CreateContainer(ctx, db.CreateContainerParams{Name: "Container1", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}})
	bin1ID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: cont})
	
	partID, _ := s.CreatePart(ctx, db.CreatePartParams{Name: "Stock Part"})

	// Assign Stock
	_ = svc.AssignStock(ctx, AssignStockRequest{PartID: partID, BinID: bin1ID, Quantity: 10})
	
	assignments, _ := s.GetPartAssignments(ctx, partID)
	assignmentID := assignments[0].ID

	// Action: Move Stock to SAME Bin
	err := svc.MoveStock(ctx, MoveStockRequest{PartID: partID, AssignmentID: assignmentID, TargetBinID: bin1ID})
	if err != nil {
		t.Fatalf("MoveStock failed: %v", err)
	}

	// Verify: Stock should still be there, unchanged
	assignments, _ = s.GetPartAssignments(ctx, partID)
	if len(assignments) != 1 {
		t.Fatalf("Expected 1 assignment, got %d. Stock was likely removed!", len(assignments))
	}
	
	if assignments[0].Quantity != 10 {
		t.Errorf("Expected quantity 10, got %d", assignments[0].Quantity)
	}
}

func TestAdjustStock_OrphanedAssignment(t *testing.T) {
	_, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(s, logger)
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	_, _ = s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	partID, err := s.CreatePart(ctx, db.CreatePartParams{Name: "Orphaned Part"})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	// Create orphaned stock row directly (bin_id IS NULL)
	if err := s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   partID,
		BinID:    sql.NullInt64{Valid: false},
		Quantity: 5,
	}); err != nil {
		t.Fatalf("CreatePartAssignment failed: %v", err)
	}

	assignments, err := s.GetPartAssignments(ctx, partID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("expected 1 orphaned assignment, got %d (err=%v)", len(assignments), err)
	}
	if assignments[0].BinID.Valid {
		t.Fatalf("expected BinID to be NULL, got %+v", assignments[0].BinID)
	}
	assignID := assignments[0].ID

	// Increment orphaned stock
	if err := svc.AdjustStock(ctx, assignID, 3); err != nil {
		t.Fatalf("AdjustStock(+3) failed: %v", err)
	}
	updated, err := s.GetAssignment(ctx, assignID)
	if err != nil {
		t.Fatalf("GetAssignment failed: %v", err)
	}
	if updated.Quantity != 8 {
		t.Fatalf("orphaned stock quantity did not persist: got %d, want 8", updated.Quantity)
	}

	// Decrement orphaned stock
	if err := svc.AdjustStock(ctx, assignID, -4); err != nil {
		t.Fatalf("AdjustStock(-4) failed: %v", err)
	}
	updated, err = s.GetAssignment(ctx, assignID)
	if err != nil {
		t.Fatalf("GetAssignment failed: %v", err)
	}
	if updated.Quantity != 4 {
		t.Fatalf("orphaned stock quantity after decrement: got %d, want 4", updated.Quantity)
	}

	// Clamp at zero
	if err := svc.AdjustStock(ctx, assignID, -10); err != nil {
		t.Fatalf("AdjustStock(-10) failed: %v", err)
	}
	updated, _ = s.GetAssignment(ctx, assignID)
	if updated.Quantity != 0 {
		t.Fatalf("expected orphaned stock clamped to 0, got %d", updated.Quantity)
	}

	logs, err := s.GetAllAuditLogs(ctx)
	if err != nil {
		t.Fatalf("GetAllAuditLogs failed: %v", err)
	}
	var incFound, decFound bool
	for _, l := range logs {
		if l.ActionType == "STOCK_INC" && l.EntityID == partID {
			var ov, nv map[string]float64
			_ = json.Unmarshal(l.OldValue, &ov)
			_ = json.Unmarshal(l.NewValue, &nv)
			if ov["quantity"] == 5 && nv["quantity"] == 8 {
				incFound = true
			}
		}
		if l.ActionType == "STOCK_DEC" && l.EntityID == partID {
			var ov, nv map[string]float64
			_ = json.Unmarshal(l.OldValue, &ov)
			_ = json.Unmarshal(l.NewValue, &nv)
			if ov["quantity"] == 8 && nv["quantity"] == 4 {
				decFound = true
			}
		}
	}
	if !incFound || !decFound {
		t.Errorf("expected accurate STOCK_INC (5->8) and STOCK_DEC (8->4) audit logs, got %+v", logs)
	}
}