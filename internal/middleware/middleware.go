package middleware

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/i18n"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/web/layouts"
)

type ContextKey string

const (
	UserContextKey ContextKey = ContextKey(config.SessionKeyUserID)
)

type settingsContextKey struct{}

var setupCacheEpoch atomic.Uint64

// ResetSetupCache clears the cached setup-completion state so the next request re-checks CountUsers.
func ResetSetupCache() {
	setupCacheEpoch.Add(1)
}

type Manager struct {
	Queries             db.Store
	Session             *scs.SessionManager
	Logger              *slog.Logger
	UIError             *uierror.Responder
	setupCompletedEpoch atomic.Uint64
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

// MaxBodyBytes wraps request bodies with http.MaxBytesReader to limit upload payloads.
func MaxBodyBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limit > 0 && r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// resolveUser returns the cached auth.User populated by Authenticate when present,
// falling back to a single GetUser lookup only when called standalone without Authenticate.
func (m *Manager) resolveUser(r *http.Request) (user auth.User, fromCtx bool, err error) {
	if ctxUser, ok := auth.LookupUser(r.Context()); ok {
		return ctxUser, true, nil
	}

	userID, _ := r.Context().Value(UserContextKey).(int64)
	if userID == 0 && m.Session != nil {
		userID = m.Session.GetInt64(r.Context(), config.SessionKeyUserID)
	}
	if userID <= 0 {
		return auth.Guest(), false, nil
	}

	dbUser, err := m.Queries.GetUser(r.Context(), userID)
	if err != nil {
		return auth.User{}, false, err
	}

	return auth.User{
		ID:                     dbUser.ID,
		Email:                  dbUser.Email,
		Role:                   dbUser.Role,
		IsGuest:                false,
		ChangePasswordRequired: dbUser.ChangePasswordRequired.Bool,
	}, false, nil
}

// InjectStockColors loads saved stock indicator colors into context (and caches db.Setting
// for downstream middleware such as RequireReadAuth) to render CSS variables in base.templ.
func (m *Manager) InjectStockColors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		s, err := m.Queries.GetSettings(ctx)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				m.Logger.Debug("failed to load settings for stock colors", "err", err)
			}
			ctx = layouts.WithStockColors(ctx, layouts.DefaultStockColors())
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		ctx = context.WithValue(ctx, settingsContextKey{}, s)
		ctx = layouts.WithStockColors(ctx, layouts.StockColors{
			Locate:   s.ColorLocate.String,
			Ok:       s.ColorStockOk.String,
			Low:      s.ColorStockLow.String,
			Critical: s.ColorStockCritical.String,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth forces a login for WRITE/Protected operations
func (m *Manager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if ctxUser, ok := auth.LookupUser(ctx); ok {
			if ctxUser.IsGuest || ctxUser.ID == 0 {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			if id, _ := ctx.Value(UserContextKey).(int64); id == 0 {
				ctx = context.WithValue(ctx, UserContextKey, int64(ctxUser.ID))
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		userID := int64(0)
		if m.Session != nil {
			userID = m.Session.GetInt64(ctx, config.SessionKeyUserID)
		}
		if userID == 0 {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		ctx = context.WithValue(ctx, UserContextKey, int64(userID))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireReadAuth checks the DB setting. If "Require Auth" is ON, it behaves like RequireAuth
func (m *Manager) RequireReadAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user, _, err := m.resolveUser(r)
		if err != nil {
			user = auth.Guest()
		}

		s, ok := ctx.Value(settingsContextKey{}).(db.Setting)
		if !ok {
			s, err = m.Queries.GetSettings(ctx)
			if err != nil {
				m.UIError.Respond(w, r, err, "Failed to load system settings", http.StatusInternalServerError)
				return
			}
			ctx = context.WithValue(ctx, settingsContextKey{}, s)
		}

		// Authorization Check
		if !user.CanRead(s) {
			m.Logger.Warn("denied read access", "user_id", user.ID, "ip", r.RemoteAddr, "path", r.URL.Path)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Pass Context
		if !user.IsGuest && user.ID > 0 {
			if id, _ := ctx.Value(UserContextKey).(int64); id == 0 {
				ctx = context.WithValue(ctx, UserContextKey, int64(user.ID))
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Manager) FirstRunCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/setup" || strings.HasPrefix(path, config.UrlPrefixStatic) || path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		currentGen := setupCacheEpoch.Load() + 1
		if m.setupCompletedEpoch.Load() == currentGen {
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

		m.setupCompletedEpoch.Store(currentGen)
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

		user, _, err := m.resolveUser(r)
		if err != nil {
			m.UIError.Respond(w, r, err, "Failed to verify account status", http.StatusInternalServerError)
			return
		}
		if user.IsGuest || user.ID == 0 {
			next.ServeHTTP(w, r)
			return
		}

		if user.ChangePasswordRequired {
			m.Logger.Info("redirecting to force-reset", "user_id", user.ID)
			http.Redirect(w, r, "/force-reset", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RequireRole enforces role-based access.
// acceptedRoles: A list of roles allowed to access the route ("admin", "editor", "viewer", "guest")
func (m *Manager) RequireRole(acceptedRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _, err := m.resolveUser(r)
			if err != nil {
				m.UIError.Respond(w, r, err, "Failed to verify permissions", http.StatusInternalServerError)
				return
			}
			if user.IsGuest || user.ID == 0 {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
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
		userID := int64(0)
		if m.Session != nil {
			userID = m.Session.GetInt64(r.Context(), config.SessionKeyUserID)
		}

		// If no ID in session, they are a Guest
		if userID == 0 {
			ctx := auth.WithUser(r.Context(), auth.Guest())
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Fetch user from DB once per request (includes Role & ChangePasswordRequired)
		dbUser, err := m.Queries.GetUser(r.Context(), userID)
		if err != nil {
			// If DB fails (e.g., user deleted but session remains), destroy session,
			// downgrade to Guest, log it as info/warn, not error, to avoid log spam on stale sessions
			if m.Session != nil {
				_ = m.Session.Destroy(r.Context())
			}
			m.Logger.Warn("authentication failed: user not found for session", "user_id", userID, "err", err)
			ctx := auth.WithUser(r.Context(), auth.Guest())
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		uiUser := auth.User{
			ID:                     dbUser.ID,
			Email:                  dbUser.Email,
			Role:                   dbUser.Role,
			IsGuest:                false,
			ChangePasswordRequired: dbUser.ChangePasswordRequired.Bool,
		}

		ctx := auth.WithUser(r.Context(), uiUser)
		ctx = context.WithValue(ctx, UserContextKey, int64(dbUser.ID))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// I18n is a middleware that detects the user's preferred language and populates the context with a Localizer.
func (m *Manager) I18n(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// URL Query Param
		lang := r.URL.Query().Get("lang")

		// Cookie
		if lang == "" {
			cookie, err := r.Cookie("lang")
			if err == nil {
				lang = cookie.Value
			}
		}

		// Accept-Language Header
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
