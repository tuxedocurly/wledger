package handler

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/i18n"
	"github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/web/components"
	"github.com/tuxedocurly/wledger/web/pages"
	"golang.org/x/crypto/bcrypt"
)

func newAuthSettingsTestHandler(t *testing.T) (*Handler, db.Store, *scs.SessionManager, *sql.DB) {
	t.Helper()
	dbConn := openTestDB(t)
	setupTestSchema(t, dbConn)

	store := db.NewStore(dbConn)
	log := slog.New(slog.DiscardHandler)
	sm := scs.New()
	sm.Store = auth.NewStore(store)
	uiErr := uierror.New(log)
	settSvc := settings.NewService(store)
	auditSvc := audit.NewService(store)

	h := &Handler{
		Logger:   log,
		Queries:  store,
		Database: dbConn,
		Session:  sm,
		UIError:  uiErr,
		Settings: settSvc,
		Audit:    auditSvc,
	}
	return h, store, sm, dbConn
}

func postForm(h http.Handler, path string, vals url.Values, htmx bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandleSetupPost_PasswordValidation(t *testing.T) {
	h, store, sm, dbConn := newAuthSettingsTestHandler(t)
	defer dbConn.Close()

	wrapped := sm.LoadAndSave(http.HandlerFunc(h.HandleSetupPost))

	for _, bad := range []string{"", "short7!", strings.Repeat("a", 73)} {
		rec := postForm(wrapped, "/setup", url.Values{
			"email":    {"admin@example.com"},
			"password": {bad},
		}, false)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("setup password %d bytes expected 400, got %d", len(bad), rec.Code)
		}
	}

	cnt, err := store.CountUsers(context.Background())
	if err != nil || cnt != 0 {
		t.Fatalf("expected 0 users after rejected setup attempts, got %d (err=%v)", cnt, err)
	}

	recOK := postForm(wrapped, "/setup", url.Values{
		"email":    {"admin@example.com"},
		"password": {"valid-setup-pass"},
	}, false)
	if recOK.Code != http.StatusSeeOther || recOK.Header().Get("Location") != "/" {
		t.Fatalf("valid setup expected 303 -> /, got %d (%q)", recOK.Code, recOK.Header().Get("Location"))
	}

	user, err := store.GetUserByEmail(context.Background(), "admin@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(user.PasswordHash))
	if err != nil || cost != auth.BcryptPasswordCost {
		t.Fatalf("setup hash cost = %d (err=%v), want %d", cost, err, auth.BcryptPasswordCost)
	}
}

func TestHandleForceResetPost_PasswordValidation(t *testing.T) {
	h, store, sm, dbConn := newAuthSettingsTestHandler(t)
	defer dbConn.Close()

	hash, _ := auth.HashPassword("initial-temp-1")
	u, err := store.CreateUser(context.Background(), db.CreateUserParams{
		Email:                  "reset@example.com",
		PasswordHash:           hash,
		Role:                   "editor",
		ChangePasswordRequired: sql.NullBool{Bool: true, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	wrapped := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sm.Put(r.Context(), config.SessionKeyUserID, u.ID)
		h.HandleForceResetPost(w, r)
	}))

	for _, tc := range []struct {
		name, newPw, confirmPw string
	}{
		{"empty", "", ""},
		{"short", "1234567", "1234567"},
		{"too_long", strings.Repeat("x", 73), strings.Repeat("x", 73)},
		{"mismatch", "valid-pass-1", "valid-pass-2"},
	} {
		rec := postForm(wrapped, "/force-reset", url.Values{
			"new_password":     {tc.newPw},
			"confirm_password": {tc.confirmPw},
		}, false)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s expected 400, got %d", tc.name, rec.Code)
		}
	}

	recOK := postForm(wrapped, "/force-reset", url.Values{
		"new_password":     {"new-valid-password"},
		"confirm_password": {"new-valid-password"},
	}, false)
	if recOK.Code != http.StatusSeeOther || recOK.Header().Get("Location") != "/" {
		t.Fatalf("valid force-reset expected 303 -> /, got %d", recOK.Code)
	}

	updated, err := store.GetUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if updated.ChangePasswordRequired.Bool {
		t.Fatal("expected ChangePasswordRequired cleared after force-reset")
	}
	if !auth.CheckPassword("new-valid-password", updated.PasswordHash) {
		t.Fatal("updated password did not verify")
	}
}

