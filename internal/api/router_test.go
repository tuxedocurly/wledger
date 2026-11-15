package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/api"
	"github.com/tuxedocurly/wledger/internal/apitoken"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/stock"
	"github.com/tuxedocurly/wledger/internal/tags"
	"github.com/tuxedocurly/wledger/internal/wled"
)

type spyWLED struct {
	mu          sync.Mutex
	locateParts []int64
	locateBins  [][2]int64
	stopCount   int
}

func (s *spyWLED) LocatePart(_ context.Context, partID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locateParts = append(s.locateParts, partID)
	return nil
}

func (s *spyWLED) LocateBin(_ context.Context, controllerID, binID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locateBins = append(s.locateBins, [2]int64{controllerID, binID})
	return nil
}

func (s *spyWLED) GlobalOff(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopCount++
	return nil
}

type stubBarcodes struct{}

func (stubBarcodes) ParseBarcode(_ context.Context, raw string) (api.ParsedBarcodeResult, error) {
	return api.ParsedBarcodeResult{
		Supplier:           "DigiKey",
		PartNumber:         "RC0603FR-0710KL",
		SupplierPartNumber: "311-10.0KHRCT-ND",
		Manufacturer:       "YAGEO",
		Quantity:           100,
		OrderNumber:        "PO-777",
		RawBarcode:         raw,
		Format:             "TIA-1063",
	}, nil
}

type stubProjects struct {
	mu           sync.Mutex
	locatedIDs   []int64
	builtCalls   [][2]int64
	shortageOnID int64
}

func (p *stubProjects) ListProjects(_ context.Context) ([]api.ProjectSummary, error) {
	return []api.ProjectSummary{
		{ID: 1, Name: "LED Matrix", Description: "8x8 board", Status: "draft", BOMItemCount: 3},
	}, nil
}

func (p *stubProjects) LocateProject(_ context.Context, projectID int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.locatedIDs = append(p.locatedIDs, projectID)
	return nil
}

func (p *stubProjects) BuildProject(_ context.Context, projectID int64, boardCount int) (api.ProjectBuildResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if projectID == p.shortageOnID {
		return api.ProjectBuildResult{}, api.ErrProjectShortage
	}
	p.builtCalls = append(p.builtCalls, [2]int64{projectID, int64(boardCount)})
	return api.ProjectBuildResult{
		ProjectID:     projectID,
		ProjectName:   "LED Matrix",
		BoardCount:    boardCount,
		ItemsBuilt:    3,
		TotalDeducted: boardCount * 10,
		Message:       "build completed",
	}, nil
}

type apiTestHarness struct {
	conn     *sql.DB
	store    db.Store
	tokenSvc apitoken.Service
	partsSvc parts.Service
	stockSvc stock.Service
	hwSvc    hardware.Service
	wledSpy  *spyWLED
	projSpy  *stubProjects
	handler  http.Handler
}

func newHarness(t *testing.T, withBarcodes, withProjects bool) *apiTestHarness {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "api_v1_test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	store := db.NewStore(conn)
	if err := store.InitSettings(context.Background()); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	discardLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokenSvc := apitoken.NewService(store, apitoken.WithLogger(discardLogger))
	t.Cleanup(tokenSvc.Close)

	tagSvc := tags.NewService(conn, store)
	docSvc := documents.NewService(store, discardLogger)
	partsSvc := parts.NewService(conn, store, discardLogger, tagSvc, docSvc)
	stockSvc := stock.NewService(store, discardLogger)
	hwSvc := hardware.NewService(store, wled.NewClient(), discardLogger)
	wSpy := &spyWLED{}
	pSpy := &stubProjects{shortageOnID: 99}

	deps := api.Dependencies{
		Store:    store,
		Tokens:   tokenSvc,
		Parts:    partsSvc,
		Stock:    stockSvc,
		Hardware: hwSvc,
		WLED:     wSpy,
		Logger:   discardLogger,
		Version:  "v2.test",
	}
	if withBarcodes {
		deps.Barcodes = stubBarcodes{}
	}
	if withProjects {
		deps.Projects = pSpy
	}

	root := chi.NewRouter()
	root.Mount("/api/v1", api.NewRouter(deps))

	return &apiTestHarness{
		conn:     conn,
		store:    store,
		tokenSvc: tokenSvc,
		partsSvc: partsSvc,
		stockSvc: stockSvc,
		hwSvc:    hwSvc,
		wledSpy:  wSpy,
		projSpy:  pSpy,
		handler:  root,
	}
}

