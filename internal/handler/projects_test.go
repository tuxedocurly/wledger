package handler

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/projects"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

type projTestRoundTripper func(*http.Request) (*http.Response, error)

func (f projTestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func setupProjectsHandlerTest(t *testing.T, rt projTestRoundTripper) (*Handler, db.Store, auth.User, auth.User) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "projects_handler.db")
	dbConn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbConn.Close() })

	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	store := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	uiErr := uierror.New(logger)

	wClient := wled.NewClient()
	if rt != nil {
		wClient = wled.NewClientWithHTTPClient(&http.Client{Transport: rt})
	}
	wSvc := wled.NewService(store, wClient, logger)
	projSvc := projects.NewService(store, wSvc, logger, projects.WithWLEDClient(wClient))

	h := &Handler{
		Logger:   logger,
		Queries:  store,
		Database: dbConn,
		Session:  scs.New(),
		WLED:     wSvc,
		UIError:  uiErr,
		Projects: projSvc,
	}

	editorRow, err := store.CreateUser(context.Background(), db.CreateUserParams{
		Email:        "editor@test.local",
		PasswordHash: "hash",
		Role:         "editor",
	})
	if err != nil {
		t.Fatalf("create editor: %v", err)
	}
	viewerRow, err := store.CreateUser(context.Background(), db.CreateUserParams{
		Email:        "viewer@test.local",
		PasswordHash: "hash",
		Role:         "viewer",
	})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}

	editor := auth.User{ID: editorRow.ID, Email: editorRow.Email, Role: editorRow.Role}
	viewer := auth.User{ID: viewerRow.ID, Email: viewerRow.Email, Role: viewerRow.Role}
	return h, store, editor, viewer
}

func newProjectsRouter(h *Handler, currentUser *auth.User) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			if currentUser != nil {
				ctx = auth.WithUser(ctx, *currentUser)
				if currentUser.ID > 0 && !currentUser.IsGuest {
					ctx = context.WithValue(ctx, middleware.UserContextKey, currentUser.ID)
				}
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	r.Get("/projects", h.HandleProjectsList)
	r.Post("/projects", h.HandleProjectCreate)
	r.Get("/projects/{id}", h.HandleProjectDetail)
	r.Post("/projects/{id}/update", h.HandleProjectUpdate)
	r.Post("/projects/{id}/delete", h.HandleProjectDelete)
	r.Post("/projects/{id}/bom", h.HandleProjectBOMAdd)
	r.Post("/projects/{id}/bom/import", h.HandleProjectBOMImport)
	r.Post("/projects/{id}/bom/{itemID}/link", h.HandleProjectBOMLink)
	r.Post("/projects/{id}/bom/{itemID}/delete", h.HandleProjectBOMDelete)
	r.Post("/projects/{id}/locate", h.HandleProjectLocate)
	r.Post("/projects/{id}/bom/{itemID}/locate", h.HandleProjectBOMItemLocate)
	r.Post("/projects/{id}/build", h.HandleProjectBuild)
	return r
}

