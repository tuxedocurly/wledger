package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/inspiration"
	"github.com/tuxedocurly/wledger/internal/logger"
)

// setupTestDB creates an isolated SQLite DB and applies the schema using db.Migrate.
func setupTestDB(t *testing.T) (*sql.DB, db.Store, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	if err := db.Migrate(conn); err != nil {
		conn.Close()
		t.Fatalf("failed to migrate test db: %v", err)
	}

	s := db.NewStore(conn)
	return conn, s, func() {
		conn.Close()
	}
}

func setupTestUploads(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wledger_test_uploads_*")
	if err != nil {
		t.Fatalf("failed to create temp uploads dir: %v", err)
	}

	dummyFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(dummyFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}

	subDir := filepath.Join(dir, "images")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "img.png"), []byte("fake image"), 0644); err != nil {
		t.Fatalf("failed to create dummy image: %v", err)
	}

	return dir, func() {
		os.RemoveAll(dir)
	}
}

func TestExport_HappyPath(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, log)
	ctx := context.Background()

	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("failed to init settings: %v", err)
	}
	_, err := s.CreateUser(ctx, db.CreateUserParams{
		Email:        "admin@example.com",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	var buf bytes.Buffer
	if err := svc.Export(ctx, &buf); err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("failed to read generated zip: %v", err)
	}

	files := make(map[string]bool)
	for _, f := range zr.File {
		files[f.Name] = true
	}

	for _, expected := range []string{"restore_data.json", "human_readable_parts.csv", "uploads/test.txt", "uploads/images/img.png"} {
		if !files[expected] {
			t.Errorf("%s missing from backup", expected)
		}
	}

	rc, err := zr.Open("restore_data.json")
	if err != nil {
		t.Fatalf("failed to open restore_data.json: %v", err)
	}
	defer rc.Close()

	var manifest Manifest
	if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
		t.Fatalf("failed to decode manifest: %v", err)
	}

	if manifest.Version != ManifestVersionV2 {
		t.Errorf("expected manifest version %q, got %q", ManifestVersionV2, manifest.Version)
	}
	if len(manifest.Users) != 1 || manifest.Users[0].Email != "admin@example.com" {
		t.Errorf("unexpected manifest users: %+v", manifest.Users)
	}
}

func TestRestore_HappyPath(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, log)
	ctx := context.Background()

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	manifest := Manifest{
		Version: "1.0",
		Users: []db.User{
			{
				ID:           99,
				Email:        "restored@example.com",
				PasswordHash: "newhash",
				Role:         "admin",
				CreatedAt:    sql.NullTime{Time: time.Now(), Valid: true},
			},
		},
		Settings: db.Setting{
			CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
			UpdatedAt: sql.NullTime{Time: time.Now(), Valid: true},
		},
	}
	fJSON, _ := zw.Create("restore_data.json")
	_ = json.NewEncoder(fJSON).Encode(manifest)

	fFile, _ := zw.Create("uploads/restored_file.txt")
	_, _ = fFile.Write([]byte("I am new here"))
	_ = zw.Close()

	zipBytes := buf.Bytes()
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	u, err := s.GetUserByEmail(ctx, "restored@example.com")
	if err != nil {
		t.Fatalf("failed to find restored user: %v", err)
	}
	if u.ID != 99 {
		t.Errorf("expected restored user ID 99, got %d", u.ID)
	}

	if _, err := os.Stat(filepath.Join(uploadsDir, "restored_file.txt")); os.IsNotExist(err) {
		t.Error("restored file missing from uploads dir")
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "test.txt")); err == nil {
		t.Error("old file 'test.txt' still exists in uploads dir (should be gone)")
	}
}