func doAPIRequest(t *testing.T, h http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reqBody = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeBody(t *testing.T, rr *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode JSON (%d %s): %v", rr.Code, rr.Body.String(), err)
	}
}

func seedUserAndToken(t *testing.T, svc apitoken.Service, store db.Store, email, role, scopes string, exp *time.Time) (int64, apitoken.CreatedToken) {
	t.Helper()
	u, err := store.CreateUser(context.Background(), db.CreateUserParams{
		Email:        email,
		PasswordHash: "hash",
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	tok, err := svc.CreateToken(context.Background(), apitoken.CreateTokenRequest{
		UserID:    u.ID,
		Name:      email + "-tok",
		Scopes:    scopes,
		ExpiresAt: exp,
	})
	if err != nil {
		t.Fatalf("create token for %s: %v", email, err)
	}
	return u.ID, tok
}

func TestAPI_HealthAndAuthEnforcement(t *testing.T) {
	h := newHarness(t, true, true)
	ctx := context.Background()

	// GET /health is accessible without authentication.
	healthRR := doAPIRequest(t, h.handler, http.MethodGet, "/api/v1/health", "", nil)
	if healthRR.Code != http.StatusOK {
		t.Fatalf("expected 200 on /health, got %d", healthRR.Code)
	}
	var health api.HealthResponse
	decodeBody(t, healthRR, &health)
	if health.Status != "ok" || health.Version != "v2.test" {
		t.Fatalf("unexpected health response: %+v", health)
	}

	// Missing / malformed / unknown / revoked / expired tokens return 401 JSON.
	for _, tc := range []struct {
		name string
		hdr  string
	}{
		{name: "no auth header", hdr: ""},
		{name: "basic scheme", hdr: "Basic dXNlcjpwYXNz"},
		{name: "empty bearer", hdr: "Bearer    "},
		{name: "invalid bearer", hdr: "Bearer wlg_doesnotexist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/parts", nil)
			if tc.hdr != "" {
				req.Header.Set("Authorization", tc.hdr)
			}
			rr := httptest.NewRecorder()
			h.handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d (%s)", rr.Code, rr.Body.String())
			}
			var errResp api.ErrorResponse
			decodeBody(t, rr, &errResp)
			if errResp.Error == "" {
				t.Fatal("expected non-empty JSON error message")
			}
		})
	}

	// Revoked token -> 401.
	editorID, revocable := seedUserAndToken(t, h.tokenSvc, h.store, "rev@example.com", "editor", "read,write", nil)
	if err := h.tokenSvc.RevokeTokenForUser(ctx, revocable.Token.ID, editorID); err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	revRR := doAPIRequest(t, h.handler, http.MethodGet, "/api/v1/parts", revocable.Plaintext, nil)
	if revRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for revoked token, got %d", revRR.Code)
	}

	// Expired token -> 401 via deterministic clock service.
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	clockSvc := apitoken.NewService(h.store, apitoken.WithNowFunc(func() time.Time { return now }))
	defer clockSvc.Close()
	exp := now.Add(5 * time.Minute)
	_, expTok := seedUserAndToken(t, clockSvc, h.store, "exp@example.com", "editor", "read", &exp)
	now = now.Add(10 * time.Minute)
	clockRouter := chi.NewRouter()
	clockRouter.Mount("/api/v1", api.NewRouter(api.Dependencies{Store: h.store, Tokens: clockSvc}))
	expRR := doAPIRequest(t, clockRouter, http.MethodGet, "/api/v1/parts", expTok.Plaintext, nil)
	if expRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired token, got %d", expRR.Code)
	}

	// Read-only token on write endpoints -> 403 Forbidden.
	_, readTok := seedUserAndToken(t, h.tokenSvc, h.store, "reader@example.com", "editor", "read", nil)
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/v1/parts", map[string]any{"name": "Denied"}},
		{http.MethodPut, "/api/v1/parts/1", map[string]any{"name": "Denied"}},
		{http.MethodDelete, "/api/v1/parts/1", nil},
		{http.MethodPost, "/api/v1/stock/assign", map[string]any{"part_id": 1, "bin_id": 1, "quantity": 5}},
		{http.MethodPost, "/api/v1/stock/1/adjust", map[string]any{"delta": 1}},
		{http.MethodPost, "/api/v1/projects/1/build", map[string]any{"board_count": 1}},
	} {
		rr := doAPIRequest(t, h.handler, tc.method, tc.path, readTok.Plaintext, tc.body)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s %s with read token expected 403, got %d (%s)", tc.method, tc.path, rr.Code, rr.Body.String())
		}
	}

	// Role demotion: token initially has write scope, then user is demoted to viewer -> 403!
	demotedID, writeTok := seedUserAndToken(t, h.tokenSvc, h.store, "demote@example.com", "editor", "write", nil)
	if _, err := h.conn.Exec(`UPDATE users SET role = 'viewer' WHERE id = ?`, demotedID); err != nil {
		t.Fatalf("demote user role: %v", err)
	}
	demotedRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/parts", writeTok.Plaintext, map[string]any{"name": "Blocked"})
	if demotedRR.Code != http.StatusForbidden {
		t.Fatalf("demoted viewer must receive 403 on POST /parts, got %d", demotedRR.Code)
	}
}