func TestHandleUserCreate_PasswordValidation(t *testing.T) {
	h, store, sm, dbConn := newAuthSettingsTestHandler(t)
	defer dbConn.Close()

	wrapped := sm.LoadAndSave(http.HandlerFunc(h.HandleUserCreate))

	for _, bad := range []string{"", "tiny7", strings.Repeat("z", 73)} {
		rec := postForm(wrapped, "/settings/users", url.Values{
			"email":         {"user@example.com"},
			"role":          {"viewer"},
			"temp_password": {bad},
		}, false)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("create user %d bytes password expected 400, got %d", len(bad), rec.Code)
		}
	}

	cnt, _ := store.CountUsers(context.Background())
	if cnt != 0 {
		t.Fatalf("expected 0 created users after validation failures, got %d", cnt)
	}
}

func TestHandleSettingsPassword_ValidationAndErrorFeedback(t *testing.T) {
	h, store, sm, dbConn := newAuthSettingsTestHandler(t)
	defer dbConn.Close()

	hash, err := auth.HashPassword("current-secret-1")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(context.Background(), db.CreateUserParams{
		Email:        "self@example.com",
		PasswordHash: hash,
		Role:         "admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	wrapped := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sm.Put(r.Context(), config.SessionKeyUserID, u.ID)
		h.HandleSettingsPassword(w, r)
	}))

	// Wrong current_password via HTMX must return 400 error toast (never silent 303 redirect).
	recWrongHTMX := postForm(wrapped, "/settings/password", url.Values{
		"current_password": {"wrong-password"},
		"new_password":     {"new-valid-secret"},
		"confirm_password": {"new-valid-secret"},
	}, true)
	if recWrongHTMX.Code != http.StatusBadRequest {
		t.Fatalf("wrong current_password HTMX expected 400, got %d", recWrongHTMX.Code)
	}
	if recWrongHTMX.Header().Get("HX-Reswap") != "none" {
		t.Fatalf("expected HX-Reswap: none on HTMX error toast, got %q", recWrongHTMX.Header().Get("HX-Reswap"))
	}
	if !strings.Contains(recWrongHTMX.Body.String(), "Current password is incorrect") {
		t.Fatalf("expected toast error message in body, got %q", recWrongHTMX.Body.String())
	}

	// Wrong current_password non-HTMX must also return 400 error page (not silent redirect).
	recWrongPlain := postForm(wrapped, "/settings/password", url.Values{
		"current_password": {"wrong-password"},
		"new_password":     {"new-valid-secret"},
		"confirm_password": {"new-valid-secret"},
	}, false)
	if recWrongPlain.Code != http.StatusBadRequest {
		t.Fatalf("wrong current_password plain POST expected 400, got %d", recWrongPlain.Code)
	}

	// Short / empty / >72B / mismatched new passwords must return 400.
	for _, tc := range []struct {
		name, newPw, confirmPw string
	}{
		{"empty", "", ""},
		{"short", "abc1234", "abc1234"},
		{"too_long", strings.Repeat("p", 73), strings.Repeat("p", 73)},
		{"mismatch", "valid-secret-a", "valid-secret-b"},
	} {
		rec := postForm(wrapped, "/settings/password", url.Values{
			"current_password": {"current-secret-1"},
			"new_password":     {tc.newPw},
			"confirm_password": {tc.confirmPw},
		}, true)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s expected 400, got %d", tc.name, rec.Code)
		}
	}

	// Valid HTMX password change returns 200 with HX-Redirect: /settings and updates hash at cost 12.
	recSuccess := postForm(wrapped, "/settings/password", url.Values{
		"current_password": {"current-secret-1"},
		"new_password":     {"brand-new-secret-2"},
		"confirm_password": {"brand-new-secret-2"},
	}, true)
	if recSuccess.Code != http.StatusOK || recSuccess.Header().Get("HX-Redirect") != "/settings" {
		t.Fatalf("valid HTMX change expected 200 HX-Redirect:/settings, got %d loc=%q", recSuccess.Code, recSuccess.Header().Get("HX-Redirect"))
	}

	updated, err := store.GetUser(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword("brand-new-secret-2", updated.PasswordHash) {
		t.Fatal("password was not updated")
	}
	cost, err := bcrypt.Cost([]byte(updated.PasswordHash))
	if err != nil || cost != auth.BcryptPasswordCost {
		t.Fatalf("updated hash cost = %d (err=%v), want %d", cost, err, auth.BcryptPasswordCost)
	}
}

