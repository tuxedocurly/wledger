package projects

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func setupProjectsTestDB(t *testing.T) (*sql.DB, db.Store, context.Context) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "projects_test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}
	store := db.NewStore(conn)

	// Seed admin user ID 1 so audit.Log records valid user_id FK.
	user, err := store.CreateUser(context.Background(), db.CreateUserParams{
		Email:        "builder@test.local",
		PasswordHash: "hashed",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, user.ID)
	return conn, store, ctx
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type mockWLEDService struct {
	mu              sync.Mutex
	locatePartCalls []int64
	locateBinCalls  [][2]int64
	locatePartErr   error
}

func (m *mockWLEDService) LocatePart(_ context.Context, partID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locatePartCalls = append(m.locatePartCalls, partID)
	return m.locatePartErr
}

func (m *mockWLEDService) LocateBin(_ context.Context, controllerID, binID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locateBinCalls = append(m.locateBinCalls, [2]int64{controllerID, binID})
	return nil
}

func (m *mockWLEDService) GlobalOff(_ context.Context) error          { return nil }
func (m *mockWLEDService) Ping(_ context.Context, _ string) (bool, error) { return true, nil }

type mockBatchWLEDService struct {
	mockWLEDService
	locateBinsCalls map[int64][]int64
}

func (m *mockBatchWLEDService) LocateBins(_ context.Context, controllerID int64, binIDs []int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locateBinsCalls == nil {
		m.locateBinsCalls = make(map[int64][]int64)
	}
	cp := append([]int64(nil), binIDs...)
	m.locateBinsCalls[controllerID] = append(m.locateBinsCalls[controllerID], cp...)
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestProjectAndBOMCRUD(t *testing.T) {
	_, store, ctx := setupProjectsTestDB(t)
	svc := NewService(store, &mockWLEDService{}, discardLogger())

	if _, err := svc.CreateProject(ctx, CreateProjectRequest{Name: "   "}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for blank project name, got %v", err)
	}
	if _, err := svc.CreateProject(ctx, CreateProjectRequest{Name: "BadStatus", Status: "flying"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for invalid status, got %v", err)
	}

	p1, err := svc.CreateProject(ctx, CreateProjectRequest{
		Name:        "Macropad RevA",
		Description: "3x3 RP2040 keypad",
	})
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if p1.Status != "draft" {
		t.Fatalf("expected default status draft, got %q", p1.Status)
	}

	p2, err := svc.CreateProject(ctx, CreateProjectRequest{
		Name:   "LED Ring",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("CreateProject p2 failed: %v", err)
	}

	if err := svc.UpdateProject(ctx, UpdateProjectRequest{
		ID:          p1.ID,
		Name:        "Macropad RevB",
		Description: "Updated revision",
		Status:      "active",
	}); err != nil {
		t.Fatalf("UpdateProject failed: %v", err)
	}

	got, err := svc.GetProject(ctx, p1.ID)
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if got.Name != "Macropad RevB" || got.Status != "active" {
		t.Fatalf("unexpected updated project: %+v", got)
	}
	if _, err := svc.GetProject(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing project, got %v", err)
	}

	partID, err := store.CreatePart(ctx, db.CreatePartParams{
		Name:       "RP2040",
		PartNumber: sql.NullString{String: "SC0914", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	if _, err := svc.AddBOMItem(ctx, AddBOMItemRequest{
		ProjectID:        p1.ID,
		Designator:       "U1",
		QuantityPerBoard: 0,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for non-positive qty, got %v", err)
	}

	missingPart := int64(98765)
	if _, err := svc.AddBOMItem(ctx, AddBOMItemRequest{
		ProjectID:        p1.ID,
		PartID:           &missingPart,
		Designator:       "U1",
		QuantityPerBoard: 1,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for missing part_id, got %v", err)
	}

	item1, err := svc.AddBOMItem(ctx, AddBOMItemRequest{
		ProjectID:        p1.ID,
		Designator:       "U1",
		MPN:              "SC0914",
		Description:      "MCU",
		QuantityPerBoard: 1,
		Notes:            "Solder last",
	})
	if err != nil {
		t.Fatalf("AddBOMItem failed: %v", err)
	}

	// Cross-project IDOR guards must reject mutations/locate when item belongs to p1, not p2.
	if err := svc.LinkBOMItemPart(ctx, p2.ID, item1.ID, &partID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on cross-project LinkBOMItemPart, got %v", err)
	}
	if err := svc.DeleteBOMItem(ctx, p2.ID, item1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on cross-project DeleteBOMItem, got %v", err)
	}
	if err := svc.LocateBOMItemForProject(ctx, p2.ID, item1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on cross-project LocateBOMItemForProject, got %v", err)
	}

	// Legitimate link + update + list check.
	if err := svc.LinkBOMItemPart(ctx, p1.ID, item1.ID, &partID); err != nil {
		t.Fatalf("LinkBOMItemPart failed: %v", err)
	}
	if err := svc.UpdateBOMItem(ctx, UpdateBOMItemRequest{
		ProjectID:        p1.ID,
		ID:               item1.ID,
		PartID:           &partID,
		Designator:       "U1, U2",
		MPN:              "SC0914",
		Description:      "Dual RP2040",
		QuantityPerBoard: 2,
		Notes:            "Lead-free",
	}); err != nil {
		t.Fatalf("UpdateBOMItem failed: %v", err)
	}

	list, err := svc.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(list))
	}

	if err := svc.DeleteBOMItem(ctx, p1.ID, item1.ID); err != nil {
		t.Fatalf("DeleteBOMItem failed: %v", err)
	}
	if err := svc.DeleteProject(ctx, p1.ID); err != nil {
		t.Fatalf("DeleteProject failed: %v", err)
	}
	if _, err := svc.GetProject(ctx, p1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestImportCSV_KiCadAndMatchingPriority(t *testing.T) {
	_, store, ctx := setupProjectsTestDB(t)
	svc := NewService(store, &mockWLEDService{}, discardLogger())

	// Seed parts exercising MPN case-insensitive -> barcode -> exact name priority.
	pCap, err := store.CreatePart(ctx, db.CreatePartParams{
		Name:        "100nF X7R 0603",
		PartNumber:  sql.NullString{String: "CL10B104KB8NNNC", Valid: true},
		BarcodeData: sql.NullString{String: "BC-CAP-100N", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart pCap: %v", err)
	}
	// Priority check: same code exists as barcode on pPriorityBarcode and part_number on pPriorityMPN -> MPN must win.
	if _, err := store.CreatePart(ctx, db.CreatePartParams{
		Name:        "Priority Barcode Decoy",
		BarcodeData: sql.NullString{String: "DUAL-CODE", Valid: true},
	}); err != nil {
		t.Fatalf("CreatePart decoy: %v", err)
	}
	pPriorityMPN, err := store.CreatePart(ctx, db.CreatePartParams{
		Name:       "Priority MPN Winner",
		PartNumber: sql.NullString{String: "dual-code", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart winner: %v", err)
	}
	pMCU, err := store.CreatePart(ctx, db.CreatePartParams{
		Name:        "ESP32-S3-WROOM-1",
		BarcodeData: sql.NullString{String: "BAR-ESP32-S3", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart pMCU: %v", err)
	}
	pResNameOnly, err := store.CreatePart(ctx, db.CreatePartParams{
		Name: "10k Resistor 0603",
	})
	if err != nil {
		t.Fatalf("CreatePart pResNameOnly: %v", err)
	}

	proj, err := svc.CreateProject(ctx, CreateProjectRequest{Name: "KiCad Sensor Node"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// KiCad semicolon-delimited CSV with UTF-8 BOM.
	csvData := "\ufeffReference;Value;MPN;Qty;Description;Notes\n" +
		"C1, C2;100nF;cl10b104kb8nnnc;2;Decoupling cap;Keep close to VDD\n" +
		"D1;Zener;DUAL-CODE;1;Priority check;\n" +
		"U1;ESP32;BAR-ESP32-S3;1;WiFi MCU;Match via barcode\n" +
		"R1, R2, R3;10k Resistor 0603;;3;;Match via exact Value name\n" +
		"R4, R5;4.7k Pullup;NONEXISTENT-MPN;;I2C Pullups;Blank qty inferred from 2 refs\n"

	res, err := svc.ImportCSV(ctx, proj.ID, strings.NewReader(csvData))
	if err != nil {
		t.Fatalf("ImportCSV failed: %v", err)
	}
	if res.ImportedCount != 5 || res.MatchedCount != 4 || res.UnmatchedCount != 1 {
		t.Fatalf("unexpected import counts: %+v", res)
	}
	if res.Items[0].PartID.Int64 != pCap || res.Items[0].QuantityPerBoard != 2 {
		t.Fatalf("row 0 expected pCap(%d) qty 2, got %+v", pCap, res.Items[0])
	}
	if res.Items[1].PartID.Int64 != pPriorityMPN {
		t.Fatalf("row 1 expected MPN priority pPriorityMPN(%d), got %+v", pPriorityMPN, res.Items[1])
	}
	if res.Items[2].PartID.Int64 != pMCU {
		t.Fatalf("row 2 expected barcode match pMCU(%d), got %+v", pMCU, res.Items[2])
	}
	if res.Items[3].PartID.Int64 != pResNameOnly || res.Items[3].QuantityPerBoard != 3 {
		t.Fatalf("row 3 expected name match pResNameOnly(%d) qty 3, got %+v", pResNameOnly, res.Items[3])
	}
	if res.Items[4].PartID.Valid || res.Items[4].QuantityPerBoard != 2 {
		t.Fatalf("row 4 expected unmatched qty 2 from designator list, got %+v", res.Items[4])
	}

	// Verify invalid CSV rolls back cleanly without inserting partial rows.
	badCSV := "Reference,Value,Qty\nC99,1uF,1\nR99,10k,notanumber\n"
	if _, err := svc.ImportCSV(ctx, proj.ID, strings.NewReader(badCSV)); err == nil {
		t.Fatal("expected ImportCSV error for invalid qty row")
	}
	itemsAfterFail, err := store.ListProjectBOMItems(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListProjectBOMItems: %v", err)
	}
	if len(itemsAfterFail) != 5 {
		t.Fatalf("expected rollback leaving 5 items, got %d", len(itemsAfterFail))
	}
}

func TestGetProjectDetail_ShortagesAndLocations(t *testing.T) {
	_, store, ctx := setupProjectsTestDB(t)
	svc := NewService(store, &mockWLEDService{}, discardLogger())

	ctrl, err := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "Rack A",
		IpAddress: "192.168.1.50",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateController: %v", err)
	}
	ledCont, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "SMD Grid",
		ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
		SegmentID:    0,
	})
	if err != nil {
		t.Fatalf("CreateContainer ledCont: %v", err)
	}
	standaloneCont, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:      "Manual Drawer",
		SegmentID: 0,
	})
	if err != nil {
		t.Fatalf("CreateContainer standaloneCont: %v", err)
	}

	binA1, err := store.CreateBin(ctx, db.CreateBinParams{
		Name:        "A1",
		ContainerID: ledCont,
		LedIndex:    sql.NullInt64{Int64: 0, Valid: true},
		Width:       sql.NullInt64{Int64: 2, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateBin binA1: %v", err)
	}
	binS1, err := store.CreateBin(ctx, db.CreateBinParams{
		Name:        "S1",
		ContainerID: standaloneCont,
	})
	if err != nil {
		t.Fatalf("CreateBin binS1: %v", err)
	}

	resPart, err := store.CreatePart(ctx, db.CreatePartParams{
		Name:       "10k Resistor",
		PartNumber: sql.NullString{String: "RC0603-10K", Valid: true},
		UnitCost:   sql.NullFloat64{Float64: 0.10, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart resPart: %v", err)
	}
	// Total stock for resPart = 6 (LED bin A1) + 2 (standalone S1) + 2 (orphaned) = 10
	for _, a := range []db.CreatePartAssignmentParams{
		{PartID: resPart, BinID: sql.NullInt64{Int64: binA1, Valid: true}, Quantity: 6},
		{PartID: resPart, BinID: sql.NullInt64{Int64: binS1, Valid: true}, Quantity: 2},
		{PartID: resPart, BinID: sql.NullInt64{}, Quantity: 2},
	} {
		if err := store.CreatePartAssignment(ctx, a); err != nil {
			t.Fatalf("CreatePartAssignment: %v", err)
		}
	}

	proj, err := svc.CreateProject(ctx, CreateProjectRequest{Name: "Amplifier"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// Two separate BOM rows pointing to the same part (2 + 3 = 5 per board).
	if _, err := svc.AddBOMItem(ctx, AddBOMItemRequest{
		ProjectID: proj.ID, PartID: &resPart, Designator: "R1, R2", QuantityPerBoard: 2,
	}); err != nil {
		t.Fatalf("AddBOMItem 1: %v", err)
	}
	if _, err := svc.AddBOMItem(ctx, AddBOMItemRequest{
		ProjectID: proj.ID, PartID: &resPart, Designator: "R3-R5", QuantityPerBoard: 3,
	}); err != nil {
		t.Fatalf("AddBOMItem 2: %v", err)
	}
	unlinked, err := svc.AddBOMItem(ctx, AddBOMItemRequest{
		ProjectID: proj.ID, Designator: "J1", MPN: "USB-C", Description: "Connector", QuantityPerBoard: 1,
	})
	if err != nil {
		t.Fatalf("AddBOMItem unlinked: %v", err)
	}

	detail1, err := svc.GetProjectDetail(ctx, proj.ID, 1)
	if err != nil {
		t.Fatalf("GetProjectDetail N=1: %v", err)
	}
	if detail1.Buildable {
		t.Fatal("expected Buildable=false while unlinked BOM item exists")
	}
	if len(detail1.Shortages) != 1 || !detail1.Shortages[0].Unlinked {
		t.Fatalf("expected 1 unlinked shortage at N=1, got %+v", detail1.Shortages)
	}
	if detail1.LocatableBinCount != 1 {
		t.Fatalf("expected LocatableBinCount=1, got %d", detail1.LocatableBinCount)
	}
	if len(detail1.Items[0].Locations) != 3 {
		t.Fatalf("expected 3 locations on resPart, got %d", len(detail1.Items[0].Locations))
	}

	// Remove unlinked row -> N=1 becomes buildable (needs 5, has 10), cost = 5 * 0.10 = 0.50.
	if err := svc.DeleteBOMItem(ctx, proj.ID, unlinked.ID); err != nil {
		t.Fatalf("DeleteBOMItem unlinked: %v", err)
	}
	detail1Ready, err := svc.GetProjectDetail(ctx, proj.ID, 1)
	if err != nil {
		t.Fatalf("GetProjectDetail N=1 ready: %v", err)
	}
	if !detail1Ready.Buildable || len(detail1Ready.Shortages) != 0 {
		t.Fatalf("expected Buildable=true at N=1, got buildable=%v shortages=%+v", detail1Ready.Buildable, detail1Ready.Shortages)
	}
	if detail1Ready.TotalCost < 0.499 || detail1Ready.TotalCost > 0.501 {
		t.Fatalf("expected TotalCost ~0.50, got %v", detail1Ready.TotalCost)
	}

	// At N=5 boards: combined demand is 5*5 = 25, stock = 10 -> shortage 15 even though individual rows are 10 and 15!
	detail5, err := svc.GetProjectDetail(ctx, proj.ID, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail N=5: %v", err)
	}
	if detail5.Buildable {
		t.Fatal("expected Buildable=false at N=5")
	}
	if len(detail5.Shortages) != 1 || detail5.Shortages[0].RequiredQuantity != 25 || detail5.Shortages[0].ShortageQuantity != 15 {
		t.Fatalf("expected combined shortage need=25 short=15 at N=5, got %+v", detail5.Shortages)
	}
	if detail5.Items[0].CombinedRequiredQuantity != 25 || detail5.Items[0].CombinedShortageQuantity != 15 || !detail5.Items[0].HasShortage {
		t.Fatalf("expected item 0 combined shortage flag, got %+v", detail5.Items[0])
	}
}

func TestPickModeLocate(t *testing.T) {
	t.Run("batched wled.Client illuminates all positive-stock bins per controller", func(t *testing.T) {
		_, store, ctx := setupProjectsTestDB(t)

		var mu sync.Mutex
		requests := make(map[string][]map[string]any)
		failHost := ""

		httpClient := &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Host == failHost {
					return &http.Response{
						StatusCode: http.StatusInternalServerError,
						Body:       io.NopCloser(strings.NewReader("controller error")),
						Header:     make(http.Header),
					}, nil
				}
				body, _ := io.ReadAll(r.Body)
				var payload map[string]any
				_ = json.Unmarshal(body, &payload)

				mu.Lock()
				requests[r.Host] = append(requests[r.Host], payload)
				mu.Unlock()

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString(`{"on":true}`)),
					Header:     make(http.Header),
				}, nil
			}),
		}

		wClient := wled.NewClientWithHTTPClient(httpClient)
		svc := NewService(store, &mockWLEDService{}, discardLogger(), WithWLEDClient(wClient))

		ctrl1, _ := store.CreateController(ctx, db.CreateControllerParams{
			Name: "Wall 1", IpAddress: "10.0.0.10", Port: sql.NullInt64{Int64: 80, Valid: true},
		})
		ctrl2, _ := store.CreateController(ctx, db.CreateControllerParams{
			Name: "Wall 2", IpAddress: "10.0.0.20", Port: sql.NullInt64{Int64: 8080, Valid: true},
		})
		cont1, _ := store.CreateContainer(ctx, db.CreateContainerParams{
			Name: "C1", ControllerID: sql.NullInt64{Int64: ctrl1.ID, Valid: true}, SegmentID: 0,
		})
		cont2, _ := store.CreateContainer(ctx, db.CreateContainerParams{
			Name: "C2", ControllerID: sql.NullInt64{Int64: ctrl2.ID, Valid: true}, SegmentID: 0,
		})

		b1, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B1", ContainerID: cont1, LedIndex: sql.NullInt64{Int64: 1, Valid: true}, Width: sql.NullInt64{Int64: 2, Valid: true}})
		b2, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B2", ContainerID: cont1, LedIndex: sql.NullInt64{Int64: 5, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}})
		bZero, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "BZero", ContainerID: cont1, LedIndex: sql.NullInt64{Int64: 9, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}})
		b3, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B3", ContainerID: cont2, LedIndex: sql.NullInt64{Int64: 2, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}})

		part1, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "Part 1"})
		part2, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "Part 2"})
		_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: part1, BinID: sql.NullInt64{Int64: b1, Valid: true}, Quantity: 4})
		_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: part1, BinID: sql.NullInt64{Int64: bZero, Valid: true}, Quantity: 0})
		_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: part2, BinID: sql.NullInt64{Int64: b2, Valid: true}, Quantity: 2})
		_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: part2, BinID: sql.NullInt64{Int64: b3, Valid: true}, Quantity: 3})

		proj, _ := svc.CreateProject(ctx, CreateProjectRequest{Name: "Pick Board"})
		item1, _ := svc.AddBOMItem(ctx, AddBOMItemRequest{ProjectID: proj.ID, PartID: &part1, Designator: "U1", QuantityPerBoard: 1})
		_, _ = svc.AddBOMItem(ctx, AddBOMItemRequest{ProjectID: proj.ID, PartID: &part2, Designator: "R1", QuantityPerBoard: 1})
		unlinked, _ := svc.AddBOMItem(ctx, AddBOMItemRequest{ProjectID: proj.ID, Designator: "R9", QuantityPerBoard: 1})

		if err := svc.LocateBOM(ctx, proj.ID); err != nil {
			t.Fatalf("LocateBOM failed: %v", err)
		}

		// Expect ONE batched request to 10.0.0.10:80 (containing LED indices 1,2 and 5, NOT 9!) and ONE to 10.0.0.20:8080 (index 2).
		mu.Lock()
		c1Reqs := requests["10.0.0.10:80"]
		c2Reqs := requests["10.0.0.20:8080"]
		mu.Unlock()

		if len(c1Reqs) != 1 || len(c2Reqs) != 1 {
			t.Fatalf("expected 1 batched request per controller, got ctrl1=%d ctrl2=%d", len(c1Reqs), len(c2Reqs))
		}
		seg1 := c1Reqs[0]["seg"].([]any)[0].(map[string]any)
		rawBytes, _ := json.Marshal(seg1["i"])
		payloadStr := string(rawBytes)
		// Indices 1,2 (width 2 starting at 1) and 5 should be lit, index 9 must NOT be lit (zero stock).
		if !strings.Contains(payloadStr, "1,") || !strings.Contains(payloadStr, "5,") {
			t.Fatalf("expected batched LED payload to contain bins at 1 and 5, got %s", payloadStr)
		}
		if strings.Contains(payloadStr, ",9,[") {
			t.Fatalf("expected zero-stock bin at index 9 to be skipped, got %s", payloadStr)
		}

		if err := svc.LocateBOMItemForProject(ctx, proj.ID, unlinked.ID); !errors.Is(err, ErrBOMItemUnlinked) {
			t.Fatalf("expected ErrBOMItemUnlinked for unlinked BOM item, got %v", err)
		}
		if err := svc.LocateBOMItemForProject(ctx, proj.ID, item1.ID); err != nil {
			t.Fatalf("LocateBOMItemForProject failed: %v", err)
		}

		failHost = "10.0.0.20:8080"
		if err := svc.LocateBOM(ctx, proj.ID); err == nil {
			t.Fatal("expected controller HTTP error to propagate from LocateBOM")
		}
	})

	t.Run("fallback LocateBins extension and standard wled.Service LocatePart", func(t *testing.T) {
		_, store, ctx := setupProjectsTestDB(t)
		ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "10.1.1.1"})
		cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{Name: "Box", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}})
		b1, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
		b2, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B2", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 1, Valid: true}})
		p1, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "P1"})
		p2, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "P2"})
		_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p1, BinID: sql.NullInt64{Int64: b1, Valid: true}, Quantity: 2})
		_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p2, BinID: sql.NullInt64{Int64: b2, Valid: true}, Quantity: 2})

		batchMock := &mockBatchWLEDService{}
		batchSvc := NewService(store, batchMock, discardLogger())
		proj, _ := batchSvc.CreateProject(ctx, CreateProjectRequest{Name: "Fallback"})
		_, _ = batchSvc.AddBOMItem(ctx, AddBOMItemRequest{ProjectID: proj.ID, PartID: &p1, Designator: "D1", QuantityPerBoard: 1})
		_, _ = batchSvc.AddBOMItem(ctx, AddBOMItemRequest{ProjectID: proj.ID, PartID: &p2, Designator: "D2", QuantityPerBoard: 1})

		if err := batchSvc.LocateBOM(ctx, proj.ID); err != nil {
			t.Fatalf("LocateBOM batch mock failed: %v", err)
		}
		gotBins := append([]int64(nil), batchMock.locateBinsCalls[ctrl.ID]...)
		sort.Slice(gotBins, func(i, j int) bool { return gotBins[i] < gotBins[j] })
		if len(gotBins) != 2 || gotBins[0] != b1 || gotBins[1] != b2 {
			t.Fatalf("expected LocateBins called with [%d %d], got %v", b1, b2, gotBins)
		}

		stdMock := &mockWLEDService{}
		stdSvc := NewService(store, stdMock, discardLogger())
		if err := stdSvc.LocateBOM(ctx, proj.ID); err != nil {
			t.Fatalf("LocateBOM std mock failed: %v", err)
		}
		if len(stdMock.locatePartCalls) != 2 || stdMock.locatePartCalls[0] != p1 || stdMock.locatePartCalls[1] != p2 {
			t.Fatalf("expected LocatePart calls [%d %d], got %v", p1, p2, stdMock.locatePartCalls)
		}
	})
}

