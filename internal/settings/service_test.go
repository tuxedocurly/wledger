package settings

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
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

func TestService_SettingsFlow(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	store.InitSettings(ctx)

	params := UpdateSettingsParams{
		RequireAuth:         true,
		LocateTimeout:       20,
		EnableLocateTimeout: true,
		EnableDebugLogs:     true,
		ColorLocate:         "#FF0000",
		ColorOk:             "#00FF00",
		ColorLow:            "#FFFF00",
		ColorCritical:       "#0000FF",
	}

	err := s.UpdateSettings(ctx, params)
	if err != nil {
		t.Fatalf("failed to update settings: %v", err)
	}

	settings, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get settings: %v", err)
	}

	if settings.LocateTimeoutSeconds.Int64 != 20 {
		t.Errorf("expected 20, got %d", settings.LocateTimeoutSeconds.Int64)
	}
	if settings.ColorLocate.String != "#FF0000" {
		t.Errorf("expected #FF0000, got %s", settings.ColorLocate.String)
	}
}

func TestService_UserFlow(t *testing.T) {
	s, _, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()

	// Create
	user, err := s.CreateUser(ctx, CreateUserParams{
		Email:    "test@test.com",
		Role:     "admin",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// List
	users, _ := s.ListUsers(ctx)
	if len(users) != 1 {
		t.Errorf("expected 1 user, got %d", len(users))
	}

	// Force Reset
	err = s.ForceReset(ctx, user.ID)
	if err != nil {
		t.Errorf("failed to force reset: %v", err)
	}

	fetched, _ := s.GetUser(ctx, user.ID)
	if !fetched.ChangePasswordRequired.Bool {
		t.Error("expected ChangePasswordRequired to be true")
	}

	// Delete
	err = s.DeleteUser(ctx, user.ID)
	if err != nil {
		t.Errorf("failed to delete: %v", err)
	}

	usersAfter, _ := s.ListUsers(ctx)
	if len(usersAfter) != 0 {
		t.Errorf("expected 0 users, got %d", len(usersAfter))
	}
}
