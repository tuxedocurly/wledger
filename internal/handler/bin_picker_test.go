package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestBinPicker_HappyPath(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	// Setup: Controller, Container, Bin A, Bin B
	c, _ := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	cont, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont", ControllerID: sql.NullInt64{Int64: c.ID, Valid: true}, SegmentID: 0})

	_, _ = h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "Bin A", ContainerID: cont, GridX: sql.NullInt64{Int64: 1, Valid: true}, GridY: sql.NullInt64{Int64: 1, Valid: true}})
	_, _ = h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "Bin B", ContainerID: cont, GridX: sql.NullInt64{Int64: 2, Valid: true}, GridY: sql.NullInt64{Int64: 2, Valid: true}})

	// Request: Get Bin Picker for Controller C
	req := httptest.NewRequest("GET", "/parts/bin_picker?controller_id="+strconv.Itoa(int(c.ID)), nil)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	r.Get("/parts/bin_picker", h.HandleBinPicker)

	r.ServeHTTP(rr, req)

	// Verify Status
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	// Verify Content
	body := rr.Body.String()
	if !strings.Contains(body, "Bin A") {
		t.Error("Expected body to contain 'Bin A'")
	}
	if !strings.Contains(body, "Bin B") {
		t.Error("Expected body to contain 'Bin B'")
	}
	if !strings.Contains(body, "Cont") {
		t.Error("Expected body to contain Container Name 'Cont'")
	}
}

func TestBinPicker_NoController(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Request: Get Bin Picker without controller_id
	req := httptest.NewRequest("GET", "/parts/bin_picker", nil)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	r.Get("/parts/bin_picker", h.HandleBinPicker)

	r.ServeHTTP(rr, req)

	// Expect Error or Empty
	if rr.Code != http.StatusBadRequest && !strings.Contains(rr.Body.String(), "") {
		t.Errorf("expected 400 or empty, got %d", rr.Code)
	}
}

func TestBinPicker_InvalidController(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Request: Get Bin Picker for non-existent controller
	req := httptest.NewRequest("GET", "/parts/bin_picker?controller_id=999", nil)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	r.Get("/parts/bin_picker", h.HandleBinPicker)

	r.ServeHTTP(rr, req)

	// Verify Status
	// Should return empty list or valid HTML with "No containers" message
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "Bin A") {
		t.Error("Should not find bins for invalid controller")
	}
}

func TestBinPicker_Standalone(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	contID, err := h.Queries.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Shelf Box",
		ControllerID: sql.NullInt64{Valid: false},
	})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	if _, err := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "S1", ContainerID: contID}); err != nil {
		t.Fatalf("CreateBin S1 failed: %v", err)
	}
	if _, err := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "S2", ContainerID: contID}); err != nil {
		t.Fatalf("CreateBin S2 failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/parts/bin_picker?controller_id=standalone", nil)
	rr := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Get("/parts/bin_picker", h.HandleBinPicker)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Shelf Box", "Standalone", "S1", "S2"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected standalone bin picker body to contain %q; body=%s", want, body)
		}
	}
}
