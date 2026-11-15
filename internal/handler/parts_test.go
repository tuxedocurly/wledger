package handler

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "image/png"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/dashboard"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/images"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/stock"
	"github.com/tuxedocurly/wledger/internal/tags"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/web/pages"
)

// Reusing setup logic from Handler_hardware_test.go
func setupPartTest(t *testing.T) (*Handler, *sql.DB) {
	// Setup DB
	dbConn := openTestDB(t)
	setupTestSchema(t, dbConn)

	// Setup Logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Setup File System for Test
	// Ensure there is a clean state for uploads
	_ = os.RemoveAll("./app/uploads")
	_ = os.MkdirAll("./app/uploads/images", 0755)
	_ = os.MkdirAll("./app/uploads/docs", 0755)

	// Init images package (ensure dir exists)
	_ = images.Init()

	s := db.NewStore(dbConn)
	uiError := uierror.New(logger)

	tagsService := tags.NewService(dbConn, s)
	docsService := documents.NewService(s, logger)
	stockService := stock.NewService(s, logger)
	partsService := parts.NewService(dbConn, s, logger, tagsService, docsService)
	dashboardService := dashboard.NewService(s)

	h := &Handler{
		Logger:    logger,
		Queries:   s,
		Database:  dbConn,
		Parts:     partsService,
		Tags:      tagsService,
		UIError:   uiError,
		Stock:     stockService,
		Documents: docsService,
		Dashboard: dashboardService,
	}

	return h, dbConn
}

func cleanupPartTest() {
	_ = os.RemoveAll("./app")
}

// Helper to create a multipart request
func createMultipartRequest(t *testing.T, uri, method string, fields map[string]string, files map[string]string) *http.Request {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add Fields
	for k, v := range fields {
		_ = writer.WriteField(k, v)
	}

	// Add Files
	for fieldName, fileName := range files {
		part, err := writer.CreateFormFile(fieldName, fileName)
		if err != nil {
			t.Fatalf("failed to create form file: %v", err)
		}
		// Write dummy content
		if strings.HasSuffix(fileName, ".jpg") || strings.HasSuffix(fileName, ".png") {
			// Generate real image data
			img := image.NewRGBA(image.Rect(0, 0, 10, 10))
			// Fill with white
			for y := 0; y < 10; y++ {
				for x := 0; x < 10; x++ {
					img.Set(x, y, color.White)
				}
			}

			// Encode to JPEG
			err := jpeg.Encode(part, img, nil)
			if err != nil {
				t.Fatalf("failed to encode test image: %v", err)
			}
		} else {
			io.WriteString(part, "dummy content for "+fileName)
		}
	}

	writer.Close()

	req := httptest.NewRequest(method, uri, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestPartCreate_HappyPath(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Prepare Request with .png to match the generated content in createMultipartRequest
	req := createMultipartRequest(t, "/parts", "POST", map[string]string{
		"name":         "Test Part",
		"barcode_data": "1001",
		"unit_cost":    "10.50",
	}, map[string]string{
		"image":     "test.png",
		"documents": "doc.pdf",
	})

	rr := httptest.NewRecorder()
	h.HandlePartsCreate(rr, req)

	// Check Redirect
	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	// Verify DB
	var count int
	dbConn.QueryRow("SELECT count(*) FROM parts").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 part, got %d", count)
	}

	var docCount int
	dbConn.QueryRow("SELECT count(*) FROM part_docs").Scan(&docCount)
	if docCount != 1 {
		t.Errorf("expected 1 doc, got %d", docCount)
	}

	// Verify Files
	files, _ := os.ReadDir("./app/uploads/images")
	if len(files) == 0 {
		t.Error("expected image file to be created")
	}

	docs, _ := os.ReadDir("./app/uploads/docs")
	if len(docs) == 0 {
		t.Error("expected doc file to be created")
	}
}

func TestPartCreate_WithTags(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Prepare Request with Tags
	req := createMultipartRequest(t, "/parts", "POST", map[string]string{
		"name":         "Tagged Part",
		"barcode_data": "1002",
		"unit_cost":    "5.00",
		"tags":         "Esp32, WiFi,  MODULE ", // Mixed case and spacing
	}, map[string]string{})

	rr := httptest.NewRecorder()
	h.HandlePartsCreate(rr, req)

	// Check Redirect
	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	// Verify Part Created
	var partID int64
	err := dbConn.QueryRow("SELECT id FROM parts WHERE name = ?", "Tagged Part").Scan(&partID)
	if err != nil {
		t.Fatalf("failed to find created part: %v", err)
	}

	// Verify Tags Created
	rows, err := dbConn.Query("SELECT name FROM tags ORDER BY name")
	if err != nil {
		t.Fatalf("failed to query Tags: %v", err)
	}
	defer rows.Close()

	var tagNames []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tagNames = append(tagNames, name)
	}

	expectedTags := []string{"esp32", "module", "wifi"}
	if len(tagNames) != 3 {
		t.Errorf("expected 3 tags, got %d: %v", len(tagNames), tagNames)
	}
	for i, name := range tagNames {
		if name != expectedTags[i] {
			t.Errorf("expected tag %s, got %s", expectedTags[i], name)
		}
	}

	// Verify Part-Tag Association
	var count int
	dbConn.QueryRow("SELECT count(*) FROM part_tags WHERE part_id = ?", partID).Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 tag associations, got %d", count)
	}
}

