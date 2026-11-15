package settings

import (
	"context"
	"database/sql"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
)

type Service interface {
	GetSettings(ctx context.Context) (db.Setting, error)
	UpdateSettings(ctx context.Context, params UpdateSettingsParams) error
	ListUsers(ctx context.Context) ([]db.ListUsersRow, error)
	GetUser(ctx context.Context, id int64) (db.User, error)
	CreateUser(ctx context.Context, params CreateUserParams) (db.CreateUserRow, error)
	UpdateUserPassword(ctx context.Context, id int64, password string) error
	DeleteUser(ctx context.Context, id int64) error
	ForceReset(ctx context.Context, id int64) error
}

func (s *service) UpdateUserPassword(ctx context.Context, id int64, password string) error {
	hashedPassword, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	return s.store.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
		PasswordHash: hashedPassword,
		ID:           id,
	})
}

type service struct {
	store db.Store
}

func NewService(store db.Store) Service {
	return &service{
		store: store,
	}
}

type UpdateSettingsParams struct {
	RequireAuth            bool
	LocateTimeout          int
	EnableLocateTimeout    bool
	EnableDebugLogs        bool
	EnableControllerPing   *bool
	ControllerPingInterval *int
	ColorLocate            string
	ColorOk                string
	ColorLow               string
	ColorCritical          string
}

type CreateUserParams struct {
	Email    string
	Role     string
	Password string
}

func (s *service) GetSettings(ctx context.Context) (db.Setting, error) {
	return s.store.GetSettings(ctx)
}

func (s *service) UpdateSettings(ctx context.Context, params UpdateSettingsParams) error {
	// Fetch current for diffing
	current, _ := s.store.GetSettings(ctx)

	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		var enablePingParam sql.NullBool
		if params.EnableControllerPing != nil {
			enablePingParam = sql.NullBool{Bool: *params.EnableControllerPing, Valid: true}
		}
		var pingIntervalParam sql.NullInt64
		if params.ControllerPingInterval != nil {
			pingIntervalParam = sql.NullInt64{Int64: int64(*params.ControllerPingInterval), Valid: true}
		}

		err := q.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
			RequireAuthForRead:            sql.NullBool{Bool: params.RequireAuth, Valid: true},
			LocateTimeoutSeconds:          sql.NullInt64{Int64: int64(params.LocateTimeout), Valid: true},
			EnableLocateTimeout:           sql.NullBool{Bool: params.EnableLocateTimeout, Valid: true},
			EnableDebugLogs:               sql.NullBool{Bool: params.EnableDebugLogs, Valid: true},
			EnableControllerPing:          enablePingParam,
			ControllerPingIntervalSeconds: pingIntervalParam,
		})
		if err != nil {
			return err
		}

		err = q.UpdateColors(ctx, db.UpdateColorsParams{
			ColorLocate:        sql.NullString{String: params.ColorLocate, Valid: params.ColorLocate != ""},
			ColorStockOk:       sql.NullString{String: params.ColorOk, Valid: params.ColorOk != ""},
			ColorStockLow:      sql.NullString{String: params.ColorLow, Valid: params.ColorLow != ""},
			ColorStockCritical: sql.NullString{String: params.ColorCritical, Valid: params.ColorCritical != ""},
		})
		if err != nil {
			return err
		}

		// Diffing Logic
		oldDiff := make(map[string]any)
		newDiff := make(map[string]any)

		if current.RequireAuthForRead.Bool != params.RequireAuth {
			oldDiff["require_auth"] = current.RequireAuthForRead.Bool
			newDiff["require_auth"] = params.RequireAuth
		}
		if current.EnableDebugLogs.Bool != params.EnableDebugLogs {
			oldDiff["enable_debug_logs"] = current.EnableDebugLogs.Bool
			newDiff["enable_debug_logs"] = params.EnableDebugLogs
		}
		if current.EnableLocateTimeout.Bool != params.EnableLocateTimeout {
			oldDiff["enable_timeout"] = current.EnableLocateTimeout.Bool
			newDiff["enable_timeout"] = params.EnableLocateTimeout
		}
		if int(current.LocateTimeoutSeconds.Int64) != params.LocateTimeout {
			oldDiff["locate_timeout"] = current.LocateTimeoutSeconds.Int64
			newDiff["locate_timeout"] = params.LocateTimeout
		}
		currentPingEnabled := !current.EnableControllerPing.Valid || current.EnableControllerPing.Bool
		if params.EnableControllerPing != nil && currentPingEnabled != *params.EnableControllerPing {
			oldDiff["enable_controller_ping"] = currentPingEnabled
			newDiff["enable_controller_ping"] = *params.EnableControllerPing
		}
		if params.ControllerPingInterval != nil && int(current.ControllerPingIntervalSeconds.Int64) != *params.ControllerPingInterval {
			oldDiff["controller_ping_interval"] = current.ControllerPingIntervalSeconds.Int64
			newDiff["controller_ping_interval"] = *params.ControllerPingInterval
		}
		if params.ColorLocate != "" && current.ColorLocate.String != params.ColorLocate {
			oldDiff["color_locate"] = current.ColorLocate.String
			newDiff["color_locate"] = params.ColorLocate
		}
		if params.ColorOk != "" && current.ColorStockOk.String != params.ColorOk {
			oldDiff["color_ok"] = current.ColorStockOk.String
			newDiff["color_ok"] = params.ColorOk
		}
		if params.ColorLow != "" && current.ColorStockLow.String != params.ColorLow {
			oldDiff["color_low"] = current.ColorStockLow.String
			newDiff["color_low"] = params.ColorLow
		}
		if params.ColorCritical != "" && current.ColorStockCritical.String != params.ColorCritical {
			oldDiff["color_critical"] = current.ColorStockCritical.String
			newDiff["color_critical"] = params.ColorCritical
		}

		if len(oldDiff) > 0 {
			audit.Log(ctx, q, "UPDATE", "SETTINGS", 1, "Updated system configuration", oldDiff, newDiff)
		}

		return nil
	})
	return err
}

