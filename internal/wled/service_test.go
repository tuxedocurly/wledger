package wled

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

	client := NewClient()
	client.primeDelay = 0
	svc := NewService(store, client, slog.New(slog.NewTextHandler(io.Discard, nil))).(*service)
	svc.timeoutOverride = 90 * time.Millisecond

	if err := svc.LocateBin(ctx, ctrl.ID, b1); err != nil {
		t.Fatalf("Locate 1 failed: %v", err)
	}
	time.Sleep(45 * time.Millisecond)

	// Second locate at 45ms should reset the 90ms timer (expiring at ~135ms, NOT 90ms).
	if err := svc.LocateBin(ctx, ctrl.ID, b2); err != nil {
		t.Fatalf("Locate 2 failed: %v", err)
	}

	// At t=105ms (past first timer's 90ms deadline), locate MUST still be active (4 payloads: 2 per locate, no clear yet).
	time.Sleep(60 * time.Millisecond)
	snaps := rec.snapshot()
	if len(snaps) != 4 {
		t.Fatalf("expected timer 1 to be cancelled so only 4 locate payloads (2 per locate) sent at t=105ms, got %d", len(snaps))
	}

	// Wait past second timer's 90ms deadline; timeout should fire once and turn off LEDs.
	time.Sleep(70 * time.Millisecond)
	snaps = rec.snapshot()
	if len(snaps) != 5 {
		t.Fatalf("expected 5th clear payload after reset timer expired, got %d", len(snaps))
	}
	if on, _ := snaps[4]["on"].(bool); on {
		t.Errorf("expected timeout clear payload to set on=false, got %v", snaps[4])
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

	// Verify locate payload sequence: Step 1 primes all 3 segments (fx=0, frz=false, turns off 1 & 2, no "i");
	// Step 2 sends individual LEDs ("i") for active segment 0 without re-mutating fx/col/bri/on.
	snaps := rec.snapshot()
	if len(snaps) < 2 {
		t.Fatalf("expected at least 2 locate payloads (prime + pixel), got %d", len(snaps))
	}
	segsRaw, _ := snaps[0]["seg"].([]any)
	if len(segsRaw) != 3 {
		t.Fatalf("expected 3 segments (0, 1, 2) in prime locate payload to prevent bleed, got %d", len(segsRaw))
	}
	seg0 := segsRaw[0].(map[string]any)
	if seg0["fx"] != float64(0) || seg0["on"] != true || seg0["frz"] != false {
		t.Errorf("expected primed seg 0 with on=true, fx=0, frz=false, got %v", seg0)
	}
	if _, hasI := seg0["i"]; hasI {
		t.Errorf("expected primed seg 0 not to contain individual pixel array i, got %v", seg0)
	}
	seg1 := segsRaw[1].(map[string]any)
	if seg1["on"] != false {
		t.Errorf("expected idle seg 1 to be forced off, got %v", seg1)
	}
	if _, hasI := seg1["i"]; hasI {
		t.Errorf("expected idle seg 1 prime payload not to contain i, got %v", seg1)
	}

	pixelSegs, _ := snaps[1]["seg"].([]any)
	if len(pixelSegs) != 1 {
		t.Fatalf("expected 1 active segment in pixel payload, got %d", len(pixelSegs))
	}
	pixelSeg0 := pixelSegs[0].(map[string]any)
	if _, hasI := pixelSeg0["i"]; !hasI {
		t.Errorf("expected pixel payload seg 0 to contain individual LED array i, got %v", pixelSeg0)
	}
	for _, key := range []string{"fx", "col", "bri", "on"} {
		if _, exists := pixelSeg0[key]; exists {
			t.Errorf("expected pixel payload seg 0 not to re-mutate %q, got %v", key, pixelSeg0)
		}
	}
	if !strings.Contains(rec.raws[1], ",5,8,[") {
		t.Errorf("expected pixel payload seg 0 to light [5, 8), got %s", rec.raws[1])
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

	// Uniform color locate: all active attached bins batched (prime + pixel payloads), skipping qty=0 and standalone.
	if err := svc.LocatePart(ctx, partID); err != nil {
		t.Fatalf("LocatePart failed: %v", err)
	}
	if len(rec.snapshot()) != 2 {
		t.Fatalf("expected 2 payloads (prime + pixel) for batched controller locate, got %d", len(rec.snapshot()))
	}
	if strings.Contains(rec.raws[0], `"i":`) {
		t.Errorf("expected prime payload not to contain individual pixels i, got %s", rec.raws[0])
	}
	rawUniform := rec.raws[1]
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
	if len(rec.snapshot()) != 4 {
		t.Fatalf("expected 4 total payloads, got %d", len(rec.snapshot()))
	}
	rawHealth := rec.raws[3]
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

func TestService_LocateMarksOfflineControllerOnline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/state" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	store, _ := setupWLEDTestDB(t, "wled_locate_online")
	ctx := context.Background()

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	portNum, _ := strconv.ParseInt(portStr, 10, 64)

	httpCtrl, err := store.CreateController(ctx, db.CreateControllerParams{
		Name: "OfflineHTTPCtrl", IpAddress: host, Port: sql.NullInt64{Int64: portNum, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateController http failed: %v", err)
	}
	if err := store.UpdateControllerInfo(ctx, db.UpdateControllerInfoParams{
		IsOnline: sql.NullBool{Bool: false, Valid: true},
		LedCount: 60,
		ID:       httpCtrl.ID,
	}); err != nil {
		t.Fatalf("UpdateControllerInfo http failed: %v", err)
	}
	httpCont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "HTTPCont", ControllerID: sql.NullInt64{Int64: httpCtrl.ID, Valid: true}, SegmentID: 0,
	})
	httpBinID, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "B1", ContainerID: httpCont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true},
	})
	partID, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "OnlineCheckPart"})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Int64: httpBinID, Valid: true}, Quantity: 3,
	})

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket udp failed: %v", err)
	}
	defer pc.Close()
	udpPort := pc.LocalAddr().(*net.UDPAddr).Port

	udpCtrl, err := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "OfflineUDPCtrl",
		IpAddress: "127.0.0.1",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateController udp failed: %v", err)
	}
	if err := store.UpdateController(ctx, db.UpdateControllerParams{
		ID:        udpCtrl.ID,
		Name:      "OfflineUDPCtrl",
		IpAddress: "127.0.0.1",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
		UseUdp:    sql.NullBool{Bool: true, Valid: true},
		UdpPort:   sql.NullInt64{Int64: int64(udpPort), Valid: true},
	}); err != nil {
		t.Fatalf("UpdateController udp failed: %v", err)
	}
	if err := store.UpdateControllerInfo(ctx, db.UpdateControllerInfoParams{
		IsOnline: sql.NullBool{Bool: false, Valid: true},
		LedCount: 30,
		ID:       udpCtrl.ID,
	}); err != nil {
		t.Fatalf("UpdateControllerInfo udp failed: %v", err)
	}
	udpCont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "UDPCont", ControllerID: sql.NullInt64{Int64: udpCtrl.ID, Valid: true}, SegmentID: 0,
	})
	udpBinID, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "UB1", ContainerID: udpCont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true},
	})

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := svc.LocatePart(ctx, partID); err != nil {
		t.Fatalf("LocatePart failed: %v", err)
	}
	gotHTTP, err := store.GetController(ctx, httpCtrl.ID)
	if err != nil {
		t.Fatalf("GetController http failed: %v", err)
	}
	if !gotHTTP.IsOnline.Bool {
		t.Errorf("expected HTTP LocatePart to mark offline controller online, got IsOnline=%+v", gotHTTP.IsOnline)
	}
	if gotHTTP.LedCount != 60 {
		t.Errorf("expected LedCount preserved at 60, got %d", gotHTTP.LedCount)
	}

	if err := svc.LocateBin(ctx, udpCtrl.ID, udpBinID); err != nil {
		t.Fatalf("LocateBin udp failed: %v", err)
	}
	gotUDP, err := store.GetController(ctx, udpCtrl.ID)
	if err != nil {
		t.Fatalf("GetController udp failed: %v", err)
	}
	if gotUDP.IsOnline.Bool {
		t.Errorf("expected fire-and-forget UDP LocateBin not to mark offline controller online")
	}
}

