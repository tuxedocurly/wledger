package handler

import (
	"net/http"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /login
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	// Check if already logged in
	if h.Session.Exists(r.Context(), config.SessionKeyUserID) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Check if guest access is allowed
	settings, err := h.Queries.GetSettings(r.Context())
	allowGuest := false
	if err == nil {
		// If RequireAuthForRead is FALSE, then Guest is TRUE
		allowGuest = !settings.RequireAuthForRead.Bool
	}

	pages.Login("", allowGuest).Render(r.Context(), w)
}

// POST /login
func (h *Handler) HandleLoginPost(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	// Get settings for re-rendering if needed
	settings, _ := h.Queries.GetSettings(r.Context())
	allowGuest := !settings.RequireAuthForRead.Bool

	// Find user
	user, err := h.Queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		h.Logger.Warn("failed login attempt: user not found", "email", email, "ip", r.RemoteAddr)
		// Generic error message for security purposes
		pages.Login("Invalid email or password", allowGuest).Render(r.Context(), w)
		return
	}

	// check password
	if !auth.CheckPassword(password, user.PasswordHash) {
		h.Logger.Warn("failed login attempt: wrong password", "email", email, "ip", r.RemoteAddr)
		pages.Login("Invalid email or password", allowGuest).Render(r.Context(), w)
		return
	}

	// Login success - Renew token
	if err := h.Session.RenewToken(r.Context()); err != nil {
		h.UIError.Respond(w, r, err, "Session renewal failed", http.StatusInternalServerError)
		return
	}

	// Store session data
	h.Session.Put(r.Context(), config.SessionKeyUserID, int64(user.ID))
	h.Session.Put(r.Context(), config.SessionKeyRole, user.Role)

	h.Logger.Info("user logged in", "email", email)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// POST /logout
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	// Destroy wipes the data, Renew creates a fresh ID
	err := h.Session.Destroy(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to destroy session", http.StatusInternalServerError)
		return
	}

	err = h.Session.RenewToken(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to renew token", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// GET /setup
func (h *Handler) HandleSetup(w http.ResponseWriter, r *http.Request) {
	// Don't show setup form if setup is already done
	count, _ := h.Queries.CountUsers(r.Context())
	if count > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	pages.Setup().Render(r.Context(), w)
}

// POST /setup
func (h *Handler) HandleSetupPost(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	// Check count (Race condition prevention)
	count, _ := h.Queries.CountUsers(r.Context())
	if count > 0 {
		h.UIError.Respond(w, r, nil, "Setup already completed", http.StatusForbidden)
		return
	}

	if err := auth.ValidatePassword(password); err != nil {
		h.UIError.Respond(w, r, err, err.Error(), http.StatusBadRequest)
		return
	}

	// Hash password
	hash, err := auth.HashPassword(password)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to process password", http.StatusInternalServerError)
		return
	}

	// Create admin user
	user, err := h.Queries.CreateUser(r.Context(), db.CreateUserParams{
		Email:        email,
		PasswordHash: hash,
		Role:         "admin",
	})
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to create initial user", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("created initial admin user", "email", email)

	// Init settings
	_ = h.Queries.InitSettings(r.Context())

	// Auto-login the user
	if err := h.Session.RenewToken(r.Context()); err == nil {
		h.Session.Put(r.Context(), config.SessionKeyUserID, int64(user.ID))
		h.Session.Put(r.Context(), config.SessionKeyRole, user.Role)
		h.Logger.Info("auto-logged in new admin user", "user_id", user.ID)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	} else {
		h.UIError.Respond(w, r, err, "Auto-login failed after setup", http.StatusInternalServerError)
	}
}

// GET /force-reset
func (h *Handler) HandleForceReset(w http.ResponseWriter, r *http.Request) {
	userID := h.Session.GetInt64(r.Context(), config.SessionKeyUserID)
	user, err := h.Queries.GetUser(r.Context(), userID)

	if err != nil || !user.ChangePasswordRequired.Bool {
		if err != nil {
			h.Logger.Error("failed to fetch user for force-reset check", "err", err, "user_id", userID)
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	pages.ForceReset().Render(r.Context(), w)
}

// POST /force-reset
func (h *Handler) HandleForceResetPost(w http.ResponseWriter, r *http.Request) {
	userID := h.Session.GetInt64(r.Context(), config.SessionKeyUserID)

	newPw := r.FormValue("new_password")
	confirmPw := r.FormValue("confirm_password")

	if newPw != confirmPw {
		h.Logger.Warn("forced password reset failed: mismatch", "user_id", userID)
		h.UIError.Respond(w, r, nil, "Passwords do not match", http.StatusBadRequest)
		return
	}

	if err := auth.ValidatePassword(newPw); err != nil {
		h.UIError.Respond(w, r, err, err.Error(), http.StatusBadRequest)
		return
	}

	// Use auth helper
	hash, err := auth.HashPassword(newPw)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to hash new password", http.StatusInternalServerError)
		return
	}

	// Update password AND clear password reset flag (Handled by SQL)
	err = h.Queries.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{
		PasswordHash: hash,
		ID:           userID,
	})
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to update password", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("user completed forced password reset", "user_id", userID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