func TestBuildProject_AtomicDeductAndRollback(t *testing.T) {
	_, store, ctx := setupProjectsTestDB(t)
	svc := NewService(store, &mockWLEDService{}, discardLogger())

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "Main", IpAddress: "10.2.2.2"})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{Name: "Grid", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}})
	b1, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
	b2, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B2", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 1, Valid: true}})
	b3, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B3", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 2, Valid: true}})

	partA, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "Cap 10uF"})
	partB, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "Regulator"})

	// Part A: Bin b1(3) + Bin b2(4) + Orphaned(3) = 10 total.
	// Note: insert Orphaned BEFORE bins to prove deterministic non-orphaned-first ordering overrides insertion ID order!
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partA, BinID: sql.NullInt64{}, Quantity: 3})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partA, BinID: sql.NullInt64{Int64: b1, Valid: true}, Quantity: 3})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partA, BinID: sql.NullInt64{Int64: b2, Valid: true}, Quantity: 4})
	// Part B: Bin b3(10).
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partB, BinID: sql.NullInt64{Int64: b3, Valid: true}, Quantity: 10})

	proj, _ := svc.CreateProject(ctx, CreateProjectRequest{Name: "Power Board"})
	_, _ = svc.AddBOMItem(ctx, AddBOMItemRequest{ProjectID: proj.ID, PartID: &partA, Designator: "C1-C3", QuantityPerBoard: 3})
	_, _ = svc.AddBOMItem(ctx, AddBOMItemRequest{ProjectID: proj.ID, PartID: &partB, Designator: "U1", QuantityPerBoard: 1})

	baselineLogs, _ := store.GetAllAuditLogs(ctx)

	// 1. Shortage rollback: building 4 boards needs 12 of Part A (has 10), even though Part B (needs 4, has 10) has enough.
	err := svc.BuildProject(ctx, proj.ID, 4)
	var shortErr *ShortageError
	if !errors.As(err, &shortErr) {
		t.Fatalf("expected *ShortageError, got %v", err)
	}
	if len(shortErr.Shortages) != 1 || shortErr.Shortages[0].ShortageQuantity != 2 {
		t.Fatalf("unexpected shortage detail: %+v", shortErr.Shortages)
	}

	// Verify 0 DB mutation on Part A & Part B and 0 new audit logs created.
	if totalA := sumPartStock(t, ctx, store, partA); totalA != 10 {
		t.Fatalf("expected Part A untouched (10), got %d", totalA)
	}
	if totalB := sumPartStock(t, ctx, store, partB); totalB != 10 {
		t.Fatalf("expected Part B untouched (10), got %d", totalB)
	}
	logsAfterShortage, _ := store.GetAllAuditLogs(ctx)
	if len(logsAfterShortage) != len(baselineLogs) {
		t.Fatalf("expected 0 new audit logs after rolled back build, got %d (baseline %d)", len(logsAfterShortage), len(baselineLogs))
	}

	// 2. Unlinked item triggers *ShortageError with rollback.
	unlinked, _ := svc.AddBOMItem(ctx, AddBOMItemRequest{ProjectID: proj.ID, Designator: "TP1", QuantityPerBoard: 1})
	logsBeforeUnlinkedBuild, _ := store.GetAllAuditLogs(ctx)
	if err := svc.BuildProject(ctx, proj.ID, 1); !errors.As(err, &shortErr) || !shortErr.Shortages[0].Unlinked {
		t.Fatalf("expected unlinked *ShortageError, got %v", err)
	}
	if totalB := sumPartStock(t, ctx, store, partB); totalB != 10 {
		t.Fatalf("expected Part B still 10 after unlinked rollback, got %d", totalB)
	}
	logsAfterUnlinkedBuild, _ := store.GetAllAuditLogs(ctx)
	if len(logsAfterUnlinkedBuild) != len(logsBeforeUnlinkedBuild) {
		t.Fatalf("expected 0 new audit logs after unlinked build failure")
	}
	_ = svc.DeleteBOMItem(ctx, proj.ID, unlinked.ID)

	// 3. Atomic success build for 3 boards: deducts 9 of Part A (b1: 3->0, b2: 4->0, orphaned: 3->1) and 3 of Part B (b3: 10->7).
	if err := svc.BuildProject(ctx, proj.ID, 3); err != nil {
		t.Fatalf("BuildProject(3) failed: %v", err)
	}

	assignsA, _ := store.GetPartAssignments(ctx, partA)
	byBinA := make(map[int64]int64)
	var orphanQty int64
	for _, a := range assignsA {
		if a.BinID.Valid {
			byBinA[a.BinID.Int64] = a.Quantity
		} else {
			orphanQty = a.Quantity
		}
	}
	if byBinA[b1] != 0 || byBinA[b2] != 0 || orphanQty != 1 {
		t.Fatalf("expected b1=0 b2=0 orphan=1 after deterministic deduction, got b1=%d b2=%d orphan=%d", byBinA[b1], byBinA[b2], orphanQty)
	}

	if totalB := sumPartStock(t, ctx, store, partB); totalB != 7 {
		t.Fatalf("expected Part B = 7 after build, got %d", totalB)
	}

	logs, _ := store.GetAllAuditLogs(ctx)
	var stockDecCount, projectBuildCount int
	for _, l := range logs {
		if l.ActionType == "STOCK_DEC" && l.EntityType == "PART" {
			stockDecCount++
		}
		if l.ActionType == "BUILD" && l.EntityType == "PROJECT" && l.EntityID == proj.ID {
			projectBuildCount++
		}
	}
	if stockDecCount != 4 || projectBuildCount != 1 {
		t.Fatalf("expected 4 STOCK_DEC logs and 1 BUILD PROJECT log, got stockDec=%d projectBuild=%d", stockDecCount, projectBuildCount)
	}
}

