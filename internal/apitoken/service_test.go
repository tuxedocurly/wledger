package apitoken

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
)

func setupTokenTestDB(t *testing.T) (*sql.DB, db.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "apitoken_test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return conn, db.NewStore(conn)
}

func createTestUser(t *testing.T, store db.Store, email, role string) db.CreateUserRow {
	t.Helper()
	u, err := store.CreateUser(context.Background(), db.CreateUserParams{
		Email:        email,
		PasswordHash: "hashed-password",
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create test user (%s, %s): %v", email, role, err)
	}
	return u
}

func isHexString(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func TestCreateTokenFormatAndSHA256Storage(t *testing.T) {
	conn, store := setupTokenTestDB(t)
	svc := NewService(store)
	defer svc.Close()

	admin := createTestUser(t, store, "admin@example.com", "admin")
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, admin.ID)

	created, err := svc.CreateToken(ctx, CreateTokenRequest{
		UserID: admin.ID,
		Name:   "CI Deployer",
		Scopes: "write, read",
	})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	if !strings.HasPrefix(created.Plaintext, TokenPrefix) {
		t.Fatalf("expected token prefix %q, got %q", TokenPrefix, created.Plaintext)
	}
	randomPart := strings.TrimPrefix(created.Plaintext, TokenPrefix)
	if len(randomPart) != TokenRandomBytes*2 || !isHexString(randomPart) {
		t.Fatalf("expected %d lowercase hex random chars, got %q", TokenRandomBytes*2, randomPart)
	}

	if created.Token.TokenPrefix != created.Plaintext[:VisiblePrefixLen] {
		t.Fatalf("expected stored prefix %q, got %q", created.Plaintext[:VisiblePrefixLen], created.Token.TokenPrefix)
	}
	if created.Token.Scopes != "read,write" {
		t.Fatalf("expected canonical scopes %q, got %q", "read,write", created.Token.Scopes)
	}

	var storedHash, storedPrefix string
	if err := conn.QueryRow(`SELECT token_hash, token_prefix FROM api_tokens WHERE id = ?`, created.Token.ID).Scan(&storedHash, &storedPrefix); err != nil {
		t.Fatalf("query stored token row: %v", err)
	}

	expectedHash := HashToken(created.Plaintext)
	if storedHash != expectedHash {
		t.Fatalf("expected SHA-256 hash %q, got %q", expectedHash, storedHash)
	}
	if storedHash == created.Plaintext || strings.Contains(storedHash, randomPart) {
		t.Fatal("plaintext token must never be stored in api_tokens")
	}
	if storedPrefix != created.Token.TokenPrefix {
		t.Fatalf("expected DB prefix %q, got %q", created.Token.TokenPrefix, storedPrefix)
	}

	rows, err := svc.ListTokensByUser(ctx, admin.ID)
	if err != nil {
		t.Fatalf("ListTokensByUser failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 listed token, got %d", len(rows))
	}
	if rows[0].Name != "CI Deployer" || rows[0].TokenPrefix != created.Token.TokenPrefix {
		t.Fatalf("unexpected listed row: %+v", rows[0])
	}

	logs, err := store.GetAllAuditLogs(ctx)
	if err != nil {
		t.Fatalf("GetAllAuditLogs failed: %v", err)
	}
	if len(logs) == 0 || logs[0].UserID.Int64 != admin.ID {
		t.Fatalf("expected audit log attributed to user %d, got %+v", admin.ID, logs)
	}
}

func TestAuthenticateAndExpiration(t *testing.T) {
	_, store := setupTokenTestDB(t)

	var mu sync.Mutex
	currentNow := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	svc := NewService(store, WithNowFunc(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return currentNow
	}))
	defer svc.Close()

	editor := createTestUser(t, store, "editor@example.com", "editor")
	ctx := context.Background()

	past := currentNow.Add(-time.Minute)
	if _, err := svc.CreateToken(ctx, CreateTokenRequest{
		UserID:    editor.ID,
		Name:      "Expired",
		Scopes:    "read",
		ExpiresAt: &past,
	}); !errors.Is(err, ErrInvalidExpiration) {
		t.Fatalf("expected ErrInvalidExpiration, got %v", err)
	}

	exp := currentNow.Add(time.Hour)
	created, err := svc.CreateToken(ctx, CreateTokenRequest{
		UserID:    editor.ID,
		Name:      "Short Lived",
		Scopes:    "read,write",
		ExpiresAt: &exp,
	})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	principal, err := svc.Authenticate(ctx, created.Plaintext)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if principal.User.ID != editor.ID || principal.User.Email != "editor@example.com" || principal.User.Role != "editor" {
		t.Fatalf("unexpected principal user: %+v", principal.User)
	}
	if !principal.HasScope(ScopeRead) || !principal.HasScope(ScopeWrite) || principal.HasScope(ScopeAdmin) {
		t.Fatalf("unexpected principal scope evaluation: %+v", principal)
	}

	for _, bad := range []string{"", "not_a_token", TokenPrefix, TokenPrefix + "0000", "Bearer " + created.Plaintext} {
		if _, err := svc.Authenticate(ctx, bad); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("expected ErrInvalidToken for %q, got %v", bad, err)
		}
	}

	mu.Lock()
	currentNow = exp
	mu.Unlock()

	if _, err := svc.Authenticate(ctx, created.Plaintext); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("expected ErrExpiredToken at expiration boundary, got %v", err)
	}
}

