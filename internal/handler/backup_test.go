package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/backup"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/logger"
	"github.com/tuxedocurly/wledger/internal/uierror"
)

type failingBackupService struct {
	err error
}

func (f failingBackupService) Export(ctx context.Context, w io.Writer) error {
	_, _ = w.Write([]byte("corrupt partial bytes that must not reach client"))
	return f.err
}

func (f failingBackupService) Restore(ctx context.Context, r io.ReaderAt, size int64) error {
	return f.err
}

func TestHandleBackupDownload_ReturnsHTTPErrorBeforeZipHeaders(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{
		Logger:  log,
		UIError: uierror.New(log),
		Backup:  failingBackupService{err: errors.New("simulated export failure")},
	}

	req := httptest.NewRequest(http.MethodGet, "/settings/backup/download", nil)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: 1, Role: "admin"}))

	rr := httptest.NewRecorder()
	h.HandleBackupDownload(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 Internal Server Error, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); strings.Contains(ct, "application/zip") {
		t.Errorf("expected no application/zip Content-Type on failed export, got %q", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); cd != "" {
		t.Errorf("expected empty Content-Disposition on failed export, got %q", cd)
	}
	if strings.Contains(rr.Body.String(), "corrupt partial") {
		t.Error("expected partial ZIP bytes NOT written to HTTP response on failure")
	}
}

func TestHandleBackupDownload_Success(t *testing.T) {
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	uploadsDir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bSvc := backup.NewService(dbConn, s, uploadsDir, log)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("InitSettings failed: %v", err)
	}

	h := &Handler{
		Logger:   log,
		Queries:  s,
		Database: dbConn,
		UIError:  uierror.New(log),
		Backup:   bSvc,
	}

	req := httptest.NewRequest(http.MethodGet, "/settings/backup/download", nil)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: 1, Role: "admin"}))
	rr := httptest.NewRecorder()
	h.HandleBackupDownload(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("expected Content-Type application/zip, got %q", ct)
	}
	if _, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len())); err != nil {
		t.Fatalf("expected valid zip archive in response, got error: %v", err)
	}
}

func TestHandleBackupRestore_DestroysActiveSessionAndSetsHXRefresh(t *testing.T) {
	defer logger.SetDebug(false)

	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("InitSettings failed: %v", err)
	}

	admin, err := s.CreateUser(ctx, db.CreateUserParams{
		Email:        "admin@test.local",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	sessionManager := scs.New()
	sessionManager.Store = auth.NewStore(s)
	sessionManager.Lifetime = time.Hour

	uploadsDir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bSvc := backup.NewService(dbConn, s, uploadsDir, log)

	h := &Handler{
		Logger:   log,
		Queries:  s,
		Database: dbConn,
		Session:  sessionManager,
		UIError:  uierror.New(log),
		Backup:   bSvc,
	}

	// Step 1: Establish an active SCS session via LoadAndSave
	mux := http.NewServeMux()
	mux.HandleFunc("/login-test", func(w http.ResponseWriter, r *http.Request) {
		sessionManager.Put(r.Context(), config.SessionKeyUserID, admin.ID)
		sessionManager.Put(r.Context(), config.SessionKeyRole, admin.Role)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/settings/backup/restore", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: admin.ID, Role: admin.Role}))
		h.HandleBackupRestore(w, r)
	})
	mux.HandleFunc("/whoami-test", func(w http.ResponseWriter, r *http.Request) {
		uid := sessionManager.GetInt64(r.Context(), config.SessionKeyUserID)
		if uid == 0 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	handlerChain := sessionManager.LoadAndSave(mux)

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/login-test", nil)
	handlerChain.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login failed: %d", loginRec.Code)
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie after login")
	}

	var preSessionCount int
	if err := dbConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&preSessionCount); err != nil {
		t.Fatalf("failed querying sessions count: %v", err)
	}
	if preSessionCount != 1 {
		t.Fatalf("expected 1 active session before restore, got %d", preSessionCount)
	}

	// Step 2: Export a valid backup ZIP
	var zipBuf bytes.Buffer
	if err := bSvc.Export(ctx, &zipBuf); err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	// Make sure Export produces valid manifest zip for upload
	if _, err := zip.NewReader(bytes.NewReader(zipBuf.Bytes()), int64(zipBuf.Len())); err != nil {
		t.Fatalf("invalid exported zip: %v", err)
	}

	var bodyBuf bytes.Buffer
	mw := multipart.NewWriter(&bodyBuf)
	part, err := mw.CreateFormFile("backup_file", "backup.zip")
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := part.Write(zipBuf.Bytes()); err != nil {
		t.Fatalf("write backup part failed: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close failed: %v", err)
	}

	// Step 3: POST /settings/backup/restore with active session cookie
	restoreReq := httptest.NewRequest(http.MethodPost, "/settings/backup/restore", &bodyBuf)
	restoreReq.Header.Set("Content-Type", mw.FormDataContentType())
	restoreReq.AddCookie(cookies[0])

	restoreRec := httptest.NewRecorder()
	handlerChain.ServeHTTP(restoreRec, restoreReq)

	if restoreRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from restore, got %d (%s)", restoreRec.Code, restoreRec.Body.String())
	}
	if got := restoreRec.Header().Get("HX-Refresh"); got != "true" {
		t.Errorf("expected HX-Refresh: true header, got %q", got)
	}

	// SCS Destroy clears cookie (MaxAge < 0 or Value == "")
	var cookieInvalidated bool
	for _, c := range restoreRec.Result().Cookies() {
		if c.Name == sessionManager.Cookie.Name && (c.MaxAge <= 0 || c.Value == "") {
			cookieInvalidated = true
		}
	}
	if !cookieInvalidated {
		t.Errorf("expected invalidated Set-Cookie for session %q, got cookies: %+v", sessionManager.Cookie.Name, restoreRec.Result().Cookies())
	}

	var postSessionCount int
	if err := dbConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&postSessionCount); err != nil {
		t.Fatalf("failed querying post-restore sessions count: %v", err)
	}
	if postSessionCount != 0 {
		t.Errorf("expected 0 rows in sessions after restore, got %d", postSessionCount)
	}

	// Reusing old session cookie MUST NOT authenticate!
	verifyRec := httptest.NewRecorder()
	verifyReq := httptest.NewRequest(http.MethodGet, "/whoami-test", nil)
	verifyReq.AddCookie(cookies[0])
	handlerChain.ServeHTTP(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized when reusing old cookie post-restore, got %d", verifyRec.Code)
	}
}
