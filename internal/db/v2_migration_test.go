package db_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/sql/schema"
)

func openMigratedFileStore(t *testing.T) (*sql.DB, db.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Migrate(conn); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	return conn, db.NewStore(conn)
}

func TestMigration007_NullableContainersAndOrphanDeduplication(t *testing.T) {
	// Test on a real file-backed WAL database to ensure -- +goose NO TRANSACTION
	// with PRAGMA foreign_keys = OFF and explicit BEGIN TRANSACTION preserves FKs.
	dbPath := filepath.Join(t.TempDir(), "mig007.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer conn.Close()

	goose.SetBaseFS(schema.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("SetDialect failed: %v", err)
	}
	if err := goose.UpTo(conn, ".", 6); err != nil {
		t.Fatalf("UpTo 6 failed: %v", err)
	}

	// Seed pre-007 data: controller, container, bins, wall, wall_card, part, and
	// duplicate (part_id, NULL) rows in part_assignments.
	_, err = conn.Exec(`
		INSERT INTO controllers (id, name, ip_address, led_count) VALUES (1, 'Ctrl1', '10.0.0.10', 64);
		INSERT INTO containers (id, name, controller_id, segment_id, position_index) VALUES (10, 'Cont1', 1, 0, 1);
		INSERT INTO bins (id, name, container_id, led_index) VALUES (100, 'A1', 10, 0), (101, 'A2', 10, 1);
		INSERT INTO walls (id, name) VALUES (20, 'Main Wall');
		INSERT INTO wall_cards (id, wall_id, container_id, position_index) VALUES (200, 20, 10, 0);
		INSERT INTO parts (id, name) VALUES (30, '10k Resistor');
		INSERT INTO part_assignments (part_id, bin_id, quantity) VALUES
			(30, 100, 12),
			(30, NULL, 4),
			(30, NULL, 7);
	`)
	if err != nil {
		t.Fatalf("seed pre-007 data failed: %v", err)
	}

	// Run 007 migration
	if err := goose.Up(conn, "."); err != nil {
		t.Fatalf("Up to latest failed: %v", err)
	}

	// Verify containers, bins, and wall_cards were all preserved cleanly.
	var contCount, binCount, cardCount int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM containers`).Scan(&contCount); err != nil || contCount != 1 {
		t.Fatalf("expected 1 container preserved, got %d (err=%v)", contCount, err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM bins`).Scan(&binCount); err != nil || binCount != 2 {
		t.Fatalf("expected 2 bins preserved, got %d (err=%v)", binCount, err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM wall_cards`).Scan(&cardCount); err != nil || cardCount != 1 {
		t.Fatalf("expected 1 wall_card preserved, got %d (err=%v)", cardCount, err)
	}

	// Verify duplicate orphaned stock rows coalesced to 1 row with qty = 11.
	var orphanRows, orphanQty int
	if err := conn.QueryRow(`SELECT COUNT(*), COALESCE(SUM(quantity), 0) FROM part_assignments WHERE part_id = 30 AND bin_id IS NULL`).Scan(&orphanRows, &orphanQty); err != nil {
		t.Fatalf("query orphaned rows failed: %v", err)
	}
	if orphanRows != 1 || orphanQty != 11 {
		t.Fatalf("expected 1 coalesced orphaned row with qty 11, got rows=%d qty=%d", orphanRows, orphanQty)
	}

	// Partial unique index must reject inserting a second (part_id, NULL) row.
	if _, err := conn.Exec(`INSERT INTO part_assignments (part_id, bin_id, quantity) VALUES (30, NULL, 2)`); err == nil {
		t.Fatalf("expected unique constraint failure inserting duplicate orphaned assignment")
	}

	// Deleting controller must orphan container without deleting bins or wall_cards.
	store := db.NewStore(conn)
	ctx := context.Background()
	if err := store.DeleteController(ctx, 1); err != nil {
		t.Fatalf("DeleteController failed: %v", err)
	}

	standalone, err := store.GetStandaloneContainers(ctx)
	if err != nil {
		t.Fatalf("GetStandaloneContainers failed: %v", err)
	}
	if len(standalone) != 1 || standalone[0].ID != 10 || standalone[0].ControllerID.Valid {
		t.Fatalf("expected orphaned container 10, got %+v", standalone)
	}

	allWithCtrl, err := store.GetAllContainersWithController(ctx)
	if err != nil || len(allWithCtrl) != 1 || allWithCtrl[0].ControllerName.Valid {
		t.Fatalf("expected 1 container with NULL controller info, got %+v (err=%v)", allWithCtrl, err)
	}

	if err := conn.QueryRow(`SELECT COUNT(*) FROM bins`).Scan(&binCount); err != nil || binCount != 2 {
		t.Fatalf("expected 2 bins still intact after controller delete, got %d (err=%v)", binCount, err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM wall_cards`).Scan(&cardCount); err != nil || cardCount != 1 {
		t.Fatalf("expected 1 wall_card still intact after controller delete, got %d (err=%v)", cardCount, err)
	}

	// Reattach standalone container to a new controller via UpdateContainerController.
	ctrl2, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl2", IpAddress: "10.0.0.20"})
	if err != nil {
		t.Fatalf("CreateController failed: %v", err)
	}
	if err := store.UpdateContainerController(ctx, db.UpdateContainerControllerParams{
		ControllerID: sql.NullInt64{Int64: ctrl2.ID, Valid: true},
		ID:           10,
	}); err != nil {
		t.Fatalf("UpdateContainerController failed: %v", err)
	}
	c10, err := store.GetContainer(ctx, 10)
	if err != nil || !c10.ControllerID.Valid || c10.ControllerID.Int64 != ctrl2.ID {
		t.Fatalf("expected container 10 attached to controller %d, got %+v (err=%v)", ctrl2.ID, c10, err)
	}
}

func TestV2_DeleteTwoBinsMergesOrphanedStock(t *testing.T) {
	conn, store := openMigratedFileStore(t)
	ctx := context.Background()

	// Create standalone container (controller_id = NULL) with two bins.
	contID, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "StandaloneBox",
		ControllerID: sql.NullInt64{Valid: false},
		SegmentID:    0,
	})
	if err != nil {
		t.Fatalf("CreateContainer standalone failed: %v", err)
	}

	bin1, err := store.CreateBin(ctx, db.CreateBinParams{Name: "B1", ContainerID: contID, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
	if err != nil {
		t.Fatalf("CreateBin 1 failed: %v", err)
	}
	bin2, err := store.CreateBin(ctx, db.CreateBinParams{Name: "B2", ContainerID: contID, LedIndex: sql.NullInt64{Int64: 1, Valid: true}})
	if err != nil {
		t.Fatalf("CreateBin 2 failed: %v", err)
	}

	partID, err := store.CreatePart(ctx, db.CreatePartParams{Name: "ESP32-S3"})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Int64: bin1, Valid: true}, Quantity: 5,
	}); err != nil {
		t.Fatalf("assign bin1 failed: %v", err)
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Int64: bin2, Valid: true}, Quantity: 8,
	}); err != nil {
		t.Fatalf("assign bin2 failed: %v", err)
	}

	// Sequential deletion of bin1 then bin2 must coalesce to a single orphaned row with qty=13.
	if err := store.DeleteBinByLed(ctx, db.DeleteBinByLedParams{ContainerID: contID, LedIndex: sql.NullInt64{Int64: 0, Valid: true}}); err != nil {
		t.Fatalf("delete bin1 failed: %v", err)
	}
	if err := store.DeleteBinByLed(ctx, db.DeleteBinByLedParams{ContainerID: contID, LedIndex: sql.NullInt64{Int64: 1, Valid: true}}); err != nil {
		t.Fatalf("delete bin2 failed: %v", err)
	}

	assignments, err := store.GetPartAssignments(ctx, partID)
	if err != nil {
		t.Fatalf("GetPartAssignments failed: %v", err)
	}
	if len(assignments) != 1 || assignments[0].BinID.Valid || assignments[0].Quantity != 13 {
		t.Fatalf("expected 1 orphaned assignment with qty 13, got %+v", assignments)
	}

	// Also verify multi-bin cascade delete (DeleteContainer when two bins hold the same part) coalesces cleanly.
	bin3, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B3", ContainerID: contID, LedIndex: sql.NullInt64{Int64: 2, Valid: true}})
	bin4, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B4", ContainerID: contID, LedIndex: sql.NullInt64{Int64: 3, Valid: true}})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: bin3, Valid: true}, Quantity: 4})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: bin4, Valid: true}, Quantity: 3})

	if err := store.DeleteContainer(ctx, contID); err != nil {
		t.Fatalf("DeleteContainer cascade coalesce failed: %v", err)
	}

	var totalQty, rowCount int
	if err := conn.QueryRow(`SELECT COUNT(*), COALESCE(SUM(quantity), 0) FROM part_assignments WHERE part_id = ? AND bin_id IS NULL`, partID).Scan(&rowCount, &totalQty); err != nil {
		t.Fatalf("query orphaned assignments failed: %v", err)
	}
	if rowCount != 1 || totalQty != 20 {
		t.Fatalf("expected 1 orphaned row with qty 20 after container delete, got rows=%d qty=%d", rowCount, totalQty)
	}
}

