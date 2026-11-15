package handler

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/logger"
	"github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /settings
func (h *Handler) HandleSettings(w http.ResponseWriter, r *http.Request) {
	// Get User
	user := auth.GetUserFromRequest(r)

	var settings db.Setting
	var users []db.ListUsersRow
	var apiTokens []db.ListAPITokensByUserRow
	var err error

	// Fetch Admin Data (Only if User is Admin)
	// Viewers/Editors don't need this data since the template hides those sections
	if user.IsAdmin() {
		settings, err = h.Settings.GetSettings(r.Context())
		if err != nil {
			h.Logger.Error("failed to get settings", "err", err)
		}

		users, err = h.Settings.ListUsers(r.Context())
		if err != nil {
			h.Logger.Error("failed to list users", "err", err)
			users = []db.ListUsersRow{}
		}
	} else {
		// Initialize empty for non-admins to satisfy signature
		users = []db.ListUsersRow{}
	}

	if user.IsAuthenticated() && h.Queries != nil {
		apiTokens, err = h.Queries.ListAPITokensByUser(r.Context(), user.ID)
		if err != nil {
			if h.Logger != nil {
				h.Logger.Error("failed to list api tokens", "err", err, "user_id", user.ID)
			}
			apiTokens = []db.ListAPITokensByUserRow{}
		}
	}

	// Render with User object
	pages.Settings(user, settings, users, apiTokens).Render(r.Context(), w)
}

// POST /settings
func (h *Handler) HandleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	requireAuth := r.FormValue("require_auth") == "on"
	enableTimeout := r.FormValue("enable_timeout") == "on"
	enableDebugLogs := r.FormValue("enable_debug_logs") == "on"
	timeout, _ := strconv.Atoi(r.FormValue("locate_timeout"))

	var enablePing *bool
	var pingInterval *int
	if r.Form.Has("controller_ping_interval") {
		interval, convErr := strconv.Atoi(r.FormValue("controller_ping_interval"))
		if convErr != nil || interval <= 0 {
			interval = 600
		} else if interval < 10 {
			interval = 10
		} else if interval > 86400 {
			interval = 86400
		}
		enabled := r.FormValue("enable_controller_ping") == "on"
		enablePing = &enabled
		pingInterval = &interval
	} else if r.Form.Has("enable_controller_ping") {
		enabled := r.FormValue("enable_controller_ping") == "on"
		enablePing = &enabled
	}

	colorLocate := r.FormValue("color_locate")
	colorOk := r.FormValue("color_ok")
	colorLow := r.FormValue("color_low")
	colorCritical := r.FormValue("color_critical")

	ctx := r.Context()

	err := h.Settings.UpdateSettings(ctx, settings.UpdateSettingsParams{
		RequireAuth:            requireAuth,
		LocateTimeout:          timeout,
		EnableLocateTimeout:    enableTimeout,
		EnableDebugLogs:        enableDebugLogs,
		ColorLocate:            colorLocate,
		ColorOk:                colorOk,
		ColorLow:               colorLow,
		ColorCritical:          colorCritical,
		EnableControllerPing:   enablePing,
		ControllerPingInterval: pingInterval,
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to update settings", http.StatusInternalServerError)
		return
	}

	// Update Runtime Log Level
	logger.SetDebug(enableDebugLogs)
	h.notifyStatusPollerSettingsChanged()

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// POST /settings/password (Self-Service)
func (h *Handler) HandleSettingsPassword(w http.ResponseWriter, r *http.Request) {
	userID := h.Session.GetInt64(r.Context(), config.SessionKeyUserID)
	if userID == 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	currentPw := r.FormValue("current_password")
	newPw := r.FormValue("new_password")
	confirmPw := r.FormValue("confirm_password")

	if newPw != confirmPw {
		h.Logger.Warn("password change failed: mismatch", "user_id", userID)
		h.UIError.Respond(w, r, nil, "Passwords do not match", http.StatusBadRequest)
		return
	}

	if err := auth.ValidatePassword(newPw); err != nil {
		h.Logger.Warn("password change failed: invalid new password", "user_id", userID, "err", err)
		h.UIError.Respond(w, r, err, err.Error(), http.StatusBadRequest)
		return
	}

	user, err := h.Settings.GetUser(r.Context(), userID)
	if err != nil {
		h.UIError.Respond(w, r, err, "User not found", http.StatusInternalServerError)
		return
	}

	if !auth.CheckPassword(currentPw, user.PasswordHash) {
		h.Logger.Warn("password change failed: wrong current password", "user_id", userID)
		h.UIError.Respond(w, r, nil, "Current password is incorrect", http.StatusBadRequest)
		return
	}

	err = h.Settings.UpdateUserPassword(r.Context(), userID, newPw)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to update password", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/settings")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// POST /settings/users (Create User)
func (h *Handler) HandleUserCreate(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	role := r.FormValue("role")
	tempPw := r.FormValue("temp_password")

	if err := auth.ValidatePassword(tempPw); err != nil {
		h.UIError.Respond(w, r, err, err.Error(), http.StatusBadRequest)
		return
	}

	_, err := h.Settings.CreateUser(r.Context(), settings.CreateUserParams{
		Email:    email,
		Role:     role,
		Password: tempPw,
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to create user", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("created user", "email", email, "role", role)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// POST /settings/users/{id}/delete
func (h *Handler) HandleUserDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)
	currentUserID := h.Session.GetInt64(r.Context(), config.SessionKeyUserID)

	// Prevent self-deletion
	if int64(id) == currentUserID {
		h.Logger.Warn("prevented self-deletion", "user_id", id)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	err := h.Settings.DeleteUser(r.Context(), int64(id))
	if err != nil {
		h.Logger.Error("failed to delete user", "err", err)
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// POST /settings/users/{id}/reset
func (h *Handler) HandleUserForceReset(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)
	currentUserID := h.Session.GetInt64(r.Context(), config.SessionKeyUserID)

	// Don't flag current user
	if int64(id) == currentUserID {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	err := h.Settings.ForceReset(r.Context(), int64(id))

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to force password reset", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("admin triggered force password reset", "target_id", id)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
