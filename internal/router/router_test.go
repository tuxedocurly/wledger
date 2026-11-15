package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/api"
	"github.com/tuxedocurly/wledger/internal/apitoken"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/barcodes"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/handler"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/i18n"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/projects"
	"github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/internal/stock"
	"github.com/tuxedocurly/wledger/internal/tags"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

type routerTestEnv struct {
	store   db.Store
	dbConn  *sql.DB
	mw      *middleware.Manager
	sm      *scs.SessionManager
	handler *handler.Handler
	uploads string
}

func setupRouterTestEnv(t *testing.T, requireAuth bool) *routerTestEnv {
	t.Helper()
	middleware.ResetSetupCache()

	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	store := db.NewStore(conn)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("InitSettings: %v", err)
	}
	if err := store.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
		RequireAuthForRead:   sql.NullBool{Bool: requireAuth, Valid: true},
		LocateTimeoutSeconds: sql.NullInt64{Int64: 10, Valid: true},
		EnableLocateTimeout:  sql.NullBool{Bool: true, Valid: true},
	}); err != nil {
		t.Fatalf("UpdateGeneralSettings: %v", err)
	}
	if err := store.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate:        sql.NullString{String: "#0000FF", Valid: true},
		ColorStockOk:       sql.NullString{String: "#00FF00", Valid: true},
		ColorStockLow:      sql.NullString{String: "#FFA500", Valid: true},
		ColorStockCritical: sql.NullString{String: "#FF0000", Valid: true},
	}); err != nil {
		t.Fatalf("UpdateColors: %v", err)
	}

	hash, err := auth.HashPassword("admin-secret-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := store.CreateUser(ctx, db.CreateUserParams{
		Email:                  "admin@example.com",
		PasswordHash:           hash,
		Role:                   "admin",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	log := slog.New(slog.DiscardHandler)
	sm := scs.New()
	sm.Store = auth.NewStore(store)
	uiErr := uierror.New(log)
	mw := middleware.New(store, sm, log, uiErr)

	auditSvc := audit.NewService(store)
	settingsSvc := settings.NewService(store)
	docSvc := documents.NewService(store, log)
	partsSvc := parts.NewService(conn, store, log, nil, docSvc)
	h := &handler.Handler{
		Logger:    log,
		Queries:   store,
		Database:  conn,
		Session:   sm,
		UIError:   uiErr,
		Audit:     auditSvc,
		Settings:  settingsSvc,
		Documents: docSvc,
		Parts:     partsSvc,
	}

	uploadsDir := t.TempDir()
	return &routerTestEnv{
		store:   store,
		dbConn:  conn,
		mw:      mw,
		sm:      sm,
		handler: h,
		uploads: uploadsDir,
	}
}

func loginSessionCookie(t *testing.T, r http.Handler, email, password string) *http.Cookie {
	t.Helper()
	form := strings.NewReader("email=" + email + "&password=" + password)
	req := httptest.NewRequest(http.MethodPost, "/login", form)
	req.Host = "example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.RemoteAddr = "192.0.2.50:1234"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login expected 303, got %d", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	t.Fatal("missing session cookie after login")
	return nil
}

