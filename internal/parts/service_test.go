package parts

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"

	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/images"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/tags"
)

// setupTestDB creates an in memory DB and applies the schema using db.Migrate
func setupTestDB(t *testing.T) (*sql.DB, db.Store, func()) {
	// Open in-memory DB
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Apply migrations automatically
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	// Create store helper
	s := db.NewStore(conn)

	// return cleanup function
	return conn, s, func() {
		conn.Close()
	}
}

func TestPartAuditLogging(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	tagSvc := tags.NewService(database, s)
	docSvc := documents.NewService(s, logger)
	svc := NewService(database, s, logger, tagSvc, docSvc)

	// Create user to satisfy FK
	_, err := s.CreateUser(context.Background(), db.CreateUserParams{
		Email:        "admin@test.com",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Test CreatePart Audit
	req := CreatePartRequest{
		Name:        "Audit Part",
		Description: "Audit Description",
		PartNumber:  "PN-123",
	}
	newID, err := svc.CreatePart(ctx, req)
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	logs, err := s.GetAllAuditLogs(ctx)
	if err != nil {
		t.Fatalf("GetAllAuditLogs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(logs))
	}
	createLog := logs[0]
	if createLog.ActionType != "CREATE" || createLog.EntityType != "PART" {
		t.Errorf("unexpected log metadata: %+v", createLog)
	}

	// Verify Create Log Body
	var newValue map[string]any
	json.Unmarshal(createLog.NewValue, &newValue)
	if newValue["id"] != float64(newID) || newValue["name"] != "Audit Part" {
		t.Errorf("expected summary in new_value, got: %v", string(createLog.NewValue))
	}

	// Test UpdatePart Audit (Diff)
	updateReq := UpdatePartRequest{
		ID:          newID,
		Name:        "Audit Part Updated",
		Description: "Audit Description",
		PartNumber:  "PN-456",
	}
	err = svc.UpdatePart(ctx, updateReq)
	if err != nil {
		t.Fatalf("UpdatePart failed: %v", err)
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 2 {
		t.Fatalf("expected 2 audit logs, got %d", len(logs))
	}

	updateLog := logs[1]
	var oldVal, newVal map[string]any
	json.Unmarshal(updateLog.OldValue, &oldVal)
	json.Unmarshal(updateLog.NewValue, &newVal)

	// Verify Old Values (Only changed fields)
	if oldVal["name"] != "Audit Part" || oldVal["part_number"] != "PN-123" {
		t.Errorf("expected old values in diff, got: %s", string(updateLog.OldValue))
	}
	if _, exists := oldVal["description"]; exists {
		t.Error("description should NOT be in diff if it didn't change")
	}

	// Verify New Values (Only changed fields)
	if newVal["name"] != "Audit Part Updated" || newVal["part_number"] != "PN-456" {
		t.Errorf("expected new values in diff, got: %s", string(updateLog.NewValue))
	}

	// Test DeletePart Audit
	err = svc.DeletePart(ctx, newID)
	if err != nil {
		t.Fatalf("DeletePart failed: %v", err)
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 3 {
		t.Fatalf("expected 3 audit logs, got %d", len(logs))
	}

	deleteLog := logs[2]
	var deleteOld map[string]any
	json.Unmarshal(deleteLog.OldValue, &deleteOld)
	if deleteOld["id"] != float64(newID) || deleteOld["name"] != "Audit Part Updated" {
		t.Errorf("expected summary in old_value for delete, got: %s", string(deleteLog.OldValue))
	}
}

func TestUpdatePartStockThresholdsToZero(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	tagSvc := tags.NewService(database, s)
	docSvc := documents.NewService(s, logger)
	svc := NewService(database, s, logger, tagSvc, docSvc)

	// Create user to satisfy FK
	_, err := s.CreateUser(context.Background(), db.CreateUserParams{
		Email:        "admin@test.com",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Create part with non-zero thresholds
	req := CreatePartRequest{
		Name:              "Stock Part",
		UnitCost:          10.50,
		ReorderLevel:      5,
		MinStockThreshold: 2,
	}
	id, err := svc.CreatePart(ctx, req)
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	// Verify initial state
	p, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	if p.ReorderLevel.Int64 != 5 {
		t.Errorf("expected reorder level 5, got %d", p.ReorderLevel.Int64)
	}
	if p.MinStockThreshold.Int64 != 2 {
		t.Errorf("expected min stock 2, got %d", p.MinStockThreshold.Int64)
	}
	if p.UnitCost.Float64 != 10.50 {
		t.Errorf("expected unit cost 10.50, got %f", p.UnitCost.Float64)
	}

	// Update to Zero
	updateReq := UpdatePartRequest{
		ID:                id,
		Name:              "Stock Part Updated",
		ReorderLevel:      0,
		MinStockThreshold: 0,
		UnitCost:          0,
	}
	err = svc.UpdatePart(ctx, updateReq)
	if err != nil {
		t.Fatalf("UpdatePart failed: %v", err)
	}

	// Verify update
	pUpdated, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}

	if pUpdated.ReorderLevel.Int64 != 0 {
		t.Errorf("expected reorder level 0, got %d", pUpdated.ReorderLevel.Int64)
	}
	if pUpdated.MinStockThreshold.Int64 != 0 {
		t.Errorf("expected min stock 0, got %d", pUpdated.MinStockThreshold.Int64)
	}
	if pUpdated.UnitCost.Float64 != 0 {
		t.Errorf("expected unit cost 0, got %f", pUpdated.UnitCost.Float64)
	}
}

func TestUpdatePartClearFields(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	tagSvc := tags.NewService(database, s)
	docSvc := documents.NewService(s, logger)
	svc := NewService(database, s, logger, tagSvc, docSvc)

	// Create user to satisfy FK
	_, err := s.CreateUser(context.Background(), db.CreateUserParams{
		Email:        "admin@test.com",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Create part with full details
	req := CreatePartRequest{
		Name:         "Full Part",
		Description:  "Has Description",
		PartNumber:   "PN-123",
		Manufacturer: "Has Manufacturer",
		Supplier:     "Has Supplier",
		BarcodeData:  "BC-123",
	}
	id, err := svc.CreatePart(ctx, req)
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	// Update to clear some fields
	updateReq := UpdatePartRequest{
		ID:           id,
		Name:         "Full Part Updated",
		Description:  "",                 // Clear
		PartNumber:   "",                 // Clear
		Manufacturer: "Has Manufacturer", // Keep
		Supplier:     "",                 // Clear
		BarcodeData:  "",                 // Clear
	}
	err = svc.UpdatePart(ctx, updateReq)
	if err != nil {
		t.Fatalf("UpdatePart failed: %v", err)
	}

	// Verify
	p, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}

	if p.Description.String != "" || p.Description.Valid {
		t.Errorf("expected description to be cleared, got %q (Valid: %v)", p.Description.String, p.Description.Valid)
	}
	if p.PartNumber.String != "" || p.PartNumber.Valid {
		t.Errorf("expected part number to be cleared, got %q (Valid: %v)", p.PartNumber.String, p.PartNumber.Valid)
	}
	if p.Supplier.String != "" || p.Supplier.Valid {
		t.Errorf("expected supplier to be cleared, got %q (Valid: %v)", p.Supplier.String, p.Supplier.Valid)
	}
	if p.BarcodeData.String != "" || p.BarcodeData.Valid {
		t.Errorf("expected barcode to be cleared, got %q (Valid: %v)", p.BarcodeData.String, p.BarcodeData.Valid)
	}
	if p.Manufacturer.String != "Has Manufacturer" {
		t.Errorf("expected manufacturer to remain, got %q", p.Manufacturer.String)
	}
}

type nopMultipartFile struct {
	*bytes.Reader
}

func (nopMultipartFile) Close() error { return nil }

func encodeTestJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 12, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 12; x++ {
			img.Set(x, y, color.RGBA{R: 20, G: 120, B: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("failed to encode test jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestSanitizeFTSQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "hyphenated mcu", input: "ESP32-S3", want: `"esp32"* "s3"*`},
		{name: "hyphenated connector", input: "USB-C", want: `"usb"* "c"*`},
		{name: "hyphenated header", input: "JST-XH", want: `"jst"* "xh"*`},
		{name: "decimal capacitance", input: "0.1uF", want: `"0"* "1uf"*`},
		{name: "decimal voltage", input: "3.3V", want: `"3"* "3v"*`},
		{name: "slash and percent", input: "10k/1%", want: `"10k"* "1"*`},
		{name: "unbalanced quote", input: `"0805`, want: `"0805"*`},
		{name: "boolean operators only", input: "AND NOT", want: ""},
		{name: "all boolean keywords", input: "AND OR NOT NEAR", want: ""},
		{name: "leading negative", input: "-ESP32", want: `"esp32"*`},
		{name: "fts operators and punctuation", input: "(10k) + 1/4W:smd^2*foo", want: `"10k"* "1"* "4w"* "smd"* "2"* "foo"*`},
		{name: "punctuation only", input: "--- *** :::", want: ""},
		{name: "whitespace only", input: "   ", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeFTSQuery(tc.input)
			if got != tc.want {
				t.Errorf("SanitizeFTSQuery(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestListParts_FTS5SpecialCharacters(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(database, s, logger, tags.NewService(database, s), documents.NewService(s, logger))
	ctx := context.Background()

	seed := []struct {
		name       string
		partNumber string
	}{
		{name: "ESP32-S3 DevKit", partNumber: "ESP32-S3-WROOM-1"},
		{name: "USB-C Receptacle", partNumber: "USB4105"},
		{name: "JST-XH 4-Pin Header", partNumber: "B4B-XH-A"},
		{name: "0.1uF Ceramic Capacitor", partNumber: "C0603X7R"},
		{name: "3.3V LDO Regulator", partNumber: "AMS1117-3.3"},
		{name: "10k/1% 0805 Resistor", partNumber: "RC0805FR-0710KL"},
	}

	for _, item := range seed {
		_, err := svc.CreatePart(ctx, CreatePartRequest{
			Name:       item.name,
			PartNumber: item.partNumber,
		})
		if err != nil {
			t.Fatalf("failed to seed part %q: %v", item.name, err)
		}
	}

	tests := []struct {
		query    string
		wantName string // empty expects zero matches without error
	}{
		{query: "ESP32-S3", wantName: "ESP32-S3 DevKit"},
		{query: "USB-C", wantName: "USB-C Receptacle"},
		{query: "JST-XH", wantName: "JST-XH 4-Pin Header"},
		{query: "0.1uF", wantName: "0.1uF Ceramic Capacitor"},
		{query: "3.3V", wantName: "3.3V LDO Regulator"},
		{query: "10k/1%", wantName: "10k/1% 0805 Resistor"},
		{query: `"0805`, wantName: "10k/1% 0805 Resistor"},
		{query: "AND NOT", wantName: ""},
		{query: "---", wantName: ""},
	}

	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			results, err := svc.ListParts(ctx, tc.query, 1, nil)
			if err != nil {
				t.Fatalf("ListParts(%q) returned FTS5 error: %v", tc.query, err)
			}
			if tc.wantName == "" {
				if len(results) != 0 {
					t.Errorf("ListParts(%q) = %d results, want 0", tc.query, len(results))
				}
				return
			}
			found := false
			for _, r := range results {
				if r.Name == tc.wantName {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("ListParts(%q) did not find %q; got %+v", tc.query, tc.wantName, results)
			}
		})
	}
}

func TestUpdatePart_RollbackPreservesExistingImage(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	_ = os.RemoveAll("./app")
	defer os.RemoveAll("./app")
	if err := images.Init(); err != nil {
		t.Fatalf("images.Init failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(database, s, logger, tags.NewService(database, s), documents.NewService(s, logger))
	ctx := context.Background()

	jpegBytes := encodeTestJPEG(t)
	p1ID, err := svc.CreatePart(ctx, CreatePartRequest{
		Name:        "Original Part",
		BarcodeData: "BARCODE-1",
		Image: &DocUpload{
			File:   nopMultipartFile{bytes.NewReader(jpegBytes)},
			Header: &multipart.FileHeader{Filename: "cover.jpg"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePart p1 failed: %v", err)
	}

	_, err = svc.CreatePart(ctx, CreatePartRequest{
		Name:        "Conflicting Part",
		BarcodeData: "BARCODE-DUP",
	})
	if err != nil {
		t.Fatalf("CreatePart p2 failed: %v", err)
	}

	p1, err := svc.GetPart(ctx, p1ID)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	if !p1.ImagePath.Valid || p1.ImagePath.String == "" {
		t.Fatalf("expected p1 to have saved image path")
	}

	fileName := filepath.Base(p1.ImagePath.String)
	mainDiskPath := filepath.Join(config.DirUploadsImages, fileName)
	thumbDiskPath := filepath.Join(config.DirUploadsImages, fileName[:len(fileName)-4]+"_thumb.jpg")
	if _, err := os.Stat(mainDiskPath); err != nil {
		t.Fatalf("expected initial image file on disk: %v", err)
	}
	if _, err := os.Stat(thumbDiskPath); err != nil {
		t.Fatalf("expected initial thumb file on disk: %v", err)
	}

	// Failed update WITHOUT a new image must NEVER delete existing cover image or thumb.
	err = svc.UpdatePart(ctx, UpdatePartRequest{
		ID:          p1ID,
		Name:        "Original Part",
		BarcodeData: "BARCODE-DUP", // collides with p2 UNIQUE constraint
		Image:       nil,
	})
	if err == nil {
		t.Fatal("expected duplicate barcode UpdatePart error, got nil")
	}
	if _, err := os.Stat(mainDiskPath); err != nil {
		t.Errorf("existing cover image was deleted on failed UpdatePart rollback: %v", err)
	}
	if _, err := os.Stat(thumbDiskPath); err != nil {
		t.Errorf("existing cover thumbnail was deleted on failed UpdatePart rollback: %v", err)
	}

	// Failed update WITH a new uploaded image should delete the unused new upload while preserving original.
	beforeFiles, _ := filepath.Glob(filepath.Join(config.DirUploadsImages, "*.jpg"))
	err = svc.UpdatePart(ctx, UpdatePartRequest{
		ID:          p1ID,
		Name:        "Original Part",
		BarcodeData: "BARCODE-DUP",
		Image: &DocUpload{
			File:   nopMultipartFile{bytes.NewReader(jpegBytes)},
			Header: &multipart.FileHeader{Filename: "replacement.jpg"},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate barcode UpdatePart error with new image, got nil")
	}
	afterFiles, _ := filepath.Glob(filepath.Join(config.DirUploadsImages, "*.jpg"))
	if len(afterFiles) != len(beforeFiles) {
		t.Errorf("expected failed replacement upload to be cleaned up: before=%v after=%v", beforeFiles, afterFiles)
	}
	if _, err := os.Stat(mainDiskPath); err != nil {
		t.Errorf("existing cover image missing after rolled-back replacement: %v", err)
	}
}

func TestGetPartDetail_StandaloneContainers(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(database, s, logger, tags.NewService(database, s), documents.NewService(s, logger))
	ctx := context.Background()

	ctrl, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl", IpAddress: "10.0.0.5"})
	_, _ = s.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "WLED Box",
		ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
	})
	standaloneID, err := s.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Standalone Drawer",
		ControllerID: sql.NullInt64{Valid: false},
	})
	if err != nil {
		t.Fatalf("CreateContainer standalone failed: %v", err)
	}

	partID, _ := svc.CreatePart(ctx, CreatePartRequest{Name: "Drawer Part"})
	detail, err := svc.GetPartDetail(ctx, partID)
	if err != nil {
		t.Fatalf("GetPartDetail failed: %v", err)
	}
	if len(detail.StandaloneContainers) != 1 || detail.StandaloneContainers[0].ID != standaloneID {
		t.Errorf("expected standalone container %d in PartDetail, got %+v", standaloneID, detail.StandaloneContainers)
	}
}

func TestPartService_LinkValidationAndIDOR(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(database, s, logger, tags.NewService(database, s), documents.NewService(s, logger))
	ctx := context.Background()

	// Create victim part with a valid initial external link.
	victimID, err := svc.CreatePart(ctx, CreatePartRequest{
		Name: "Victim Part",
		Links: []LinkDTO{
			{URL: "https://victim.example.com/datasheet.pdf", Label: "Victim DS"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePart victim failed: %v", err)
	}
	victimDetail, err := svc.GetPartDetail(ctx, victimID)
	if err != nil || len(victimDetail.Links) != 1 {
		t.Fatalf("expected 1 victim link, got len=%d err=%v", len(victimDetail.Links), err)
	}
	victimLinkID := victimDetail.Links[0].ID

	// CreatePart rejects XSS/unsafe/relative link schemes.
	badURLs := []string{
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
		"/relative/link",
	}
	for _, bad := range badURLs {
		if _, err := svc.CreatePart(ctx, CreatePartRequest{
			Name:  "Bad Link Part",
			Links: []LinkDTO{{URL: bad, Label: "Evil"}},
		}); err == nil {
			t.Fatalf("CreatePart accepted unsafe link URL %q", bad)
		}
	}

	// Create attacker part with its own valid link.
	attackerID, err := svc.CreatePart(ctx, CreatePartRequest{
		Name: "Attacker Part",
		Links: []LinkDTO{
			{URL: "https://attacker.example.com/orig.pdf", Label: "Orig"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePart attacker failed: %v", err)
	}
	attackerDetail, err := svc.GetPartDetail(ctx, attackerID)
	if err != nil || len(attackerDetail.Links) != 1 {
		t.Fatalf("expected 1 attacker link, got len=%d err=%v", len(attackerDetail.Links), err)
	}
	attackerLinkID := attackerDetail.Links[0].ID

	// UpdatePart rejects unsafe ExistingLinks and rolls back other mutations.
	for _, bad := range badURLs {
		if err := svc.UpdatePart(ctx, UpdatePartRequest{
			ID:   attackerID,
			Name: "Should Rollback",
			ExistingLinks: []LinkDTO{
				{ID: attackerLinkID, URL: bad, Label: "Evil"},
			},
		}); err == nil {
			t.Fatalf("UpdatePart accepted unsafe ExistingLinks URL %q", bad)
		}
		if err := svc.UpdatePart(ctx, UpdatePartRequest{
			ID:       attackerID,
			Name:     "Should Rollback",
			NewLinks: []LinkDTO{{URL: bad, Label: "EvilNew"}},
		}); err == nil {
			t.Fatalf("UpdatePart accepted unsafe NewLinks URL %q", bad)
		}
	}

	// Cross-part ExistingLinks IDOR must fail and leave victim link untouched.
	if err := svc.UpdatePart(ctx, UpdatePartRequest{
		ID:   attackerID,
		Name: "IDOR Attempt",
		ExistingLinks: []LinkDTO{
			{ID: victimLinkID, URL: "https://hijacked.example.com/pwn", Label: "Pwned"},
		},
	}); err == nil {
		t.Fatal("expected cross-part ExistingLinks IDOR to fail, got nil")
	}
	if err := svc.UpdatePart(ctx, UpdatePartRequest{
		ID:   attackerID,
		Name: "Negative Link ID",
		ExistingLinks: []LinkDTO{
			{ID: -1, URL: "https://example.com/ok", Label: "Neg"},
		},
	}); err == nil {
		t.Fatal("expected negative ExistingLinks ID to fail, got nil")
	}

	// Confirm transaction rollback preserved attacker name and victim link was untouched.
	gotAttacker, err := svc.GetPart(ctx, attackerID)
	if err != nil {
		t.Fatalf("GetPart attacker failed: %v", err)
	}
	if gotAttacker.Name != "Attacker Part" {
		t.Fatalf("expected attacker name rollback to 'Attacker Part', got %q", gotAttacker.Name)
	}
	gotVictimDetail, err := svc.GetPartDetail(ctx, victimID)
	if err != nil || len(gotVictimDetail.Links) != 1 {
		t.Fatalf("expected 1 victim link after blocked IDOR, got %d err=%v", len(gotVictimDetail.Links), err)
	}
	if gotVictimDetail.Links[0].Url != "https://victim.example.com/datasheet.pdf" || gotVictimDetail.Links[0].Label.String != "Victim DS" {
		t.Fatalf("victim link was mutated by cross-part IDOR: %+v", gotVictimDetail.Links[0])
	}

	// Valid own link update + new link creation still succeeds and trims whitespace.
	if err := svc.UpdatePart(ctx, UpdatePartRequest{
		ID:   attackerID,
		Name: "Attacker Updated",
		ExistingLinks: []LinkDTO{
			{ID: attackerLinkID, URL: "  https://attacker.example.com/updated.pdf ", Label: "Updated"},
		},
		NewLinks: []LinkDTO{
			{URL: "http://attacker.example.com/extra", Label: "Extra"},
		},
	}); err != nil {
		t.Fatalf("valid UpdatePart with links failed: %v", err)
	}
	gotAttackerDetail, err := svc.GetPartDetail(ctx, attackerID)
	if err != nil {
		t.Fatalf("GetPartDetail attacker failed: %v", err)
	}
	if len(gotAttackerDetail.Links) != 2 {
		t.Fatalf("expected 2 links on attacker part, got %d", len(gotAttackerDetail.Links))
	}
	if gotAttackerDetail.Links[0].Url != "https://attacker.example.com/updated.pdf" {
		t.Fatalf("expected trimmed updated URL, got %q", gotAttackerDetail.Links[0].Url)
	}
}