func TestTemplates_SafeConfirmAndNoHardcodedDefaultPassword(t *testing.T) {
	t.Cleanup(func() { _ = i18n.Init() })
	localeFS := fstest.MapFS{
		"active.en.json": &fstest.MapFile{
			Data: []byte(`{
				"CurrentPassword": "Current Password",
				"NewPassword": "New Password",
				"PasswordMinLength": "Minimum 8 characters",
				"ConfirmNewPassword": "Confirm New Password",
				"ReEnterPassword": "Re-enter",
				"PasswordsDoNotMatch": "Passwords don't match",
				"UpdatePassword": "Update",
				"Settings": "Settings",
				"SystemSettings": "System Settings",
				"SystemSettingsDesc": "Desc",
				"Users": "Users",
				"AddUser": "Add User",
				"Email": "Email",
				"Role": "Role",
				"Status": "Status",
				"Actions": "Actions",
				"RoleAdmin": "Admin",
				"RoleEditor": "Editor",
				"RoleViewer": "Viewer",
				"PasswordResetPending": "Pending",
				"Active": "Active",
				"ForceReset": "Reset",
				"Reset": "Reset",
				"Delete": "Delete",
				"ConfirmForceReset": "Reset user's password?",
				"ConfirmDeleteUser": "Delete user's account?",
				"General": "General",
				"RequireReadAuthLabel": "Require Read Auth",
				"EnableDebugLogs": "Debug Logs",
				"EnableDebugLogsDescription": "Enable debug",
				"HardwareAutomation": "Hardware",
				"AutoTurnOffLocateLED": "Auto Off",
				"TurnOffAfter": "After",
				"Seconds": "sec",
				"CustomIndicatorColors": "Colors",
				"SaveAllSettings": "Save",
				"DangerZone": "Danger",
				"FactoryResetBackup": "Backup",
				"BackupAndRestore": "Backup and Restore",
				"BackupWarning": "Warn",
				"DownloadBackupDB": "Download",
				"RestoreBackupDB": "Restore",
				"ConfirmRestore": "Restore backup's data?",
				"Restore": "Restore",
				"NewUser": "New User",
				"EmailAddress": "Email Address",
				"TemporaryPassword": "Temp Password",
				"UserMustChangePasswordWarning": "Must change",
				"Cancel": "Cancel",
				"CreateUser": "Create"
			}`),
		},
	}
	if err := i18n.InitWithFS(localeFS); err != nil {
		t.Fatalf("i18n.InitWithFS: %v", err)
	}
	ctx := i18n.WithLocalizer(context.Background(), "en")

	var pwBuf bytes.Buffer
	if err := components.ChangePasswordForm("/settings/password", true).Render(ctx, &pwBuf); err != nil {
		t.Fatalf("render ChangePasswordForm: %v", err)
	}
	pwHTML := pwBuf.String()
	if !strings.Contains(pwHTML, `hx-post="/settings/password"`) {
		t.Fatal("expected hx-post attribute on ChangePasswordForm when showCurrentPassword=true")
	}
	if !strings.Contains(pwHTML, `data-mismatch=`) || !strings.Contains(pwHTML, `this.dataset.mismatch`) {
		t.Fatal("expected data-mismatch / this.dataset.mismatch safe attribute usage")
	}
	if !strings.Contains(pwHTML, `maxlength="72"`) {
		t.Fatal("expected maxlength=72 on password inputs")
	}

	var setBuf bytes.Buffer
	err := pages.Settings(
		auth.User{ID: 1, Role: "admin"},
		db.Setting{},
		[]db.ListUsersRow{
			{ID: 1, Email: "a@b.c", Role: "admin"},
			{ID: 2, Email: "e@b.c", Role: "editor"},
		},
	).Render(ctx, &setBuf)
	if err != nil {
		t.Fatalf("render Settings: %v", err)
	}
	setHTML := setBuf.String()
	if strings.Contains(setHTML, "Wledger123!") {
		t.Fatal("hardcoded default password Wledger123! must be removed from settings template")
	}
	if !strings.Contains(setHTML, `data-confirm=`) || !strings.Contains(setHTML, `return confirm(this.dataset.confirm)`) {
		t.Fatal("expected data-confirm / this.dataset.confirm on delete/reset confirm forms")
	}
	if strings.Contains(setHTML, "return confirm('") {
		t.Fatal("unsafe single-quoted inline confirm() string interpolation still present")
	}
}