func TestRouter_StaticServedWithoutSessionOrDBAndNoDirListing(t *testing.T) {
	env := setupRouterTestEnv(t, true)
	defer env.dbConn.Close()

	// Remove all users so FirstRunCheck would redirect any app route to /setup.
	if _, err := env.dbConn.Exec("DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	middleware.ResetSetupCache()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := NewWithOptions(ctx, env.mw, env.sm, env.handler, Options{UploadsDir: env.uploads})

	// Known embedded static file loads cleanly with 200 and emits no session Set-Cookie.
	req := httptest.NewRequest(http.MethodGet, "/static/css/output.css", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for embedded static css, got %d", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatalf("expected no session cookies on /static/*, got %v", rec.Result().Cookies())
	}

	// Directory listings under /static/ must return 404.
	for _, dirPath := range []string{"/static/", "/static/css/"} {
		dReq := httptest.NewRequest(http.MethodGet, dirPath, nil)
		dRec := httptest.NewRecorder()
		r.ServeHTTP(dRec, dReq)
		if dRec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for static directory %q, got %d", dirPath, dRec.Code)
		}
	}
}

func TestRouter_CSRFProtection(t *testing.T) {
	env := setupRouterTestEnv(t, true)
	defer env.dbConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := NewWithOptions(ctx, env.mw, env.sm, env.handler, Options{UploadsDir: env.uploads})

	// Cross-origin browser POST to /login must be blocked with 403 Forbidden.
	crossOrigin := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=a&password=b"))
	crossOrigin.Host = "wledger.local"
	crossOrigin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	crossOrigin.Header.Set("Sec-Fetch-Site", "cross-site")
	crossOrigin.Header.Set("Origin", "https://attacker.example")
	recCross := httptest.NewRecorder()
	r.ServeHTTP(recCross, crossOrigin)
	if recCross.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST /login expected 403, got %d", recCross.Code)
	}

	// Origin-only mismatch (no Sec-Fetch-Site) must also be blocked.
	originMismatch := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=a&password=b"))
	originMismatch.Host = "wledger.local"
	originMismatch.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	originMismatch.Header.Set("Origin", "https://attacker.example")
	recOrigin := httptest.NewRecorder()
	r.ServeHTTP(recOrigin, originMismatch)
	if recOrigin.Code != http.StatusForbidden {
		t.Fatalf("mismatched Origin POST /login expected 403, got %d", recOrigin.Code)
	}

	// Same-origin POST succeeds (redirects 303 on valid credentials).
	cookie := loginSessionCookie(t, r, "admin@example.com", "admin-secret-123")
	if cookie == nil {
		t.Fatal("expected valid same-origin login")
	}

	// Verify CSRF middleware helper directly for Bearer /api/v1/* bypass vs cookie/cross-site block.
	apiProtected := csrfProtectionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Cross-site POST to /api/v1/* WITH Bearer token bypasses CSRF.
	bearerReq := httptest.NewRequest(http.MethodPost, "/api/v1/parts", nil)
	bearerReq.Host = "wledger.local"
	bearerReq.Header.Set("Sec-Fetch-Site", "cross-site")
	bearerReq.Header.Set("Origin", "https://attacker.example")
	bearerReq.Header.Set("Authorization", "Bearer tok_123")
	recBearer := httptest.NewRecorder()
	apiProtected.ServeHTTP(recBearer, bearerReq)
	if recBearer.Code != http.StatusOK {
		t.Fatalf("Bearer /api/v1/* POST expected 200 CSRF bypass, got %d", recBearer.Code)
	}

	// Cross-site POST to /api/v1/* WITHOUT Bearer token must still be blocked with 403.
	noBearerReq := httptest.NewRequest(http.MethodPost, "/api/v1/parts", nil)
	noBearerReq.Host = "wledger.local"
	noBearerReq.Header.Set("Sec-Fetch-Site", "cross-site")
	noBearerReq.Header.Set("Origin", "https://attacker.example")
	recNoBearer := httptest.NewRecorder()
	apiProtected.ServeHTTP(recNoBearer, noBearerReq)
	if recNoBearer.Code != http.StatusForbidden {
		t.Fatalf("non-Bearer cross-site POST /api/v1/* expected 403, got %d", recNoBearer.Code)
	}
}

func TestRouter_RealIPGatedBehindTrustProxy(t *testing.T) {
	env := setupRouterTestEnv(t, false)
	defer env.dbConn.Close()

	runWithTrustProxy := func(trust bool) string {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := NewWithOptions(ctx, env.mw, env.sm, env.handler, Options{
			TrustProxy: trust,
			UploadsDir: env.uploads,
		})
		var seenRemoteAddr string
		r.Get("/probe-ip", func(w http.ResponseWriter, req *http.Request) {
			seenRemoteAddr = req.RemoteAddr
			w.WriteHeader(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodGet, "/probe-ip", nil)
		req.RemoteAddr = "10.0.0.5:4321"
		req.Header.Set("X-Forwarded-For", "203.0.113.9")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return seenRemoteAddr
	}

	if got := runWithTrustProxy(false); got != "10.0.0.5:4321" {
		t.Fatalf("TrustProxy=false should preserve RemoteAddr, got %q", got)
	}
	if got := runWithTrustProxy(true); got != "203.0.113.9" {
		t.Fatalf("TrustProxy=true should apply X-Forwarded-For via RealIP, got %q", got)
	}
}

func TestRouter_LoginRateLimit429(t *testing.T) {
	env := setupRouterTestEnv(t, true)
	defer env.dbConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	limiter := middleware.NewIPRateLimiter(ctx, 0.01, 2, time.Minute)
	r := NewWithOptions(ctx, env.mw, env.sm, env.handler, Options{
		UploadsDir:       env.uploads,
		LoginRateLimiter: limiter,
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=admin@example.com&password=wrong"))
		req.Host = "localhost"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.RemoteAddr = "198.51.100.44:1001"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d unexpectedly rate limited", i+1)
		}
	}

	reqLimited := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=admin@example.com&password=wrong"))
	reqLimited.Host = "localhost"
	reqLimited.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqLimited.Header.Set("Sec-Fetch-Site", "same-origin")
	reqLimited.RemoteAddr = "198.51.100.44:2002"
	recLimited := httptest.NewRecorder()
	r.ServeHTTP(recLimited, reqLimited)
	if recLimited.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd POST /login expected 429 Too Many Requests, got %d", recLimited.Code)
	}
}

func TestRouter_UploadsSecurityAndHeaders(t *testing.T) {
	env := setupRouterTestEnv(t, true)
	defer env.dbConn.Close()

	docsDir := filepath.Join(env.uploads, "docs")
	imagesDir := filepath.Join(env.uploads, "images")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "notes.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "manual.pdf"), []byte("%PDF-1.4\n%test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imagesDir, "part.jpg"), []byte("imgdata"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := NewWithOptions(ctx, env.mw, env.sm, env.handler, Options{UploadsDir: env.uploads})

	// 1. Unauthenticated guest must be redirected to /login when RequireAuth=true.
	for _, p := range []string{"/uploads/docs/notes.txt", "/uploads/images/part.jpg"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Fatalf("guest GET %s expected redirect to /login, got %d (%q)", p, rec.Code, rec.Header().Get("Location"))
		}
	}

	cookie := loginSessionCookie(t, r, "admin@example.com", "admin-secret-123")

	// 2. Directory listings under /uploads/* must return 404.
	for _, dirPath := range []string{"/uploads/", "/uploads/docs/", "/uploads/docs", "/uploads/images/"} {
		req := httptest.NewRequest(http.MethodGet, dirPath, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("authenticated GET %q directory expected 404, got %d", dirPath, rec.Code)
		}
	}

	// 3. Non-PDF/non-image doc sets nosniff, sandbox CSP, and Content-Disposition: attachment.
	reqTxt := httptest.NewRequest(http.MethodGet, "/uploads/docs/notes.txt", nil)
	reqTxt.AddCookie(cookie)
	recTxt := httptest.NewRecorder()
	r.ServeHTTP(recTxt, reqTxt)
	if recTxt.Code != http.StatusOK {
		t.Fatalf("GET notes.txt expected 200, got %d", recTxt.Code)
	}
	if got := recTxt.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options=nosniff, got %q", got)
	}
	if got := recTxt.Header().Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
		t.Fatalf("expected CSP sandbox, got %q", got)
	}
	if got := recTxt.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Fatalf("expected Content-Disposition attachment for .txt, got %q", got)
	}

	// 4. PDF doc sets nosniff + sandbox CSP without forcing Content-Disposition: attachment.
	reqPDF := httptest.NewRequest(http.MethodGet, "/uploads/docs/manual.pdf", nil)
	reqPDF.AddCookie(cookie)
	recPDF := httptest.NewRecorder()
	r.ServeHTTP(recPDF, reqPDF)
	if recPDF.Code != http.StatusOK {
		t.Fatalf("GET manual.pdf expected 200, got %d", recPDF.Code)
	}
	if got := recPDF.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options=nosniff on pdf, got %q", got)
	}
	if got := recPDF.Header().Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
		t.Fatalf("expected CSP sandbox on pdf, got %q", got)
	}
	if got := recPDF.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("did not expect Content-Disposition attachment for .pdf, got %q", got)
	}

	// 5. Image under /uploads/images/ works for authenticated user.
	reqImg := httptest.NewRequest(http.MethodGet, "/uploads/images/part.jpg", nil)
	reqImg.AddCookie(cookie)
	recImg := httptest.NewRecorder()
	r.ServeHTTP(recImg, reqImg)
	if recImg.Code != http.StatusOK {
		t.Fatalf("GET part.jpg expected 200, got %d", recImg.Code)
	}
	body, _ := io.ReadAll(recImg.Body)
	if string(body) != "imgdata" {
		t.Fatalf("unexpected image body: %q", string(body))
	}
}

func TestRouter_Wave2CHardwareRoutes(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatalf("i18n.Init: %v", err)
	}
	env := setupRouterTestEnv(t, true)
	defer env.dbConn.Close()

	log := slog.New(slog.DiscardHandler)
	env.handler.Hardware = hardware.NewService(env.store, wled.NewClient(), log)

	viewerHash, err := auth.HashPassword("viewer-secret-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.CreateUser(context.Background(), db.CreateUserParams{
		Email:                  "viewer@example.com",
		PasswordHash:           viewerHash,
		Role:                   "viewer",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := NewWithOptions(ctx, env.mw, env.sm, env.handler, Options{UploadsDir: env.uploads})

	// Verify POST /hardware/discover is registered on the router.
	foundDiscover := false
	if err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodPost && route == "/hardware/discover" {
			foundDiscover = true
		}
		return nil
	}); err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	if !foundDiscover {
		t.Fatal("expected POST /hardware/discover route to be registered")
	}

	// Guest is redirected on standalone container configure read route.
	reqGuest := httptest.NewRequest(http.MethodGet, "/hardware/containers/1/configure", nil)
	recGuest := httptest.NewRecorder()
	r.ServeHTTP(recGuest, reqGuest)
	if recGuest.Code != http.StatusSeeOther || recGuest.Header().Get("Location") != "/login" {
		t.Fatalf("guest GET /hardware/containers/1/configure expected 303 -> /login, got %d (%q)", recGuest.Code, recGuest.Header().Get("Location"))
	}

	// Viewer is forbidden (403) on all admin hardware/container mutation routes.
	viewerCookie := loginSessionCookie(t, r, "viewer@example.com", "viewer-secret-123")
	for _, path := range []string{
		"/hardware/discover",
		"/hardware/1/update",
		"/hardware/containers",
		"/hardware/containers/1/configure",
		"/hardware/containers/1/controller",
		"/hardware/containers/1/delete",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Host = "example.com"
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.AddCookie(viewerCookie)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("viewer POST %s expected 403, got %d", path, rec.Code)
		}
	}

	// Admin can create, read, configure, bind/unbind, and delete standalone containers, and route /hardware/{id}/update.
	adminCookie := loginSessionCookie(t, r, "admin@example.com", "admin-secret-123")
	postAdmin := func(path, form string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form))
		req.Host = "example.com"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.AddCookie(adminCookie)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	recCreate := postAdmin("/hardware/containers", "name=Shelf+A&rows=2&cols=2")
	if recCreate.Code != http.StatusSeeOther || !strings.HasPrefix(recCreate.Header().Get("Location"), "/hardware/containers/") {
		t.Fatalf("POST /hardware/containers expected 303 to configure, got %d (%q)", recCreate.Code, recCreate.Header().Get("Location"))
	}
	configPath := recCreate.Header().Get("Location")

	reqCfg := httptest.NewRequest(http.MethodGet, configPath, nil)
	reqCfg.AddCookie(adminCookie)
	recCfg := httptest.NewRecorder()
	r.ServeHTTP(recCfg, reqCfg)
	if recCfg.Code != http.StatusOK {
		t.Fatalf("GET %s expected 200, got %d", configPath, recCfg.Code)
	}

	saveForm := url.Values{
		"grid_data":   {"[]"},
		"config_data": {`[{"id":1,"name":"Shelf A","segment_id":0,"config":{"sections":[{"rows":2,"cols":2,"serpentine":true,"startCorner":"top_left","direction":"horizontal"}]}}]`},
	}.Encode()
	recSave := postAdmin(configPath, saveForm)
	if recSave.Code != http.StatusSeeOther || recSave.Header().Get("Location") != "/hardware" {
		t.Fatalf("POST %s expected 303 -> /hardware, got %d (%s)", configPath, recSave.Code, recSave.Body.String())
	}

	recBind := postAdmin(strings.TrimSuffix(configPath, "/configure")+"/controller", "controller_id=")
	if recBind.Code != http.StatusSeeOther || recBind.Header().Get("Location") != "/hardware" {
		t.Fatalf("POST controller bind expected 303 -> /hardware, got %d", recBind.Code)
	}

	recDel := postAdmin(strings.TrimSuffix(configPath, "/configure")+"/delete", "")
	if recDel.Code != http.StatusSeeOther || recDel.Header().Get("Location") != "/hardware" {
		t.Fatalf("POST container delete expected 303 -> /hardware, got %d", recDel.Code)
	}

	recUpdate := postAdmin("/hardware/1/update", "name=Missing")
	if recUpdate.Code == http.StatusNotFound || recUpdate.Code == http.StatusMethodNotAllowed {
		t.Fatalf("POST /hardware/1/update was not routed to HandleControllerUpdate (got %d)", recUpdate.Code)
	}
}

type testBarcodeAdapter struct{}

func (testBarcodeAdapter) ParseBarcode(_ context.Context, raw string) (api.ParsedBarcodeResult, error) {
	p := barcodes.Parse(raw)
	return api.ParsedBarcodeResult{
		Supplier:           p.Supplier,
		PartNumber:         p.PartNumber,
		SupplierPartNumber: p.SupplierPartNumber,
		Manufacturer:       p.Manufacturer,
		Quantity:           p.Quantity,
		OrderNumber:        p.OrderNumber,
		RawBarcode:         p.RawBarcode,
		Format:             p.Format,
	}, nil
}

type testProjectAdapter struct {
	svc projects.Service
}

func (a testProjectAdapter) ListProjects(ctx context.Context) ([]api.ProjectSummary, error) {
	rows, err := a.svc.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.ProjectSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.ProjectSummary{
			ID:           row.ID,
			Name:         row.Name,
			Description:  row.Description.String,
			Status:       row.Status,
			BOMItemCount: row.BomItemCount,
		})
	}
	return out, nil
}

func (a testProjectAdapter) LocateProject(ctx context.Context, projectID int64) error {
	return a.svc.LocateBOM(ctx, projectID)
}

func (a testProjectAdapter) BuildProject(ctx context.Context, projectID int64, boardCount int) (api.ProjectBuildResult, error) {
	if err := a.svc.BuildProject(ctx, projectID, int64(boardCount)); err != nil {
		var shortageErr *projects.ShortageError
		if errors.As(err, &shortageErr) {
			return api.ProjectBuildResult{}, fmt.Errorf("%w: %v", api.ErrProjectShortage, shortageErr)
		}
		if errors.Is(err, projects.ErrEmptyBOM) {
			return api.ProjectBuildResult{}, fmt.Errorf("invalid build request: %w", err)
		}
		return api.ProjectBuildResult{}, err
	}
	return api.ProjectBuildResult{
		ProjectID:  projectID,
		BoardCount: boardCount,
		Message:    fmt.Sprintf("Built %d board(s)", boardCount),
	}, nil
}

func wireWave4RouterEnv(t *testing.T, env *routerTestEnv) (apitoken.Service, projects.Service, http.Handler) {
	t.Helper()
	log := slog.New(slog.DiscardHandler)
	wledClient := wled.NewClient()
	wledSvc := wled.NewService(env.store, wledClient, log)
	stockSvc := stock.NewService(env.store, log)
	hwSvc := hardware.NewService(env.store, wledClient, log)
	tagsSvc := tags.NewService(env.dbConn, env.store)
	partsSvc := parts.NewService(env.dbConn, env.store, log, tagsSvc, env.handler.Documents)
	projectsSvc := projects.NewService(env.store, wledSvc, log, projects.WithWLEDClient(wledClient))
	tokenSvc := apitoken.NewService(env.store, apitoken.WithLogger(log))
	tokenHandler := handler.NewAPITokenHandler(env.store, tokenSvc, env.handler.UIError)

	env.handler.Parts = partsSvc
	env.handler.Stock = stockSvc
	env.handler.WLED = wledSvc
	env.handler.WithProjects(projectsSvc)
	env.handler.WithAPITokens(tokenHandler)

	apiRouter := api.NewRouter(api.Dependencies{
		Store:    env.store,
		Tokens:   tokenSvc,
		Parts:    env.handler.Parts,
		Stock:    stockSvc,
		Hardware: hwSvc,
		WLED:     wledSvc,
		Barcodes: testBarcodeAdapter{},
		Projects: testProjectAdapter{svc: projectsSvc},
		Logger:   log,
		Version:  "v2",
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := NewWithOptions(ctx, env.mw, env.sm, env.handler, Options{
		UploadsDir: env.uploads,
		APIRouter:  apiRouter,
		// Omit TokenHandler to also exercise fallback to h.APITokens.
	})
	return tokenSvc, projectsSvc, r
}

func TestRouter_APIv1_PreSetupHealthAndBearerEndpoints(t *testing.T) {
	env := setupRouterTestEnv(t, true)
	defer env.dbConn.Close()
	tokenSvc, projectsSvc, r := wireWave4RouterEnv(t, env)
	defer tokenSvc.Close()

	// Pre-setup: remove all users so web UI routes redirect to /setup,
	// while /api/v1/health remains directly reachable without session cookies.
	if _, err := env.dbConn.Exec("DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	middleware.ResetSetupCache()

	webReq := httptest.NewRequest(http.MethodGet, "/", nil)
	webRec := httptest.NewRecorder()
	r.ServeHTTP(webRec, webReq)
	if webRec.Code != http.StatusSeeOther || webRec.Header().Get("Location") != "/setup" {
		t.Fatalf("expected pre-setup GET / to redirect 303 -> /setup, got %d (%s)", webRec.Code, webRec.Header().Get("Location"))
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthRec := httptest.NewRecorder()
	r.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("expected pre-setup GET /api/v1/health 200, got %d (%s)", healthRec.Code, healthRec.Body.String())
	}
	if len(healthRec.Result().Cookies()) != 0 {
		t.Fatalf("expected no session cookies on /api/v1/health, got %v", healthRec.Result().Cookies())
	}
	var health api.HealthResponse
	if err := json.NewDecoder(healthRec.Body).Decode(&health); err != nil {
		t.Fatalf("decode health JSON: %v", err)
	}
	if health.Status != "ok" || health.Version != "v2" {
		t.Fatalf("unexpected health payload: %+v", health)
	}

	// Re-create setup users (viewer + editor) for Bearer API checks.
	ctx := context.Background()
	viewerHash, _ := auth.HashPassword("viewer-secret-123")
	viewerUser, err := env.store.CreateUser(ctx, db.CreateUserParams{
		Email:                  "viewer@example.com",
		PasswordHash:           viewerHash,
		Role:                   "viewer",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	editorHash, _ := auth.HashPassword("editor-secret-123")
	editorUser, err := env.store.CreateUser(ctx, db.CreateUserParams{
		Email:                  "editor@example.com",
		PasswordHash:           editorHash,
		Role:                   "editor",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	middleware.ResetSetupCache()

	readTok, err := tokenSvc.CreateToken(ctx, apitoken.CreateTokenRequest{
		UserID: viewerUser.ID,
		Name:   "ReadOnly",
		Scopes: "read",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTok, err := tokenSvc.CreateToken(ctx, apitoken.CreateTokenRequest{
		UserID: editorUser.ID,
		Name:   "EditorWrite",
		Scopes: "write",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Seed part, standalone container + bin + stock, and a linked project BOM.
	partID, err := env.handler.Parts.CreatePart(ctx, parts.CreatePartRequest{
		Name:       "10k Resistor",
		PartNumber: "RC0805FR-0710KL",
	})
	if err != nil {
		t.Fatal(err)
	}
	contID, err := env.store.CreateContainer(ctx, db.CreateContainerParams{Name: "Drawer A"})
	if err != nil {
		t.Fatal(err)
	}
	binID, err := env.store.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: contID})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.handler.Stock.AssignStock(ctx, stock.AssignStockRequest{
		PartID:   partID,
		BinID:    binID,
		Quantity: 10,
	}); err != nil {
		t.Fatal(err)
	}
	proj, err := projectsSvc.CreateProject(ctx, projects.CreateProjectRequest{Name: "Sensor Board", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projectsSvc.AddBOMItem(ctx, projects.AddBOMItemRequest{
		ProjectID:        proj.ID,
		PartID:           &partID,
		Designator:       "R1,R2",
		MPN:              "RC0805FR-0710KL",
		QuantityPerBoard: 4,
	}); err != nil {
		t.Fatal(err)
	}

	callAPI := func(method, path, bearer, body string) *httptest.ResponseRecorder {
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rdr)
		req.Host = "wledger.local"
		req.Header.Set("Origin", "https://cli.external.example")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// Unauthenticated /api/v1/parts returns 401.
	if rec := callAPI(http.MethodGet, "/api/v1/parts", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /api/v1/parts expected 401, got %d", rec.Code)
	}

	// Read token can GET /api/v1/parts and /api/v1/projects without session cookies.
	recParts := callAPI(http.MethodGet, "/api/v1/parts", readTok.Plaintext, "")
	if recParts.Code != http.StatusOK || !strings.Contains(recParts.Body.String(), "10k Resistor") {
		t.Fatalf("GET /api/v1/parts expected 200 with seeded part, got %d (%s)", recParts.Code, recParts.Body.String())
	}
	if len(recParts.Result().Cookies()) != 0 {
		t.Fatalf("expected no session cookie on Bearer /api/v1/parts, got %v", recParts.Result().Cookies())
	}

	recProjList := callAPI(http.MethodGet, "/api/v1/projects", readTok.Plaintext, "")
	if recProjList.Code != http.StatusOK || !strings.Contains(recProjList.Body.String(), "Sensor Board") {
		t.Fatalf("GET /api/v1/projects expected 200 with project, got %d (%s)", recProjList.Code, recProjList.Body.String())
	}

	// Read token cannot call write endpoints, while read-only barcode parsing succeeds with ScopeRead.
	if rec := callAPI(http.MethodPost, "/api/v1/parts", readTok.Plaintext, `{"name":"Blocked Part"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("read-scoped POST /api/v1/parts expected 403, got %d", rec.Code)
	}
	if rec := callAPI(http.MethodGet, "/api/v1/parts", readTok.Plaintext, ""); rec.Code != http.StatusOK {
		t.Fatalf("read-scoped GET /api/v1/parts expected 200, got %d", rec.Code)
	}
	if rec := callAPI(http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/build", proj.ID), readTok.Plaintext, `{"board_count":1}`); rec.Code != http.StatusForbidden {
		t.Fatalf("read-scoped POST /api/v1/projects/{id}/build expected 403, got %d", rec.Code)
	}

	// Write token can create parts across origins via Bearer without CSRF cookies.
	if rec := callAPI(http.MethodPost, "/api/v1/parts", writeTok.Plaintext, `{"name":"API Created Cap"}`); rec.Code != http.StatusCreated {
		t.Fatalf("write-scoped POST /api/v1/parts expected 201, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Parse barcode works with Bearer token and returns parsed fields.
	recBarcode := callAPI(http.MethodPost, "/api/v1/barcodes/parse", readTok.Plaintext, `{"barcode":"RC0805FR-0710KL"}`)
	if recBarcode.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/barcodes/parse expected 200, got %d (%s)", recBarcode.Code, recBarcode.Body.String())
	}
	var parsedResp api.ParsedBarcodeResult
	if err := json.NewDecoder(recBarcode.Body).Decode(&parsedResp); err != nil {
		t.Fatalf("decode barcode response: %v", err)
	}
	if parsedResp.RawBarcode != "RC0805FR-0710KL" {
		t.Fatalf("expected raw_barcode preserved, got %+v", parsedResp)
	}

	// Project build shortage maps to 409 Conflict; valid build maps to 200 OK and deducts stock.
	buildURL := fmt.Sprintf("/api/v1/projects/%d/build", proj.ID)
	if recShort := callAPI(http.MethodPost, buildURL, writeTok.Plaintext, `{"board_count":5}`); recShort.Code != http.StatusConflict {
		t.Fatalf("shortage POST %s expected 409 Conflict, got %d (%s)", buildURL, recShort.Code, recShort.Body.String())
	}
	if recBuild := callAPI(http.MethodPost, buildURL, writeTok.Plaintext, `{"board_count":2}`); recBuild.Code != http.StatusOK {
		t.Fatalf("valid POST %s expected 200, got %d (%s)", buildURL, recBuild.Code, recBuild.Body.String())
	}
	stockRows, err := env.store.GetPartAssignments(ctx, partID)
	if err != nil || len(stockRows) != 1 || stockRows[0].Quantity != 2 {
		t.Fatalf("expected remaining stock 2 after building 2x4=8, got %+v err=%v", stockRows, err)
	}
}

func TestRouter_Wave4_SettingsTokens_Barcode_And_ProjectsRBAC(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatalf("i18n.Init: %v", err)
	}

	env := setupRouterTestEnv(t, true)
	defer env.dbConn.Close()
	ctx := context.Background()

	viewerHash, _ := auth.HashPassword("viewer-secret-123")
	if _, err := env.store.CreateUser(ctx, db.CreateUserParams{
		Email:                  "viewer@example.com",
		PasswordHash:           viewerHash,
		Role:                   "viewer",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	editorHash, _ := auth.HashPassword("editor-secret-123")
	editorUser, err := env.store.CreateUser(ctx, db.CreateUserParams{
		Email:                  "editor@example.com",
		PasswordHash:           editorHash,
		Role:                   "editor",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	tokenSvc, _, r := wireWave4RouterEnv(t, env)
	defer tokenSvc.Close()

	viewerCookie := loginSessionCookie(t, r, "viewer@example.com", "viewer-secret-123")
	editorCookie := loginSessionCookie(t, r, "editor@example.com", "editor-secret-123")

	doCookieReq := func(c *http.Cookie, method, path, body, ct string) *httptest.ResponseRecorder {
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rdr)
		req.Host = "example.com"
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		if c != nil {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// 1. Settings page renders API Tokens card and /settings/tokens lifecycle works.
	recSettings := doCookieReq(editorCookie, http.MethodGet, "/settings", "", "")
	if recSettings.Code != http.StatusOK || !strings.Contains(recSettings.Body.String(), "api-tokens-card") {
		t.Fatalf("GET /settings expected 200 with api-tokens-card, got %d", recSettings.Code)
	}

	recCreateTok := doCookieReq(editorCookie, http.MethodPost, "/settings/tokens", url.Values{
		"name":  {"Lab CLI"},
		"scope": {"read"},
	}.Encode(), "application/x-www-form-urlencoded")
	if recCreateTok.Code != http.StatusCreated || !strings.Contains(recCreateTok.Body.String(), "wlg_") {
		t.Fatalf("POST /settings/tokens expected 201 revealing wlg_ token, got %d (%s)", recCreateTok.Code, recCreateTok.Body.String())
	}
	bodyHTML := recCreateTok.Body.String()
	revealIdx := strings.Index(bodyHTML, `id="api-token-reveal"`)
	if revealIdx < 0 {
		t.Fatalf("created token HTML missing #api-token-reveal banner")
	}
	idx := strings.Index(bodyHTML[revealIdx:], "wlg_")
	if idx < 0 {
		t.Fatalf("created token banner missing wlg_ plaintext")
	}
	start := revealIdx + idx
	endIdx := strings.IndexAny(bodyHTML[start:], `<"'`)
	if endIdx <= 0 {
		t.Fatalf("could not extract plaintext token from HTML")
	}
	createdPlaintext := bodyHTML[start : start+endIdx]
	apiReqBefore := httptest.NewRequest(http.MethodGet, "/api/v1/parts", nil)
	apiReqBefore.Header.Set("Authorization", "Bearer "+createdPlaintext)
	apiRecBefore := httptest.NewRecorder()
	r.ServeHTTP(apiRecBefore, apiReqBefore)
	if apiRecBefore.Code != http.StatusOK {
		t.Fatalf("newly UI-created Bearer token expected 200 on /api/v1/parts, got %d", apiRecBefore.Code)
	}

	// Role ceiling enforced on web UI token creation.
	recEscalate := doCookieReq(editorCookie, http.MethodPost, "/settings/tokens", url.Values{
		"name":  {"Escalate"},
		"scope": {"admin"},
	}.Encode(), "application/x-www-form-urlencoded")
	if recEscalate.Code < 400 || recEscalate.Code >= 500 {
		t.Fatalf("editor POST /settings/tokens with scope=admin expected 4xx, got %d", recEscalate.Code)
	}

	recListTok := doCookieReq(editorCookie, http.MethodGet, "/settings/tokens", "", "")
	if recListTok.Code != http.StatusOK || !strings.Contains(recListTok.Body.String(), "Lab CLI") {
		t.Fatalf("GET /settings/tokens expected 200 containing Lab CLI, got %d", recListTok.Code)
	}

	rows, err := env.store.ListAPITokensByUser(ctx, editorUser.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("expected 1 persisted token for editor, got %d err=%v", len(rows), err)
	}
	tokID := rows[0].ID

	// Viewer cannot revoke editor's token.
	_ = doCookieReq(viewerCookie, http.MethodPost, fmt.Sprintf("/settings/tokens/%d/delete", tokID), "", "")
	afterCrossRevoke, err := env.store.ListAPITokensByUser(ctx, editorUser.ID)
	if err != nil || len(afterCrossRevoke) != 1 {
		t.Fatalf("viewer cross-user revoke must not delete editor token (len=%d, err=%v)", len(afterCrossRevoke), err)
	}

	// Owner revokes via /revoke alias (303 redirect on standard form POST), plus verify /delete and DELETE /{id} routes work.
	recRevoke := doCookieReq(editorCookie, http.MethodPost, fmt.Sprintf("/settings/tokens/%d/revoke", tokID), "", "")
	if recRevoke.Code != http.StatusSeeOther || recRevoke.Header().Get("Location") != "/settings" {
		t.Fatalf("POST /settings/tokens/%d/revoke expected 303 -> /settings, got %d", tokID, recRevoke.Code)
	}
	apiReqAfter := httptest.NewRequest(http.MethodGet, "/api/v1/parts", nil)
	apiReqAfter.Header.Set("Authorization", "Bearer "+createdPlaintext)
	apiRecAfter := httptest.NewRecorder()
	r.ServeHTTP(apiRecAfter, apiReqAfter)
	if apiRecAfter.Code != http.StatusUnauthorized {
		t.Fatalf("revoked UI-created Bearer token expected 401 on /api/v1/parts, got %d", apiRecAfter.Code)
	}
	tok2, _ := tokenSvc.CreateToken(ctx, apitoken.CreateTokenRequest{UserID: editorUser.ID, Name: "Temp2", Scopes: "read"})
	if recDelPost := doCookieReq(editorCookie, http.MethodPost, fmt.Sprintf("/settings/tokens/%d/delete", tok2.Token.ID), "", ""); recDelPost.Code != http.StatusSeeOther || recDelPost.Header().Get("Location") != "/settings" {
		t.Fatalf("POST /settings/tokens/%d/delete expected 303 -> /settings, got %d", tok2.Token.ID, recDelPost.Code)
	}
	tok3, _ := tokenSvc.CreateToken(ctx, apitoken.CreateTokenRequest{UserID: editorUser.ID, Name: "Temp3", Scopes: "read"})
	if recDelHTTP := doCookieReq(editorCookie, http.MethodDelete, fmt.Sprintf("/settings/tokens/%d", tok3.Token.ID), "", ""); recDelHTTP.Code != http.StatusOK {
		t.Fatalf("DELETE /settings/tokens/%d expected 200, got %d", tok3.Token.ID, recDelHTTP.Code)
	}

	// 2. /parts/parse-barcode RBAC (viewer 403, editor 200).
	if rec := doCookieReq(viewerCookie, http.MethodPost, "/parts/parse-barcode", `{"barcode":"ABC123"}`, "application/json"); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer POST /parts/parse-barcode expected 403, got %d", rec.Code)
	}
	recParse := doCookieReq(editorCookie, http.MethodPost, "/parts/parse-barcode", `{"barcode":"ABC123"}`, "application/json")
	if recParse.Code != http.StatusOK || !strings.Contains(recParse.Body.String(), `"raw_barcode":"ABC123"`) {
		t.Fatalf("editor POST /parts/parse-barcode expected 200, got %d (%s)", recParse.Code, recParse.Body.String())
	}

	// 3. /projects Read + Locate vs Write RBAC.
	if rec := doCookieReq(viewerCookie, http.MethodGet, "/projects", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("viewer GET /projects expected 200, got %d", rec.Code)
	}
	if rec := doCookieReq(viewerCookie, http.MethodPost, "/projects", url.Values{"name": {"Blocked"}}.Encode(), "application/x-www-form-urlencoded"); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer POST /projects expected 403, got %d", rec.Code)
	}

	recProjCreate := doCookieReq(editorCookie, http.MethodPost, "/projects", url.Values{
		"name":   {"Controller Rev2"},
		"status": {"draft"},
	}.Encode(), "application/x-www-form-urlencoded")
	if recProjCreate.Code != http.StatusSeeOther || !strings.HasPrefix(recProjCreate.Header().Get("Location"), "/projects/") {
		t.Fatalf("editor POST /projects expected 303 -> /projects/{id}, got %d (%s)", recProjCreate.Code, recProjCreate.Header().Get("Location"))
	}
	projPath := recProjCreate.Header().Get("Location")

	if rec := doCookieReq(viewerCookie, http.MethodGet, projPath, "", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Controller Rev2") {
		t.Fatalf("viewer GET %s expected 200 with project name, got %d", projPath, rec.Code)
	}
	if rec := doCookieReq(viewerCookie, http.MethodPost, projPath+"/locate", "", ""); rec.Code != http.StatusSeeOther {
		t.Fatalf("viewer Pick Mode POST %s/locate expected 303, got %d", projPath, rec.Code)
	}

	// Write mutations on project are forbidden to viewer.
	for _, writePath := range []string{
		projPath + "/update",
		projPath + "/bom",
		projPath + "/bom/import",
		projPath + "/bom/1/link",
		projPath + "/bom/1/delete",
		projPath + "/build",
		projPath + "/delete",
	} {
		if rec := doCookieReq(viewerCookie, http.MethodPost, writePath, "name=x", "application/x-www-form-urlencoded"); rec.Code != http.StatusForbidden {
			t.Fatalf("viewer POST %s expected 403, got %d", writePath, rec.Code)
		}
	}

	// Editor can add a BOM item and delete the project.
	recAddBOM := doCookieReq(editorCookie, http.MethodPost, projPath+"/bom", url.Values{
		"designator":         {"U1"},
		"mpn":                {"ESP32-S3"},
		"quantity_per_board": {"1"},
	}.Encode(), "application/x-www-form-urlencoded")
	if recAddBOM.Code != http.StatusSeeOther {
		t.Fatalf("editor POST %s/bom expected 303, got %d (%s)", projPath, recAddBOM.Code, recAddBOM.Body.String())
	}
	if rec := doCookieReq(editorCookie, http.MethodPost, projPath+"/delete", "", ""); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/projects" {
		t.Fatalf("editor POST %s/delete expected 303 -> /projects, got %d", projPath, rec.Code)
	}
}