func TestPartCreate_RollbackOnError(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Create a part to occupy barcode "1001"
	_, _ = h.Queries.CreatePart(context.Background(), db.CreatePartParams{
		Name: "Existing Part", BarcodeData: sql.NullString{String: "1001", Valid: true},
	})

	// Try to create ANOTHER part with SAME barcode (DB Constraint Violation)
	req := createMultipartRequest(t, "/parts", "POST", map[string]string{
		"name":         "Duplicate Part",
		"barcode_data": "1001",
	}, map[string]string{
		"image":     "dup.png",
		"documents": "dup_doc.pdf",
	})

	rr := httptest.NewRecorder()
	h.HandlePartsCreate(rr, req)

	// Expect Error (Conflict or Internal Server Error Handled by Handler)
	// The Handler catches UNIQUE constraint and returns 409 Conflict
	if rr.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict, got %d", rr.Code)
	}

	// Verify DB Rollback
	var count int
	dbConn.QueryRow("SELECT count(*) FROM parts").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 part, got %d", count)
	}

	// Verify File Cleanup
	files, _ := os.ReadDir("./app/uploads/images")
	if len(files) > 0 {
		t.Errorf("expected 0 images (cleanup failed), got %d: %v", len(files), files)
	}

	docs, _ := os.ReadDir("./app/uploads/docs")
	if len(docs) > 0 {
		t.Errorf("expected 0 docs (cleanup failed), got %d", len(docs))
	}
}

func TestPartUpdate_RollbackOnError(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Create Initial Part with Image
	_ = os.MkdirAll("./app/uploads/images", 0755)
	initialImgName := "initial.jpg"
	_ = os.WriteFile("./app/uploads/images/"+initialImgName, []byte("dummy"), 0644)

	id, err := h.Queries.CreatePart(context.Background(), db.CreatePartParams{
		Name:      "Original Part",
		ImagePath: sql.NullString{String: "/uploads/images/" + initialImgName, Valid: true},
	})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Create another part to conflict with
	_, _ = h.Queries.CreatePart(context.Background(), db.CreatePartParams{
		Name: "Blocker", BarcodeData: sql.NullString{String: "9999", Valid: true},
	})

	// Update "Original Part" to have barcode "9999" (Conflict)
	req := createMultipartRequest(t, "/parts/"+strconv.Itoa(int(id))+"/update", "POST", map[string]string{
		"name":         "Updated Name",
		"barcode_data": "9999", // Conflict
	}, map[string]string{
		"image": "new.png",
	})

	// Setup Router
	r := chi.NewRouter()
	r.Post("/parts/{id}/update", h.HandlePartUpdate)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// Expect Error
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}

	// Verify DB state
	p, _ := h.Queries.GetPart(context.Background(), id)
	if p.Name != "Original Part" {
		t.Errorf("DB modified despite rollback! Name: %s", p.Name)
	}
	if p.ImagePath.String != "/uploads/images/"+initialImgName {
		t.Errorf("Image path modified! %s", p.ImagePath.String)
	}

	// Verify File System
	if _, err := os.Stat("./app/uploads/images/" + initialImgName); os.IsNotExist(err) {
		t.Error("Original image was deleted!")
	}

	files, _ := os.ReadDir("./app/uploads/images")
	for _, f := range files {
		if f.Name() != initialImgName && !strings.Contains(f.Name(), "_thumb") {
			t.Errorf("Found unexpected file (cleanup failed): %s", f.Name())
		}
	}
}

