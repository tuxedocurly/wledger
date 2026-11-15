package middleware

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/web/layouts"
)

type countingStore struct {
	db.Store
	getUserCalls     atomic.Int64
	getSettingsCalls atomic.Int64
	countUsersCalls  atomic.Int64
}

func (s *countingStore) GetUser(ctx context.Context, id int64) (db.User, error) {
	s.getUserCalls.Add(1)
	return s.Store.GetUser(ctx, id)
}

func (s *countingStore) GetSettings(ctx context.Context) (db.Setting, error) {
	s.getSettingsCalls.Add(1)
	return s.Store.GetSettings(ctx)
}

func (s *countingStore) CountUsers(ctx context.Context) (int64, error) {
	s.countUsersCalls.Add(1)
	return s.Store.CountUsers(ctx)
}

func setupTestMiddleware(t *testing.T) (*countingStore, *Manager, *scs.SessionManager, func()) {
	t.Helper()
	ResetSetupCache()

	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}

	rawStore := db.NewStore(conn)
	store := &countingStore{Store: rawStore}
	log := slog.New(slog.DiscardHandler)
	sm := scs.New()
	sm.Store = auth.NewStore(rawStore)
	uiErr := uierror.New(log)
	mw := New(store, sm, log, uiErr)

	return store, mw, sm, func() { _ = conn.Close() }
}

func createAuthenticatedCookie(t *testing.T, sm *scs.SessionManager, userID int64) *http.Cookie {
	t.Helper()
	h := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sm.Put(r.Context(), config.SessionKeyUserID, userID)
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}
	return cookies[0]
}

func TestMiddleware_SingleGetUserAndSettingsAndSetupCache(t *testing.T) {
	store, mw, sm, cleanup := setupTestMiddleware(t)
	defer cleanup()

	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("InitSettings failed: %v", err)
	}
	if err := store.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
		RequireAuthForRead:   sql.NullBool{Bool: true, Valid: true},
		LocateTimeoutSeconds: sql.NullInt64{Int64: 15, Valid: true},
		EnableLocateTimeout:  sql.NullBool{Bool: true, Valid: true},
	}); err != nil {
		t.Fatalf("UpdateGeneralSettings failed: %v", err)
	}
	if err := store.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate:        sql.NullString{String: "#112233", Valid: true},
		ColorStockOk:       sql.NullString{String: "#223344", Valid: true},
		ColorStockLow:      sql.NullString{String: "#334455", Valid: true},
		ColorStockCritical: sql.NullString{String: "#445566", Valid: true},
	}); err != nil {
		t.Fatalf("UpdateColors failed: %v", err)
	}

	hash, err := auth.HashPassword("adminpass123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := store.CreateUser(ctx, db.CreateUserParams{
		Email:                  "admin@example.com",
		PasswordHash:           hash,
		Role:                   "admin",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	cookie := createAuthenticatedCookie(t, sm, user.ID)

	var seenColors layouts.StockColors
	var seenUser auth.User
	chain := sm.LoadAndSave(
		mw.Authenticate(
			mw.FirstRunCheck(
				mw.InjectStockColors(
					mw.RequireReadAuth(
						mw.RequireAuth(
							mw.RequirePasswordChange(
								mw.RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
									seenColors = layouts.GetStockColors(r.Context())
									seenUser = auth.GetUser(r.Context())
									w.WriteHeader(http.StatusOK)
								})),
							),
						),
					),
				),
			),
		),
	)

	// Request 1: populates setup cache, fetches GetUser once and GetSettings once.
	req1 := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req1.AddCookie(cookie)
	rec1 := httptest.NewRecorder()
	chain.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec1.Code)
	}
	if seenUser.ID != user.ID || seenUser.Role != "admin" {
		t.Fatalf("unexpected resolved user: %+v", seenUser)
	}
	wantColors := layouts.StockColors{Locate: "#112233", Ok: "#223344", Low: "#334455", Critical: "#445566"}
	if seenColors != wantColors {
		t.Fatalf("got stock colors %+v, want %+v", seenColors, wantColors)
	}
	if got := store.getUserCalls.Load(); got != 1 {
		t.Fatalf("expected 1 GetUser call per request, got %d", got)
	}
	if got := store.getSettingsCalls.Load(); got != 1 {
		t.Fatalf("expected 1 GetSettings call per request, got %d", got)
	}
	if got := store.countUsersCalls.Load(); got != 1 {
		t.Fatalf("expected 1 CountUsers call on first request, got %d", got)
	}

	// Request 2: CountUsers must be cached (still 1), GetUser/GetSettings still +1 each (total 2).
	req2 := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req2.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	chain.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("req2 expected 200 OK, got %d", rec2.Code)
	}
	if got := store.getUserCalls.Load(); got != 2 {
		t.Fatalf("expected 2 total GetUser calls after 2 requests, got %d", got)
	}
	if got := store.getSettingsCalls.Load(); got != 2 {
		t.Fatalf("expected 2 total GetSettings calls after 2 requests, got %d", got)
	}
	if got := store.countUsersCalls.Load(); got != 1 {
		t.Fatalf("expected CountUsers cached at 1, got %d", got)
	}

	// ResetSetupCache invalidates the cached setup completion flag.
	ResetSetupCache()
	req3 := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req3.AddCookie(cookie)
	rec3 := httptest.NewRecorder()
	chain.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("req3 expected 200 OK, got %d", rec3.Code)
	}
	if got := store.countUsersCalls.Load(); got != 2 {
		t.Fatalf("expected CountUsers re-queried (2 total) after ResetSetupCache, got %d", got)
	}
}