func TestV2_UsersRoleValidationTriggers(t *testing.T) {
	conn, store := openMigratedFileStore(t)
	ctx := context.Background()

	for _, role := range []string{"admin", "editor", "viewer"} {
		if _, err := store.CreateUser(ctx, db.CreateUserParams{
			Email:        role + "@example.com",
			PasswordHash: "hash",
			Role:         role,
		}); err != nil {
			t.Fatalf("valid role %q rejected: %v", role, err)
		}
	}

	_, err := store.CreateUser(ctx, db.CreateUserParams{
		Email:        "bad@example.com",
		PasswordHash: "hash",
		Role:         "superadmin",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid user role") {
		t.Fatalf("expected invalid user role on INSERT, got: %v", err)
	}

	_, err = conn.Exec(`UPDATE users SET role = 'hacker' WHERE email = 'viewer@example.com'`)
	if err == nil || !strings.Contains(err.Error(), "invalid user role") {
		t.Fatalf("expected invalid user role on UPDATE, got: %v", err)
	}
}

func TestV2_ListPartsAndSearchParts_AccurateTotalsAndFTS5(t *testing.T) {
	_, store := openMigratedFileStore(t)
	ctx := context.Background()

	ctrlA, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "CtrlA", IpAddress: "10.0.0.1"})
	ctrlB, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "CtrlB", IpAddress: "10.0.0.2"})
	contA, _ := store.CreateContainer(ctx, db.CreateContainerParams{Name: "ContA", ControllerID: sql.NullInt64{Int64: ctrlA.ID, Valid: true}})
	contB, _ := store.CreateContainer(ctx, db.CreateContainerParams{Name: "ContB", ControllerID: sql.NullInt64{Int64: ctrlB.ID, Valid: true}})

	binA, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: contA, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
	binB, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B1", ContainerID: contB, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})

	p1, err := store.CreatePart(ctx, db.CreatePartParams{
		Name:         "Yageo 10k Resistor",
		PartNumber:   sql.NullString{String: "RC0805FR-0710KL", Valid: true},
		Manufacturer: sql.NullString{String: "Yageo", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}
	p2, _ := store.CreatePart(ctx, db.CreatePartParams{
		Name: "Murata 100nF Capacitor",
	})

	// P1 has 10 in BinA, 20 in BinB, and 5 orphaned -> total=35, valid=30, orphaned=5.
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p1, BinID: sql.NullInt64{Int64: binA, Valid: true}, Quantity: 10})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p1, BinID: sql.NullInt64{Int64: binB, Valid: true}, Quantity: 20})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p1, BinID: sql.NullInt64{Valid: false}, Quantity: 5})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p2, BinID: sql.NullInt64{Int64: binB, Valid: true}, Quantity: 50})

	// ListParts filtered by BinA must return only P1, while keeping global totals (35, 30, 5).
	rowsBinA, err := store.ListParts(ctx, db.ListPartsParams{
		BinID:  sql.NullInt64{Int64: binA, Valid: true},
		Limit:  20,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListParts(BinID=binA) failed: %v", err)
	}
	if len(rowsBinA) != 1 || rowsBinA[0].ID != p1 {
		t.Fatalf("expected only P1 in BinA, got %+v", rowsBinA)
	}
	if rowsBinA[0].TotalStock != 35 || rowsBinA[0].ValidStock != 30 || rowsBinA[0].OrphanedStock != 5 {
		t.Fatalf("expected totals (35, 30, 5), got (%d, %d, %d)", rowsBinA[0].TotalStock, rowsBinA[0].ValidStock, rowsBinA[0].OrphanedStock)
	}

	// ListParts filtered by ControllerID = ctrlA must also return P1 with global totals (35, 30, 5).
	rowsCtrlA, err := store.ListParts(ctx, db.ListPartsParams{
		ControllerID: sql.NullInt64{Int64: ctrlA.ID, Valid: true},
		Limit:        20,
		Offset:       0,
	})
	if err != nil {
		t.Fatalf("ListParts(ControllerID=ctrlA) failed: %v", err)
	}
	if len(rowsCtrlA) != 1 || rowsCtrlA[0].ID != p1 || rowsCtrlA[0].TotalStock != 35 || rowsCtrlA[0].OrphanedStock != 5 {
		t.Fatalf("expected P1 with totals (35, 5), got %+v", rowsCtrlA)
	}

	// Pure-Go FTS5 SearchParts with BinID filter and tag-trigger sync check.
	tagRow, err := store.CreateTag(ctx, "precision")
	if err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}
	if err := store.AddTagToPart(ctx, db.AddTagToPartParams{PartID: p1, TagID: tagRow.ID}); err != nil {
		t.Fatalf("AddTagToPart failed: %v", err)
	}

	searchRows, err := store.SearchParts(ctx, db.SearchPartsParams{
		Query:  sql.NullString{String: "precis*", Valid: true},
		BinID:  sql.NullInt64{Int64: binA, Valid: true},
		Limit:  20,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("SearchParts pure-Go FTS5 failed: %v", err)
	}
	if len(searchRows) != 1 || searchRows[0].ID != p1 {
		t.Fatalf("expected FTS5 tag match for P1, got %+v", searchRows)
	}
	if searchRows[0].TotalStock != 35 || searchRows[0].ValidStock != 30 || searchRows[0].OrphanedStock != 5 {
		t.Fatalf("expected SearchParts totals (35, 30, 5), got (%d, %d, %d)", searchRows[0].TotalStock, searchRows[0].ValidStock, searchRows[0].OrphanedStock)
	}

	// Test UpdatePartAssignmentQuantityByID.
	assigns, err := store.GetPartAssignments(ctx, p1)
	if err != nil || len(assigns) == 0 {
		t.Fatalf("GetPartAssignments failed: %v", err)
	}
	var binAAssignID int64
	for _, a := range assigns {
		if a.BinID.Valid && a.BinID.Int64 == binA {
			binAAssignID = a.ID
		}
	}
	if err := store.UpdatePartAssignmentQuantityByID(ctx, db.UpdatePartAssignmentQuantityByIDParams{
		Quantity: 15,
		ID:       binAAssignID,
	}); err != nil {
		t.Fatalf("UpdatePartAssignmentQuantityByID failed: %v", err)
	}
	updatedAssign, err := store.GetAssignment(ctx, binAAssignID)
	if err != nil || updatedAssign.Quantity != 15 {
		t.Fatalf("expected quantity 15, got %+v (err=%v)", updatedAssign, err)
	}
}

