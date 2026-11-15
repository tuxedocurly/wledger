package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func TestHandleSettingsUpdate(t *testing.T) {
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	session := scs.New()
	uiError := uierror.New(logger)
	wClient := wled.NewClient()
	wService := wled.NewService(s, wClient, logger)
	hwService := hardware.NewService(s, wClient, logger)
	settService := settings.NewService(s)

	// Initialize settings
	s.InitSettings(context.Background())

	h := &Handler{
		Logger:   logger,
		Queries:  s,
		Database: dbConn,
		Session:  session,
		UIError:  uiError,
		Hardware: hwService,
		Settings: settService,
		WLED:     wService,
	}

	// Verify initial state (should be false)
	settings, _ := s.GetSettings(context.Background())
	if settings.EnableDebugLogs.Bool {
		t.Fatal("expected EnableDebugLogs to be false initially")
	}

	// Perform update
	form := url.Values{}
	form.Add("require_auth", "on")
	form.Add("enable_timeout", "on")
	form.Add("locate_timeout", "15")
	form.Add("enable_debug_logs", "on")
	form.Add("color_locate", "#0000FF")
	form.Add("color_ok", "#00FF00")
	form.Add("color_low", "#FFFF00")
	form.Add("color_critical", "#FF0000")
	form.Add("controller_ping_interval", "120")

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.HandleSettingsUpdate(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d", rr.Code)
	}

	// Verify final state
	settings, _ = s.GetSettings(context.Background())
	if !settings.EnableDebugLogs.Bool {
		t.Error("expected EnableDebugLogs to be true after update")
	}
	if settings.LocateTimeoutSeconds.Int64 != 15 {
		t.Errorf("expected locate_timeout to be 15, got %d", settings.LocateTimeoutSeconds.Int64)
	}
	if !settings.EnableControllerPing.Valid || settings.EnableControllerPing.Bool {
		t.Errorf("expected EnableControllerPing false when checkbox omitted with ping interval form field, got %+v", settings.EnableControllerPing)
	}
	if settings.ControllerPingIntervalSeconds.Int64 != 120 {
		t.Errorf("expected controller_ping_interval to be 120, got %d", settings.ControllerPingIntervalSeconds.Int64)
	}

	// Verify clamping below minimum (3 -> 10) when re-enabling ping.
	formLow := url.Values{}
	formLow.Add("enable_controller_ping", "on")
	formLow.Add("controller_ping_interval", "3")
	reqLow := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(formLow.Encode()))
	reqLow.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLow := httptest.NewRecorder()
	h.HandleSettingsUpdate(rrLow, reqLow)
	if rrLow.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on low interval update, got %d", rrLow.Code)
	}
	settings, _ = s.GetSettings(context.Background())
	if !settings.EnableControllerPing.Bool || settings.ControllerPingIntervalSeconds.Int64 != 10 {
		t.Errorf("expected enabled=true clamped interval=10, got enabled=%+v interval=%d", settings.EnableControllerPing, settings.ControllerPingIntervalSeconds.Int64)
	}

	// Verify clamping above maximum (999999 -> 86400).
	formHigh := url.Values{}
	formHigh.Add("enable_controller_ping", "on")
	formHigh.Add("controller_ping_interval", "999999")
	reqHigh := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(formHigh.Encode()))
	reqHigh.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrHigh := httptest.NewRecorder()
	h.HandleSettingsUpdate(rrHigh, reqHigh)
	if rrHigh.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on high interval update, got %d", rrHigh.Code)
	}
	settings, _ = s.GetSettings(context.Background())
	if settings.ControllerPingIntervalSeconds.Int64 != 86400 {
		t.Errorf("expected interval clamped to 86400, got %d", settings.ControllerPingIntervalSeconds.Int64)
	}

	// Partial form POST omitting controller ping fields must preserve existing ping configuration.
	partial := url.Values{}
	partial.Add("require_auth", "on")
	partial.Add("locate_timeout", "20")
	reqPartial := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(partial.Encode()))
	reqPartial.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrPartial := httptest.NewRecorder()
	h.HandleSettingsUpdate(rrPartial, reqPartial)
	if rrPartial.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on partial update, got %d", rrPartial.Code)
	}
	settings, _ = s.GetSettings(context.Background())
	if !settings.EnableControllerPing.Bool || settings.ControllerPingIntervalSeconds.Int64 != 86400 {
		t.Errorf("expected partial POST to preserve ping settings (true, 86400), got enabled=%+v interval=%d", settings.EnableControllerPing, settings.ControllerPingIntervalSeconds.Int64)
	}
}