func (s *service) ListUsers(ctx context.Context) ([]db.ListUsersRow, error) {
	return s.store.ListUsers(ctx)
}

func (s *service) GetUser(ctx context.Context, id int64) (db.User, error) {
	return s.store.GetUser(ctx, id)
}

func (s *service) CreateUser(ctx context.Context, params CreateUserParams) (db.CreateUserRow, error) {
	hashedPassword, err := auth.HashPassword(params.Password)
	if err != nil {
		return db.CreateUserRow{}, err
	}

	user, err := s.store.CreateUser(ctx, db.CreateUserParams{
		Email:                  params.Email,
		PasswordHash:           hashedPassword,
		Role:                   params.Role,
		ChangePasswordRequired: sql.NullBool{Bool: true, Valid: true},
	})
	if err != nil {
		return user, err
	}

	audit.Log(ctx, s.store, "CREATE", "USER", user.ID, "Created user", nil,
		map[string]any{"email": user.Email, "role": user.Role})

	return user, nil
}

func (s *service) DeleteUser(ctx context.Context, id int64) error {
	// Fetch before delete
	u, err := s.store.GetUser(ctx, id)
	if err == nil {
		audit.Log(ctx, s.store, "DELETE", "USER", id, "Deleted user",
			map[string]any{"email": u.Email, "role": u.Role}, nil)
	}

	return s.store.DeleteUser(ctx, id)
}

func (s *service) ForceReset(ctx context.Context, id int64) error {
	err := s.store.SetPasswordResetFlag(ctx, db.SetPasswordResetFlagParams{
		ChangePasswordRequired: sql.NullBool{Bool: true, Valid: true},
		ID:                     id,
	})
	if err != nil {
		return err
	}

	audit.Log(ctx, s.store, "UPDATE", "USER", id, "Forced password reset",
		map[string]any{"reset_required": false},
		map[string]any{"reset_required": true})

	return nil
}

