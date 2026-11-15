package wled

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

func setupWLEDTestDB(t *testing.T, name string) (db.Store, *sql.DB) {
	t.Helper()
	dbConn, err := db.Open("file:" + name + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })

	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}
	store := db.NewStore(dbConn)
	if err := store.InitSettings(context.Background()); err != nil {
		t.Fatalf("failed to init settings: %v", err)
	}
	return store, dbConn
}

type recordedState struct {
	mu       sync.Mutex
	payloads []map[string]any
	raws     []string
}

func (r *recordedState) record(raw string, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.raws = append(r.raws, raw)
	r.payloads = append(r.payloads, payload)
}

func (r *recordedState) snapshot() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, len(r.payloads))
	copy(out, r.payloads)
	return out
}

func TestService_Locate(t *testing.T) {
	receivedColor := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/state" {
			body, _ := io.ReadAll(r.Body)
			receivedColor = string(body)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	ip := server.URL[7:]
	store, _ := setupWLEDTestDB(t, "wled_test")
	ctx := context.Background()

	err := store.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate: sql.NullString{String: "#FF0000", Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to update colors: %v", err)
	}

	client := NewClient()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(store, client, logger)

	t.Run("LocateBin", func(t *testing.T) {
		c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Test", IpAddress: ip})
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}

		cont, err := store.CreateContainer(ctx, db.CreateContainerParams{
			Name:         "Cont",
			ControllerID: sql.NullInt64{Int64: c.ID, Valid: true},
			SegmentID:    0,
		})
		if err != nil {
			t.Fatalf("failed to create container: %v", err)
		}

		b, err := store.CreateBin(ctx, db.CreateBinParams{
			Name: "B1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create bin: %v", err)
		}

		if err := svc.LocateBin(ctx, c.ID, b); err != nil {
			t.Fatalf("LocateBin failed: %v", err)
		}

		if receivedColor == "" {
			t.Error("No color received by mock server")
		}
	})
}

func TestService_RapidBackToBackLocateTimerReset(t *testing.T) {
	rec := &recordedState{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"state": map[string]any{"on": false, "bri": 128, "seg": []any{map[string]any{"id": 0}}},
				"info":  map[string]any{"leds": map[string]any{"count": 16}},
			})
		case "/json/state":
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			rec.record(string(body), payload)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	store, _ := setupWLEDTestDB(t, "wled_timer_reset")
	ctx := context.Background()
	_ = store.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
		EnableLocateTimeout:  sql.NullBool{Bool: true, Valid: true},
		LocateTimeoutSeconds: sql.NullInt64{Int64: 1, Valid: true},
	})

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	portNum, _ := strconv.ParseInt(portStr, 10, 64)

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "TimerCtrl",
		IpAddress: host,
		Port:      sql.NullInt64{Int64: portNum, Valid: true},
	})
	// Disable RestoreState so timeout produces an explicit {"on": false} clear.
	_ = store.UpdateController(ctx, db.UpdateControllerParams{
		ID:           ctrl.ID,
		Name:         "TimerCtrl",
		IpAddress:    host,
		Port:         sql.NullInt64{Int64: portNum, Valid: true},
		RestoreState: sql.NullBool{Bool: false, Valid: true},
		UdpPort:      sql.NullInt64{Int64: DefaultUDPPort, Valid: true},
	})

	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "C", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}, SegmentID: 0,
	})
	b1, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}})
	b2, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "B2", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 3, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}})

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil))).(*service)
	svc.timeoutOverride = 90 * time.Millisecond

	if err := svc.LocateBin(ctx, ctrl.ID, b1); err != nil {
		t.Fatalf("Locate 1 failed: %v", err)
	}
	time.Sleep(45 * time.Millisecond)

	// Second locate at 45ms should reset the 90ms timer (expiring at ~135ms, NOT 90ms).
	if err := svc.LocateBin(ctx, ctrl.ID, b2); err != nil {
		t.Fatalf("Locate 2 failed: %v", err)
	}

	// At t=105ms (past first timer's 90ms deadline), locate MUST still be active (only 2 payloads, no clear yet).
	time.Sleep(60 * time.Millisecond)
	snaps := rec.snapshot()
	if len(snaps) != 2 {
		t.Fatalf("expected timer 1 to be cancelled so only 2 locate payloads sent at t=105ms, got %d", len(snaps))
	}

	// Wait past second timer's 90ms deadline; timeout should fire once and turn off LEDs.
	time.Sleep(70 * time.Millisecond)
	snaps = rec.snapshot()
	if len(snaps) != 3 {
		t.Fatalf("expected 3rd clear payload after reset timer expired, got %d", len(snaps))
	}
	if on, _ := snaps[2]["on"].(bool); on {
		t.Errorf("expected timeout clear payload to set on=false, got %v", snaps[2])
	}
}

