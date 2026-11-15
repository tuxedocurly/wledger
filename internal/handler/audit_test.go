package handler

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/dashboard"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func TestHandleAuditLogs(t *testing.T) {
	// Reusing openTestDB and setupTestSchema from hardware_test.go
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	session := scs.New()
	uiError := uierror.New(logger)

	auditService := audit.NewService(s)
	dashboardService := dashboard.NewService(s)
	wClient := wled.NewClient()
	wService := wled.NewService(s, wClient, logger)
	hardwareService := hardware.NewService(s, wClient, logger)
	settingsService := settings.NewService(s)

	h := &Handler{
		Logger:    logger,
		Queries:   s,
		Database:  dbConn,
		Session:   session,
		UIError:   uiError,
		Audit:     auditService,
		Dashboard: dashboardService,
		Hardware:  hardwareService,
		Settings:  settingsService,
		WLED:      wService,
	}

	ctx := context.Background()

	// Setup Users
	admin, err := s.CreateUser(ctx, db.CreateUserParams{
		Email:                  "admin@test.com",
		Role:                   "admin",
		PasswordHash:           "hash",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create admin: %v", err)
	}

	viewer, err := s.CreateUser(ctx, db.CreateUserParams{
		Email:                  "viewer@test.com",
		Role:                   "viewer",
		PasswordHash:           "hash",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create viewer: %v", err)
	}

	// Setup Data
	err = s.CreateAuditLog(ctx, db.CreateAuditLogParams{
		ActionType: "CREATE",
		EntityType: "PART",
		EntityID:   1,
		Details:    sql.NullString{String: "Created Part", Valid: true},
		OldValue:   []byte("{}"),
		NewValue:   []byte("{}"),
	})
	if err != nil {
		t.Fatalf("failed to create log 1: %v", err)
	}

	err = s.CreateAuditLog(ctx, db.CreateAuditLogParams{
		ActionType: "DELETE",
		EntityType: "BIN",
		EntityID:   2,
		Details:    sql.NullString{String: "Deleted Bin", Valid: true},
		OldValue:   []byte("{}"),
		NewValue:   []byte("{}"),
	})
	if err != nil {
		t.Fatalf("failed to create log 2: %v", err)
	}

	// Test: Admin can filter logs
	req := httptest.NewRequest(http.MethodGet, "/audit-logs?action_type=CREATE", nil)
	// Inject Admin Context
	ctx = auth.WithUser(req.Context(), auth.User{ID: admin.ID, Role: "admin"})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.HandleAuditLogs(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "Created Part") {
		t.Errorf("expected HTML to contain 'Created Part'")
	}
	if strings.Contains(body, "Deleted Bin") {
		t.Errorf("expected HTML NOT to contain 'Deleted Bin' (filtered out)")
	}

	// Test: Non-Admin is Forbidden
	req = httptest.NewRequest(http.MethodGet, "/audit-logs", nil)
	// Inject Viewer Context
	ctx = auth.WithUser(req.Context(), auth.User{ID: viewer.ID, Role: "viewer"})
	req = req.WithContext(ctx)

	rr = httptest.NewRecorder()
	h.HandleAuditLogs(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for viewer, got %d", rr.Code)
	}

	// Test: Infinite Scroll Partial Render
	req = httptest.NewRequest(http.MethodGet, "/audit-logs?scroll=true", nil)
	ctx = auth.WithUser(req.Context(), auth.User{ID: admin.ID, Role: "admin"})
	req = req.WithContext(ctx)

	rr = httptest.NewRecorder()
	h.HandleAuditLogs(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
	body = rr.Body.String()
	// Should contain rows but NOT the full page header
	if !strings.Contains(body, "Created Part") {
		t.Errorf("expected HTML to contain log entries")
	}
	if strings.Contains(body, "<html") || strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("expected partial HTML, but got full page wrapper")
	}

	// Test: Default Batch Size
	// Create > 20 logs to verify default limit
	for i := 0; i < 25; i++ {
		_ = s.CreateAuditLog(ctx, db.CreateAuditLogParams{
			ActionType: "UPDATE",
			EntityType: "PART",
			EntityID:   int64(i + 100),
			Details:    sql.NullString{String: "Bulk Log", Valid: true},
			OldValue:   []byte("{}"),
			NewValue:   []byte("{}"),
		})
	}

	req = httptest.NewRequest(http.MethodGet, "/audit-logs", nil)
	ctx = auth.WithUser(req.Context(), auth.User{ID: admin.ID, Role: "admin"})
	req = req.WithContext(ctx)

	rr = httptest.NewRecorder()
	h.HandleAuditLogs(rr, req)

	body = rr.Body.String()
	// Count occurrences of "Bulk Log"
	count := strings.Count(body, "Bulk Log")

	if count > 20 {
		t.Errorf("expected default limit of 20, but found %d items", count)
	}
}

func TestHandleAuditLogs_FiltersAndDateRange(t *testing.T) {
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	h := &Handler{
		Logger:   logger,
		Queries:  s,
		Database: dbConn,
		UIError:  uierror.New(logger),
		Audit:    audit.NewService(s),
		Settings: settings.NewService(s),
	}

	ctx := context.Background()
	admin, err := s.CreateUser(ctx, db.CreateUserParams{
		Email:        "admin@filter.test",
		Role:         "admin",
		PasswordHash: "hash",
	})
	if err != nil {
		t.Fatalf("failed to create admin: %v", err)
	}

	insertDatedLog := func(id int64, action, entity, details, ts string) {
		t.Helper()
		_, err := dbConn.ExecContext(ctx,
			`INSERT INTO audit_logs (id, user_id, action_type, entity_type, entity_id, details, old_value, new_value, created_at)
			 VALUES (?, ?, ?, ?, 1, ?, '{}', '{}', ?)`,
			id, admin.ID, action, entity, details, ts,
		)
		if err != nil {
			t.Fatalf("failed inserting dated log %d: %v", id, err)
		}
	}

	insertDatedLog(1, "STOCK_INC", "PART", "Incremented stock on Nov 10", "2026-11-10 12:00:00")
	insertDatedLog(2, "UPDATE", "HARDWARE", "Late Nov 15 WLED change", "2026-11-15 23:30:00")
	insertDatedLog(3, "STOCK_DEC", "HARDWARE", "Early Nov 16 decrement", "2026-11-16 00:00:01")

	makeReq := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: admin.ID, Role: "admin"}))
		rr := httptest.NewRecorder()
		h.HandleAuditLogs(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d", target, rr.Code)
		}
		return rr
	}

	// 1. Filter by action_type=STOCK_INC and inspect updated filter options
	body := makeReq("/audit-logs?action_type=STOCK_INC").Body.String()
	if !strings.Contains(body, "Incremented stock on Nov 10") {
		t.Error("expected STOCK_INC log in filtered output")
	}
	if strings.Contains(body, "Late Nov 15 WLED change") || strings.Contains(body, "Early Nov 16 decrement") {
		t.Error("unexpected non-STOCK_INC log in filtered output")
	}
	if strings.Contains(body, `value="ADJUST_STOCK"`) {
		t.Error("stale ADJUST_STOCK option should not be in dropdown")
	}
	for _, opt := range []string{`value="STOCK_INC"`, `value="BULK_DELETE"`, `value="RESTORE"`, `value="HARDWARE"`, `value="PROJECT"`} {
		if !strings.Contains(body, opt) {
			t.Errorf("expected dropdown option %s in audit logs page", opt)
		}
	}

	// 2. Filter by entity_type=HARDWARE
	body = makeReq("/audit-logs?entity_type=HARDWARE").Body.String()
	if !strings.Contains(body, "Late Nov 15 WLED change") || !strings.Contains(body, "Early Nov 16 decrement") {
		t.Error("expected both HARDWARE logs in entity filtered output")
	}
	if strings.Contains(body, "Incremented stock on Nov 10") {
		t.Error("unexpected PART log in HARDWARE filtered output")
	}

	// 3. Filter by inclusive start_date & end_date (2026-11-15 captures 23:30:00, excludes Nov 10 and Nov 16)
	body = makeReq("/audit-logs?start_date=2026-11-15&end_date=2026-11-15").Body.String()
	if !strings.Contains(body, "Late Nov 15 WLED change") {
		t.Error("expected end-of-day log on 2026-11-15 23:30:00 to be included for end_date=2026-11-15")
	}
	if strings.Contains(body, "Incremented stock on Nov 10") || strings.Contains(body, "Early Nov 16 decrement") {
		t.Error("expected out-of-range logs to be excluded by date range filter")
	}
	if !strings.Contains(body, `value="2026-11-15"`) {
		t.Error("expected date input values to remain populated in rendered form")
	}
	_ = time.UTC
}
