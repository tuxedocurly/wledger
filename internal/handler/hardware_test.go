package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

// openTestDB opens an in-memory database with Foreign Keys enabled.
func openTestDB(t *testing.T) *sql.DB {
	dbConn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	return dbConn
}

// setupTestSchema applies migrations using db.Migrate
func setupTestSchema(t *testing.T, dbConn *sql.DB) {
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}
}

func TestControllerDeleteOrphansContainersPreservingBins(t *testing.T) {
	// Setup Environment
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	uiError := uierror.New(logger)
	wClient := wled.NewClient()
	wService := wled.NewService(s, wClient, logger)
	hwService := hardware.NewService(s, wClient, logger)
	settService := settings.NewService(s)

	h := &Handler{
		Logger:   logger,
		Queries:  s,
		Database: dbConn,
		UIError:  uiError,
		Hardware: hwService,
		Settings: settService,
		WLED:     wService,
	}

	ctx := context.Background()

	// Create Controller
	ctrl, err := s.CreateController(ctx, db.CreateControllerParams{
		Name:      "TestController",
		IpAddress: "192.168.1.100",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	// Create Container
	cont, err := s.CreateContainer(ctx, db.CreateContainerParams{
		Name: "C1", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}, SegmentID: 0,
	})
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	// Create Bins (Simulate an 8-LED strip)
	for i := 0; i < 8; i++ {
		_, err := s.CreateBin(ctx, db.CreateBinParams{
			Name:        "Bin-" + strconv.Itoa(i),
			ContainerID: cont,
			LedIndex:    sql.NullInt64{Int64: int64(i), Valid: true},
			Width:       sql.NullInt64{Int64: 1, Valid: true},
			GridX:       sql.NullInt64{Int64: int64(i), Valid: true},
			GridY:       sql.NullInt64{Int64: 0, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create bin %d: %v", i, err)
		}
	}

	// Verify Bins exist
	binsBefore, err := s.GetBinsByContainer(ctx, cont)
	if err != nil {
		t.Fatalf("failed to fetch bins: %v", err)
	}
	if len(binsBefore) != 8 {
		t.Fatalf("expected 8 bins, got %d", len(binsBefore))
	}

	// Delete Controller via HTTP Handler
	// This exercises the `HandleHardwareDelete` method
	r := chi.NewRouter()
	r.Post("/hardware/{id}/delete", h.HandleHardwareDelete)

	target := "/hardware/" + strconv.Itoa(int(ctrl.ID)) + "/delete"
	req := httptest.NewRequest(http.MethodPost, target, nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	// Check Handler Response
	if rr.Code != http.StatusSeeOther {
		t.Errorf("Handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	// Assertions (v2: controller deletion orphans container instead of cascading to bins)
	contAfter, err := s.GetContainer(ctx, cont)
	if err != nil {
		t.Fatalf("expected container %d to remain orphaned after controller delete: %v", cont, err)
	}
	if contAfter.ControllerID.Valid {
		t.Errorf("expected container controller_id to be NULL, got %d", contAfter.ControllerID.Int64)
	}

	// Check Bins by Container (Should preserve all 8 bins)
	binsAfter, err := s.GetBinsByContainer(ctx, cont)
	if err != nil {
		t.Fatalf("failed to fetch bins after delete: %v", err)
	}
	if len(binsAfter) != 8 {
		t.Errorf("expected 8 bins preserved in orphaned container, got %d", len(binsAfter))
	}

	var count int
	err = dbConn.QueryRow("SELECT count(*) FROM bins").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count bins: %v", err)
	}
	if count != 8 {
		t.Errorf("Expected 8 preserved bins, got %d", count)
	}
}

func TestHardwareAuditLogging(t *testing.T) {
	// Setup
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	session := scs.New()
	uiError := uierror.New(logger)
	wClient := wled.NewClient()
	wService := wled.NewService(s, wClient, logger)
	hwService := hardware.NewService(s, wClient, logger)
	settService := settings.NewService(s)

	h := &Handler{
		Logger:   logger,
		Queries:  s,
		Database: dbConn,
		Session:  session,
		UIError:  uiError,
		Hardware: hwService,
		Settings: settService,
		WLED:     wService,
	}

	// Mock Admin Context
	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Create Controller
	r := chi.NewRouter()
	r.Post("/hardware", h.HandleHardwareCreate)

	form := url.Values{}
	form.Add("name", "Audit Ctrl")
	form.Add("ip_address", "10.0.0.1")
	form.Add("port", "80")

	req := httptest.NewRequest(http.MethodPost, "/hardware", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// Verify Create Log
	logs, _ := s.GetAllAuditLogs(ctx)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log after create, got %d", len(logs))
	}
	createLog := logs[0]
	var createNew map[string]any
	json.Unmarshal(createLog.NewValue, &createNew)
	if createNew["name"] != "Audit Ctrl" || createNew["ip_address"] != "10.0.0.1" {
		t.Errorf("expected summary in create log, got %s", string(createLog.NewValue))
	}

	// Update Grid (Grid Save)
	ctrl, _ := s.GetControllers(ctx)
	id := ctrl[0].ID

	// Fetch the default container created during controller creation
	containers, _ := s.GetContainersByController(ctx, sql.NullInt64{Int64: id, Valid: true})
	contID := containers[0].ID

	r2 := chi.NewRouter()
	r2.Post("/hardware/{id}/grid", h.HandleHardwareGridSave)

	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":0,"name":"A1"}]`
	configData := fmt.Sprintf(`[{"id":%d,"name":"Audit Ctrl (Main)","segment_id":0,"config":{"type":"grid","rows":1,"cols":1}}]`, contID)
	form2 := url.Values{}
	form2.Add("grid_data", gridData)
	form2.Add("config_data", configData)

	req2 := httptest.NewRequest(http.MethodPost, "/hardware/"+strconv.Itoa(int(id))+"/grid", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2 = req2.WithContext(ctx)
	rr2 := httptest.NewRecorder()
	r2.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusSeeOther {
		t.Fatalf("Grid save failed: %d - %s", rr2.Code, rr2.Body.String())
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs after grid update, got %d", len(logs))
	}
	gridLog := logs[1]

	if len(gridLog.NewValue) < 5 { // Check if empty
		t.Errorf("expected rich log for grid update, got empty")
	}

	// Delete Controller
	r3 := chi.NewRouter()
	r3.Post("/hardware/{id}/delete", h.HandleHardwareDelete)
	req3 := httptest.NewRequest(http.MethodPost, "/hardware/"+strconv.Itoa(int(id))+"/delete", nil)
	req3 = req3.WithContext(ctx)
	rr3 := httptest.NewRecorder()
	r3.ServeHTTP(rr3, req3)

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 3 {
		t.Fatalf("expected 3 logs after delete, got %d", len(logs))
	}
	deleteLog := logs[2]
	var deleteOld map[string]any
	json.Unmarshal(deleteLog.OldValue, &deleteOld)
	if deleteOld["name"] != "Audit Ctrl" {
		t.Errorf("expected summary in delete log, got %s", string(deleteLog.OldValue))
	}
}