func TestRestore_RollbackOnDBError(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, log)
	ctx := context.Background()

	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("failed to init settings: %v", err)
	}

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	manifest := Manifest{
		Version: "1.0",
		Settings: db.Setting{
			CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
			UpdatedAt: sql.NullTime{Time: time.Now(), Valid: true},
		},
		PartAssignments: []db.PartAssignment{
			{
				ID: 1, PartID: 9999, Quantity: 10, // FK violation
			},
		},
	}
	fJSON, _ := zw.Create("restore_data.json")
	_ = json.NewEncoder(fJSON).Encode(manifest)

	fFile, _ := zw.Create("uploads/should_not_exist.txt")
	_, _ = fFile.Write([]byte("bad"))
	_ = zw.Close()

	zipBytes := buf.Bytes()
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err == nil {
		t.Fatal("Expected error during restore due to FK violation, got nil")
	}

	if _, err := s.GetSettings(ctx); err != nil {
		t.Errorf("Settings missing after rollback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "test.txt")); os.IsNotExist(err) {
		t.Error("original file 'test.txt' missing after rollback")
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "should_not_exist.txt")); err == nil {
		t.Error("new file 'should_not_exist.txt' exists despite rollback")
	}
}

func TestExportAndRestore_V2FullFidelity(t *testing.T) {
	defer logger.SetDebug(false)

	srcDB, srcStore, srcCleanup := setupTestDB(t)
	defer srcCleanup()
	srcUploads, srcFsCleanup := setupTestUploads(t)
	defer srcFsCleanup()

	docsDir := filepath.Join(srcUploads, "docs")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		t.Fatalf("failed to create docs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "datasheet.pdf"), []byte("%PDF-1.4 mock"), 0644); err != nil {
		t.Fatalf("failed to write pdf: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srcSvc := NewService(srcDB, srcStore, srcUploads, log)
	ctx := context.Background()

	// Seed v2 settings including EnableDebugLogs, InspirationSeedsApplied, LocateColorByStock, and ping interval settings.
	if err := srcStore.RestoreSettings(ctx, db.RestoreSettingsParams{
		RequireAuthForRead:            sql.NullBool{Bool: true, Valid: true},
		LocateTimeoutSeconds:          sql.NullInt64{Int64: 45, Valid: true},
		EnableLocateTimeout:           sql.NullBool{Bool: true, Valid: true},
		EnableDebugLogs:               sql.NullBool{Bool: true, Valid: true},
		ColorLocate:                   sql.NullString{String: "#112233", Valid: true},
		ColorStockOk:                  sql.NullString{String: "#00FF00", Valid: true},
		ColorStockLow:                 sql.NullString{String: "#FFAA00", Valid: true},
		ColorStockCritical:            sql.NullString{String: "#FF0000", Valid: true},
		InspirationSeedsApplied:       sql.NullBool{Bool: true, Valid: true},
		LocateColorByStock:            sql.NullBool{Bool: true, Valid: true},
		EnableControllerPing:          sql.NullBool{Bool: false, Valid: true},
		ControllerPingIntervalSeconds: sql.NullInt64{Int64: 120, Valid: true},
		CreatedAt:                     sql.NullTime{Time: time.Now().UTC(), Valid: true},
		UpdatedAt:                     sql.NullTime{Time: time.Now().UTC(), Valid: true},
	}); err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	user, err := srcStore.CreateUser(ctx, db.CreateUserParams{
		Email:        "owner@wledger.local",
		PasswordHash: "hashed",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	ctrl, err := srcStore.CreateController(ctx, db.CreateControllerParams{
		Name:      "Shelf WLED",
		IpAddress: "192.168.1.50",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}
	if err := srcStore.UpdateController(ctx, db.UpdateControllerParams{
		ID:           ctrl.ID,
		Name:         "Shelf WLED",
		IpAddress:    "192.168.1.50",
		Port:         sql.NullInt64{Int64: 80, Valid: true},
		UseUdp:       sql.NullBool{Bool: true, Valid: true},
		UdpPort:      sql.NullInt64{Int64: 22334, Valid: true},
		RestoreState: sql.NullBool{Bool: false, Valid: true},
		IdlePresetID: sql.NullInt64{Int64: 4, Valid: true},
	}); err != nil {
		t.Fatalf("failed to update controller v2 fields: %v", err)
	}
	if err := srcStore.UpdateControllerInfo(ctx, db.UpdateControllerInfoParams{
		ID:       ctrl.ID,
		IsOnline: sql.NullBool{Bool: true, Valid: true},
		LedCount: 60,
	}); err != nil {
		t.Fatalf("failed to update controller led_count: %v", err)
	}

	linkedContID, err := srcStore.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "LED Rack",
		ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
		SegmentID:    0,
		ConfigJson:   sql.NullString{String: `{"rows":2,"cols":2}`, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create linked container: %v", err)
	}

	standaloneContID, err := srcStore.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Drawer Without WLED",
		ControllerID: sql.NullInt64{Valid: false},
		SegmentID:    0,
		ConfigJson:   sql.NullString{String: `{"rows":1,"cols":1}`, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create standalone container: %v", err)
	}

	partID, err := srcStore.CreatePart(ctx, db.CreatePartParams{
		Name:       "ESP32-S3",
		PartNumber: sql.NullString{String: "ESP32S3-WROOM", Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create part: %v", err)
	}

	tmpl, err := srcStore.CreateInspirationTemplate(ctx, db.CreateInspirationTemplateParams{
		Title:           "Custom Synth Prompt",
		TemplateContent: "Build a MIDI synth with:",
	})
	if err != nil {
		t.Fatalf("failed to create inspiration template: %v", err)
	}

	if err := srcStore.SetFlag(ctx, db.SetFlagParams{Key: "hardware_migration_v2", Value: "complete"}); err != nil {
		t.Fatalf("failed to set system flag: %v", err)
	}

	proj, err := srcStore.CreateProject(ctx, db.CreateProjectParams{
		Name:        "LED Matrix Clock",
		Description: sql.NullString{String: "Desk clock", Valid: true},
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	_, err = srcStore.AddProjectBOMItem(ctx, db.AddProjectBOMItemParams{
		ProjectID:        proj.ID,
		PartID:           sql.NullInt64{Int64: partID, Valid: true},
		Designator:       sql.NullString{String: "U1", Valid: true},
		Mpn:              sql.NullString{String: "ESP32S3-WROOM", Valid: true},
		QuantityPerBoard: 2,
		Notes:            sql.NullString{String: "Main MCU", Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to add BOM item: %v", err)
	}

	if err := srcStore.CreateAuditLog(ctx, db.CreateAuditLogParams{
		UserID:     sql.NullInt64{Int64: user.ID, Valid: true},
		ActionType: "STOCK_INC",
		EntityType: "PART",
		EntityID:   partID,
		Details:    sql.NullString{String: "Incremented ESP32 stock", Valid: true},
		OldValue:   []byte(`{"quantity":1}`),
		NewValue:   []byte(`{"quantity":5}`),
	}); err != nil {
		t.Fatalf("failed to create audit log: %v", err)
	}

	// Export v2 backup
	var buf bytes.Buffer
	if err := srcSvc.Export(ctx, &buf); err != nil {
		t.Fatalf("v2 Export failed: %v", err)
	}

	// Restore into a separate DB that currently has stale sessions, tokens, and debug=false
	dstDB, dstStore, dstCleanup := setupTestDB(t)
	defer dstCleanup()
	dstUploads, dstFsCleanup := setupTestUploads(t)
	defer dstFsCleanup()

	logger.SetDebug(false)

	dstUser, err := dstStore.CreateUser(ctx, db.CreateUserParams{
		Email:        "stale@wledger.local",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to create dst stale user: %v", err)
	}
	if err := dstStore.CreateSession(ctx, db.CreateSessionParams{
		Token:  "stale-session-token",
		Data:   []byte("session-payload"),
		Expiry: float64(time.Now().Add(time.Hour).Unix()),
	}); err != nil {
		t.Fatalf("failed to seed stale session: %v", err)
	}
	if _, err := dstStore.CreateAPIToken(ctx, db.CreateAPITokenParams{
		UserID:      dstUser.ID,
		Name:        "stale-token",
		TokenHash:   "hash123",
		TokenPrefix: "wlg_",
		Scopes:      "read",
	}); err != nil {
		t.Fatalf("failed to seed stale api token: %v", err)
	}

	dstSvc := NewService(dstDB, dstStore, dstUploads, log)
	zipBytes := buf.Bytes()
	if err := dstSvc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("v2 Restore failed: %v", err)
	}

	// Verify runtime debug log level updated to true
	if logger.LogLevel.Level() != slog.LevelDebug {
		t.Errorf("expected runtime log level Debug after restore, got %v", logger.LogLevel.Level())
	}

	// Verify Settings fidelity
	gotSettings, err := dstStore.GetSettings(ctx)
	if err != nil {
		t.Fatalf("failed to fetch restored settings: %v", err)
	}
	if !gotSettings.EnableDebugLogs.Bool {
		t.Error("expected EnableDebugLogs=true after restore")
	}
	if !gotSettings.InspirationSeedsApplied.Bool {
		t.Error("expected InspirationSeedsApplied=true after restore")
	}
	if !gotSettings.LocateColorByStock.Bool {
		t.Error("expected LocateColorByStock=true after restore")
	}
	if !gotSettings.EnableControllerPing.Valid || gotSettings.EnableControllerPing.Bool {
		t.Errorf("expected EnableControllerPing=false after restore, got %+v", gotSettings.EnableControllerPing)
	}
	if !gotSettings.ControllerPingIntervalSeconds.Valid || gotSettings.ControllerPingIntervalSeconds.Int64 != 120 {
		t.Errorf("expected ControllerPingIntervalSeconds=120 after restore, got %+v", gotSettings.ControllerPingIntervalSeconds)
	}
	if gotSettings.ColorLocate.String != "#112233" {
		t.Errorf("expected ColorLocate=#112233, got %q", gotSettings.ColorLocate.String)
	}

	// Verify SeedTemplates does NOT insert duplicates after restore
	inspSvc := inspiration.NewService(dstStore)
	if err := inspSvc.SeedTemplates(ctx); err != nil {
		t.Fatalf("SeedTemplates after restore failed: %v", err)
	}
	templates, err := dstStore.GetAllInspirationTemplates(ctx)
	if err != nil {
		t.Fatalf("GetAllInspirationTemplates failed: %v", err)
	}
	if len(templates) != 1 || templates[0].ID != tmpl.ID || templates[0].Title != tmpl.Title {
		t.Errorf("expected only restored template (%q), got %+v (SeedTemplates duplicated defaults!)", tmpl.Title, templates)
	}

	// Verify SystemFlags
	flagVal, err := dstStore.GetFlag(ctx, "hardware_migration_v2")
	if err != nil || flagVal != "complete" {
		t.Errorf("expected restored system flag 'complete', got %q (err=%v)", flagVal, err)
	}

	// Verify Projects & BOM Items
	gotProjects, err := dstStore.GetAllProjects(ctx)
	if err != nil || len(gotProjects) != 1 || gotProjects[0].Name != "LED Matrix Clock" {
		t.Errorf("unexpected restored projects: %+v (err=%v)", gotProjects, err)
	}
	gotBOM, err := dstStore.GetAllProjectBOMItems(ctx)
	if err != nil || len(gotBOM) != 1 || gotBOM[0].QuantityPerBoard != 2 || !gotBOM[0].PartID.Valid || gotBOM[0].PartID.Int64 != partID {
		t.Errorf("unexpected restored BOM items: %+v (err=%v)", gotBOM, err)
	}

	// Verify WLED v2 controller columns
	gotCtrls, err := dstStore.GetControllers(ctx)
	if err != nil || len(gotCtrls) != 1 {
		t.Fatalf("expected 1 restored controller, got %+v (err=%v)", gotCtrls, err)
	}
	if !gotCtrls[0].UseUdp.Bool || gotCtrls[0].UdpPort.Int64 != 22334 || gotCtrls[0].RestoreState.Bool || gotCtrls[0].IdlePresetID.Int64 != 4 {
		t.Errorf("expected WLED v2 settings preserved, got %+v", gotCtrls[0])
	}

	// Verify nullable Container.ControllerID
	gotContainers, err := dstStore.GetAllContainers(ctx)
	if err != nil || len(gotContainers) != 2 {
		t.Fatalf("expected 2 restored containers, got %+v (err=%v)", gotContainers, err)
	}
	var foundLinked, foundStandalone bool
	for _, c := range gotContainers {
		if c.ID == linkedContID && c.ControllerID.Valid && c.ControllerID.Int64 == ctrl.ID {
			foundLinked = true
		}
		if c.ID == standaloneContID && !c.ControllerID.Valid {
			foundStandalone = true
		}
	}
	if !foundLinked || !foundStandalone {
		t.Errorf("containers did not preserve linked/standalone controller_id: %+v", gotContainers)
	}

	// Verify AuditLog JSON payload preservation
	gotLogs, err := dstStore.GetAllAuditLogs(ctx)
	if err != nil {
		t.Fatalf("GetAllAuditLogs failed: %v", err)
	}
	var foundStockInc bool
	for _, l := range gotLogs {
		if l.ActionType != "STOCK_INC" {
			continue
		}
		var oldMap, newMap map[string]any
		if err := json.Unmarshal(l.OldValue, &oldMap); err == nil {
			if err := json.Unmarshal(l.NewValue, &newMap); err == nil {
				if oldMap["quantity"] == float64(1) && newMap["quantity"] == float64(5) {
					foundStockInc = true
				}
			}
		}
	}
	if !foundStockInc {
		t.Errorf("expected restored STOCK_INC audit log with JSON old/new values, got %+v", gotLogs)
	}

	// Verify ClearSessions and ClearAPITokens wiped stale auth rows
	var sessionCount, tokenCount int
	if err := dstDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&sessionCount); err != nil {
		t.Fatalf("failed to count sessions: %v", err)
	}
	if sessionCount != 0 {
		t.Errorf("expected 0 sessions after restore, got %d", sessionCount)
	}
	if err := dstDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM api_tokens").Scan(&tokenCount); err != nil {
		t.Fatalf("failed to count api_tokens: %v", err)
	}
	if tokenCount != 0 {
		t.Errorf("expected 0 api_tokens after restore, got %d", tokenCount)
	}

	// Verify docs/datasheet.pdf restored
	pdfData, err := os.ReadFile(filepath.Join(dstUploads, "docs", "datasheet.pdf"))
	if err != nil || string(pdfData) != "%PDF-1.4 mock" {
		t.Errorf("expected restored datasheet.pdf, got %q (err=%v)", string(pdfData), err)
	}
}

func TestRestore_V1BackwardCompatibility(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, log)
	ctx := context.Background()

	// Pre-Wave-1 v1.0 JSON format: plain int controller_id, no v2 controller columns, no locate_color_by_stock.
	v1ManifestJSON := `{
		"version": "1.0",
		"exported_at": "2026-01-15T12:00:00Z",
		"settings": {
			"id": 1,
			"require_auth_for_read": {"Bool": false, "Valid": true},
			"locate_timeout_seconds": {"Int64": 60, "Valid": true},
			"enable_locate_timeout": {"Bool": true, "Valid": true},
			"enable_debug_logs": {"Bool": true, "Valid": true},
			"color_locate": {"String": "#0000FF", "Valid": true},
			"inspiration_seeds_applied": {"Bool": true, "Valid": true}
		},
		"users": [
			{"id": 1, "email": "v1admin@example.com", "password_hash": "hash", "role": "admin"}
		],
		"controllers": [
			{"id": 1, "name": "V1 Ctrl", "ip_address": "10.0.0.5", "port": {"Int64": 80, "Valid": true}, "led_count": 32}
		],
		"containers": [
			{"id": 1, "name": "V1 Grid", "controller_id": 1, "segment_id": 0, "position_index": 0}
		],
		"audit_logs": [
			{
				"id": 1,
				"user_id": {"Int64": 1, "Valid": true},
				"action_type": "UPDATE",
				"entity_type": "PART",
				"entity_id": 5,
				"details": {"String": "v1 edit", "Valid": true},
				"old_value": {"name": "Old"},
				"new_value": {"name": "New"}
			}
		]
	}`

	defer logger.SetDebug(false)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fJSON, _ := zw.Create("restore_data.json")
	_, _ = fJSON.Write([]byte(v1ManifestJSON))
	fUpload, _ := zw.Create("uploads/images/v1.png")
	_, _ = fUpload.Write([]byte("v1-png"))
	_ = zw.Close()

	zipBytes := buf.Bytes()
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("v1.0 backward-compatible Restore failed: %v", err)
	}

	gotUser, err := s.GetUserByEmail(ctx, "v1admin@example.com")
	if err != nil || gotUser.ID != 1 {
		t.Fatalf("failed to restore v1 user: %+v (err=%v)", gotUser, err)
	}

	gotContainers, err := s.GetAllContainers(ctx)
	if err != nil || len(gotContainers) != 1 || !gotContainers[0].ControllerID.Valid || gotContainers[0].ControllerID.Int64 != 1 {
		t.Errorf("expected v1 integer controller_id=1 converted to NullInt64{1,true}, got %+v (err=%v)", gotContainers, err)
	}

	gotCtrls, err := s.GetControllers(ctx)
	if err != nil || len(gotCtrls) != 1 {
		t.Fatalf("expected 1 controller, got %+v (err=%v)", gotCtrls, err)
	}
	if gotCtrls[0].UdpPort.Int64 != 21324 || !gotCtrls[0].RestoreState.Bool {
		t.Errorf("expected v2 defaults for v1 controller, got %+v", gotCtrls[0])
	}

	gotSettings, err := s.GetSettings(ctx)
	if err != nil || !gotSettings.EnableDebugLogs.Bool || !gotSettings.InspirationSeedsApplied.Bool || gotSettings.LocateColorByStock.Bool {
		t.Errorf("unexpected settings after v1 restore: %+v (err=%v)", gotSettings, err)
	}
	if !gotSettings.EnableControllerPing.Valid || !gotSettings.EnableControllerPing.Bool {
		t.Errorf("expected default EnableControllerPing=true after v1 restore, got %+v", gotSettings.EnableControllerPing)
	}
	if !gotSettings.ControllerPingIntervalSeconds.Valid || gotSettings.ControllerPingIntervalSeconds.Int64 != 600 {
		t.Errorf("expected default ControllerPingIntervalSeconds=600 after v1 restore, got %+v", gotSettings.ControllerPingIntervalSeconds)
	}

	gotLogs, err := s.GetAllAuditLogs(ctx)
	if err != nil || len(gotLogs) != 1 || !strings.Contains(string(gotLogs[0].OldValue), `"Old"`) {
		t.Errorf("unexpected audit logs after v1 restore: %+v (err=%v)", gotLogs, err)
	}
}

func makeTestZipWithEntries(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	manifest := Manifest{
		Version: ManifestVersionV2,
		Settings: db.Setting{
			CreatedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true},
			UpdatedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true},
		},
	}
	fJSON, err := zw.Create("restore_data.json")
	if err != nil {
		t.Fatalf("failed to create restore_data.json: %v", err)
	}
	if err := json.NewEncoder(fJSON).Encode(manifest); err != nil {
		t.Fatalf("failed to encode manifest: %v", err)
	}

	for name, content := range entries {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create entry %s: %v", name, err)
		}
		if _, err := f.Write(content); err != nil {
			t.Fatalf("failed to write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip: %v", err)
	}
	return buf.Bytes()
}

func TestRestore_ZipSlipSiblingPrefix(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("failed to init settings: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := newServiceWithLimits(database, s, uploadsDir, log, defaultMaxEntryBytes, defaultMaxTotalBytes)
	fixedTime := time.Unix(1700000000, 123456789)
	svc.now = func() time.Time { return fixedTime }

	// Craft exact sibling prefix of .restore_tmp_<timestamp> that bypassed strings.HasPrefix(cleanTarget, tempDir)
	siblingDirName := ".restore_tmp_1700000000123456789_evil"
	evilEntry := "uploads/../" + siblingDirName + "/pwn.html"

	for _, entryName := range []string{
		evilEntry,
		`uploads\..\outside.txt`,
		"uploads/..",
	} {
		zipBytes := makeTestZipWithEntries(t, map[string][]byte{
			entryName: []byte("pwned"),
		})
		err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
		if err == nil {
			t.Fatalf("expected security rejection for zip-slip entry %q, got nil", entryName)
		}
		if !strings.Contains(err.Error(), "security block") {
			t.Errorf("expected security block error for %q, got: %v", entryName, err)
		}
	}

	if _, err := os.Stat(filepath.Join(uploadsDir, siblingDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sibling evil directory %q should not exist", siblingDirName)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "outside.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Error("outside.txt should not exist")
	}
	// Live uploads and settings must remain intact
	if _, err := os.Stat(filepath.Join(uploadsDir, "test.txt")); err != nil {
		t.Errorf("expected live test.txt preserved after rejected restore: %v", err)
	}
	if _, err := s.GetSettings(ctx); err != nil {
		t.Errorf("expected live settings preserved: %v", err)
	}
}

func TestRestore_ZipBombLimits(t *testing.T) {
	t.Run("rejects >100MB entry using default NewService limits", func(t *testing.T) {
		database, s, dbCleanup := setupTestDB(t)
		defer dbCleanup()
		uploadsDir, fsCleanup := setupTestUploads(t)
		defer fsCleanup()

		ctx := context.Background()
		if err := s.InitSettings(ctx); err != nil {
			t.Fatalf("failed to init settings: %v", err)
		}

		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		svc := NewService(database, s, uploadsDir, log)

		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		manifest := Manifest{
			Version: ManifestVersionV2,
			Settings: db.Setting{
				CreatedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true},
				UpdatedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true},
			},
		}
		fJSON, _ := zw.Create("restore_data.json")
		_ = json.NewEncoder(fJSON).Encode(manifest)

		// Stream 101 MB of zeroes (compresses highly in deflate without high RAM usage)
		fBomb, err := zw.Create("uploads/bomb.bin")
		if err != nil {
			t.Fatalf("failed to create bomb entry: %v", err)
		}
		const bombSize = (100 << 20) + (1 << 20)
		zeroChunk := make([]byte, 1<<20)
		for written := 0; written < bombSize; written += len(zeroChunk) {
			if _, err := fBomb.Write(zeroChunk); err != nil {
				t.Fatalf("failed writing bomb chunk: %v", err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("failed closing zip: %v", err)
		}

		zipBytes := buf.Bytes()
		err = svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
		if err == nil {
			t.Fatal("expected Zip-Bomb error for >100MB entry, got nil")
		}
		if !strings.Contains(err.Error(), "security block") {
			t.Errorf("expected security block error, got: %v", err)
		}
		if _, err := os.Stat(filepath.Join(uploadsDir, "test.txt")); err != nil {
			t.Errorf("live uploads damaged after rejected zip bomb: %v", err)
		}
	})

	t.Run("rejects multi-entry cumulative size exceeding total limit and oversized manifest", func(t *testing.T) {
		database, s, dbCleanup := setupTestDB(t)
		defer dbCleanup()
		uploadsDir, fsCleanup := setupTestUploads(t)
		defer fsCleanup()

		ctx := context.Background()
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		// Two 900-byte files: manifest (~1.2KB) + a.txt (900B) fits under 2500B total,
		// while adding b.txt (900B) exceeds 2500B total limit even though each entry < 2000B.
		svc := newServiceWithLimits(database, s, uploadsDir, log, 2000, 2500)

		zipBytes := makeTestZipWithEntries(t, map[string][]byte{
			"uploads/a.txt": bytes.Repeat([]byte("a"), 900),
			"uploads/b.txt": bytes.Repeat([]byte("b"), 900),
		})
		err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
		if err == nil || !strings.Contains(err.Error(), "maximum total size") {
			t.Errorf("expected maximum total size error, got: %v", err)
		}

		// Oversized restore_data.json exceeding maxEntryBytes
		svcTiny := newServiceWithLimits(database, s, uploadsDir, log, 64, 512)
		err = svcTiny.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
		if err == nil || !strings.Contains(err.Error(), "security block") {
			t.Errorf("expected security block error for oversized manifest, got: %v", err)
		}
	})
}

func TestRestore_DisallowedDocExtensions(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("failed to init settings: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, log)

	for _, badDoc := range []string{
		"uploads/docs/payload.html",
		"uploads/docs/nested/xss.SVG",
		"uploads/docs/page.htm",
		"uploads/docs/script.js",
	} {
		zipBytes := makeTestZipWithEntries(t, map[string][]byte{
			badDoc: []byte("<script>alert(1)</script>"),
		})
		err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
		if err == nil || !strings.Contains(err.Error(), "disallowed document extension") {
			t.Errorf("expected disallowed extension error for %q, got: %v", badDoc, err)
		}
	}

	// Safe document extension inside uploads/docs/ must succeed
	okZip := makeTestZipWithEntries(t, map[string][]byte{
		"uploads/docs/manual.pdf": []byte("%PDF-1.4"),
	})
	if err := svc.Restore(ctx, bytes.NewReader(okZip), int64(len(okZip))); err != nil {
		t.Fatalf("expected valid .pdf doc to restore cleanly, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "docs", "manual.pdf")); err != nil {
		t.Errorf("expected manual.pdf restored: %v", err)
	}
}

func TestExport_ErrorPropagation(t *testing.T) {
	t.Run("returns DB error when database is closed", func(t *testing.T) {
		database, s, dbCleanup := setupTestDB(t)
		uploadsDir, fsCleanup := setupTestUploads(t)
		defer fsCleanup()
		dbCleanup() // close DB before Export

		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		svc := NewService(database, s, uploadsDir, log)

		var buf bytes.Buffer
		if err := svc.Export(context.Background(), &buf); err == nil {
			t.Fatal("expected Export to fail on closed database, got nil")
		}
	})

	t.Run("returns filepath.Walk error when upload subdirectory is unreadable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("chmod 0000 remains readable by root")
		}

		database, s, dbCleanup := setupTestDB(t)
		defer dbCleanup()
		uploadsDir, fsCleanup := setupTestUploads(t)
		defer fsCleanup()

		unreadableDir := filepath.Join(uploadsDir, "locked")
		if err := os.MkdirAll(unreadableDir, 0755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
		if err := os.Chmod(unreadableDir, 0000); err != nil {
			t.Fatalf("chmod failed: %v", err)
		}
		defer os.Chmod(unreadableDir, 0755)

		ctx := context.Background()
		if err := s.InitSettings(ctx); err != nil {
			t.Fatalf("init settings failed: %v", err)
		}

		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		svc := NewService(database, s, uploadsDir, log)

		var buf bytes.Buffer
		if err := svc.Export(ctx, &buf); err == nil {
			t.Fatal("expected Export to propagate filepath.Walk permission error, got nil")
		}
	})
}
