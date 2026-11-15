package settings

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"golang.org/x/crypto/bcrypt"
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

func TestService_PasswordHashingAndValidation(t *testing.T) {
	s, _, dbConn := setupTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	// Reject short and >72 byte passwords on CreateUser.
	if _, err := s.CreateUser(ctx, CreateUserParams{Email: "bad@test.com", Role: "viewer", Password: ""}); err == nil {
		t.Fatal("expected empty password to be rejected on CreateUser")
	}
	if _, err := s.CreateUser(ctx, CreateUserParams{Email: "bad@test.com", Role: "viewer", Password: "short"}); err == nil {
		t.Fatal("expected short password to be rejected on CreateUser")
	}
	if _, err := s.CreateUser(ctx, CreateUserParams{Email: "bad@test.com", Role: "viewer", Password: strings.Repeat("a", 73)}); err == nil {
		t.Fatal("expected >72 byte password to be rejected on CreateUser")
	}

	user, err := s.CreateUser(ctx, CreateUserParams{
		Email:    "cost@test.com",
		Role:     "editor",
		Password: "valid-password-1",
	})
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	fetched, err := s.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(fetched.PasswordHash))
	if err != nil {
		t.Fatalf("bcrypt.Cost failed: %v", err)
	}
	if cost != auth.BcryptPasswordCost {
		t.Fatalf("CreateUser bcrypt cost = %d, want %d", cost, auth.BcryptPasswordCost)
	}

	// Reject short and >72 byte passwords on UpdateUserPassword.
	if err := s.UpdateUserPassword(ctx, user.ID, "short"); err == nil {
		t.Fatal("expected short password to be rejected on UpdateUserPassword")
	}
	if err := s.UpdateUserPassword(ctx, user.ID, strings.Repeat("b", 73)); err == nil {
		t.Fatal("expected >72 byte password to be rejected on UpdateUserPassword")
	}

	if err := s.UpdateUserPassword(ctx, user.ID, "valid-password-2"); err != nil {
		t.Fatalf("UpdateUserPassword failed: %v", err)
	}
	updated, err := s.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser after update failed: %v", err)
	}
	cost, err = bcrypt.Cost([]byte(updated.PasswordHash))
	if err != nil {
		t.Fatalf("bcrypt.Cost after update failed: %v", err)
	}
	if cost != auth.BcryptPasswordCost {
		t.Fatalf("UpdateUserPassword bcrypt cost = %d, want %d", cost, auth.BcryptPasswordCost)
	}
	if !auth.CheckPassword("valid-password-2", updated.PasswordHash) {
		t.Fatal("updated password hash did not verify")
	}
}
