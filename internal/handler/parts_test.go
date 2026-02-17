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
	"strconv"
	"strings"
	"testing"

	_ "image/png"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/dashboard"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/images"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/stock"
	"github.com/tuxedocurly/wledger/internal/tags"
	"github.com/tuxedocurly/wledger/internal/uierror"
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
	cont, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont", ControllerID: c.ID})

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
	cont, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont", ControllerID: c.ID})

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
	cont, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont", ControllerID: c.ID})

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

func TestHandlePartClone(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	// Create a part to clone
	origID, err := h.Queries.CreatePart(ctx, db.CreatePartParams{
		Name:        "Clone Me",
		Description: sql.NullString{String: "A description", Valid: true},
		PartNumber:  sql.NullString{String: "PN-999", Valid: true},
	})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/parts/%d/clone", origID), nil)
	r := chi.NewRouter()
	r.Get("/parts/{id}/clone", h.HandlePartClone)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	// Verify redirect goes to edit page
	location := rr.Header().Get("Location")
	if !strings.HasSuffix(location, "/edit") {
		t.Errorf("expected redirect to edit page, got %q", location)
	}

	// Verify new part was created
	var count int
	dbConn.QueryRow("SELECT count(*) FROM parts").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 parts, got %d", count)
	}

	// Verify clone has correct name
	var clonedName string
	dbConn.QueryRow("SELECT name FROM parts WHERE id != ?", origID).Scan(&clonedName)
	if clonedName != "Clone Me (Copy)" {
		t.Errorf("expected 'Clone Me (Copy)', got %q", clonedName)
	}

	// Verify original is untouched
	var origName string
	dbConn.QueryRow("SELECT name FROM parts WHERE id = ?", origID).Scan(&origName)
	if origName != "Clone Me" {
		t.Errorf("original part name changed: %q", origName)
	}
}