func TestMiddleware_ForceResetUsesCachedUserOnce(t *testing.T) {
	store, mw, sm, cleanup := setupTestMiddleware(t)
	defer cleanup()

	ctx := context.Background()
	hash, _ := auth.HashPassword("temp-password-1")
	user, err := store.CreateUser(ctx, db.CreateUserParams{
		Email:                  "forced@example.com",
		PasswordHash:           hash,
		Role:                   "editor",
		ChangePasswordRequired: sql.NullBool{Bool: true, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cookie := createAuthenticatedCookie(t, sm, user.ID)

	chain := sm.LoadAndSave(mw.Authenticate(mw.RequireAuth(mw.RequirePasswordChange(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))))

	req := httptest.NewRequest(http.MethodGet, "/parts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/force-reset" {
		t.Fatalf("expected redirect to /force-reset, got status=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	if got := store.getUserCalls.Load(); got != 1 {
		t.Fatalf("expected single GetUser lookup across Authenticate+RequireAuth+RequirePasswordChange, got %d", got)
	}
}

func TestIPRateLimiter_TokenBucketAndEviction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	cur := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	limiter := NewIPRateLimiter(ctx, 1.0, 3, 5*time.Minute)
	limiter.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return cur
	}
	advance := func(d time.Duration) {
		mu.Lock()
		cur = cur.Add(d)
		mu.Unlock()
	}

	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Different ephemeral ports from the same IP must share one bucket.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = "198.51.100.7:" + string(rune('1'+i)) + "000"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d expected 200, got %d", i+1, rec.Code)
		}
	}

	// 4th attempt exceeds burst=3 -> 429 Too Many Requests with Retry-After.
	req4 := httptest.NewRequest(http.MethodPost, "/login", nil)
	req4.RemoteAddr = "198.51.100.7:9999"
	rec4 := httptest.NewRecorder()
	handler.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d", rec4.Code)
	}
	if rec4.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on 429 response")
	}

	// Different IP is unaffected.
	reqOther := httptest.NewRequest(http.MethodPost, "/login", nil)
	reqOther.RemoteAddr = "198.51.100.8:1234"
	recOther := httptest.NewRecorder()
	handler.ServeHTTP(recOther, reqOther)
	if recOther.Code != http.StatusOK {
		t.Fatalf("other IP expected 200, got %d", recOther.Code)
	}

	// Advance 2s -> 2 tokens refilled for 198.51.100.7.
	advance(2 * time.Second)
	reqRefill := httptest.NewRequest(http.MethodPost, "/login", nil)
	reqRefill.RemoteAddr = "198.51.100.7:5555"
	recRefill2 := httptest.NewRecorder()
	handler.ServeHTTP(recRefill2, reqRefill)
	if recRefill2.Code != http.StatusOK {
		t.Fatalf("expected refill to allow request, got %d", recRefill2.Code)
	}

	// Advance past staleAfter -> evictStale clears old entries.
	advance(10 * time.Minute)
	limiter.evictStale()
	limiter.mu.Lock()
	remaining := len(limiter.buckets)
	limiter.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected stale buckets to be evicted, %d remained", remaining)
	}
}

func TestMaxBodyBytes(t *testing.T) {
	h := MaxBodyBytes(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, "too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	recSmall := httptest.NewRecorder()
	h.ServeHTTP(recSmall, httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("12345")))
	if recSmall.Code != http.StatusOK {
		t.Fatalf("small body expected 200, got %d", recSmall.Code)
	}

	recLarge := httptest.NewRecorder()
	h.ServeHTTP(recLarge, httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("123456789012345")))
	if recLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body expected 413, got %d", recLarge.Code)
	}
}