func TestService_SnapshotRestoreAndMultiSegmentClear(t *testing.T) {
	var snapshotGets int
	var mu sync.Mutex
	rec := &recordedState{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			mu.Lock()
			snapshotGets++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"state": map[string]any{
					"on":  true,
					"bri": 120,
					"ps":  3,
					"seg": []any{
						map[string]any{"id": 0, "on": true, "fx": 9, "col": []any{[]any{255, 160, 0}}},
						map[string]any{"id": 1, "on": true, "fx": 5, "col": []any{[]any{0, 255, 128}}},
					},
				},
				"info": map[string]any{"leds": map[string]any{"count": 64}},
			})
		case "/json/state":
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			rec.record(string(body), payload)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	store, _ := setupWLEDTestDB(t, "wled_snapshot_restore")
	ctx := context.Background()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	portNum, _ := strconv.ParseInt(portStr, 10, 64)

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{
		Name: "SnapCtrl", IpAddress: host, Port: sql.NullInt64{Int64: portNum, Valid: true},
	})
	cont0, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Seg0", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}, SegmentID: 0,
	})
	_, _ = store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Seg2", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}, SegmentID: 2,
	})
	b1, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "B1", ContainerID: cont0, LedIndex: sql.NullInt64{Int64: 5, Valid: true}, Width: sql.NullInt64{Int64: 3, Valid: true},
	})
	b2, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "B2", ContainerID: cont0, LedIndex: sql.NullInt64{Int64: 10, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true},
	})

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	// First locate captures snapshot; second locate reuses it without overwriting with locating state.
	if err := svc.LocateBin(ctx, ctrl.ID, b1); err != nil {
		t.Fatalf("LocateBin 1 failed: %v", err)
	}
	if err := svc.LocateBin(ctx, ctrl.ID, b2); err != nil {
		t.Fatalf("LocateBin 2 failed: %v", err)
	}

	mu.Lock()
	gets := snapshotGets
	mu.Unlock()
	if gets != 1 {
		t.Errorf("expected exactly 1 /json snapshot capture across active session, got %d", gets)
	}

	// Verify locate payload forces fx=0, black wipe, and turns off non-located segments (1 and 2).
	snaps := rec.snapshot()
	segsRaw, _ := snaps[0]["seg"].([]any)
	if len(segsRaw) != 3 {
		t.Fatalf("expected 3 segments (0, 1, 2) in locate payload to prevent bleed, got %d", len(segsRaw))
	}
	seg0 := segsRaw[0].(map[string]any)
	if seg0["fx"] != float64(0) || seg0["on"] != true {
		t.Errorf("expected active seg 0 with on=true and fx=0, got %v", seg0)
	}
	seg1 := segsRaw[1].(map[string]any)
	if seg1["on"] != false {
		t.Errorf("expected idle seg 1 to be forced off, got %v", seg1)
	}

	// GlobalOff restores saved snapshot (including bri=120, ps=3, and unfreezing frz=false).
	if err := svc.GlobalOff(ctx); err != nil {
		t.Fatalf("GlobalOff failed: %v", err)
	}
	snaps = rec.snapshot()
	restored := snaps[len(snaps)-1]
	if restored["ps"] != float64(3) || restored["bri"] != float64(120) {
		t.Errorf("expected saved preset/bri restored on GlobalOff, got %v", restored)
	}
	restoredSegs := restored["seg"].([]any)
	if restoredSegs[0].(map[string]any)["frz"] != false {
		t.Errorf("expected restored segment to explicitly set frz=false, got %v", restoredSegs[0])
	}

	// Now configure IdlePresetID > 0 and verify GlobalOff sends {"ps": 7}.
	_ = store.UpdateController(ctx, db.UpdateControllerParams{
		ID: ctrl.ID, Name: "SnapCtrl", IpAddress: host, Port: sql.NullInt64{Int64: portNum, Valid: true},
		RestoreState: sql.NullBool{Bool: false, Valid: true},
		IdlePresetID: sql.NullInt64{Int64: 7, Valid: true},
		UdpPort:      sql.NullInt64{Int64: DefaultUDPPort, Valid: true},
	})
	if err := svc.GlobalOff(ctx); err != nil {
		t.Fatalf("GlobalOff with idle preset failed: %v", err)
	}
	snaps = rec.snapshot()
	presetCmd := snaps[len(snaps)-1]
	if presetCmd["ps"] != float64(7) || len(presetCmd) != 1 {
		t.Errorf("expected idle preset command {ps:7}, got %v", presetCmd)
	}

	// Now disable IdlePresetID & RestoreState and verify GlobalOff clears ALL segments + on:false.
	_ = store.UpdateController(ctx, db.UpdateControllerParams{
		ID: ctrl.ID, Name: "SnapCtrl", IpAddress: host, Port: sql.NullInt64{Int64: portNum, Valid: true},
		RestoreState: sql.NullBool{Bool: false, Valid: true},
		IdlePresetID: sql.NullInt64{Int64: 0, Valid: true},
		UdpPort:      sql.NullInt64{Int64: DefaultUDPPort, Valid: true},
	})
	if err := svc.GlobalOff(ctx); err != nil {
		t.Fatalf("GlobalOff full clear failed: %v", err)
	}
	snaps = rec.snapshot()
	clearCmd := snaps[len(snaps)-1]
	if clearCmd["on"] != false {
		t.Errorf("expected top-level on=false on GlobalOff clear, got %v", clearCmd)
	}
	clearSegs := clearCmd["seg"].([]any)
	if len(clearSegs) != 3 {
		t.Errorf("expected all known segments (0, 1, 2) cleared, got %d segments: %v", len(clearSegs), clearSegs)
	}
}