func TestPartUpdate_HappyPath_ImageSwap(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Create Initial Part with Image
	initialImgName := "part_old.jpg"
	_ = os.WriteFile("./app/uploads/images/"+initialImgName, []byte("dummy"), 0644)
	_ = os.WriteFile("./app/uploads/images/part_old_thumb.jpg", []byte("dummy"), 0644)

	id, _ := h.Queries.CreatePart(context.Background(), db.CreatePartParams{
		Name:      "Old Name",
		ImagePath: sql.NullString{String: "/uploads/images/" + initialImgName, Valid: true},
	})

	// Update with NEW image
	req := createMultipartRequest(t, "/parts/"+strconv.Itoa(int(id))+"/update", "POST", map[string]string{
		"name": "New Name",
	}, map[string]string{
		"image": "new_swap.png",
	})

	r := chi.NewRouter()
	r.Post("/parts/{id}/update", h.HandlePartUpdate)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", rr.Code)
	}

	// Verify DB
	p, _ := h.Queries.GetPart(context.Background(), id)
	if p.Name != "New Name" {
		t.Error("Name not updated")
	}
	if p.ImagePath.String == "/uploads/images/"+initialImgName {
		t.Error("Image path not updated")
	}

	// Verify FS
	if _, err := os.Stat("./app/uploads/images/" + initialImgName); !os.IsNotExist(err) {
		t.Error("Old image was NOT deleted")
	}
	newDiskPath := "." + strings.Replace(p.ImagePath.String, "/uploads", "/app/uploads", 1)
	if _, err := os.Stat(newDiskPath); os.IsNotExist(err) {
		t.Errorf("New image not found at %s", newDiskPath)
	}
}

func TestPartStockMove_Move(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	// Setup Data: Part, Bin A, Bin B, Stock in Bin A
	p, _ := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "Part A"})

	c, _ := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	cont, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont", ControllerID: sql.NullInt64{Int64: c.ID, Valid: true}})

	binA, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "Bin A", ContainerID: cont})
	binB, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "Bin B", ContainerID: cont})

	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: p, BinID: sql.NullInt64{Int64: binA, Valid: true}, Quantity: 10,
	})

	assignID, _ := h.Queries.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: p, BinID: sql.NullInt64{Int64: binA, Valid: true},
	})

	// Request: Move from Bin A to Bin B
	targetPath := fmt.Sprintf("/parts/%d/stock/%d/move", p, assignID)
	// Create Form Data
	body := "bin_id=" + strconv.Itoa(int(binB))

	req := httptest.NewRequest("POST", targetPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	r := chi.NewRouter()
	r.Post("/parts/{id}/stock/{assignment_id}/move", h.HandlePartStockMove)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", rr.Code)
	}

	// Verify
	// Bin A should be empty (no assignment)
	_, err := h.Queries.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: p, BinID: sql.NullInt64{Int64: binA, Valid: true},
	})
	if err == nil {
		t.Error("Source assignment should be gone")
	}

	// Bin B should have 10
	assignB, err := h.Queries.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: p, BinID: sql.NullInt64{Int64: binB, Valid: true},
	})
	if err != nil {
		t.Error("Target assignment missing")
	}
	row, _ := h.Queries.GetAssignment(ctx, assignB)
	if row.Quantity != 10 {
		t.Errorf("Expected 10 in target, got %d", row.Quantity)
	}
}