func sumHandlerPartStock(t *testing.T, ctx context.Context, store db.Store, partID int64) int64 {
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

func TestProjectsHandler_LifecycleAndShortage(t *testing.T) {
	var mu sync.Mutex
	var wledPayloads []string
	failWLED := false

	h, store, editor, _ := setupProjectsHandlerTest(t, func(r *http.Request) (*http.Response, error) {
		if failWLED {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader("offline")),
				Header:     make(http.Header),
			}, nil
		}
		if r.URL.Path == "/json" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"state":{"on":false,"bri":128,"seg":[{"id":0}]},"info":{"leds":{"count":16}}}`)),
				Header:     make(http.Header),
			}, nil
		}
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		mu.Lock()
		wledPayloads = append(wledPayloads, string(body))
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"on":true}`)),
			Header:     make(http.Header),
		}, nil
	})

	ctx := context.Background()
	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{
		Name: "Bench Ctrl", IpAddress: "192.168.10.5", Port: sql.NullInt64{Int64: 80, Valid: true},
	})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Drawer", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}, SegmentID: 0,
	})
	b1, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
	b2, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B2", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 3, Valid: true}})

	part1, _ := store.CreatePart(ctx, db.CreatePartParams{
		Name:       "10k 0603",
		PartNumber: sql.NullString{String: "MPN-R10K", Valid: true},
		UnitCost:   sql.NullFloat64{Float64: 0.50, Valid: true},
	})
	part2, _ := store.CreatePart(ctx, db.CreatePartParams{
		Name:       "100nF 0603",
		PartNumber: sql.NullString{String: "MPN-C100N", Valid: true},
		UnitCost:   sql.NullFloat64{Float64: 0.25, Valid: true},
	})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: part1, BinID: sql.NullInt64{Int64: b1, Valid: true}, Quantity: 5})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: part2, BinID: sql.NullInt64{Int64: b2, Valid: true}, Quantity: 10})

	activeUser := editor
	router := newProjectsRouter(h, &activeUser)

	// 1. Empty projects list
	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "No projects yet") {
		t.Fatalf("expected 200 empty state on GET /projects, got %d: %s", rr.Code, rr.Body.String())
	}

	// 2. Create project
	form := url.Values{"name": {"Sensor Board"}, "description": {"Env monitor"}, "status": {"active"}}
	req = httptest.NewRequest(http.MethodPost, "/projects", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on POST /projects, got %d: %s", rr.Code, rr.Body.String())
	}
	projectURL := rr.Header().Get("Location")
	if !strings.HasPrefix(projectURL, "/projects/") {
		t.Fatalf("unexpected redirect Location %q", projectURL)
	}

	// Verify list renders project card
	req = httptest.NewRequest(http.MethodGet, "/projects", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Sensor Board") {
		t.Fatalf("expected Sensor Board in projects list, got %d", rr.Code)
	}

	// 3. Import BOM CSV via multipart upload (HTMX)
	var bodyBuf bytes.Buffer
	mw := multipart.NewWriter(&bodyBuf)
	fw, err := mw.CreateFormFile("file", "bom.csv")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = fw.Write([]byte("Reference,Value,MPN,Qty,Description\nR1 R2,10k,MPN-R10K,2,Pullup\nC1,100nF,MPN-C100N,3,Bypass\nU1,Unknown,MPN-MISS,1,Custom MCU\n"))
	_ = mw.Close()

	req = httptest.NewRequest(http.MethodPost, projectURL+"/bom/import", &bodyBuf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Imported 3 BOM line(s): 2 auto-linked, 1 unmatched.") {
		t.Fatalf("unexpected CSV import response %d: %s", rr.Code, rr.Body.String())
	}

	// Also verify multipart form with empty file field + pasted csv_data works cleanly!
	var pasteBuf bytes.Buffer
	mw2 := multipart.NewWriter(&pasteBuf)
	_, _ = mw2.CreateFormFile("file", "") // simulates browser empty file input alongside pasted textarea
	_ = mw2.WriteField("csv_data", "Reference,Value,MPN,Qty\nTP1,TestPoint,MPN-TP,1\n")
	_ = mw2.Close()

	req = httptest.NewRequest(http.MethodPost, projectURL+"/bom/import", &pasteBuf)
	req.Header.Set("Content-Type", mw2.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Imported 1 BOM line(s): 0 auto-linked, 1 unmatched.") {
		t.Fatalf("unexpected pasted multipart CSV response %d: %s", rr.Code, rr.Body.String())
	}

	// 4. Inspect detail for ?boards=3 -> renders shortages (-1 on R10K + unlinked rows) & physical locations.
	req = httptest.NewRequest(http.MethodGet, projectURL+"?boards=3", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET detail?boards=3 failed: %d", rr.Code)
	}
	html := rr.Body.String()
	for _, want := range []string{"10k 0603", "Short -1", "Unlinked", "Drawer &gt; B1", "Drawer &gt; B2"} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected detail HTML for ?boards=3 to contain %q", want)
		}
	}

	// Remove the two unlinked rows so boardCount=2 becomes buildable, then test link endpoint too.
	projs, _ := h.Projects.ListProjects(ctx)
	projID := projs[0].ID
	items, _ := store.ListProjectBOMItems(ctx, projID)
	var unlinkedIDs []int64
	var item1ID int64
	for _, it := range items {
		if !it.PartID.Valid {
			unlinkedIDs = append(unlinkedIDs, it.ID)
		} else if it.PartID.Int64 == part1 {
			item1ID = it.ID
		}
	}
	// Link second unlinked item to part2 briefly then delete both unlinked items cleanly.
	linkForm := url.Values{"part_id": {fmt.Sprintf("%d", part2)}}
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/projects/%d/bom/%d/link", projID, unlinkedIDs[1]), strings.NewReader(linkForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on link BOM item, got %d", rr.Code)
	}

	for _, uid := range unlinkedIDs {
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/projects/%d/bom/%d/delete", projID, uid), nil)
		rr = httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("expected 303 deleting unlinked item %d, got %d", uid, rr.Code)
		}
	}

	// ?boards=2 should now report Ready for 2 board(s)
	req = httptest.NewRequest(http.MethodGet, projectURL+"?boards=2", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Ready for 2 board(s)") {
		t.Fatalf("expected Ready for 2 board(s), got %d", rr.Code)
	}

	// 5. Pick Mode Locate (all + single item + WLED error propagation)
	req = httptest.NewRequest(http.MethodPost, projectURL+"/locate", nil)
	req.Header.Set("HX-Request", "true")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 on Pick Mode Locate, got %d: %s", rr.Code, rr.Body.String())
	}
	mu.Lock()
	payloadCount := len(wledPayloads)
	var firstPayload string
	if payloadCount > 0 {
		firstPayload = wledPayloads[0]
	}
	mu.Unlock()
	if payloadCount != 1 || !strings.Contains(firstPayload, "\"i\":[0,") || !strings.Contains(firstPayload, ",3,") {
		t.Fatalf("expected single batched WLED state lighting indices 0 and 3, got count=%d payload=%s", payloadCount, firstPayload)
	}

	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/projects/%d/bom/%d/locate", projID, item1ID), nil)
	req.Header.Set("HX-Request", "true")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 on BOM item locate, got %d", rr.Code)
	}

	failWLED = true
	req = httptest.NewRequest(http.MethodPost, projectURL+"/locate", nil)
	req.Header.Set("HX-Request", "true")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when WLED offline, got %d", rr.Code)
	}
	failWLED = false

	// 6. Build shortage rollback (3 boards needs 6 of part1, only 5 in stock) -> 409 Conflict, no stock changed.
	buildFailForm := url.Values{"board_count": {"3"}}
	req = httptest.NewRequest(http.MethodPost, projectURL+"/build", strings.NewReader(buildFailForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on insufficient stock build, got %d", rr.Code)
	}
	if sumHandlerPartStock(t, ctx, store, part1) != 5 || sumHandlerPartStock(t, ctx, store, part2) != 10 {
		t.Fatal("stock modified despite rolled-back shortage build")
	}

	// 7. Build 2 boards via HTMX -> deducts 4 of part1 (5->1) and 6 of part2 (10->4).
	buildOKForm := url.Values{"board_count": {"2"}}
	req = httptest.NewRequest(http.MethodPost, projectURL+"/build", strings.NewReader(buildOKForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Built 2 board(s)") {
		t.Fatalf("expected 200 HTMX build success, got %d: %s", rr.Code, rr.Body.String())
	}
	if got1, got2 := sumHandlerPartStock(t, ctx, store, part1), sumHandlerPartStock(t, ctx, store, part2); got1 != 1 || got2 != 4 {
		t.Fatalf("expected post-build stock part1=1 part2=4, got %d and %d", got1, got2)
	}

	// 8. Manual add BOM item, project update, and delete
	addForm := url.Values{"designator": {"J1"}, "mpn": {"HDR-4"}, "quantity_per_board": {"1"}}
	req = httptest.NewRequest(http.MethodPost, projectURL+"/bom", strings.NewReader(addForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on add BOM item, got %d", rr.Code)
	}

	updForm := url.Values{"name": {"Sensor Board Rev2"}, "status": {"completed"}}
	req = httptest.NewRequest(http.MethodPost, projectURL+"/update", strings.NewReader(updForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on update project, got %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, projectURL+"/delete", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/projects" {
		t.Fatalf("expected 303 -> /projects on delete, got %d loc=%s", rr.Code, rr.Header().Get("Location"))
	}
}

func TestProjectsHandler_RBAC(t *testing.T) {
	h, store, editor, viewer := setupProjectsHandlerTest(t, nil)
	ctx := context.Background()

	partID, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "Test Part"})
	proj, err := h.Projects.CreateProject(ctx, projects.CreateProjectRequest{Name: "RBAC Board", Status: "draft"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	bomItem, err := h.Projects.AddBOMItem(ctx, projects.AddBOMItemRequest{
		ProjectID: proj.ID, PartID: &partID, Designator: "U1", QuantityPerBoard: 1,
	})
	if err != nil {
		t.Fatalf("AddBOMItem: %v", err)
	}
	_ = editor

	writePaths := []string{
		"/projects",
		fmt.Sprintf("/projects/%d/update", proj.ID),
		fmt.Sprintf("/projects/%d/delete", proj.ID),
		fmt.Sprintf("/projects/%d/bom", proj.ID),
		fmt.Sprintf("/projects/%d/bom/import", proj.ID),
		fmt.Sprintf("/projects/%d/bom/%d/link", proj.ID, bomItem.ID),
		fmt.Sprintf("/projects/%d/bom/%d/delete", proj.ID, bomItem.ID),
		fmt.Sprintf("/projects/%d/build", proj.ID),
	}

	// Authenticated viewer can read list, detail, and Pick Mode locate, but gets 403 on all writes.
	activeUser := viewer
	router := newProjectsRouter(h, &activeUser)

	for _, path := range []string{"/projects", fmt.Sprintf("/projects/%d", proj.ID)} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("viewer GET %s expected 200, got %d", path, rr.Code)
		}
		if strings.Contains(rr.Body.String(), "create-project-modal") || strings.Contains(rr.Body.String(), "Build 1 Board") {
			t.Fatalf("viewer page %s leaked write controls", path)
		}
	}

	rrLoc := httptest.NewRecorder()
	router.ServeHTTP(rrLoc, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/projects/%d/locate", proj.ID), nil))
	if rrLoc.Code != http.StatusSeeOther && rrLoc.Code != http.StatusOK {
		t.Fatalf("viewer locate expected 303/200, got %d", rrLoc.Code)
	}

	for _, p := range writePaths {
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader("name=X&quantity_per_board=1&board_count=1"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("viewer POST %s expected 403 Forbidden, got %d", p, rr.Code)
		}
	}

	// Unauthenticated guest receives 401 Unauthorized on write endpoints.
	activeUser = auth.Guest()
	for _, p := range writePaths {
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader("name=X&quantity_per_board=1&board_count=1"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("guest POST %s expected 401 Unauthorized, got %d", p, rr.Code)
		}
	}
}
