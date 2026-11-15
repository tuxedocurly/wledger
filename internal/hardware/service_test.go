package hardware

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func setupTest(t *testing.T) (Service, db.Store, *sql.DB) {
	dbConn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	store := db.NewStore(dbConn)
	wClient := wled.NewClient()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	return NewService(store, wClient, logger), store, dbConn
}

func TestService_ListControllers(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	_, err := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "Test",
		IpAddress: "1.1.1.1",
	})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	list, err := s.ListControllers(ctx)
	if err != nil {
		t.Errorf("failed to list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 controller, got %d", len(list))
	}
}

func TestService_DeleteController(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	c, _ := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "Test",
		IpAddress: "1.1.1.1",
	})

	// Create Container
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Default",
		ControllerID: sql.NullInt64{Int64: c.ID, Valid: true},
		SegmentID:    0,
	})

	// Add a bin
	store.CreateBin(ctx, db.CreateBinParams{
		Name:        "Bin 1",
		ContainerID: cont,
	})

	err := s.DeleteController(ctx, c.ID)
	if err != nil {
		t.Errorf("failed to delete: %v", err)
	}

	// Verify container is orphaned and bin is preserved (v2 ON DELETE SET NULL)
	contRow, err := store.GetContainer(ctx, cont)
	if err != nil {
		t.Fatalf("expected container to remain orphaned: %v", err)
	}
	if contRow.ControllerID.Valid {
		t.Errorf("expected container controller_id to be NULL, got %d", contRow.ControllerID.Int64)
	}

	var count int
	dbConn.QueryRow("SELECT count(*) FROM bins").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 preserved bin, got %d", count)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func setupIsolatedTest(t *testing.T, dbName string, rt roundTripFunc) (Service, db.Store, *sql.DB) {
	t.Helper()
	dbConn, err := db.Open("file:" + dbName + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}
	store := db.NewStore(dbConn)
	var wClient *wled.Client
	if rt != nil {
		wClient = wled.NewClientWithHTTPClient(&http.Client{Transport: rt})
	} else {
		wClient = wled.NewClient()
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(store, wClient, logger), store, dbConn
}

func TestService_EndpointValidation(t *testing.T) {
	svc, _, _ := setupIsolatedTest(t, "hw_endpoint_val", nil)
	ctx := context.Background()

	invalidCases := []struct {
		name string
		ip   string
		port sql.NullInt64
	}{
		{"scheme http", "http://192.168.1.10", sql.NullInt64{Int64: 80, Valid: true}},
		{"scheme https", "https://192.168.1.10", sql.NullInt64{Int64: 80, Valid: true}},
		{"path slash", "192.168.1.10/admin", sql.NullInt64{Int64: 80, Valid: true}},
		{"query", "192.168.1.10?x=1", sql.NullInt64{Int64: 80, Valid: true}},
		{"fragment", "192.168.1.10#hash", sql.NullInt64{Int64: 80, Valid: true}},
		{"userinfo", "admin@192.168.1.10", sql.NullInt64{Int64: 80, Valid: true}},
		{"whitespace", "192.168.1.10 bad", sql.NullInt64{Int64: 80, Valid: true}},
		{"unspecified ipv4", "0.0.0.0", sql.NullInt64{Int64: 80, Valid: true}},
		{"unspecified ipv6", "::", sql.NullInt64{Int64: 80, Valid: true}},
		{"multicast ipv4", "224.0.0.251", sql.NullInt64{Int64: 80, Valid: true}},
		{"broadcast ipv4", "255.255.255.255", sql.NullInt64{Int64: 80, Valid: true}},
		{"port zero", "192.168.1.10", sql.NullInt64{Int64: 0, Valid: true}},
		{"port too high", "192.168.1.10", sql.NullInt64{Int64: 65536, Valid: true}},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateController(ctx, db.CreateControllerParams{
				Name:      "Bad",
				IpAddress: tc.ip,
				Port:      tc.port,
			})
			if !errors.Is(err, ErrInvalidEndpoint) {
				t.Errorf("expected ErrInvalidEndpoint for %q (port=%v), got %v", tc.ip, tc.port, err)
			}
		})
	}

	validCases := []struct {
		ip       string
		port     sql.NullInt64
		wantHost string
		wantPort int64
	}{
		{"192.168.1.50", sql.NullInt64{Int64: 8080, Valid: true}, "192.168.1.50", 8080},
		{"wled-bench.local", sql.NullInt64{Valid: false}, "wled-bench.local", 80},
		{"127.0.0.1:54321", sql.NullInt64{Valid: false}, "127.0.0.1", 54321},
		{"::1", sql.NullInt64{Int64: 80, Valid: true}, "::1", 80},
	}

	for _, tc := range validCases {
		row, err := svc.CreateController(ctx, db.CreateControllerParams{
			Name:      "OK-" + tc.ip,
			IpAddress: tc.ip,
			Port:      tc.port,
		})
		if err != nil {
			t.Fatalf("expected valid endpoint %q to succeed, got %v", tc.ip, err)
		}
		ctrl, _ := svc.GetController(ctx, row.ID)
		if ctrl.IpAddress != tc.wantHost || ctrl.Port.Int64 != tc.wantPort {
			t.Errorf("got host=%q port=%d, want host=%q port=%d", ctrl.IpAddress, ctrl.Port.Int64, tc.wantHost, tc.wantPort)
		}
	}
}

