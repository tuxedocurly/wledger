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
	"sync/atomic"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestStatusPoller_PeriodicPingUpdatesDashboardOnlineCount(t *testing.T) {
	var online atomic.Bool
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !online.Load() {
			return nil, errors.New("connection refused")
		}
		body, _ := json.Marshal(map[string]any{
			"name": "WLED",
			"mac":  "AA:BB:CC:DD:EE:01",
			"leds": map[string]any{"count": 60},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	svc, store, dbConn := setupIsolatedTest(t, "poller_periodic", rt)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("InitSettings failed: %v", err)
	}
	if _, err := dbConn.ExecContext(ctx, `UPDATE settings SET enable_controller_ping = 1, controller_ping_interval_seconds = 10 WHERE id = 1`); err != nil {
		t.Fatalf("update settings failed: %v", err)
	}

	ctrl, err := svc.CreateController(ctx, db.CreateControllerParams{
		Name:      "Rack A",
		IpAddress: "10.0.0.10",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateController failed: %v", err)
	}
	if err := store.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{
		IsOnline: sql.NullBool{Bool: false, Valid: true},
		ID:       ctrl.ID,
	}); err != nil {
		t.Fatalf("UpdateControllerStatus failed: %v", err)
	}

	cycleDone := make(chan struct{}, 32)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	poller := NewStatusPoller(
		store,
		svc,
		logger,
		WithIntervalUnit(time.Millisecond),
		WithMinInterval(5*time.Millisecond),
		WithOnCycle(func() {
			select {
			case cycleDone <- struct{}{}:
			default:
			}
		}),
	)

	pollCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		poller.Run(pollCtx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	// Wait for the immediate startup cycle while the controller is unreachable.
	select {
	case <-cycleDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial poller cycle")
	}

	stats, err := store.GetDashboardStats(ctx)
	if err != nil {
		t.Fatalf("GetDashboardStats failed: %v", err)
	}
	if stats.OnlineControllers != 0 || stats.TotalControllers != 1 {
		t.Fatalf("expected 0/1 online controllers after startup cycle, got %d/%d", stats.OnlineControllers, stats.TotalControllers)
	}

	// Bring controller online and verify a periodic tick promotes dashboard online count to 1.
	online.Store(true)
	deadline := time.After(2 * time.Second)
	for {
		stats, err = store.GetDashboardStats(ctx)
		if err != nil {
			t.Fatalf("GetDashboardStats failed: %v", err)
		}
		if stats.OnlineControllers == 1 {
			break
		}
		select {
		case <-cycleDone:
		case <-deadline:
			t.Fatalf("timed out waiting for periodic ping to mark controller online (online=%d/%d)", stats.OnlineControllers, stats.TotalControllers)
		}
	}
}

func TestStatusPoller_SettingsReloadAndTriggerNow(t *testing.T) {
	var online atomic.Bool
	var pingCount atomic.Int64
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		pingCount.Add(1)
		if !online.Load() {
			return nil, errors.New("offline")
		}
		body, _ := json.Marshal(map[string]any{
			"name": "WLED",
			"mac":  "AA:BB:CC:DD:EE:02",
			"leds": map[string]any{"count": 48},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	svc, store, dbConn := setupIsolatedTest(t, "poller_reload_trigger", rt)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("InitSettings failed: %v", err)
	}
	if _, err := dbConn.ExecContext(ctx, `UPDATE settings SET enable_controller_ping = 1, controller_ping_interval_seconds = 10 WHERE id = 1`); err != nil {
		t.Fatalf("update settings failed: %v", err)
	}

	if _, err := svc.CreateController(ctx, db.CreateControllerParams{
		Name:      "Rack B",
		IpAddress: "10.0.0.11",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	}); err != nil {
		t.Fatalf("CreateController failed: %v", err)
	}

	cycleDone := make(chan struct{}, 32)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	poller := NewStatusPoller(
		store,
		svc,
		logger,
		WithIntervalUnit(time.Millisecond),
		WithMinInterval(5*time.Millisecond),
		WithOnCycle(func() {
			select {
			case cycleDone <- struct{}{}:
			default:
			}
		}),
	)

	pollCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		poller.Run(pollCtx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	// Wait for startup cycle.
	select {
	case <-cycleDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial poller cycle")
	}

	// Disable periodic ping and notify poller to reload settings.
	if _, err := dbConn.ExecContext(ctx, `UPDATE settings SET enable_controller_ping = 0 WHERE id = 1`); err != nil {
		t.Fatalf("disable ping in db failed: %v", err)
	}
	poller.NotifySettingsChanged()

	// Allow any in-flight cycle to finish, then confirm no new periodic ticks happen while paused.
	time.Sleep(30 * time.Millisecond)
drainLoop:
	for {
		select {
		case <-cycleDone:
		default:
			break drainLoop
		}
	}

	pausedCount := pingCount.Load()
	select {
	case <-cycleDone:
		t.Fatalf("expected no periodic ping cycles while disabled (count moved from %d to %d)", pausedCount, pingCount.Load())
	case <-time.After(50 * time.Millisecond):
	}

	// TriggerNow must run a one-shot ping cycle even while periodic ping is disabled.
	online.Store(true)
	poller.TriggerNow()
	select {
	case <-cycleDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TriggerNow cycle")
	}
	if pingCount.Load() <= pausedCount {
		t.Fatalf("expected TriggerNow to execute a ping request (paused=%d now=%d)", pausedCount, pingCount.Load())
	}
	stats, err := store.GetDashboardStats(ctx)
	if err != nil {
		t.Fatalf("GetDashboardStats failed: %v", err)
	}
	if stats.OnlineControllers != 1 {
		t.Fatalf("expected controller online after TriggerNow, got %d", stats.OnlineControllers)
	}

	// Re-enable periodic ping with controller unreachable and verify NotifySettingsChanged triggers an immediate ping cycle.
	online.Store(false)
	if _, err := dbConn.ExecContext(ctx, `UPDATE settings SET enable_controller_ping = 1, controller_ping_interval_seconds = 10 WHERE id = 1`); err != nil {
		t.Fatalf("re-enable ping in db failed: %v", err)
	}
	poller.NotifySettingsChanged()

	deadline := time.After(2 * time.Second)
	for {
		stats, err = store.GetDashboardStats(ctx)
		if err != nil {
			t.Fatalf("GetDashboardStats failed: %v", err)
		}
		if stats.OnlineControllers == 0 {
			break
		}
		select {
		case <-cycleDone:
		case <-deadline:
			t.Fatalf("timed out waiting for re-enabled poller to mark controller offline")
		}
	}
}