func TestPartStockMove_Merge(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	// Setup: Part, Bin A (10), Bin B (5)
	p, _ := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "Part A"})

	c, _ := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	cont, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont", ControllerID: sql.NullInt64{Int64: c.ID, Valid: true}})

	binA, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "Bin A", ContainerID: cont})
	binB, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "Bin B", ContainerID: cont})

	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: p, BinID: sql.NullInt64{Int64: binA, Valid: true}, Quantity: 10,
	})
	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: p, BinID: sql.NullInt64{Int64: binB, Valid: true}, Quantity: 5,
	})

	assignA, _ := h.Queries.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: p, BinID: sql.NullInt64{Int64: binA, Valid: true},
	})

	// Request: Move from Bin A to Bin B
	targetPath := fmt.Sprintf("/parts/%d/stock/%d/move", p, assignA)
	body := "bin_id=" + strconv.Itoa(int(binB))

	req := httptest.NewRequest("POST", targetPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	r := chi.NewRouter()
	r.Post("/parts/{id}/stock/{assignment_id}/move", h.HandlePartStockMove)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// Verify
	// Bin A gone
	_, err := h.Queries.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: p, BinID: sql.NullInt64{Int64: binA, Valid: true},
	})
	if err == nil {
		t.Error("Source assignment should be gone")
	}

	// Bin B should have 15 (10+5)
	assignB, _ := h.Queries.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: p, BinID: sql.NullInt64{Int64: binB, Valid: true},
	})
	row, _ := h.Queries.GetAssignment(ctx, assignB)
	if row.Quantity != 15 {
		t.Errorf("Expected 15 in target, got %d", row.Quantity)
	}
}

func TestPartStockMove_Merge_Rollback(t *testing.T) {
	// This tests atomic rollback.
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	p, _ := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "Part A"})

	c, _ := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	cont, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont", ControllerID: sql.NullInt64{Int64: c.ID, Valid: true}})

	binA, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "Bin A", ContainerID: cont})

	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: p, BinID: sql.NullInt64{Int64: binA, Valid: true}, Quantity: 10,
	})
	assignA, _ := h.Queries.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: p, BinID: sql.NullInt64{Int64: binA, Valid: true},
	})

	// Request: Move to Bin 999 (Does not exist)
	targetPath := fmt.Sprintf("/parts/%d/stock/%d/move", p, assignA)
	body := "bin_id=999"

	req := httptest.NewRequest("POST", targetPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	r := chi.NewRouter()
	r.Post("/parts/{id}/stock/{assignment_id}/move", h.HandlePartStockMove)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// Expect Error
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 (FK violation), got %d", rr.Code)
	}

	// Verify Source Still Exists (Rollback or No-Op)
	row, err := h.Queries.GetAssignment(ctx, assignA)
	if err != nil {
		t.Error("Source assignment was deleted despite error!")
	}
	if row.Quantity != 10 {
		t.Error("Source quantity changed!")
	}
}

