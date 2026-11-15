package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestHandlePartsList_BinFilter(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	ctx := context.Background()

	// Setup
	ctrl, _ := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "C1", IpAddress: "1.2.3.4"})
	cont, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont1", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}})
	binA, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "Bin A", ContainerID: cont})
	binB, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "Bin B", ContainerID: cont})

	// Create Parts
	p1, _ := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "Part A"})
	p2, _ := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "Part B"})

	// Assign
	h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p1, BinID: sql.NullInt64{Int64: binA, Valid: true}, Quantity: 10})
	h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p2, BinID: sql.NullInt64{Int64: binB, Valid: true}, Quantity: 10})

	// Request with Bin A Filter
	req := httptest.NewRequest("GET", "/parts?bin="+strings.TrimSpace(strconv.FormatInt(binA, 10)), nil)

	// Mock User
	user := auth.User{ID: 1, Email: "admin@test.com", Role: "admin"}
	req = req.WithContext(auth.WithUser(req.Context(), user))

	rr := httptest.NewRecorder()
	h.HandlePartsList(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	// Should contain Part A but NOT Part B
	if !strings.Contains(body, "Part A") {
		t.Errorf("expected Part A in response, got body: %s", body)
	}
	if strings.Contains(body, "Part B") {
		t.Error("Part B should not be in response")
	}
}
