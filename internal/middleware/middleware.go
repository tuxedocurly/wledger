package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/i18n"
	"github.com/tuxedocurly/wledger/internal/uierror"
)

type ContextKey string

const (
	UserContextKey ContextKey = ContextKey(config.SessionKeyUserID)
)

type Manager struct {
	Queries db.Store
	Session *scs.SessionManager
	Logger  *slog.Logger
	UIError *uierror.Responder
}

func New(q db.Store, sm *scs.SessionManager, l *slog.Logger, uiError *uierror.Responder) *Manager {
	return &Manager{
		Queries: q,
		Session: sm,
		Logger:  l,
		UIError: uiError,
	}
}

func (m *Manager) RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)
		m.Logger.Info(
			"http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"duration", time.Since(start),
			"ip", r.RemoteAddr,
		)
	})
}

// RequireAuth forces a login for WRITE/Protected operations
func (m *Manager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use GetInt to match how Login stores user_id
		userID := m.Session.GetInt64(r.Context(), config.SessionKeyUserID)
		if userID == 0 {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Cast to int64 for DB compatibility
		ctx := context.WithValue(r.Context(), UserContextKey, int64(userID))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireReadAuth checks the DB setting. If "Require Auth" is ON, it behaves like RequireAuth
func (m *Manager) RequireReadAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Determine Identity
		userID := m.Session.GetInt64(r.Context(), config.SessionKeyUserID)
		var user auth.User

		if userID > 0 {
			// Authenticated
			u, err := m.Queries.GetUser(r.Context(), userID)
			if err == nil {
				user = auth.User{ID: u.ID, Role: u.Role, IsGuest: false}
			} else {
				user = auth.Guest()
			}
		} else {
			user = auth.Guest()
		}

		// Fetch Settings
		s, err := m.Queries.GetSettings(r.Context())
		if err != nil {
			m.UIError.Respond(w, r, err, "Failed to load system settings", http.StatusInternalServerError)
			return
		}

		// Authorization Check
		if !user.CanRead(s) {
			m.Logger.Warn("denied read access", "user_id", userID, "ip", r.RemoteAddr, "path", r.URL.Path)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Pass Context
		if userID > 0 {
			ctx := context.WithValue(r.Context(), UserContextKey, int64(userID))
			next.ServeHTTP(w, r.WithContext(ctx))
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

func (m *Manager) FirstRunCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/setup" || strings.HasPrefix(path, config.UrlPrefixStatic) || path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		count, err := m.Queries.CountUsers(r.Context())
		if err != nil {
			m.Logger.Error("failed to count users for first run check", "err", err)
			next.ServeHTTP(w, r)
			return
		}

		if count == 0 {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RequirePasswordChange checks if the user is flagged to change their password.
func (m *Manager) RequirePasswordChange(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip checks for the reset page itself, logout, and static files
		path := r.URL.Path
		if path == "/force-reset" || path == "/logout" || strings.HasPrefix(path, config.UrlPrefixStatic) {
			next.ServeHTTP(w, r)
			return
		}

		// Retrieve User ID
		// First, check the Context (populated by RequireAuth/RequireReadAuth)
		userID, ok := r.Context().Value(UserContextKey).(int64)

		// If not in context, try Session directly (fallback)
		if !ok || userID == 0 {
			userID = m.Session.GetInt64(r.Context(), config.SessionKeyUserID)
		}

		// If still 0, user is definitely not logged in
		// If this is a public route, let user pass. If protected, RequireAuth will catch user
		if userID == 0 {
			next.ServeHTTP(w, r)
			return
		}

		// Check DB for require password change Flag
		user, err := m.Queries.GetUser(r.Context(), userID)
		if err != nil {
			m.UIError.Respond(w, r, err, "Failed to verify account status", http.StatusInternalServerError)
			return
		}

		if user.ChangePasswordRequired.Bool {
			m.Logger.Info("redirecting to force-reset", "user_id", userID)
			http.Redirect(w, r, "/force-reset", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RequireRole enforces role-based access.
// acceptedRoles: A list of roles allowed to access the route ("admin", "editor", "viewer")
func (m *Manager) RequireRole(acceptedRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get User ID (Check Context first, then Session)
			userID, ok := r.Context().Value(UserContextKey).(int64)
			if !ok || userID == 0 {
				userID = m.Session.GetInt64(r.Context(), config.SessionKeyUserID)
			}

			if userID == 0 {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			// Fetch User to get Role
			user, err := m.Queries.GetUser(r.Context(), userID)
			if err != nil {
				m.UIError.Respond(w, r, err, "Failed to verify permissions", http.StatusInternalServerError)
				return
			}

			// Check if user's role is in the allowed list
			allowed := false
			for _, role := range acceptedRoles {
				if user.Role == role {
					allowed = true
					break
				}
			}

			if !allowed {
				m.UIError.Respond(w, r, nil, "Forbidden: Insufficient Permissions", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Authenticate is a middleware that populates the context with the auth.User.
// It acts as the bridge between the Session (Cookie) and the Context (Application).
func (m *Manager) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check Session for ID
		userID := m.Session.GetInt64(r.Context(), config.SessionKeyUserID)

		// If no ID in session, they are a Guest
		if userID == 0 {
			ctx := auth.WithUser(r.Context(), auth.Guest())
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Fetch user from DB
		// Role is needed to construct the full auth.User
		dbUser, err := m.Queries.GetUser(r.Context(), userID)
		if err != nil {
			// If DB fails (e.g., user deleted but session remains), destroy session,
			// downgrade to Guest, log it as info/warn, not error, to avoid log spam on stale sessions
			_ = m.Session.Destroy(r.Context())
			m.Logger.Warn("authentication failed: user not found for session", "user_id", userID, "err", err)
			ctx := auth.WithUser(r.Context(), auth.Guest())
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Construct the UI User
		uiUser := auth.User{
			ID:      dbUser.ID,
			Email:   dbUser.Email,
			Role:    dbUser.Role,
			IsGuest: false,
		}

		// Update Context
		// Now every handler downstream can call auth.GetUser(r.Context())
		ctx := auth.WithUser(r.Context(), uiUser)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// I18n is a middleware that detects the user's preferred language and populates the context with a Localizer.
func (m *Manager) I18n(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. URL Query Param
		lang := r.URL.Query().Get("lang")

		// 2. Cookie
		if lang == "" {
			cookie, err := r.Cookie("lang")
			if err == nil {
				lang = cookie.Value
			}
		}

		// 3. Accept-Language Header
		if lang == "" {
			acceptLang := r.Header.Get("Accept-Language")
			if acceptLang != "" {
				// Simple parsing, taking the first one
				lang = strings.Split(acceptLang, ",")[0]
				lang = strings.Split(lang, ";")[0] // Handle q-values
				lang = strings.TrimSpace(lang)
			}
		}

		// Update Context with Localizer
		ctx := i18n.WithLocalizer(r.Context(), lang)

		// If lang was provided in query param, persist it to a cookie for future requests
		if r.URL.Query().Get("lang") != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     "lang",
				Value:    lang,
				Path:     "/",
				HttpOnly: true,
				MaxAge:   365 * 24 * 60 * 60, // 1 year
				SameSite: http.SameSiteLaxMode,
			})
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