func TestV2_GetPartsForInspirationFiltered_ThreeMatchingTagsNoMultiplication(t *testing.T) {
	_, store := openMigratedFileStore(t)
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "10.0.0.1"})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{Name: "Box", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}})
	b1, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
	b2, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B2", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 1, Valid: true}})

	p1, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "ESP32-C3"})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p1, BinID: sql.NullInt64{Int64: b1, Valid: true}, Quantity: 12})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p1, BinID: sql.NullInt64{Int64: b2, Valid: true}, Quantity: 8})

	tags := []string{"mcu", "wifi", "ble"}
	for _, name := range tags {
		tagRow, err := store.CreateTag(ctx, name)
		if err != nil {
			t.Fatalf("CreateTag(%q) failed: %v", name, err)
		}
		if err := store.AddTagToPart(ctx, db.AddTagToPartParams{PartID: p1, TagID: tagRow.ID}); err != nil {
			t.Fatalf("AddTagToPart failed: %v", err)
		}
	}

	rows, err := store.GetPartsForInspirationFiltered(ctx, tags)
	if err != nil {
		t.Fatalf("GetPartsForInspirationFiltered failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 deduplicated part row, got %d (%+v)", len(rows), rows)
	}
	if rows[0].TotalQuantity != 20 {
		t.Fatalf("expected exact unmultiplied stock 20 (12+8), got %d", rows[0].TotalQuantity)
	}
}