func TestSettingsAuditLogging(t *testing.T) {
	// Setup
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	session := scs.New()
	uiError := uierror.New(logger)
	wClient := wled.NewClient()
	wService := wled.NewService(s, wClient, logger)
	hwService := hardware.NewService(s, wClient, logger)
	settService := settings.NewService(s)

	h := &Handler{
		Logger:   logger,
		Queries:  s,
		Database: dbConn,
		Session:  session,
		UIError:  uiError,
		Hardware: hwService,
		Settings: settService,
		WLED:     wService,
	}

	s.InitSettings(context.Background())

	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Update Settings
	r := chi.NewRouter()
	r.Post("/settings", h.HandleSettingsUpdate)

	// change something from default (require_auth is default true, debug false)
	form := url.Values{}
	form.Add("require_auth", "off")
	form.Add("enable_debug_logs", "on")
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	logs, _ := s.GetAllAuditLogs(ctx)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log after settings update, got %d", len(logs))
	}
	// Clear logs
	dbConn.ExecContext(ctx, "DELETE FROM audit_logs")

	req1b := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req1b.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req1b = req1b.WithContext(ctx)
	rr1b := httptest.NewRecorder()
	r.ServeHTTP(rr1b, req1b)

	logsNoChange, _ := s.GetAllAuditLogs(ctx)
	if len(logsNoChange) != 0 {
		t.Errorf("expected 0 logs after no-op update, got %d", len(logsNoChange))
	}

	// Create User
	// Clear logs again to avoid counting old logs
	dbConn.ExecContext(ctx, "DELETE FROM audit_logs")

	r2 := chi.NewRouter()
	r2.Post("/settings/users", h.HandleUserCreate)
	form2 := url.Values{}
	form2.Add("email", "newuser@test.com")
	form2.Add("role", "viewer")
	form2.Add("temp_password", "password123")

	req2 := httptest.NewRequest(http.MethodPost, "/settings/users", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2 = req2.WithContext(ctx)
	rr2 := httptest.NewRecorder()
	r2.ServeHTTP(rr2, req2)

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log after user create, got %d", len(logs))
	}
	userLog := logs[0]
	var userNew map[string]any
	json.Unmarshal(userLog.NewValue, &userNew)
	if userNew["email"] != "newuser@test.com" {
		t.Errorf("expected summary in user create log")
	}

	userDeleteHandler := session.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session.Put(r.Context(), config.SessionKeyUserID, int64(1))
		h.HandleUserDelete(w, r)
	}))

	r3 := chi.NewRouter()
	r3.Post("/settings/users/{id}/delete", userDeleteHandler.ServeHTTP)

	req3 := httptest.NewRequest(http.MethodPost, "/settings/users/2/delete", nil)
	req3 = req3.WithContext(ctx)
	rr3 := httptest.NewRecorder()
	r3.ServeHTTP(rr3, req3)

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs after user delete, got %d", len(logs))
	}
	userDeleteLog := logs[1]
	var userDelOld map[string]any
	json.Unmarshal(userDeleteLog.OldValue, &userDelOld)
	if userDelOld["email"] != "newuser@test.com" {
		t.Errorf("expected summary in user delete log")
	}
}
