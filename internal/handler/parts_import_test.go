package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestHandlePartsImport_WithLocation(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	// Setup Data: Controller, Container, Bin
	controller, _ := h.Queries.CreateController(ctx, db.CreateControllerParams{
		Name: "Test Controller", IpAddress: "192.168.1.100",
	})
	containerID, _ := h.Queries.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Test Container", ControllerID: sql.NullInt64{Int64: controller.ID, Valid: true}, SegmentID: 2,
	})
	binID, _ := h.Queries.CreateBin(ctx, db.CreateBinParams{
		Name: "Bin 5", ContainerID: containerID, LedIndex: sql.NullInt64{Int64: 5, Valid: true},
	})

	// Prepare CSV Content with valid location
	csvContent := "Name,Controller IP,Segment ID,LED Index,Quantity\n" +
		"Located Part,192.168.1.100,2,5,50\n"

	req := createMultipartRequest(t, "/parts/import", "POST", map[string]string{
		"raw_text": csvContent,
	}, map[string]string{})
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{Role: "admin"}))

	rr := httptest.NewRecorder()
	h.HandlePartsImport(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	// Verify Part Created and Assigned
	var partID int64
	dbConn.QueryRow("SELECT id FROM parts WHERE name = ?", "Located Part").Scan(&partID)

	var count int
	dbConn.QueryRow("SELECT quantity FROM part_assignments WHERE part_id = ? AND bin_id = ?", partID, binID).Scan(&count)
	if count != 50 {
		t.Errorf("expected 50 quantity in bin %d, got %d", binID, count)
	}
}

func TestHandlePartsImport_InvalidLocation(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Prepare CSV Content with non-existent location
	csvContent := "Name,Controller IP,Segment ID,LED Index\n" +
		"Bad Loc,999.999.999.999,0,0\n"

	req := createMultipartRequest(t, "/parts/import", "POST", map[string]string{
		"raw_text": csvContent,
	}, map[string]string{})
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{Role: "admin"}))

	rr := httptest.NewRecorder()
	h.HandlePartsImport(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK (HTMX pattern), got %d", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), "Location not found") {
		t.Errorf("expected error message about location not found, got: %s", rr.Body.String())
	}
}

func TestHandlePartsImport_WithTagsAndLinks(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Prepare CSV Content
	csvContent := "Name,Description,Tags,Links\n" +
		"Imported Part,A test part,Tag1|Tag2,https://example.com/1|https://example.com/2\n"

	// Create Request
	req := createMultipartRequest(t, "/parts/import", "POST", map[string]string{
		"raw_text": csvContent,
	}, map[string]string{})

	// Set user with write permissions
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{Role: "admin"}))

	rr := httptest.NewRecorder()
	h.HandlePartsImport(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	// Verify Part Created
	var partID int64
	err := dbConn.QueryRow("SELECT id FROM parts WHERE name = ?", "Imported Part").Scan(&partID)
	if err != nil {
		t.Fatalf("failed to find imported part: %v", err)
	}

	// Verify Tags Associated
	var tagCount int
	dbConn.QueryRow("SELECT count(*) FROM part_tags WHERE part_id = ?", partID).Scan(&tagCount)
	if tagCount != 2 {
		t.Errorf("expected 2 tag associations, got %d", tagCount)
	}

	// Verify Links Created
	var linkCount int
	dbConn.QueryRow("SELECT count(*) FROM part_links WHERE part_id = ?", partID).Scan(&linkCount)
	if linkCount != 2 {
		t.Errorf("expected 2 links, got %d", linkCount)
	}

	// Verify specific tag names

	rows, _ := dbConn.Query("SELECT t.name FROM tags t JOIN part_tags pt ON t.id = pt.tag_id WHERE pt.part_id = ? ORDER BY t.name", partID)
	defer rows.Close()
	var tagNames []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tagNames = append(tagNames, name)
	}
	if tagNames[0] != "tag1" || tagNames[1] != "tag2" {
		t.Errorf("expected tags [tag1, tag2], got %v", tagNames)
	}
}