func sumPartStock(t *testing.T, ctx context.Context, store db.Store, partID int64) int64 {
	t.Helper()
	rows, err := store.GetPartAssignments(ctx, partID)
	if err != nil {
		t.Fatalf("GetPartAssignments(%d): %v", partID, err)
	}
	var sum int64
	for _, r := range rows {
		sum += r.Quantity
	}
	return sum
}

func TestLocateProjectBOM_XYAndGridContainers(t *testing.T) {
	_, store, ctx := setupProjectsTestDB(t)

	var mu sync.Mutex
	var payloads []map[string]any
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			mu.Lock()
			payloads = append(payloads, payload)
			mu.Unlock()
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`{"on":true}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	wClient := wled.NewClientWithHTTPClient(httpClient)
	svc := NewService(store, &mockWLEDService{}, discardLogger(), WithWLEDClient(wClient))

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{
		Name: "MixedWall", IpAddress: "10.0.0.42", Port: sql.NullInt64{Int64: 80, Valid: true},
	})
	xyCont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "XY Drawer", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}, SegmentID: 0,
		ConfigJson: sql.NullString{String: `{"type":"xy","rows":4,"cols":6,"x_start_led":0,"y_start_led":6}`, Valid: true},
	})
	gridCont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Grid Tray", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}, SegmentID: 0,
		ConfigJson: sql.NullString{String: `{"type":"grid","rows":2,"cols":4}`, Valid: true},
	})

	xyBin, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "XY-D3", ContainerID: xyCont,
		LedIndex: sql.NullInt64{Valid: false},
		Width:    sql.NullInt64{Int64: 1, Valid: true},
		GridX:    sql.NullInt64{Int64: 3, Valid: true}, // X = 3
		GridY:    sql.NullInt64{Int64: 2, Valid: true}, // Y = 6 + 2 = 8
	})
	gridBin, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "G-A1", ContainerID: gridCont,
		LedIndex: sql.NullInt64{Int64: 12, Valid: true},
		Width:    sql.NullInt64{Int64: 2, Valid: true},
		GridX:    sql.NullInt64{Int64: 0, Valid: true},
		GridY:    sql.NullInt64{Int64: 0, Valid: true},
	})

	xyPart, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "XY Part"})
	gridPart, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "Grid Part"})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: xyPart, BinID: sql.NullInt64{Int64: xyBin, Valid: true}, Quantity: 5})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: gridPart, BinID: sql.NullInt64{Int64: gridBin, Valid: true}, Quantity: 5})

	proj, _ := svc.CreateProject(ctx, CreateProjectRequest{Name: "Mixed BOM"})
	_, _ = svc.AddBOMItem(ctx, AddBOMItemRequest{ProjectID: proj.ID, PartID: &xyPart, Designator: "U1", QuantityPerBoard: 1})
	_, _ = svc.AddBOMItem(ctx, AddBOMItemRequest{ProjectID: proj.ID, PartID: &gridPart, Designator: "R1", QuantityPerBoard: 1})

	detail, err := svc.GetProjectDetail(ctx, proj.ID, 1)
	if err != nil {
		t.Fatalf("GetProjectDetail failed: %v", err)
	}
	if detail.LocatableBinCount != 2 {
		t.Fatalf("expected LocatableBinCount=2 (1 XY + 1 Grid), got %d", detail.LocatableBinCount)
	}
	for _, it := range detail.Items {
		if len(it.Locations) != 1 || !it.Locations[0].HasLED {
			t.Errorf("expected HasLED=true for BOM item %s location, got %+v", it.Designator, it.Locations)
		}
	}

	if err := svc.LocateBOM(ctx, proj.ID); err != nil {
		t.Fatalf("LocateBOM failed: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("expected 1 batched WLED request, got %d", len(payloads))
	}
	seg0 := payloads[0]["seg"].([]any)[0].(map[string]any)
	rawBytes, _ := json.Marshal(seg0["i"])
	iStr := string(rawBytes)
	if !strings.Contains(iStr, "3,4,[") || !strings.Contains(iStr, "8,9,[") || !strings.Contains(iStr, "12,14,[") {
		t.Fatalf("expected XY indices 3 and 8 plus grid range [12,14) in batched payload, got %s", iStr)
	}
}