func TestHandlePartStockAdjust_OOBTotalStock(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	partID, err := h.Queries.CreatePart(ctx, db.CreatePartParams{
		Name:              "OOB Part",
		MinStockThreshold: sql.NullInt64{Int64: 5, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}
	contID, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Box"})
	binID, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: contID})

	// Seed binned stock (qty 4) + orphaned stock (qty 3) -> total 7
	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Int64: binID, Valid: true}, Quantity: 4,
	})
	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Valid: false}, Quantity: 3,
	})
	assignBinID, _ := h.Queries.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: partID, BinID: sql.NullInt64{Int64: binID, Valid: true},
	})
	assignments, _ := h.Queries.GetPartAssignments(ctx, partID)
	var orphanAssignID int64
	for _, a := range assignments {
		if !a.BinID.Valid {
			orphanAssignID = a.ID
		}
	}

	r := chi.NewRouter()
	r.Post("/parts/{id}/stock/{assignment_id}/adjust", h.HandlePartStockAdjust)

	// 1. Increment binned assignment by +1 -> bin qty 5, global total 8 (5 + 3)
	req := httptest.NewRequest("POST", fmt.Sprintf("/parts/%d/stock/%d/adjust?delta=1", partID, assignBinID), nil)
	req.Header.Set("HX-Request", "true")
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: 1, Role: "admin"}))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `id="part-total-stock"`) || !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Fatalf("expected OOB #part-total-stock swap in response, got: %s", body)
	}
	if !strings.Contains(body, ">8<") && !strings.Contains(body, "8\n") && !strings.Contains(strings.TrimSpace(body), "8") {
		t.Errorf("expected updated total 8 in OOB swap, got: %s", body)
	}
	// Extract OOB segment specifically to verify total 8 inside #part-total-stock
	oobIdx := strings.Index(body, `id="part-total-stock"`)
	if oobIdx < 0 || !strings.Contains(body[oobIdx:], "8") {
		t.Errorf("expected total 8 inside #part-total-stock, got: %s", body)
	}

	// 2. Decrement orphaned assignment by -3 -> orphan qty 0, global total drops to 5 (triggers low-stock desc)
	req2 := httptest.NewRequest("POST", fmt.Sprintf("/parts/%d/stock/%d/adjust?delta=-3", partID, orphanAssignID), nil)
	req2.Header.Set("HX-Request", "true")
	req2 = req2.WithContext(auth.WithUser(req2.Context(), auth.User{ID: 1, Role: "admin"}))
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on orphan adjust, got %d: %s", rr2.Code, rr2.Body.String())
	}
	body2 := rr2.Body.String()
	oobIdx2 := strings.Index(body2, `id="part-total-stock"`)
	if oobIdx2 < 0 || !strings.Contains(body2[oobIdx2:], `hx-swap-oob="true"`) || !strings.Contains(body2[oobIdx2:], "5") {
		t.Errorf("expected OOB #part-total-stock with total 5 after orphan adjust, got: %s", body2)
	}
}