func TestScopeValidationAndRBACCeiling(t *testing.T) {
	conn, store := setupTokenTestDB(t)
	svc := NewService(store)
	defer svc.Close()

	ctx := context.Background()
	viewer := createTestUser(t, store, "viewer@example.com", "viewer")
	editor := createTestUser(t, store, "editor@example.com", "editor")
	admin := createTestUser(t, store, "admin2@example.com", "admin")

	if _, err := svc.CreateToken(ctx, CreateTokenRequest{UserID: viewer.ID, Name: "   ", Scopes: "read"}); !errors.Is(err, ErrInvalidTokenName) {
		t.Fatalf("expected ErrInvalidTokenName, got %v", err)
	}
	if _, err := svc.CreateToken(ctx, CreateTokenRequest{UserID: viewer.ID, Name: "BadScope", Scopes: "read,superuser"}); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("expected ErrInvalidScope, got %v", err)
	}
	if _, err := svc.CreateToken(ctx, CreateTokenRequest{UserID: 999999, Name: "NoUser", Scopes: "read"}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}

	if _, err := svc.CreateToken(ctx, CreateTokenRequest{UserID: viewer.ID, Name: "VWrite", Scopes: "write"}); !errors.Is(err, ErrScopeExceedsRole) {
		t.Fatalf("viewer requesting write should return ErrScopeExceedsRole, got %v", err)
	}
	if _, err := svc.CreateToken(ctx, CreateTokenRequest{UserID: viewer.ID, Name: "VAdmin", Scopes: "admin"}); !errors.Is(err, ErrScopeExceedsRole) {
		t.Fatalf("viewer requesting admin should return ErrScopeExceedsRole, got %v", err)
	}
	if _, err := svc.CreateToken(ctx, CreateTokenRequest{UserID: editor.ID, Name: "EAdmin", Scopes: "admin"}); !errors.Is(err, ErrScopeExceedsRole) {
		t.Fatalf("editor requesting admin should return ErrScopeExceedsRole, got %v", err)
	}

	adminTok, err := svc.CreateToken(ctx, CreateTokenRequest{UserID: admin.ID, Name: "Full", Scopes: "admin"})
	if err != nil {
		t.Fatalf("admin CreateToken failed: %v", err)
	}
	p, err := svc.Authenticate(ctx, adminTok.Plaintext)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if !p.HasScope(ScopeRead) || !p.HasScope(ScopeWrite) || !p.HasScope(ScopeAdmin) {
		t.Fatalf("admin token should satisfy read, write, admin scopes")
	}
	if p.HasScope("unknown") {
		t.Fatal("HasScope should reject unknown scopes")
	}

	// Demoting user's DB role must immediately restrict existing tokens via Principal.HasScope.
	if _, err := conn.Exec(`UPDATE users SET role = 'viewer' WHERE id = ?`, admin.ID); err != nil {
		t.Fatalf("demote user role: %v", err)
	}
	demoted, err := svc.Authenticate(ctx, adminTok.Plaintext)
	if err != nil {
		t.Fatalf("Authenticate after demotion failed: %v", err)
	}
	if !demoted.HasScope(ScopeRead) {
		t.Fatal("demoted viewer should retain read scope")
	}
	if demoted.HasScope(ScopeWrite) || demoted.HasScope(ScopeAdmin) {
		t.Fatalf("demoted viewer must lose write/admin scope enforcement, got %+v", demoted)
	}
}