func TestService_LocateXY_HTTPAndUDP(t *testing.T) {
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

	store, _ := setupWLEDTestDB(t, "wled_locate_xy")
	ctx := context.Background()

	_ = store.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate:        sql.NullString{String: "#0000FF", Valid: true},
		ColorStockOk:       sql.NullString{String: "#00FF00", Valid: true},
		ColorStockLow:      sql.NullString{String: "#FFA500", Valid: true},
		ColorStockCritical: sql.NullString{String: "#FF0000", Valid: true},
	})

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	portNum, _ := strconv.ParseInt(portStr, 10, 64)

	ctrl, err := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "XY-HTTP",
		IpAddress: host,
		Port:      sql.NullInt64{Int64: portNum, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateController failed: %v", err)
	}

	contID, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "XY Wall",
		ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
		SegmentID:    0,
		ConfigJson:   sql.NullString{String: `{"type":"xy","rows":4,"cols":6}`, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}

	// Standalone XY container must not be located even when coords are set.
	standaloneXY, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:       "Standalone XY",
		SegmentID:  0,
		ConfigJson: sql.NullString{String: `{"type":"xy","rows":4,"cols":6}`, Valid: true},
	})

	binLit, err := store.CreateBin(ctx, db.CreateBinParams{
		Name:        "C4",
		ContainerID: contID,
		LedIndex:    sql.NullInt64{Valid: false},
		Width:       sql.NullInt64{Int64: 1, Valid: true},
		GridX:       sql.NullInt64{Int64: 2, Valid: true}, // X = 0 + 2 = 2
		GridY:       sql.NullInt64{Int64: 3, Valid: true}, // Y = 6 + 3 = 9
	})
	if err != nil {
		t.Fatalf("CreateBin lit failed: %v", err)
	}
	binZero, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name:        "A1",
		ContainerID: contID,
		LedIndex:    sql.NullInt64{Valid: false},
		Width:       sql.NullInt64{Int64: 1, Valid: true},
		GridX:       sql.NullInt64{Int64: 0, Valid: true},
		GridY:       sql.NullInt64{Int64: 0, Valid: true},
	})
	binStandalone, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name:        "S1",
		ContainerID: standaloneXY,
		LedIndex:    sql.NullInt64{Valid: false},
		Width:       sql.NullInt64{Int64: 1, Valid: true},
		GridX:       sql.NullInt64{Int64: 1, Valid: true},
		GridY:       sql.NullInt64{Int64: 1, Valid: true},
	})

	partID, _ := store.CreatePart(ctx, db.CreatePartParams{
		Name:              "ESP32-S3",
		ReorderLevel:      sql.NullInt64{Int64: 5, Valid: true},
		MinStockThreshold: sql.NullInt64{Int64: 1, Valid: true},
	})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: binLit, Valid: true}, Quantity: 12})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: binZero, Valid: true}, Quantity: 0})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: binStandalone, Valid: true}, Quantity: 50})

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. LocateBin on XY container lights both X=2 and Y=9 (prime + pixel payloads).
	if err := svc.LocateBin(ctx, ctrl.ID, binLit); err != nil {
		t.Fatalf("LocateBin XY failed: %v", err)
	}
	if len(rec.raws) != 2 {
		t.Fatalf("expected 2 payloads (prime + pixel) from LocateBin, got %d", len(rec.raws))
	}
	binPayload := rec.raws[1]
	if !strings.Contains(binPayload, `,2,3,[0,0,255]`) || !strings.Contains(binPayload, `,9,10,[0,0,255]`) {
		t.Errorf("expected XY LocateBin to light X=2 and Y=9 with [0,0,255], got %s", binPayload)
	}

	// 2. LocatePart on XY container with stock-health enabled uses OK color [0,255,0] and skips qty=0 & standalone.
	_ = store.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
		LocateColorByStock: sql.NullBool{Bool: true, Valid: true},
	})
	if err := svc.LocatePart(ctx, partID); err != nil {
		t.Fatalf("LocatePart XY failed: %v", err)
	}
	if len(rec.raws) != 4 {
		t.Fatalf("expected 4 payloads after LocatePart, got %d", len(rec.raws))
	}
	partPayload := rec.raws[3]
	if !strings.Contains(partPayload, `,2,3,[0,255,0]`) || !strings.Contains(partPayload, `,9,10,[0,255,0]`) {
		t.Errorf("expected XY LocatePart to light X=2 and Y=9 with stock-ok [0,255,0], got %s", partPayload)
	}
	if strings.Contains(partPayload, `,0,1,[0,255,0]`) || strings.Contains(partPayload, `,1,2,[0,255,0]`) || strings.Contains(partPayload, `,7,8,[0,255,0]`) {
		t.Errorf("expected qty=0 and standalone XY bins to remain unlit, got %s", partPayload)
	}

	// 3. UDP DNRGB mode with custom offsets + reverse wiring on both axes.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket failed: %v", err)
	}
	defer pc.Close()
	udpAddr := pc.LocalAddr().(*net.UDPAddr)

	ctrlUDP, err := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "XY-UDP",
		IpAddress: "127.0.0.1",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateController UDP failed: %v", err)
	}
	if err := store.UpdateController(ctx, db.UpdateControllerParams{
		ID:        ctrlUDP.ID,
		Name:      "XY-UDP",
		IpAddress: "127.0.0.1",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
		UseUdp:    sql.NullBool{Bool: true, Valid: true},
		UdpPort:   sql.NullInt64{Int64: int64(udpAddr.Port), Valid: true},
	}); err != nil {
		t.Fatalf("UpdateController UDP failed: %v", err)
	}

	contUDP, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Reversed XY",
		ControllerID: sql.NullInt64{Int64: ctrlUDP.ID, Valid: true},
		SegmentID:    0,
		ConfigJson:   sql.NullString{String: `{"type":"xy","rows":4,"cols":6,"x_start_led":10,"x_reverse":true,"y_start_led":20,"y_reverse":true}`, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateContainer UDP failed: %v", err)
	}
	binUDP, err := store.CreateBin(ctx, db.CreateBinParams{
		Name:        "B1",
		ContainerID: contUDP,
		LedIndex:    sql.NullInt64{Valid: false},
		Width:       sql.NullInt64{Int64: 1, Valid: true},
		GridX:       sql.NullInt64{Int64: 1, Valid: true}, // X = 10 + (6-1-1) = 14
		GridY:       sql.NullInt64{Int64: 0, Valid: true}, // Y = 20 + (4-1-0) = 23
	})
	if err != nil {
		t.Fatalf("CreateBin UDP failed: %v", err)
	}

	if err := svc.LocateBin(ctx, ctrlUDP.ID, binUDP); err != nil {
		t.Fatalf("LocateBin UDP failed: %v", err)
	}

	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom UDP failed: %v", err)
	}
	pkt := buf[:n]
	if len(pkt) != 4+24*3 || pkt[0] != DNRGBProtocolID {
		t.Fatalf("unexpected DNRGB packet len=%d header=%v (want len=76 proto=4)", len(pkt), pkt[:4])
	}
	for px := 0; px < 24; px++ {
		off := 4 + px*3
		gotRGB := [3]byte{pkt[off], pkt[off+1], pkt[off+2]}
		wantRGB := [3]byte{0, 0, 0}
		if px == 14 || px == 23 {
			wantRGB = [3]byte{0, 0, 255}
		}
		if gotRGB != wantRGB {
			t.Errorf("pixel %d RGB = %v, want %v", px, gotRGB, wantRGB)
		}
	}
}

