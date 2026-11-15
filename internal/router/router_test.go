package router

import (
	"context"
	"database/sql"
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
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/handler"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/i18n"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/settings"
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