func TestAPI_PartsCRUDFilterLowStockAndAuditAttribution(t *testing.T) {
	h := newHarness(t, true, true)
	ctx := context.Background()

	editorID, writeTok := seedUserAndToken(t, h.tokenSvc, h.store, "builder@example.com", "editor", "read,write", nil)
	_, readTok := seedUserAndToken(t, h.tokenSvc, h.store, "ro@example.com", "viewer", "read", nil)

	// Seed controller + container + bin so bin_id filter and stock assignment work end-to-end.
	ctrl, err := h.store.CreateController(ctx, db.CreateControllerParams{Name: "MainCtrl", IpAddress: "192.168.1.50"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	contID, err := h.store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Shelf A",
		ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
		SegmentID:    0,
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	binID, err := h.store.CreateBin(ctx, db.CreateBinParams{
		Name:        "A1",
		ContainerID: contID,
		LedIndex:    sql.NullInt64{Int64: 0, Valid: true},
		Width:       sql.NullInt64{Int64: 1, Valid: true},
	})
	if err != nil {
		t.Fatalf("create bin: %v", err)
	}

	// POST /parts -> 201 Created.
	createRes := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/parts", writeTok.Plaintext, map[string]any{
		"name":                "10k Resistor 0603",
		"description":         "1% thick film",
		"part_number":         "RC0603FR-0710KL",
		"manufacturer":        "YAGEO",
		"supplier":            "DigiKey",
		"barcode_data":        "DK-BARCODE-10K",
		"unit_cost":           0.05,
		"reorder_level":       20,
		"min_stock_threshold": 5,
		"tags":                []string{"smd", "resistor"},
		"links":               []map[string]string{{"label": "Datasheet", "url": "https://example.com/ds.pdf"}},
	})
	if createRes.Code != http.StatusCreated {
		t.Fatalf("POST /parts expected 201, got %d (%s)", createRes.Code, createRes.Body.String())
	}
	var p1 api.PartDetailResponse
	decodeBody(t, createRes, &p1)
	if p1.ID <= 0 || p1.Name != "10k Resistor 0603" || len(p1.Tags) != 2 {
		t.Fatalf("unexpected created part payload: %+v", p1)
	}

	// Duplicate barcode_data -> 409 Conflict.
	dupRes := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/parts", writeTok.Plaintext, map[string]any{
		"name":         "Duplicate Barcode Part",
		"barcode_data": "DK-BARCODE-10K",
	})
	if dupRes.Code != http.StatusConflict {
		t.Fatalf("duplicate barcode expected 409 Conflict, got %d (%s)", dupRes.Code, dupRes.Body.String())
	}

	// Create second healthy-stock part and third default-threshold part.
	p2Res := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/parts", writeTok.Plaintext, map[string]any{
		"name":                "ESP32-S3 DevKit",
		"part_number":         "ESP32-S3-DEV",
		"reorder_level":       2,
		"min_stock_threshold": 1,
		"tags":                []string{"mcu"},
	})
	if p2Res.Code != http.StatusCreated {
		t.Fatalf("POST second part expected 201, got %d", p2Res.Code)
	}
	var p2 api.PartDetailResponse
	decodeBody(t, p2Res, &p2)

	// Assign 10 units to p1 (still <= reorder_level 20 -> low stock) and 5 units to p2 (> reorder_level 2 -> ok).
	assign1 := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/stock/assign", writeTok.Plaintext, map[string]any{
		"part_id":  p1.ID,
		"bin_id":   binID,
		"quantity": 10,
	})
	if assign1.Code != http.StatusOK {
		t.Fatalf("POST /stock/assign p1 expected 200, got %d (%s)", assign1.Code, assign1.Body.String())
	}
	assign2 := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/stock/assign", writeTok.Plaintext, map[string]any{
		"part_id":  p2.ID,
		"bin_id":   binID,
		"quantity": 5,
	})
	if assign2.Code != http.StatusOK {
		t.Fatalf("POST /stock/assign p2 expected 200, got %d (%s)", assign2.Code, assign2.Body.String())
	}

	// GET /parts/{id}
	getRR := doAPIRequest(t, h.handler, http.MethodGet, "/api/v1/parts/"+strconv.FormatInt(p1.ID, 10), readTok.Plaintext, nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET /parts/%d expected 200, got %d", p1.ID, getRR.Code)
	}
	var fetched api.PartDetailResponse
	decodeBody(t, getRR, &fetched)
	if fetched.TotalStock != 10 || fetched.ValidStock != 10 || len(fetched.Assignments) != 1 || len(fetched.Links) != 1 {
		t.Fatalf("unexpected fetched detail: %+v", fetched)
	}

	// GET /parts filters: q, tag, bin_id, limit/offset.
	searchRR := doAPIRequest(t, h.handler, http.MethodGet, "/api/v1/parts?q=Resistor&tag=SMD&bin_id="+strconv.FormatInt(binID, 10)+"&limit=1&offset=0", readTok.Plaintext, nil)
	if searchRR.Code != http.StatusOK {
		t.Fatalf("GET /parts filtered expected 200, got %d (%s)", searchRR.Code, searchRR.Body.String())
	}
	var list api.PartListResponse
	decodeBody(t, searchRR, &list)
	if list.Count != 1 || list.Parts[0].ID != p1.ID {
		t.Fatalf("expected 1 filtered part (%d), got %+v", p1.ID, list)
	}

	// GET /parts pagination offset past end returns empty array.
	emptyPage := doAPIRequest(t, h.handler, http.MethodGet, "/api/v1/parts?tag=smd&limit=5&offset=10", readTok.Plaintext, nil)
	if emptyPage.Code != http.StatusOK {
		t.Fatalf("GET /parts empty offset expected 200, got %d", emptyPage.Code)
	}
	var emptyList api.PartListResponse
	decodeBody(t, emptyPage, &emptyList)
	if emptyList.Count != 0 || len(emptyList.Parts) != 0 {
		t.Fatalf("expected 0 parts on high offset, got %+v", emptyList)
	}

	// GET /low-stock returns p1 (10 <= 20) and omits p2 (5 > 2).
	lowRR := doAPIRequest(t, h.handler, http.MethodGet, "/api/v1/low-stock", readTok.Plaintext, nil)
	if lowRR.Code != http.StatusOK {
		t.Fatalf("GET /low-stock expected 200, got %d", lowRR.Code)
	}
	var lowList api.PartListResponse
	decodeBody(t, lowRR, &lowList)
	if lowList.Count != 1 || lowList.Parts[0].ID != p1.ID || lowList.Parts[0].StockStatus != "low" {
		t.Fatalf("expected only p1 in low-stock, got %+v", lowList)
	}

	// Partial PUT /parts/{id}: update only name & verify description/tags/reorder_level preserved.
	putRR := doAPIRequest(t, h.handler, http.MethodPut, "/api/v1/parts/"+strconv.FormatInt(p1.ID, 10), writeTok.Plaintext, map[string]any{
		"name": "10k SMD Resistor Updated",
	})
	if putRR.Code != http.StatusOK {
		t.Fatalf("PUT /parts/%d expected 200, got %d (%s)", p1.ID, putRR.Code, putRR.Body.String())
	}
	var updated api.PartDetailResponse
	decodeBody(t, putRR, &updated)
	if updated.Name != "10k SMD Resistor Updated" || updated.Description != "1% thick film" || updated.ReorderLevel != 20 || len(updated.Tags) != 2 {
		t.Fatalf("partial PUT should preserve omitted fields, got %+v", updated)
	}

	// POST /stock/{assignmentID}/adjust (+4 -> 14).
	assignmentID := fetched.Assignments[0].ID
	adjRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/stock/"+strconv.FormatInt(assignmentID, 10)+"/adjust", writeTok.Plaintext, map[string]any{
		"delta": 4,
	})
	if adjRR.Code != http.StatusOK {
		t.Fatalf("POST /stock/%d/adjust expected 200, got %d (%s)", assignmentID, adjRR.Code, adjRR.Body.String())
	}

	// Verify orphaned stock adjustment works cleanly after controller deletion orphans containers/bins.
	if err := h.hwSvc.DeleteController(ctx, ctrl.ID); err != nil {
		t.Fatalf("orphan controller: %v", err)
	}
	orphanAdjRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/stock/"+strconv.FormatInt(assignmentID, 10)+"/adjust", writeTok.Plaintext, map[string]any{
		"delta": -2,
	})
	if orphanAdjRR.Code != http.StatusOK {
		t.Fatalf("adjusting orphaned stock expected 200, got %d (%s)", orphanAdjRR.Code, orphanAdjRR.Body.String())
	}
	afterOrphan, err := h.store.GetAssignment(ctx, assignmentID)
	if err != nil || afterOrphan.Quantity != 12 {
		t.Fatalf("expected quantity 12 after orphaned adjust, got %+v err=%v", afterOrphan, err)
	}

	// DELETE /parts/{id} then verify 404 on subsequent GET.
	delRR := doAPIRequest(t, h.handler, http.MethodDelete, "/api/v1/parts/"+strconv.FormatInt(p2.ID, 10), writeTok.Plaintext, nil)
	if delRR.Code != http.StatusOK {
		t.Fatalf("DELETE /parts/%d expected 200, got %d", p2.ID, delRR.Code)
	}
	notFoundRR := doAPIRequest(t, h.handler, http.MethodGet, "/api/v1/parts/"+strconv.FormatInt(p2.ID, 10), readTok.Plaintext, nil)
	if notFoundRR.Code != http.StatusNotFound {
		t.Fatalf("GET deleted part expected 404, got %d", notFoundRR.Code)
	}

	// Verify audit logs carry editor's user_id!
	logs, err := h.store.GetAllAuditLogs(ctx)
	if err != nil {
		t.Fatalf("GetAllAuditLogs: %v", err)
	}
	foundPartLog := false
	foundStockLog := false
	for _, l := range logs {
		if l.EntityType == "PART" && l.UserID.Valid && l.UserID.Int64 == editorID {
			foundPartLog = true
		}
		if (l.EntityType == "STOCK" || l.ActionType == "STOCK_INC") && l.UserID.Valid && l.UserID.Int64 == editorID {
			foundStockLog = true
		}
	}
	if !foundPartLog || !foundStockLog {
		t.Fatalf("expected PART and STOCK audit logs attributed to user %d, got %+v", editorID, logs)
	}
}