func TestClient_LightRangesTwoStepPrimeThenPixels(t *testing.T) {
	t.Run("primes then sends individual pixels after settle delay", func(t *testing.T) {
		type capturedReq struct {
			at      time.Time
			raw     string
			payload map[string]any
		}
		var (
			mu   sync.Mutex
			reqs []capturedReq
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/json/state" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("invalid JSON payload: %v", err)
			}
			mu.Lock()
			reqs = append(reqs, capturedReq{
				at:      time.Now(),
				raw:     string(body),
				payload: payload,
			})
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
		portNum, _ := strconv.ParseInt(portStr, 10, 64)

		client := NewClient()
		client.primeDelay = 25 * time.Millisecond

		err := client.LightRangesWithPort(
			context.Background(),
			host,
			sql.NullInt64{Int64: portNum, Valid: true},
			64,
			[]int{0, 1},
			map[int][]LEDRange{
				0: {{Start: 14, Count: 1, RGB: [3]byte{0, 0, 255}}},
			},
		)
		if err != nil {
			t.Fatalf("LightRangesWithPort failed: %v", err)
		}

		mu.Lock()
		got := append([]capturedReq(nil), reqs...)
		mu.Unlock()

		if len(got) != 2 {
			t.Fatalf("expected 2 /json/state requests, got %d", len(got))
		}
		if elapsed := got[1].at.Sub(got[0].at); elapsed < 20*time.Millisecond {
			t.Errorf("expected >= 20ms settle delay between prime and pixel requests, got %v", elapsed)
		}

		// Verify Step 1 (prime) payload.
		p1 := got[0].payload
		if p1["on"] != true || p1["bri"] != float64(255) || p1["live"] != false || p1["tt"] != float64(0) {
			t.Errorf("unexpected Step 1 top-level fields: %v", p1)
		}
		segs1, _ := p1["seg"].([]any)
		if len(segs1) != 2 {
			t.Fatalf("expected 2 segments in Step 1 payload, got %d", len(segs1))
		}
		s0 := segs1[0].(map[string]any)
		if s0["id"] != float64(0) || s0["on"] != true || s0["bri"] != float64(255) || s0["fx"] != float64(0) || s0["frz"] != false {
			t.Errorf("unexpected Step 1 seg 0 fields: %v", s0)
		}
		if _, hasI := s0["i"]; hasI {
			t.Errorf("expected Step 1 seg 0 not to have i, got %v", s0)
		}
		col0, _ := json.Marshal(s0["col"])
		if string(col0) != "[[0,0,0]]" {
			t.Errorf("expected Step 1 seg 0 col [[0,0,0]], got %s", col0)
		}

		s1 := segs1[1].(map[string]any)
		if s1["id"] != float64(1) || s1["on"] != false || s1["fx"] != float64(0) || s1["frz"] != false {
			t.Errorf("unexpected Step 1 seg 1 fields: %v", s1)
		}
		if _, hasI := s1["i"]; hasI {
			t.Errorf("expected Step 1 seg 1 not to have i, got %v", s1)
		}
		col1, _ := json.Marshal(s1["col"])
		if string(col1) != "[[0,0,0]]" {
			t.Errorf("expected Step 1 seg 1 col [[0,0,0]], got %s", col1)
		}

		// Verify Step 2 (pixels) payload.
		p2 := got[1].payload
		if p2["tt"] != float64(0) {
			t.Errorf("expected Step 2 tt=0, got %v", p2["tt"])
		}
		for _, topKey := range []string{"on", "bri", "live"} {
			if _, exists := p2[topKey]; exists {
				t.Errorf("expected Step 2 not to re-send top-level %q, got %v", topKey, p2)
			}
		}
		segs2, _ := p2["seg"].([]any)
		if len(segs2) != 1 {
			t.Fatalf("expected 1 active segment in Step 2 payload, got %d", len(segs2))
		}
		ps0 := segs2[0].(map[string]any)
		if ps0["id"] != float64(0) {
			t.Errorf("expected Step 2 seg id=0, got %v", ps0["id"])
		}
		for _, segKey := range []string{"on", "bri", "fx", "col", "frz"} {
			if _, exists := ps0[segKey]; exists {
				t.Errorf("expected Step 2 seg 0 not to contain %q, got %v", segKey, ps0)
			}
		}
		indRaw, _ := json.Marshal(ps0["i"])
		if string(indRaw) != `[0,64,[0,0,0],14,15,[0,0,255]]` {
			t.Errorf("expected Step 2 i array [0,64,[0,0,0],14,15,[0,0,255]], got %s", indRaw)
		}
	})

	t.Run("aborts before pixel request on prime error", func(t *testing.T) {
		var (
			mu       sync.Mutex
			reqCount int
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			reqCount++
			mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
		portNum, _ := strconv.ParseInt(portStr, 10, 64)

		client := NewClient()
		client.primeDelay = 25 * time.Millisecond

		err := client.LightRangesWithPort(
			context.Background(),
			host,
			sql.NullInt64{Int64: portNum, Valid: true},
			64,
			[]int{0},
			map[int][]LEDRange{0: {{Start: 0, Count: 1, RGB: [3]byte{255, 0, 0}}}},
		)
		if err == nil {
			t.Fatal("expected error when Step 1 prime returns HTTP 500")
		}

		mu.Lock()
		gotCount := reqCount
		mu.Unlock()
		if gotCount != 1 {
			t.Errorf("expected only 1 request when prime fails, got %d", gotCount)
		}
	})

	t.Run("aborts before pixel request on context cancellation during settle delay", func(t *testing.T) {
		var (
			mu           sync.Mutex
			reqCount     int
			firstReqSeen = make(chan struct{})
			markSeen     sync.Once
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			reqCount++
			mu.Unlock()
			markSeen.Do(func() { close(firstReqSeen) })
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
		portNum, _ := strconv.ParseInt(portStr, 10, 64)

		client := NewClient()
		client.primeDelay = 500 * time.Millisecond

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- client.LightRangesWithPort(
				ctx,
				host,
				sql.NullInt64{Int64: portNum, Valid: true},
				64,
				[]int{0},
				map[int][]LEDRange{0: {{Start: 0, Count: 1, RGB: [3]byte{255, 0, 0}}}},
			)
		}()

		<-firstReqSeen
		cancel()

		err := <-errCh
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled error, got %v", err)
		}

		mu.Lock()
		gotCount := reqCount
		mu.Unlock()
		if gotCount != 1 {
			t.Errorf("expected only 1 request when cancelled during settle delay, got %d", gotCount)
		}
	})
}