func TestV2_ControllersSettingsUsersFlagsAndBackupQueries(t *testing.T) {
	_, store := openMigratedFileStore(t)
	ctx := context.Background()

	// Controllers v2 queries: UpdateControllerStatus must preserve led_count.
	created, err := store.CreateController(ctx, db.CreateControllerParams{
		Name: "Bench WLED", IpAddress: "192.168.1.50", Port: sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateController failed: %v", err)
	}
	if err := store.UpdateControllerInfo(ctx, db.UpdateControllerInfoParams{
		IsOnline:   sql.NullBool{Bool: true, Valid: true},
		LedCount:   144,
		MacAddress: sql.NullString{String: "AA:BB:CC:DD:EE:FF", Valid: true},
		ID:         created.ID,
	}); err != nil {
		t.Fatalf("UpdateControllerInfo failed: %v", err)
	}
	if err := store.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{
		IsOnline: sql.NullBool{Bool: false, Valid: true},
		ID:       created.ID,
	}); err != nil {
		t.Fatalf("UpdateControllerStatus failed: %v", err)
	}

	ctrl, err := store.GetControllerByMac(ctx, sql.NullString{String: "AA:BB:CC:DD:EE:FF", Valid: true})
	if err != nil {
		t.Fatalf("GetControllerByMac failed: %v", err)
	}
	if ctrl.IsOnline.Bool || ctrl.LedCount != 144 {
		t.Fatalf("expected offline with preserved led_count=144, got online=%v led_count=%d", ctrl.IsOnline.Bool, ctrl.LedCount)
	}

	if err := store.UpdateController(ctx, db.UpdateControllerParams{
		Name:         "Bench WLED v2",
		IpAddress:    "192.168.1.51",
		Port:         sql.NullInt64{Int64: 8080, Valid: true},
		UseUdp:       sql.NullBool{Bool: true, Valid: true},
		UdpPort:      sql.NullInt64{Int64: 21324, Valid: true},
		RestoreState: sql.NullBool{Bool: false, Valid: true},
		IdlePresetID: sql.NullInt64{Int64: 3, Valid: true},
		ID:           created.ID,
	}); err != nil {
		t.Fatalf("UpdateController failed: %v", err)
	}
	ctrl, _ = store.GetController(ctx, created.ID)
	if !ctrl.UseUdp.Bool || ctrl.UdpPort.Int64 != 21324 || ctrl.RestoreState.Bool || ctrl.IdlePresetID.Int64 != 3 {
		t.Fatalf("unexpected v2 controller fields: %+v", ctrl)
	}

	// Settings v2 fields.
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("InitSettings failed: %v", err)
	}
	if err := store.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
		LocateColorByStock: sql.NullBool{Bool: true, Valid: true},
	}); err != nil {
		t.Fatalf("UpdateGeneralSettings failed: %v", err)
	}
	sett, err := store.GetSettings(ctx)
	if err != nil || !sett.LocateColorByStock.Bool {
		t.Fatalf("expected LocateColorByStock=true, got %+v (err=%v)", sett, err)
	}

	// Inspiration template backup/restore & system flags & sessions clear.
	if _, err := store.CreateInspirationTemplate(ctx, db.CreateInspirationTemplateParams{
		Title: "T1", TemplateContent: "Build something",
	}); err != nil {
		t.Fatalf("CreateInspirationTemplate failed: %v", err)
	}
	if err := store.ClearInspirationTemplates(ctx); err != nil {
		t.Fatalf("ClearInspirationTemplates failed: %v", err)
	}
	if err := store.RestoreInspirationTemplate(ctx, db.RestoreInspirationTemplateParams{
		ID: 42, Title: "Restored", TemplateContent: "Content", CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
	}); err != nil {
		t.Fatalf("RestoreInspirationTemplate failed: %v", err)
	}
	tpls, _ := store.GetAllInspirationTemplates(ctx)
	if len(tpls) != 1 || tpls[0].ID != 42 {
		t.Fatalf("unexpected inspiration templates after restore: %+v", tpls)
	}

	_ = store.SetFlag(ctx, db.SetFlagParams{Key: "k1", Value: "v1"})
	flags, err := store.GetAllSystemFlags(ctx)
	if err != nil || len(flags) != 1 || flags[0].Key != "k1" {
		t.Fatalf("GetAllSystemFlags failed: %+v (err=%v)", flags, err)
	}
	_ = store.ClearSystemFlags(ctx)
	_ = store.RestoreSystemFlag(ctx, db.RestoreSystemFlagParams{Key: "k2", Value: "v2"})
	flags, _ = store.GetAllSystemFlags(ctx)
	if len(flags) != 1 || flags[0].Key != "k2" {
		t.Fatalf("unexpected flags after restore: %+v", flags)
	}

	_ = store.CreateSession(ctx, db.CreateSessionParams{Token: "tok1", Data: []byte("abc"), Expiry: 9999999999})
	if err := store.ClearSessions(ctx); err != nil {
		t.Fatalf("ClearSessions failed: %v", err)
	}
	if _, err := store.GetSession(ctx, "tok1"); err == nil {
		t.Fatalf("expected session tok1 to be cleared")
	}
}