func TestHandlePartsImport_EdgeCases(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Prepare CSV Content with edge cases:
	// Empty Tags/Links
	// Extra spaces
	// One tag, one link
	csvContent := "Name,Tags,Links\n" +
		"Part 1,, \n" +
		"Part 2, Tag A | Tag B , https://a.com | https://b.com \n" +
		"Part 3, SingleTag, https://single.com \n"

	req := createMultipartRequest(t, "/parts/import", "POST", map[string]string{
		"raw_text": csvContent,
	}, map[string]string{})
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{Role: "admin"}))

	rr := httptest.NewRecorder()
	h.HandlePartsImport(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	// Verify Part 1 (No tags/links)
	var p1ID int64
	dbConn.QueryRow("SELECT id FROM parts WHERE name = ?", "Part 1").Scan(&p1ID)
	var c int
	dbConn.QueryRow("SELECT count(*) FROM part_tags WHERE part_id = ?", p1ID).Scan(&c)
	if c != 0 {
		t.Error("Part 1 should have 0 tags")
	}

	// Verify Part 2 (Spaces and multiple)
	var p2ID int64
	dbConn.QueryRow("SELECT id FROM parts WHERE name = ?", "Part 2").Scan(&p2ID)
	dbConn.QueryRow("SELECT count(*) FROM part_tags WHERE part_id = ?", p2ID).Scan(&c)
	if c != 2 {
		t.Errorf("Part 2 expected 2 tags, got %d", c)
	}

	var tagName string
	dbConn.QueryRow("SELECT t.name FROM tags t JOIN part_tags pt ON t.id = pt.tag_id WHERE pt.part_id = ? ORDER BY t.name LIMIT 1", p2ID).Scan(&tagName)
	if tagName != "tag a" {
		t.Errorf("expected 'tag a', got '%s'", tagName)
	}

	// Verify Part 3 (Single)
	var p3ID int64
	dbConn.QueryRow("SELECT id FROM parts WHERE name = ?", "Part 3").Scan(&p3ID)
	dbConn.QueryRow("SELECT count(*) FROM part_links WHERE part_id = ?", p3ID).Scan(&c)
	if c != 1 {
		t.Errorf("Part 3 expected 1 link, got %d", c)
	}
}

func TestHandlePartsImport_InvalidURL(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Prepare CSV Content with invalid URL
	csvContent := "Name,Links\n" +
		"Bad Link,not-a-url\n"

	req := createMultipartRequest(t, "/parts/import", "POST", map[string]string{
		"raw_text": csvContent,
	}, map[string]string{})
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{Role: "admin"}))

	rr := httptest.NewRecorder()
	h.HandlePartsImport(rr, req)

	// Should return error in the result component (but HTMX response is 200 containing the error alert)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK (HTMX pattern), got %d", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), "invalid URL") {
		t.Errorf("expected error message about invalid URL, got: %s", rr.Body.String())
	}
}

func TestHandlePartsImport_LinkLabels(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	csvContent := "Name,Links\n" +
		"Link Part,https://www.google.com/search?q=test|http://example.org/foo\n"

	req := createMultipartRequest(t, "/parts/import", "POST", map[string]string{
		"raw_text": csvContent,
	}, map[string]string{})
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{Role: "admin"}))

	rr := httptest.NewRecorder()
	h.HandlePartsImport(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}

	var partID int64
	dbConn.QueryRow("SELECT id FROM parts WHERE name = ?", "Link Part").Scan(&partID)

	// Verify Labels
	rows, err := dbConn.Query("SELECT url, label FROM part_links WHERE part_id = ? ORDER BY url", partID)
	if err != nil {
		t.Fatalf("failed to query links: %v", err)
	}
	defer rows.Close()

	links := make(map[string]string)
	for rows.Next() {
		var u, l string
		rows.Scan(&u, &l)
		links[u] = l
	}

	if links["http://example.org/foo"] != "example.org" {
		t.Errorf("expected label 'example.org', got '%s'", links["http://example.org/foo"])
	}
	if links["https://www.google.com/search?q=test"] != "www.google.com" {
		t.Errorf("expected label 'www.google.com', got '%s'", links["https://www.google.com/search?q=test"])
	}
}