func TestService_UpdateControllerAndStatusSyncPreservesLedCount(t *testing.T) {
	online := true
	var lastReqURL string
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		lastReqURL = r.URL.String()
		if !online {
			return nil, errors.New("connection refused")
		}
		body, _ := json.Marshal(map[string]any{
			"name": "WLED",
			"mac":  "AA-BB-CC-DD-EE-FF",
			"leds": map[string]any{"count": 150},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	svc, store, _ := setupIsolatedTest(t, "hw_status_sync", rt)
	ctx := context.Background()

	created, err := svc.CreateController(ctx, db.CreateControllerParams{
		Name:      "Bench",
		IpAddress: "192.168.1.40",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateController failed: %v", err)
	}

	// Edit controller including port, UDP realtime, restore_state, idle_preset_id
	err = svc.UpdateController(ctx, db.UpdateControllerParams{
		ID:           created.ID,
		Name:         "Bench Updated",
		IpAddress:    "192.168.1.41",
		Port:         sql.NullInt64{Int64: 8081, Valid: true},
		UseUdp:       sql.NullBool{Bool: true, Valid: true},
		UdpPort:      sql.NullInt64{Int64: 21325, Valid: true},
		RestoreState: sql.NullBool{Bool: false, Valid: true},
		IdlePresetID: sql.NullInt64{Int64: 4, Valid: true},
	})
	if err != nil {
		t.Fatalf("UpdateController failed: %v", err)
	}

	// Online sync should query 192.168.1.41:8081 and populate led_count + normalized mac_address
	isOn, err := svc.UpdateStatus(ctx, created.ID)
	if err != nil || !isOn {
		t.Fatalf("expected online=true, got online=%v err=%v", isOn, err)
	}
	if lastReqURL != "http://192.168.1.41:8081/json/info" {
		t.Errorf("expected request to honor controller.Port 8081, got %q", lastReqURL)
	}
	got, _ := store.GetController(ctx, created.ID)
	if got.LedCount != 150 || got.MacAddress.String != "aa:bb:cc:dd:ee:ff" || !got.IsOnline.Bool {
		t.Errorf("unexpected controller info after online sync: %+v", got)
	}

	// Now go offline: UpdateStatus must ONLY flip is_online=false and MUST NOT reset led_count to 0!
	online = false
	isOn, err = svc.UpdateStatus(ctx, created.ID)
	if err != nil || isOn {
		t.Fatalf("expected online=false, got online=%v err=%v", isOn, err)
	}
	gotAfterOffline, _ := store.GetController(ctx, created.ID)
	if gotAfterOffline.IsOnline.Bool {
		t.Error("expected is_online=false after offline sync")
	}
	if gotAfterOffline.LedCount != 150 {
		t.Errorf("expected led_count preserved at 150 when offline, got %d", gotAfterOffline.LedCount)
	}
	if gotAfterOffline.MacAddress.String != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("expected mac_address preserved when offline, got %q", gotAfterOffline.MacAddress.String)
	}
}

func TestService_ReconcileDiscoveredDevices_MACSelfHealing(t *testing.T) {
	svc, store, _ := setupIsolatedTest(t, "hw_mdns_heal", nil)
	ctx := context.Background()

	cRow, _ := svc.CreateController(ctx, db.CreateControllerParams{
		Name:      "Rack",
		IpAddress: "192.168.1.10",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	_ = store.UpdateControllerInfo(ctx, db.UpdateControllerInfoParams{
		IsOnline:   sql.NullBool{Bool: false, Valid: true},
		LedCount:   60,
		MacAddress: sql.NullString{String: "aa:bb:cc:dd:ee:01", Valid: true},
		ID:         cRow.ID,
	})

	res, err := svc.ReconcileDiscoveredDevices(ctx, []wled.DiscoveredDevice{
		{
			Name:     "Rack-WLED",
			IP:       "192.168.1.99",
			Port:     80,
			MAC:      "AABBCCDDEE01",
			LedCount: 120,
		},
		{
			Name:     "Brand-New-WLED",
			IP:       "192.168.1.200",
			Port:     8080,
			MAC:      "11:22:33:44:55:66",
			LedCount: 30,
		},
	})
	if err != nil {
		t.Fatalf("ReconcileDiscoveredDevices failed: %v", err)
	}
	if len(res.Devices) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(res.Devices))
	}

	if res.Devices[0].Status != "ip_updated" || res.Devices[0].PreviousIP != "192.168.1.10" {
		t.Errorf("expected first device ip_updated self-heal, got %+v", res.Devices[0])
	}
	if res.Devices[1].Status != "new" {
		t.Errorf("expected second device status new, got %+v", res.Devices[1])
	}

	healed, _ := store.GetController(ctx, cRow.ID)
	if healed.IpAddress != "192.168.1.99" || healed.LedCount != 120 || !healed.IsOnline.Bool {
		t.Errorf("controller DHCP IP not auto-healed cleanly: %+v", healed)
	}
}

func TestService_SaveGrid_IDORRejectionAndMultiLEDWidth(t *testing.T) {
	svc, store, _ := setupIsolatedTest(t, "hw_idor_width", nil)
	ctx := context.Background()

	cA, _ := svc.CreateController(ctx, db.CreateControllerParams{Name: "A", IpAddress: "10.0.0.10"})
	cB, _ := svc.CreateController(ctx, db.CreateControllerParams{Name: "B", IpAddress: "10.0.0.20"})

	contsB, _ := svc.GetContainers(ctx, cB.ID)
	victimContainerID := contsB[0].ID

	// Attempt to mutate controller B's container via SaveGrid on controller A (IDOR attack)
	hijackCfg, _ := json.Marshal([]map[string]any{
		{"id": victimContainerID, "name": "Hijacked!", "segment_id": 0, "config": map[string]any{"type": "grid", "rows": 2, "cols": 2}},
	})
	_, err := svc.SaveGrid(ctx, cA.ID, `[]`, string(hijackCfg))
	if !errors.Is(err, ErrContainerOwnership) {
		t.Fatalf("expected ErrContainerOwnership rejecting cross-controller container ID, got %v", err)
	}

	// Verify victim container B was not renamed
	victimAfter, _ := store.GetContainer(ctx, victimContainerID)
	if victimAfter.Name == "Hijacked!" {
		t.Fatal("IDOR failed to block mutation of controller B container!")
	}

	// Verify multi-LED width persistence and led count calculation on controller A
	contsA, _ := svc.GetContainers(ctx, cA.ID)
	validCfg, _ := json.Marshal([]map[string]any{
		{"id": contsA[0].ID, "name": contsA[0].Name, "segment_id": 0, "config": map[string]any{"type": "grid", "rows": 2, "cols": 2}},
	})
	validBins := `[{"container_index":0,"x":0,"y":0,"led_index":0,"width":3,"name":"WideA1"},{"container_index":0,"x":1,"y":0,"led_index":3,"width":2,"name":"WideB1"}]`
	totalLEDs, err := svc.SaveGrid(ctx, cA.ID, validBins, string(validCfg))
	if err != nil {
		t.Fatalf("SaveGrid valid multi-width failed: %v", err)
	}
	if totalLEDs != 5 {
		t.Errorf("expected totalLEDs = 5 (led 3 + width 2), got %d", totalLEDs)
	}

	savedBins, _ := svc.GetBinsByController(ctx, cA.ID)
	if len(savedBins) != 2 || savedBins[0].Width.Int64 != 3 || savedBins[1].Width.Int64 != 2 {
		t.Errorf("expected persisted bin widths 3 and 2, got %+v", savedBins)
	}
}

func TestService_StandaloneContainerCRUDAndBind(t *testing.T) {
	svc, store, _ := setupIsolatedTest(t, "hw_standalone", nil)
	ctx := context.Background()

	contID, err := svc.CreateStandaloneContainer(ctx, "Travel Kit", 2, 3)
	if err != nil {
		t.Fatalf("CreateStandaloneContainer failed: %v", err)
	}

	standalone, err := svc.ListStandaloneContainers(ctx)
	if err != nil || len(standalone) != 1 || standalone[0].ID != contID {
		t.Fatalf("expected 1 standalone container, got %+v (err=%v)", standalone, err)
	}

	// Save standalone grid with unmapped LED (led_index: null) and wide width=2
	cfg := `[{"id":` + jsonInt(contID) + `,"name":"Travel Kit Updated","segment_id":0,"config":{"type":"grid","rows":2,"cols":3}}]`
	bins := `[{"container_index":0,"x":0,"y":0,"led_index":null,"width":2,"name":"A1"},{"container_index":0,"x":1,"y":0,"led_index":null,"width":1,"name":"B1"}]`
	if _, err := svc.SaveStandaloneGrid(ctx, contID, bins, cfg); err != nil {
		t.Fatalf("SaveStandaloneGrid failed: %v", err)
	}

	savedBins, _ := svc.GetBinsByContainer(ctx, contID)
	if len(savedBins) != 2 || savedBins[0].LedIndex.Valid || savedBins[0].Width.Int64 != 2 {
		t.Errorf("unexpected standalone bins: %+v", savedBins)
	}

	// Reject standalone grid save attempting to hijack a controller-owned container
	ctrlRow, _ := svc.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl1", IpAddress: "10.0.0.50"})
	ctrlConts, _ := svc.GetContainers(ctx, ctrlRow.ID)
	badCfg := `[{"id":` + jsonInt(ctrlConts[0].ID) + `,"name":"Wrong","segment_id":0,"config":{"type":"grid","rows":2,"cols":2}}]`
	if _, err := svc.SaveStandaloneGrid(ctx, ctrlConts[0].ID, `[]`, badCfg); !errors.Is(err, ErrContainerOwnership) {
		t.Errorf("expected ErrContainerOwnership saving controller container as standalone, got %v", err)
	}

	// Attach standalone container to controller, then detach back to NULL
	if err := svc.UpdateContainerController(ctx, contID, sql.NullInt64{Int64: ctrlRow.ID, Valid: true}); err != nil {
		t.Fatalf("attach UpdateContainerController failed: %v", err)
	}
	cAfterAttach, _ := store.GetContainer(ctx, contID)
	if !cAfterAttach.ControllerID.Valid || cAfterAttach.ControllerID.Int64 != ctrlRow.ID {
		t.Errorf("expected container attached to controller %d, got %+v", ctrlRow.ID, cAfterAttach.ControllerID)
	}

	if err := svc.UpdateContainerController(ctx, contID, sql.NullInt64{Valid: false}); err != nil {
		t.Fatalf("detach UpdateContainerController failed: %v", err)
	}
	afterDetach, _ := svc.ListStandaloneContainers(ctx)
	if len(afterDetach) != 1 || afterDetach[0].ID != contID {
		t.Errorf("expected detached container back in standalone list, got %+v", afterDetach)
	}

	if err := svc.DeleteContainer(ctx, contID); err != nil {
		t.Fatalf("DeleteContainer failed: %v", err)
	}
	afterDel, _ := svc.ListStandaloneContainers(ctx)
	if len(afterDel) != 0 {
		t.Errorf("expected 0 standalone containers after delete, got %d", len(afterDel))
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
