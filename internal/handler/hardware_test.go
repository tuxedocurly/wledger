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
	"github.com/tuxedocurly/wledger/internal/i18n"
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

type mockRoundTripper func(*http.Request) (*http.Response, error)

func (f mockRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func setupHardwareHandlerTest(t *testing.T, name string, rt mockRoundTripper) (*Handler, db.Store, context.Context) {
	t.Helper()
	dbConn, err := db.Open("file:" + name + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	_ = s.InitSettings(context.Background())
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	uiError := uierror.New(logger)
	wClient := wled.NewClient()
	if rt != nil {
		wClient = wled.NewClientWithHTTPClient(&http.Client{Transport: rt})
	}
	wService := wled.NewService(s, wClient, logger)
	hwService := hardware.NewService(s, wClient, logger)
	settService := settings.NewService(s)

	h := &Handler{
		Logger:   logger,
		Queries:  s,
		Database: dbConn,
		Session:  scs.New(),
		UIError:  uiError,
		Hardware: hwService,
		Settings: settService,
		WLED:     wService,
	}

	u, err := s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, u.ID)
	return h, s, ctx
}

func TestHandler_ControllerValidationEditAndStatusBadge(t *testing.T) {
	rt := func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("offline")
	}
	h, s, ctx := setupHardwareHandlerTest(t, "handler_ctrl_val", rt)

	r := chi.NewRouter()
	r.Get("/hardware", h.HandleHardwareList)
	r.Post("/hardware", h.HandleHardwareCreate)
	r.Post("/hardware/{id}/update", h.HandleControllerUpdate)
	r.Get("/hardware/{id}/status", h.HandleHardwareStatus)

	// SSRF rejection on create
	badForm := url.Values{}
	badForm.Add("name", "Bad")
	badForm.Add("ip_address", "http://192.168.1.1/evil")
	badForm.Add("port", "80")
	reqBad := httptest.NewRequest(http.MethodPost, "/hardware", strings.NewReader(badForm.Encode()))
	reqBad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqBad = reqBad.WithContext(ctx)
	rrBad := httptest.NewRecorder()
	r.ServeHTTP(rrBad, reqBad)
	if rrBad.Code != http.StatusBadRequest {
		t.Errorf("expected 400 BadRequest for SSRF URL scheme, got %d", rrBad.Code)
	}

	// Create valid controller and edit via HandleControllerUpdate
	cRow, err := h.Hardware.CreateController(ctx, db.CreateControllerParams{Name: "Desk", IpAddress: "10.0.0.5", Port: sql.NullInt64{Int64: 80, Valid: true}})
	if err != nil {
		t.Fatalf("CreateController failed: %v", err)
	}

	updForm := url.Values{}
	updForm.Add("name", "Desk Pro")
	updForm.Add("ip_address", "10.0.0.6")
	updForm.Add("port", "8080")
	updForm.Add("use_udp", "true")
	updForm.Add("udp_port", "21325")
	updForm.Add("restore_state", "true")
	updForm.Add("idle_preset_id", "2")
	reqUpd := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/hardware/%d/update", cRow.ID), strings.NewReader(updForm.Encode()))
	reqUpd.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUpd = reqUpd.WithContext(ctx)
	rrUpd := httptest.NewRecorder()
	r.ServeHTTP(rrUpd, reqUpd)
	if rrUpd.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on update, got %d (%s)", rrUpd.Code, rrUpd.Body.String())
	}

	updated, _ := s.GetController(ctx, cRow.ID)
	if updated.Name != "Desk Pro" || updated.IpAddress != "10.0.0.6" || updated.Port.Int64 != 8080 || !updated.UseUdp.Bool || updated.UdpPort.Int64 != 21325 || updated.IdlePresetID.Int64 != 2 {
		t.Errorf("controller update fields did not persist: %+v", updated)
	}

	// Status control renders outerHTML target + manual refresh button without automatic polling
	if err := i18n.Init(); err != nil {
		t.Fatalf("i18n.Init failed: %v", err)
	}
	uiCtx := i18n.WithLocalizer(ctx, "en")

	reqList := httptest.NewRequest(http.MethodGet, "/hardware", nil).WithContext(uiCtx)
	rrList := httptest.NewRecorder()
	r.ServeHTTP(rrList, reqList)
	if rrList.Code != http.StatusOK {
		t.Fatalf("expected 200 on GET /hardware, got %d", rrList.Code)
	}
	listBody := rrList.Body.String()
	wantID := fmt.Sprintf("controller-status-%d", cRow.ID)
	if !strings.Contains(listBody, wantID) {
		t.Errorf("expected GET /hardware to contain %q", wantID)
	}
	if strings.Contains(listBody, "every 10s") {
		t.Errorf("expected GET /hardware not to contain client polling trigger every 10s")
	}

	reqStatus := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/hardware/%d/status", cRow.ID), nil).WithContext(uiCtx)
	rrStatus := httptest.NewRecorder()
	r.ServeHTTP(rrStatus, reqStatus)
	statusBody := rrStatus.Body.String()
	if rrStatus.Code != http.StatusOK || !strings.Contains(statusBody, `hx-swap="outerHTML"`) || !strings.Contains(statusBody, wantID) {
		t.Errorf("expected outerHTML status control %q, got code=%d body=%q", wantID, rrStatus.Code, statusBody)
	}
	if strings.Contains(statusBody, "every 10s") {
		t.Errorf("expected status snippet not to include client polling trigger every 10s, got %q", statusBody)
	}
	if !strings.Contains(statusBody, "Refresh Status") {
		t.Errorf("expected status snippet to include manual refresh control, got %q", statusBody)
	}

	reqInvalid := httptest.NewRequest(http.MethodGet, "/hardware/abc/status", nil).WithContext(uiCtx)
	rrInvalid := httptest.NewRecorder()
	r.ServeHTTP(rrInvalid, reqInvalid)
	if rrInvalid.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for non-numeric controller ID, got %d", rrInvalid.Code)
	}

	reqMissing := httptest.NewRequest(http.MethodGet, "/hardware/999999/status", nil).WithContext(uiCtx)
	rrMissing := httptest.NewRecorder()
	r.ServeHTTP(rrMissing, reqMissing)
	if rrMissing.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found for missing controller ID, got %d", rrMissing.Code)
	}
}