func TestRevokeTokenOwnershipAndAdminRevoke(t *testing.T) {
	_, store := setupTokenTestDB(t)
	svc := NewService(store)
	defer svc.Close()

	ctx := context.Background()
	owner := createTestUser(t, store, "owner@example.com", "editor")
	other := createTestUser(t, store, "other@example.com", "admin")

	t1, err := svc.CreateToken(ctx, CreateTokenRequest{UserID: owner.ID, Name: "T1", Scopes: "read"})
	if err != nil {
		t.Fatalf("CreateToken T1: %v", err)
	}
	t2, err := svc.CreateToken(ctx, CreateTokenRequest{UserID: owner.ID, Name: "T2", Scopes: "write"})
	if err != nil {
		t.Fatalf("CreateToken T2: %v", err)
	}

	if err := svc.RevokeTokenForUser(ctx, t1.Token.ID, other.ID); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("cross-user revoke expected ErrTokenNotFound, got %v", err)
	}
	if _, err := svc.Authenticate(ctx, t1.Plaintext); err != nil {
		t.Fatalf("token should remain valid after failed cross-user revoke: %v", err)
	}

	if err := svc.RevokeTokenForUser(ctx, t1.Token.ID, owner.ID); err != nil {
		t.Fatalf("owner revoke failed: %v", err)
	}
	if _, err := svc.Authenticate(ctx, t1.Plaintext); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken after revoke, got %v", err)
	}
	if err := svc.RevokeTokenForUser(ctx, t1.Token.ID, owner.ID); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("revoking already-deleted token expected ErrTokenNotFound, got %v", err)
	}

	if err := svc.RevokeToken(ctx, t2.Token.ID); err != nil {
		t.Fatalf("admin RevokeToken failed: %v", err)
	}
	if _, err := svc.Authenticate(ctx, t2.Plaintext); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken after admin revoke, got %v", err)
	}
}

func TestLastUsedUpdateThrottleAndConcurrency(t *testing.T) {
	conn, store := setupTokenTestDB(t)

	var mu sync.Mutex
	currentNow := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	svc := NewService(
		store,
		WithNowFunc(func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return currentNow
		}),
		WithLastUsedThrottle(time.Hour),
	)
	defer svc.Close()

	ctx := context.Background()
	admin := createTestUser(t, store, "concurrent@example.com", "admin")
	tok, err := svc.CreateToken(ctx, CreateTokenRequest{UserID: admin.ID, Name: "LoadTest", Scopes: "admin"})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	if _, err := svc.Authenticate(ctx, tok.Plaintext); err != nil {
		t.Fatalf("first Authenticate: %v", err)
	}
	svc.Flush()

	stored, err := store.GetAPIToken(ctx, tok.Token.ID)
	if err != nil {
		t.Fatalf("GetAPIToken: %v", err)
	}
	if !stored.LastUsedAt.Valid {
		t.Fatal("expected last_used_at to be populated after Flush")
	}

	// Clear last_used_at directly and verify throttle suppresses immediate re-write.
	if _, err := conn.Exec(`UPDATE api_tokens SET last_used_at = NULL WHERE id = ?`, tok.Token.ID); err != nil {
		t.Fatalf("reset last_used_at: %v", err)
	}

	mu.Lock()
	currentNow = currentNow.Add(10 * time.Minute)
	mu.Unlock()
	if _, err := svc.Authenticate(ctx, tok.Plaintext); err != nil {
		t.Fatalf("throttled Authenticate: %v", err)
	}
	svc.Flush()

	stored, err = store.GetAPIToken(ctx, tok.Token.ID)
	if err != nil {
		t.Fatalf("GetAPIToken after throttle: %v", err)
	}
	if stored.LastUsedAt.Valid {
		t.Fatal("expected last_used_at write to be skipped within throttle window")
	}

	// Advance past throttle window and verify update runs again.
	mu.Lock()
	currentNow = currentNow.Add(55 * time.Minute)
	mu.Unlock()
	if _, err := svc.Authenticate(ctx, tok.Plaintext); err != nil {
		t.Fatalf("post-throttle Authenticate: %v", err)
	}
	svc.Flush()

	stored, err = store.GetAPIToken(ctx, tok.Token.ID)
	if err != nil {
		t.Fatalf("GetAPIToken post-throttle: %v", err)
	}
	if !stored.LastUsedAt.Valid {
		t.Fatal("expected last_used_at to be updated after throttle elapsed")
	}

	// Concurrent authenticate + DB write stress test with zero throttle.
	stressSvc := NewService(store, WithLastUsedThrottle(0))
	defer stressSvc.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 64)

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, authErr := stressSvc.Authenticate(ctx, tok.Plaintext); authErr != nil {
					errCh <- authErr
					return
				}
			}
		}()
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, cErr := stressSvc.CreateToken(ctx, CreateTokenRequest{
					UserID: admin.ID,
					Name:   "WorkerToken",
					Scopes: "read",
				}); cErr != nil {
					errCh <- cErr
					return
				}
			}
		}(i)
	}

	wg.Wait()
	stressSvc.Flush()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent operation failed: %v", err)
	}
}
