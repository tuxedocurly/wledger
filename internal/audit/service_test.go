package audit

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
)

func setupTest(t *testing.T) (Service, db.Store, *sql.DB) {
	dbConn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	store := db.NewStore(dbConn)
	return NewService(store), store, dbConn
}

func TestService_LogAndList(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()

	// Create a user to satisfy FK
	user, err := store.CreateUser(ctx, db.CreateUserParams{
		Email:        "audit@test.com",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Add user context
	ctx = context.WithValue(ctx, middleware.UserContextKey, user.ID)

	s.Log(ctx, "CREATE", "PART", 1, "test log", nil, map[string]any{"name": "test"})

	logs, err := s.ListLogs(ctx, db.ListAuditLogsParams{
		Limit:  10,
		Offset: 0,
	})

	if err != nil {
		t.Fatalf("failed to list logs: %v", err)
	}

	if len(logs) != 1 {
		t.Errorf("expected 1 log, got %d", len(logs))
	}

	if logs[0].UserID.Int64 != user.ID {
		t.Errorf("expected userID %d, got %d", user.ID, logs[0].UserID.Int64)
	}

	if logs[0].Details.String != "test log" {
		t.Errorf("expected details 'test log', got %s", logs[0].Details.String)
	}
}