func TestHandler_StandaloneContainerAndIDORForbidden(t *testing.T) {
	h, s, ctx := setupHardwareHandlerTest(t, "handler_standalone", nil)

	r := chi.NewRouter()
	r.Post("/hardware/containers", h.HandleContainerCreateStandalone)
	r.Get("/hardware/containers/{id}/configure", h.HandleContainerConfigure)
	r.Post("/hardware/containers/{id}/configure", h.HandleContainerConfigureSave)
	r.Post("/hardware/containers/{id}/controller", h.HandleContainerBindController)
	r.Post("/hardware/{id}/grid", h.HandleHardwareGridSave)

	// Create standalone container via handler
	form := url.Values{}
	form.Add("name", "Organizer Box")
	form.Add("rows", "2")
	form.Add("cols", "3")
	req := httptest.NewRequest(http.MethodPost, "/hardware/containers", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect creating standalone container, got %d", rr.Code)
	}

	standaloneList, _ := h.Hardware.ListStandaloneContainers(ctx)
	if len(standaloneList) != 1 {
		t.Fatalf("expected 1 standalone container, got %d", len(standaloneList))
	}
	contID := standaloneList[0].ID

	// GET configure renders StandaloneGridPainter
	reqGet := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/hardware/containers/%d/configure", contID), nil).WithContext(ctx)
	rrGet := httptest.NewRecorder()
	r.ServeHTTP(rrGet, reqGet)
	if rrGet.Code != http.StatusOK || !strings.Contains(rrGet.Body.String(), "Organizer Box") {
		t.Fatalf("expected 200 standalone configure page, got %d", rrGet.Code)
	}

	// IDOR check: try saving standalone container ID under controller grid save
	cRow, _ := h.Hardware.CreateController(ctx, db.CreateControllerParams{Name: "C1", IpAddress: "10.0.0.70"})
	idorCfg := fmt.Sprintf(`[{"id":%d,"name":"Hijacked","segment_id":0,"config":{"type":"grid","rows":2,"cols":2}}]`, contID)
	idorForm := url.Values{}
	idorForm.Add("grid_data", `[]`)
	idorForm.Add("config_data", idorCfg)
	reqIDOR := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/hardware/%d/grid", cRow.ID), strings.NewReader(idorForm.Encode())).WithContext(ctx)
	reqIDOR.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrIDOR := httptest.NewRecorder()
	r.ServeHTTP(rrIDOR, reqIDOR)
	if rrIDOR.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden on cross-controller container IDOR, got %d", rrIDOR.Code)
	}

	// Bind standalone container to controller cRow.ID
	bindForm := url.Values{}
	bindForm.Add("controller_id", strconv.FormatInt(cRow.ID, 10))
	reqBind := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/hardware/containers/%d/controller", contID), strings.NewReader(bindForm.Encode())).WithContext(ctx)
	reqBind.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrBind := httptest.NewRecorder()
	r.ServeHTTP(rrBind, reqBind)
	if rrBind.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on container bind, got %d", rrBind.Code)
	}

	bound, _ := s.GetContainer(ctx, contID)
	if !bound.ControllerID.Valid || bound.ControllerID.Int64 != cRow.ID {
		t.Errorf("expected container bound to controller %d, got %+v", cRow.ID, bound.ControllerID)
	}
}