func TestV2_APITokensAndProjectsBOMShortages(t *testing.T) {
	_, store := openMigratedFileStore(t)
	ctx := context.Background()

	// API Tokens CRUD + auth lookup.
	user, err := store.CreateUser(ctx, db.CreateUserParams{
		Email: "api@example.com", PasswordHash: "hash", Role: "editor",
	})
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	tok, err := store.CreateAPIToken(ctx, db.CreateAPITokenParams{
		UserID:      user.ID,
		Name:        "CI Scanner",
		TokenHash:   "sha256-deadbeef",
		TokenPrefix: "wld_dead",
		Scopes:      "read,write",
	})
	if err != nil {
		t.Fatalf("CreateAPIToken failed: %v", err)
	}
	withUser, err := store.GetAPITokenWithUserByHash(ctx, "sha256-deadbeef")
	if err != nil || withUser.UserRole != "editor" || withUser.UserEmail != "api@example.com" {
		t.Fatalf("GetAPITokenWithUserByHash failed: %+v (err=%v)", withUser, err)
	}
	_ = store.UpdateAPITokenLastUsed(ctx, tok.ID)
	listed, err := store.ListAPITokensByUser(ctx, user.ID)
	if err != nil || len(listed) != 1 || !listed[0].LastUsedAt.Valid {
		t.Fatalf("ListAPITokensByUser failed: %+v (err=%v)", listed, err)
	}
	if err := store.DeleteAPITokenForUser(ctx, db.DeleteAPITokenForUserParams{ID: tok.ID, UserID: user.ID}); err != nil {
		t.Fatalf("DeleteAPITokenForUser failed: %v", err)
	}

	// Projects & BOM shortage analysis.
	r10k, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "10k 0805", PartNumber: sql.NullString{String: "R10K", Valid: true}})
	c100n, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "100nF 0805", PartNumber: sql.NullString{String: "C100N", Valid: true}})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: r10k, Quantity: 15})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: c100n, Quantity: 50})

	proj, err := store.CreateProject(ctx, db.CreateProjectParams{
		Name:        "LED Matrix Controller",
		Description: sql.NullString{String: "Rev B", Valid: true},
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	// Two BOM lines referencing the same part R10K (2 + 3 = 5 per board), one line for C100N (1/board), one unlinked part (1/board).
	_, err = store.AddProjectBOMItem(ctx, db.AddProjectBOMItemParams{
		ProjectID: proj.ID, PartID: sql.NullInt64{Int64: r10k, Valid: true},
		Designator: sql.NullString{String: "R1,R2", Valid: true}, QuantityPerBoard: 2,
	})
	if err != nil {
		t.Fatalf("AddProjectBOMItem 1 failed: %v", err)
	}
	_, _ = store.AddProjectBOMItem(ctx, db.AddProjectBOMItemParams{
		ProjectID: proj.ID, PartID: sql.NullInt64{Int64: r10k, Valid: true},
		Designator: sql.NullString{String: "R3-R5", Valid: true}, QuantityPerBoard: 3,
	})
	_, _ = store.AddProjectBOMItem(ctx, db.AddProjectBOMItemParams{
		ProjectID: proj.ID, PartID: sql.NullInt64{Int64: c100n, Valid: true},
		Designator: sql.NullString{String: "C1", Valid: true}, QuantityPerBoard: 1,
	})
	_, _ = store.AddProjectBOMItem(ctx, db.AddProjectBOMItemParams{
		ProjectID: proj.ID, Mpn: sql.NullString{String: "ESP32-S3-WROOM", Valid: true},
		Designator: sql.NullString{String: "U1", Valid: true}, QuantityPerBoard: 1,
	})

	// For 5 boards:
	// - R10K: need 25, have 15 -> shortage 10
	// - ESP32-S3-WROOM (unlinked): need 5, have 0 -> shortage 5
	// - C100N: need 5, have 50 -> no shortage
	shortages, err := store.GetProjectShortages(ctx, db.GetProjectShortagesParams{
		ProjectID:  proj.ID,
		BoardCount: 5,
	})
	if err != nil {
		t.Fatalf("GetProjectShortages failed: %v", err)
	}
	if len(shortages) != 2 {
		t.Fatalf("expected 2 shortage groups, got %d (%+v)", len(shortages), shortages)
	}
	if shortages[0].ShortageQuantity != 10 || shortages[0].RequiredQuantity != 25 || shortages[0].AvailableStock != 15 {
		t.Fatalf("unexpected first shortage (R10K): %+v", shortages[0])
	}
	if shortages[1].ShortageQuantity != 5 || shortages[1].RequiredQuantity != 5 || shortages[1].AvailableStock != 0 {
		t.Fatalf("unexpected second shortage (unlinked ESP32): %+v", shortages[1])
	}
}

func TestMigration008_ControllerPingSettings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mig008.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer conn.Close()

	goose.SetBaseFS(schema.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("SetDialect failed: %v", err)
	}
	if err := goose.UpTo(conn, ".", 7); err != nil {
		t.Fatalf("UpTo 7 failed: %v", err)
	}

	// Seed pre-008 settings row at version 7.
	if _, err := conn.Exec(`INSERT INTO settings (id, require_auth_for_read) VALUES (1, 1)`); err != nil {
		t.Fatalf("seed pre-008 settings failed: %v", err)
	}

	if err := goose.Up(conn, "."); err != nil {
		t.Fatalf("Up to 008 failed: %v", err)
	}

	store := db.NewStore(conn)
	ctx := context.Background()
	sett, err := store.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings after 008 failed: %v", err)
	}
	if !sett.EnableControllerPing.Valid || !sett.EnableControllerPing.Bool {
		t.Fatalf("expected EnableControllerPing=true by default, got %+v", sett.EnableControllerPing)
	}
	if !sett.ControllerPingIntervalSeconds.Valid || sett.ControllerPingIntervalSeconds.Int64 != 600 {
		t.Fatalf("expected ControllerPingIntervalSeconds=600 by default, got %+v", sett.ControllerPingIntervalSeconds)
	}

	if err := store.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
		EnableControllerPing:          sql.NullBool{Bool: false, Valid: true},
		ControllerPingIntervalSeconds: sql.NullInt64{Int64: 180, Valid: true},
	}); err != nil {
		t.Fatalf("UpdateGeneralSettings failed: %v", err)
	}

	sett, err = store.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings after update failed: %v", err)
	}
	if sett.EnableControllerPing.Bool || sett.ControllerPingIntervalSeconds.Int64 != 180 {
		t.Fatalf("expected updated ping settings (false, 180), got %+v", sett)
	}
}
