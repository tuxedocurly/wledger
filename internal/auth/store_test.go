package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
)

func setupTestDB(t *testing.T) (db.Store, func()) {
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	store := db.NewStore(conn)

	return store, func() {
		conn.Close()
	}
}

func TestSQLCStore(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	sessionStore := auth.NewStore(store)

	token := "test_token"
	data := []byte("test_data")
	expiry := time.Now().Add(1 * time.Hour)

	t.Run("Commit and Find", func(t *testing.T) {
		err := sessionStore.Commit(token, data, expiry)
		if err != nil {
			t.Fatalf("failed to commit session: %v", err)
		}

		b, exists, err := sessionStore.Find(token)
		if err != nil {
			t.Fatalf("failed to find session: %v", err)
		}
		if !exists {
			t.Fatal("expected session to exist")
		}
		if string(b) != string(data) {
			t.Errorf("expected data %s, got %s", string(data), string(b))
		}
	})

	t.Run("Find Non-Existent", func(t *testing.T) {
		_, exists, err := sessionStore.Find("non_existent")
		if err != nil {
			t.Fatalf("failed to search non-existent session: %v", err)
		}
		if exists {
			t.Fatal("expected session not to exist")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		err := sessionStore.Delete(token)
		if err != nil {
			t.Fatalf("failed to delete session: %v", err)
		}

		_, exists, err := sessionStore.Find(token)
		if err != nil {
			t.Fatalf("failed to find session after delete: %v", err)
		}
		if exists {
			t.Fatal("expected session not to exist after delete")
		}
	})

	t.Run("Find Expired", func(t *testing.T) {
		expiredToken := "expired_token"
		expiredExpiry := time.Now().Add(-1 * time.Hour)
		err := sessionStore.Commit(expiredToken, data, expiredExpiry)
		if err != nil {
			t.Fatalf("failed to commit expired session: %v", err)
		}

		_, exists, err := sessionStore.Find(expiredToken)
		if err != nil {
			t.Fatalf("failed to find expired session: %v", err)
		}
		if exists {
			t.Fatal("expected expired session not to exist")
		}
	})

	t.Run("CleanupExpired", func(t *testing.T) {
		activeToken := "active_cleanup_token"
		expiredToken := "expired_cleanup_token"
		if err := sessionStore.Commit(activeToken, data, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("commit active: %v", err)
		}
		if err := sessionStore.Commit(expiredToken, data, time.Now().Add(-time.Hour)); err != nil {
			t.Fatalf("commit expired: %v", err)
		}
		if err := sessionStore.CleanupExpired(context.Background()); err != nil {
			t.Fatalf("CleanupExpired failed: %v", err)
		}
		if _, exists, err := sessionStore.Find(activeToken); err != nil || !exists {
			t.Fatalf("expected active token to remain, exists=%v err=%v", exists, err)
		}
		if _, exists, err := sessionStore.Find(expiredToken); err != nil || exists {
			t.Fatalf("expected expired token to be deleted, exists=%v err=%v", exists, err)
		}
	})
}