func TestHandler_LocateNetworkErrorPropagatesHTMXToast(t *testing.T) {
	rt := func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("network unreachable")
	}
	h, s, ctx := setupHardwareHandlerTest(t, "handler_locate_err", rt)

	cRow, _ := h.Hardware.CreateController(ctx, db.CreateControllerParams{Name: "Dead", IpAddress: "10.0.0.99", Port: sql.NullInt64{Int64: 80, Valid: true}})
	conts, _ := h.Hardware.GetContainers(ctx, cRow.ID)
	binID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: conts[0].ID, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})

	r := chi.NewRouter()
	r.Post("/hardware/{id}/locate", h.HandleHardwareLocate)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/hardware/%d/locate?bin_id=%d", cRow.ID, binID), nil).WithContext(ctx)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code == http.StatusOK {
		t.Fatalf("expected non-200 status on WLED network error, got 200")
	}
	if rr.Header().Get("HX-Reswap") != "none" {
		t.Errorf("expected HX-Reswap=none HTMX error toast header, got %q", rr.Header().Get("HX-Reswap"))
	}
}

func TestHandler_ControllerAddressNormalization(t *testing.T) {
	h, s, ctx := setupHardwareHandlerTest(t, "handler_addr_norm", nil)

	r := chi.NewRouter()
	r.Post("/hardware", h.HandleHardwareCreate)
	r.Post("/hardware/{id}/update", h.HandleControllerUpdate)

	validCases := []struct {
		name     string
		rawIP    string
		wantHost string
	}{
		{"plain http scheme", "http://wled-da8eec.local", "wled-da8eec.local"},
		{"wled ui copy paste whitespace", "http:// \nwled-da8eec\n .local", "wled-da8eec.local"},
		{"uppercase scheme trailing slash", "  HTTP://wled-da8eec.local/  ", "wled-da8eec.local"},
		{"ipv4 http with trailing slash", "  http://192.168.1.55/  ", "192.168.1.55"},
	}

	for idx, tc := range validCases {
		t.Run("create_"+tc.name, func(t *testing.T) {
			form := url.Values{}
			form.Add("name", fmt.Sprintf("Ctrl-%d", idx))
			form.Add("ip_address", tc.rawIP)
			form.Add("port", "80")

			req := httptest.NewRequest(http.MethodPost, "/hardware", strings.NewReader(form.Encode())).WithContext(ctx)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)

			if rr.Code != http.StatusSeeOther {
				t.Fatalf("expected 303 redirect for %q, got %d (%s)", tc.rawIP, rr.Code, rr.Body.String())
			}

			ctrls, _ := s.GetControllers(ctx)
			if ctrls[len(ctrls)-1].IpAddress != tc.wantHost {
				t.Errorf("saved IP = %q, want %q", ctrls[len(ctrls)-1].IpAddress, tc.wantHost)
			}
		})
	}

	// Verify HandleControllerUpdate also normalizes inputs cleanly.
	baseCtrl, err := h.Hardware.CreateController(ctx, db.CreateControllerParams{
		Name:      "UpdTarget",
		IpAddress: "10.0.0.88",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateController setup failed: %v", err)
	}

	updForm := url.Values{}
	updForm.Add("name", "UpdTarget")
	updForm.Add("ip_address", "http:// \nwled-da8eec\n .local/")
	updForm.Add("port", "80")
	reqUpd := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/hardware/%d/update", baseCtrl.ID), strings.NewReader(updForm.Encode())).WithContext(ctx)
	reqUpd.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrUpd := httptest.NewRecorder()
	r.ServeHTTP(rrUpd, reqUpd)
	if rrUpd.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on update, got %d (%s)", rrUpd.Code, rrUpd.Body.String())
	}
	gotCtrl, _ := s.GetController(ctx, baseCtrl.ID)
	if gotCtrl.IpAddress != "wled-da8eec.local" {
		t.Errorf("updated controller IP = %q, want wled-da8eec.local", gotCtrl.IpAddress)
	}

	invalidCases := []string{
		"https://192.168.1.55",
		"http://192.168.1.55/evil",
		"192.168.1.55 bad",
		"bad host.local",
	}

	for _, badIP := range invalidCases {
		t.Run("reject_"+badIP, func(t *testing.T) {
			form := url.Values{}
			form.Add("name", "Invalid")
			form.Add("ip_address", badIP)
			form.Add("port", "80")

			reqCreate := httptest.NewRequest(http.MethodPost, "/hardware", strings.NewReader(form.Encode())).WithContext(ctx)
			reqCreate.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rrCreate := httptest.NewRecorder()
			r.ServeHTTP(rrCreate, reqCreate)
			if rrCreate.Code != http.StatusBadRequest {
				t.Errorf("create: expected 400 BadRequest for %q, got %d", badIP, rrCreate.Code)
			}

			reqBadUpd := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/hardware/%d/update", baseCtrl.ID), strings.NewReader(form.Encode())).WithContext(ctx)
			reqBadUpd.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rrBadUpd := httptest.NewRecorder()
			r.ServeHTTP(rrBadUpd, reqBadUpd)
			if rrBadUpd.Code != http.StatusBadRequest {
				t.Errorf("update: expected 400 BadRequest for %q, got %d", badIP, rrBadUpd.Code)
			}
		})
	}
}