func TestAPI_BarcodesHardwareAndProjects(t *testing.T) {
	h := newHarness(t, true, true)
	ctx := context.Background()

	_, readTok := seedUserAndToken(t, h.tokenSvc, h.store, "hwread@example.com", "viewer", "read", nil)
	_, writeTok := seedUserAndToken(t, h.tokenSvc, h.store, "hwwrite@example.com", "editor", "read,write", nil)

	// Seed controller + container + bin + part for locate & barcode match tests.
	ctrl, err := h.store.CreateController(ctx, db.CreateControllerParams{Name: "Rack 1", IpAddress: "10.0.0.10"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	contID, err := h.store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Drawer Grid",
		ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
		SegmentID:    0,
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	binID, err := h.store.CreateBin(ctx, db.CreateBinParams{
		Name:        "B1",
		ContainerID: contID,
		LedIndex:    sql.NullInt64{Int64: 3, Valid: true},
		Width:       sql.NullInt64{Int64: 2, Valid: true},
	})
	if err != nil {
		t.Fatalf("create bin: %v", err)
	}
	partID, err := h.partsSvc.CreatePart(ctx, parts.CreatePartRequest{
		Name:       "10k Yageo",
		PartNumber: "RC0603FR-0710KL",
	})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}

	// POST /barcodes/parse matches existing part in SQLite!
	barRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/barcodes/parse", readTok.Plaintext, map[string]any{
		"barcode": "[)>\x1e06\x1d1PRC0603FR-0710KL\x1dQ100\x1e\x04",
	})
	if barRR.Code != http.StatusOK {
		t.Fatalf("POST /barcodes/parse expected 200, got %d (%s)", barRR.Code, barRR.Body.String())
	}
	var barRes api.ParsedBarcodeResult
	decodeBody(t, barRR, &barRes)
	if barRes.Supplier != "DigiKey" || barRes.Quantity != 100 || barRes.ExistingPartID == nil || *barRes.ExistingPartID != partID {
		t.Fatalf("unexpected barcode parse result: %+v", barRes)
	}

	// Nil BarcodeParser returns clear 501 Not Implemented.
	nilHarness := newHarness(t, false, false)
	_, nilReadTok := seedUserAndToken(t, nilHarness.tokenSvc, nilHarness.store, "nilread@example.com", "viewer", "read", nil)
	nilBarRR := doAPIRequest(t, nilHarness.handler, http.MethodPost, "/api/v1/barcodes/parse", nilReadTok.Plaintext, map[string]any{"barcode": "12345"})
	if nilBarRR.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 when BarcodeParser is nil, got %d", nilBarRR.Code)
	}

	// GET /containers & GET /controllers
	contRR := doAPIRequest(t, h.handler, http.MethodGet, "/api/v1/containers", readTok.Plaintext, nil)
	if contRR.Code != http.StatusOK {
		t.Fatalf("GET /containers expected 200, got %d", contRR.Code)
	}
	var contPayload struct {
		Containers []api.ContainerResponse `json:"containers"`
		Count      int                     `json:"count"`
	}
	decodeBody(t, contRR, &contPayload)
	if contPayload.Count != 1 || len(contPayload.Containers[0].Bins) != 1 || contPayload.Containers[0].Bins[0].ID != binID {
		t.Fatalf("unexpected containers payload: %+v", contPayload)
	}

	ctrlRR := doAPIRequest(t, h.handler, http.MethodGet, "/api/v1/controllers", readTok.Plaintext, nil)
	if ctrlRR.Code != http.StatusOK {
		t.Fatalf("GET /controllers expected 200, got %d", ctrlRR.Code)
	}
	var ctrlPayload struct {
		Controllers []api.ControllerResponse `json:"controllers"`
		Count       int                      `json:"count"`
	}
	decodeBody(t, ctrlRR, &ctrlPayload)
	if ctrlPayload.Count != 1 || ctrlPayload.Controllers[0].ID != ctrl.ID {
		t.Fatalf("unexpected controllers payload: %+v", ctrlPayload)
	}

	// POST /parts/{id}/locate (`read` scope)
	locPartRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/parts/"+strconv.FormatInt(partID, 10)+"/locate", readTok.Plaintext, nil)
	if locPartRR.Code != http.StatusOK {
		t.Fatalf("POST /parts/{id}/locate expected 200, got %d (%s)", locPartRR.Code, locPartRR.Body.String())
	}

	// POST /controllers/{id}/locate with omitted bin_id resolves first bin, and with explicit bin_id uses explicit bin.
	locCtrlAutoRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/controllers/"+strconv.FormatInt(ctrl.ID, 10)+"/locate", readTok.Plaintext, nil)
	if locCtrlAutoRR.Code != http.StatusOK {
		t.Fatalf("POST /controllers/{id}/locate auto-bin expected 200, got %d (%s)", locCtrlAutoRR.Code, locCtrlAutoRR.Body.String())
	}
	locCtrlExplicitRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/controllers/"+strconv.FormatInt(ctrl.ID, 10)+"/locate", readTok.Plaintext, map[string]any{"bin_id": binID})
	if locCtrlExplicitRR.Code != http.StatusOK {
		t.Fatalf("POST /controllers/{id}/locate explicit bin expected 200, got %d", locCtrlExplicitRR.Code)
	}

	// POST /hardware/stop-locate
	stopRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/hardware/stop-locate", readTok.Plaintext, nil)
	if stopRR.Code != http.StatusOK {
		t.Fatalf("POST /hardware/stop-locate expected 200, got %d", stopRR.Code)
	}

	h.wledSpy.mu.Lock()
	if len(h.wledSpy.locateParts) != 1 || h.wledSpy.locateParts[0] != partID {
		t.Fatalf("unexpected WLED LocatePart calls: %+v", h.wledSpy.locateParts)
	}
	if len(h.wledSpy.locateBins) != 2 || h.wledSpy.locateBins[0] != [2]int64{ctrl.ID, binID} || h.wledSpy.locateBins[1] != [2]int64{ctrl.ID, binID} {
		t.Fatalf("unexpected WLED LocateBin calls: %+v", h.wledSpy.locateBins)
	}
	if h.wledSpy.stopCount != 1 {
		t.Fatalf("expected 1 GlobalOff call, got %d", h.wledSpy.stopCount)
	}
	h.wledSpy.mu.Unlock()

	// Projects endpoints: GET /projects, POST /projects/{id}/locate, POST /projects/{id}/build, 409 shortage.
	projRR := doAPIRequest(t, h.handler, http.MethodGet, "/api/v1/projects", readTok.Plaintext, nil)
	if projRR.Code != http.StatusOK {
		t.Fatalf("GET /projects expected 200, got %d", projRR.Code)
	}

	// DB fallback GET /projects on nil ProjectManager works directly against Wave 1 tables!
	createdProj, err := nilHarness.store.CreateProject(ctx, db.CreateProjectParams{
		Name:   "DB Fallback Project",
		Status: "draft",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	nilProjRR := doAPIRequest(t, nilHarness.handler, http.MethodGet, "/api/v1/projects", nilReadTok.Plaintext, nil)
	if nilProjRR.Code != http.StatusOK {
		t.Fatalf("GET /projects fallback expected 200, got %d", nilProjRR.Code)
	}
	var nilProjBody struct {
		Projects []api.ProjectSummary `json:"projects"`
		Count    int                  `json:"count"`
	}
	decodeBody(t, nilProjRR, &nilProjBody)
	if nilProjBody.Count != 1 || nilProjBody.Projects[0].ID != createdProj.ID {
		t.Fatalf("unexpected DB fallback projects response: %+v", nilProjBody)
	}

	locProjRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/projects/1/locate", readTok.Plaintext, nil)
	if locProjRR.Code != http.StatusOK {
		t.Fatalf("POST /projects/1/locate expected 200, got %d", locProjRR.Code)
	}

	buildRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/projects/1/build", writeTok.Plaintext, map[string]any{"board_count": 3})
	if buildRR.Code != http.StatusOK {
		t.Fatalf("POST /projects/1/build expected 200, got %d (%s)", buildRR.Code, buildRR.Body.String())
	}
	var buildRes api.ProjectBuildResult
	decodeBody(t, buildRR, &buildRes)
	if buildRes.ProjectID != 1 || buildRes.BoardCount != 3 || buildRes.TotalDeducted != 30 {
		t.Fatalf("unexpected build response: %+v", buildRes)
	}

	shortageRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/projects/99/build", writeTok.Plaintext, map[string]any{"board_count": 1})
	if shortageRR.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on BOM shortage, got %d (%s)", shortageRR.Code, shortageRR.Body.String())
	}

	badBuildRR := doAPIRequest(t, h.handler, http.MethodPost, "/api/v1/projects/1/build", writeTok.Plaintext, map[string]any{"board_count": 0})
	if badBuildRR.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for board_count=0, got %d", badBuildRR.Code)
	}
}