func TestService_LocatePart_BatchSkipAndStockHealthColor(t *testing.T) {
	rec := &recordedState{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/state" {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			rec.record(string(body), payload)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	store, _ := setupWLEDTestDB(t, "wled_locate_part")
	ctx := context.Background()

	_ = store.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate:        sql.NullString{String: "#0000FF", Valid: true},
		ColorStockOk:       sql.NullString{String: "#112233", Valid: true},
		ColorStockLow:      sql.NullString{String: "#445566", Valid: true},
		ColorStockCritical: sql.NullString{String: "#778899", Valid: true},
	})

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	portNum, _ := strconv.ParseInt(portStr, 10, 64)

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{
		Name: "BatchCtrl", IpAddress: host, Port: sql.NullInt64{Int64: portNum, Valid: true},
	})
	attachedCont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Attached", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}, SegmentID: 0,
	})
	standaloneCont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Standalone", ControllerID: sql.NullInt64{Valid: false}, SegmentID: 0,
	})

	bZero, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "Zero", ContainerID: attachedCont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}})
	bLow, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "Low", ContainerID: attachedCont, LedIndex: sql.NullInt64{Int64: 2, Valid: true}, Width: sql.NullInt64{Int64: 2, Valid: true}})
	bOk, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "Ok", ContainerID: attachedCont, LedIndex: sql.NullInt64{Int64: 10, Valid: true}, Width: sql.NullInt64{Int64: 3, Valid: true}})
	bCrit, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "Crit", ContainerID: attachedCont, LedIndex: sql.NullInt64{Int64: 20, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}})
	bStandalone, _ := store.CreateBin(ctx, db.CreateBinParams{Name: "S1", ContainerID: standaloneCont, LedIndex: sql.NullInt64{Valid: false}, Width: sql.NullInt64{Int64: 1, Valid: true}})

	partID, _ := store.CreatePart(ctx, db.CreatePartParams{
		Name:              "10k Resistor",
		ReorderLevel:      sql.NullInt64{Int64: 10, Valid: true},
		MinStockThreshold: sql.NullInt64{Int64: 2, Valid: true},
	})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: bZero, Valid: true}, Quantity: 0})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: bLow, Valid: true}, Quantity: 5})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: bOk, Valid: true}, Quantity: 25})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: bCrit, Valid: true}, Quantity: 2})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: bStandalone, Valid: true}, Quantity: 99})

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Uniform color locate: all active attached bins batched in ONE /json/state call, skipping qty=0 and standalone.
	if err := svc.LocatePart(ctx, partID); err != nil {
		t.Fatalf("LocatePart failed: %v", err)
	}
	if len(rec.snapshot()) != 1 {
		t.Fatalf("expected single batched HTTP payload for controller, got %d", len(rec.snapshot()))
	}
	rawUniform := rec.raws[0]
	if strings.Contains(rawUniform, ",0,1,[0,0,255]") {
		t.Errorf("expected qty=0 bin (LED 0) to be skipped, payload=%s", rawUniform)
	}
	if !strings.Contains(rawUniform, ",2,4,[0,0,255]") || !strings.Contains(rawUniform, ",10,13,[0,0,255]") {
		t.Errorf("expected wide bins [2,4) and [10,13) with locate color in payload: %s", rawUniform)
	}

	// Stock-health locate mode: enable locate_color_by_stock
	_ = store.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
		LocateColorByStock: sql.NullBool{Bool: true, Valid: true},
	})
	if err := svc.LocatePart(ctx, partID); err != nil {
		t.Fatalf("LocatePart stock-health mode failed: %v", err)
	}
	if len(rec.snapshot()) != 2 {
		t.Fatalf("expected 2 total payloads, got %d", len(rec.snapshot()))
	}
	rawHealth := rec.raws[1]
	// #445566 -> [68,85,102]; #112233 -> [17,34,51]; #778899 -> [119,136,153]
	if !strings.Contains(rawHealth, "[68,85,102]") || !strings.Contains(rawHealth, "[17,34,51]") || !strings.Contains(rawHealth, "[119,136,153]") {
		t.Errorf("expected low, ok, and critical stock colors in payload, got: %s", rawHealth)
	}
}

func TestService_NetworkErrorPropagationAndPortHonored(t *testing.T) {
	// Bind and immediately close a listener to get an unreachable port cleanly.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	deadAddr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()

	store, _ := setupWLEDTestDB(t, "wled_net_error")
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{
		Name: "DeadCtrl", IpAddress: "127.0.0.1", Port: sql.NullInt64{Int64: int64(deadAddr.Port), Valid: true},
	})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "C", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}, SegmentID: 0,
	})
	binID, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "B1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true},
	})
	partID, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "P1"})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Int64: binID, Valid: true}, Quantity: 5,
	})

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := svc.LocateBin(ctx, ctrl.ID, binID); err == nil {
		t.Error("expected LocateBin to propagate network error for unreachable controller")
	}
	if err := svc.LocatePart(ctx, partID); err == nil {
		t.Error("expected LocatePart to propagate network error for unreachable controller")
	}
	if err := svc.GlobalOff(ctx); err == nil {
		t.Error("expected GlobalOff to propagate network error for unreachable controller")
	}
}