func TestImageThumbURL_AndCardVsDetailRendering(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	_ = os.MkdirAll(config.DirUploadsImages, 0755)
	withThumb := "/uploads/images/part_hasthumb.jpg"
	withoutThumb := "/uploads/images/part_nothumb.jpg"

	if err := os.WriteFile(filepath.Join(config.DirUploadsImages, "part_hasthumb.jpg"), []byte("full"), 0644); err != nil {
		t.Fatalf("write main image failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(config.DirUploadsImages, "part_hasthumb_thumb.jpg"), []byte("thumb"), 0644); err != nil {
		t.Fatalf("write thumb image failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(config.DirUploadsImages, "part_nothumb.jpg"), []byte("full"), 0644); err != nil {
		t.Fatalf("write nothumb image failed: %v", err)
	}

	if got := pages.ImageThumbURL(""); got != "" {
		t.Errorf("ImageThumbURL(\"\") = %q, want \"\"", got)
	}
	if got := pages.ImageThumbURL("https://example.com/a.jpg"); got != "https://example.com/a.jpg" {
		t.Errorf("ImageThumbURL(external) = %q, want unchanged", got)
	}
	if got := pages.ImageThumbURL(withThumb); got != "/uploads/images/part_hasthumb_thumb.jpg" {
		t.Errorf("ImageThumbURL(withThumb) = %q, want _thumb.jpg path", got)
	}
	if got := pages.ImageThumbURL(withoutThumb); got != withoutThumb {
		t.Errorf("ImageThumbURL(withoutThumb) = %q, want fallback %q", got, withoutThumb)
	}
	if got := pages.ImageThumbURL("/uploads/images/part_hasthumb_thumb.jpg"); got != "/uploads/images/part_hasthumb_thumb.jpg" {
		t.Errorf("ImageThumbURL(alreadyThumb) = %q, want unchanged", got)
	}

	partID, err := h.Queries.CreatePart(ctx, db.CreatePartParams{
		Name:      "Thumb Card Part",
		ImagePath: sql.NullString{String: withThumb, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	// List cards should use thumbnail URL
	listReq := httptest.NewRequest("GET", "/parts", nil).WithContext(auth.WithUser(ctx, auth.User{Role: "admin"}))
	listRR := httptest.NewRecorder()
	h.HandlePartsList(listRR, listReq)
	if !strings.Contains(listRR.Body.String(), "/uploads/images/part_hasthumb_thumb.jpg") {
		t.Errorf("expected parts list card to render thumbnail path")
	}

	// Detail page should keep full image path
	r := chi.NewRouter()
	r.Get("/parts/{id}", h.HandlePartDetail)
	detReq := httptest.NewRequest("GET", fmt.Sprintf("/parts/%d", partID), nil).WithContext(auth.WithUser(ctx, auth.User{Role: "admin"}))
	detRR := httptest.NewRecorder()
	r.ServeHTTP(detRR, detReq)
	if !strings.Contains(detRR.Body.String(), `src="/uploads/images/part_hasthumb.jpg"`) {
		t.Errorf("expected part detail page to keep full image URL %q", withThumb)
	}
}

func TestHandlePartsList_BinFilterTrueTotalStockAndPreservedFilters(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	partID, _ := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "MultiBin Resistor"})
	ctrl, _ := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "MainCtrl", IpAddress: "10.0.0.9"})
	contID, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "MainBox",
		ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
	})
	binA, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "BinA", ContainerID: contID})
	binB, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "BinB", ContainerID: contID})

	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: binA, Valid: true}, Quantity: 4})
	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: binB, Valid: true}, Quantity: 6})
	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Valid: false}, Quantity: 5})

	// Bin-filtered query via bin_id + active search bar filter parameters
	urlStr := fmt.Sprintf("/parts?bin_id=%d&tag=smd&controller_id=%d&stock=low&sort=name&q=MultiBin", binA, ctrl.ID)
	req := httptest.NewRequest("GET", urlStr, nil).WithContext(auth.WithUser(ctx, auth.User{Role: "admin"}))
	rr := httptest.NewRecorder()
	h.HandlePartsList(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "MultiBin Resistor") {
		t.Fatalf("expected filtered part in response")
	}
	if !strings.Contains(body, ": 15<") {
		t.Errorf("expected true global total stock 15 (4+6+5) on bin-filtered part card, body=%s", body)
	}
	for _, hidden := range []string{
		fmt.Sprintf(`name="bin_id" value="%d"`, binA),
		`name="tag" value="smd"`,
		fmt.Sprintf(`name="controller_id" value="%d"`, ctrl.ID),
		`name="stock" value="low"`,
		`name="sort" value="name"`,
		`hx-include="closest form"`,
	} {
		if !strings.Contains(body, hidden) {
			t.Errorf("expected search bar HTML to preserve %s", hidden)
		}
	}
}

func TestHandlePartDetail_StandaloneAndSafeConfirm(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	partID, _ := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "Standalone Stored Part"})
	standaloneCont, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Shelf Drawer",
		ControllerID: sql.NullInt64{Valid: false},
	})
	standaloneBin, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "D1", ContainerID: standaloneCont})

	_ = h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Int64: standaloneBin, Valid: true}, Quantity: 12,
	})

	r := chi.NewRouter()
	r.Get("/parts/{id}", h.HandlePartDetail)
	req := httptest.NewRequest("GET", fmt.Sprintf("/parts/%d", partID), nil).WithContext(auth.WithUser(ctx, auth.User{Role: "admin"}))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Shelf Drawer") || !strings.Contains(body, "Standalone") {
		t.Errorf("expected standalone container hardware label in stock table")
	}
	if !strings.Contains(body, `<option value="standalone">`) {
		t.Errorf("expected standalone option in stock assignment/move modals")
	}
	if strings.Contains(body, "Reassign orphaned stock") || strings.Contains(body, ">Fix<") {
		t.Errorf("standalone container binned stock must show Move instead of Fix Orphaned")
	}
	if strings.Contains(body, "return confirm('") {
		t.Errorf("found unsafe single-quote confirm handler in HTML")
	}
	if !strings.Contains(body, `data-confirm="`) || !strings.Contains(body, `onsubmit="return confirm(this.dataset.confirm)"`) {
		t.Errorf("expected safe data-confirm + dataset.confirm onsubmit handler")
	}
}
