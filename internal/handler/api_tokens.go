package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/apitoken"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/web/components"
)

const maxTokenExpiryDays = 3650

// APITokenHandler manages web UI requests for a user's Personal Access Tokens.
type APITokenHandler struct {
	store  db.Store
	tokens apitoken.Service
	uiErr  *uierror.Responder
}

// NewAPITokenHandler constructs an APITokenHandler without adding fields to Handler.
func NewAPITokenHandler(store db.Store, tokenSvc apitoken.Service, uiErr *uierror.Responder) *APITokenHandler {
	return &APITokenHandler{
		store:  store,
		tokens: tokenSvc,
		uiErr:  uiErr,
	}
}

// HandleListAPITokens renders the API tokens card partial for the authenticated user.
func (h *APITokenHandler) HandleListAPITokens(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	ctx := ensureTokenAuditContext(r.Context(), user.ID)
	tokens, err := h.listUserTokens(ctx, user.ID)
	if err != nil {
		h.respondError(w, r, err, "Failed to load API tokens", http.StatusInternalServerError)
		return
	}

	h.renderTokensCard(w, r.WithContext(ctx), http.StatusOK, tokens, "")
}

// HandleCreateAPIToken creates a scoped Personal Access Token and renders the one-time plaintext banner.
func (h *APITokenHandler) HandleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if h.tokens == nil {
		h.respondError(w, r, nil, "API token service unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.respondError(w, r, err, "Invalid form data", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	scopes := strings.TrimSpace(r.FormValue("scopes"))
	if scopes == "" {
		scopes = strings.TrimSpace(r.FormValue("scope"))
	}
	if scopes == "" {
		scopes = apitoken.ScopeRead
	}

	var expiresAt *time.Time
	if rawDays := strings.TrimSpace(r.FormValue("expires_days")); rawDays != "" {
		days, err := strconv.Atoi(rawDays)
		if err != nil || days <= 0 || days > maxTokenExpiryDays {
			h.respondError(w, r, err, "Expiration days must be between 1 and 3650", http.StatusBadRequest)
			return
		}
		exp := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
		expiresAt = &exp
	}

	ctx := ensureTokenAuditContext(r.Context(), user.ID)
	created, err := h.tokens.CreateToken(ctx, apitoken.CreateTokenRequest{
		UserID:    user.ID,
		Name:      name,
		Scopes:    scopes,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		h.respondCreateError(w, r, err)
		return
	}

	tokens, err := h.listUserTokens(ctx, user.ID)
	if err != nil {
		h.respondError(w, r, err, "Token created, but failed to refresh token list", http.StatusInternalServerError)
		return
	}

	h.renderTokensCard(w, r.WithContext(ctx), http.StatusCreated, tokens, created.Plaintext)
}

// HandleRevokeAPIToken deletes an API token owned by the authenticated user.
func (h *APITokenHandler) HandleRevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if h.tokens == nil {
		h.respondError(w, r, nil, "API token service unavailable", http.StatusServiceUnavailable)
		return
	}

	tokenID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || tokenID <= 0 {
		h.respondError(w, r, err, "Invalid API token ID", http.StatusBadRequest)
		return
	}

	ctx := ensureTokenAuditContext(r.Context(), user.ID)
	if err := h.tokens.RevokeTokenForUser(ctx, tokenID, user.ID); err != nil {
		if errors.Is(err, apitoken.ErrTokenNotFound) {
			h.respondError(w, r, err, "API token not found", http.StatusNotFound)
			return
		}
		h.respondError(w, r, err, "Failed to revoke API token", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" || r.Method == http.MethodDelete {
		tokens, err := h.listUserTokens(ctx, user.ID)
		if err != nil {
			h.respondError(w, r, err, "Token revoked, but failed to refresh token list", http.StatusInternalServerError)
			return
		}
		h.renderTokensCard(w, r.WithContext(ctx), http.StatusOK, tokens, "")
		return
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *APITokenHandler) requireUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user := auth.GetUserFromRequest(r)
	if !user.IsAuthenticated() {
		h.respondError(w, r, nil, "Authentication required", http.StatusUnauthorized)
		return auth.User{}, false
	}
	return user, true
}

func (h *APITokenHandler) listUserTokens(ctx context.Context, userID int64) ([]db.ListAPITokensByUserRow, error) {
	if h.tokens != nil {
		return h.tokens.ListTokensByUser(ctx, userID)
	}
	if h.store != nil {
		rows, err := h.store.ListAPITokensByUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		if rows == nil {
			return []db.ListAPITokensByUserRow{}, nil
		}
		return rows, nil
	}
	return []db.ListAPITokensByUserRow{}, nil
}

func (h *APITokenHandler) renderTokensCard(w http.ResponseWriter, r *http.Request, status int, tokens []db.ListAPITokensByUserRow, newToken string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = components.APITokens(tokens, newToken).Render(r.Context(), w)
}

func (h *APITokenHandler) respondCreateError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, apitoken.ErrInvalidTokenName):
		h.respondError(w, r, err, "Token name is required", http.StatusBadRequest)
	case errors.Is(err, apitoken.ErrInvalidScope):
		h.respondError(w, r, err, "Invalid token scope", http.StatusBadRequest)
	case errors.Is(err, apitoken.ErrInvalidExpiration):
		h.respondError(w, r, err, "Invalid expiration date", http.StatusBadRequest)
	case errors.Is(err, apitoken.ErrScopeExceedsRole):
		h.respondError(w, r, err, "Requested scope exceeds your account role permissions", http.StatusForbidden)
	case errors.Is(err, apitoken.ErrUserNotFound):
		h.respondError(w, r, err, "User account not found", http.StatusNotFound)
	default:
		h.respondError(w, r, err, "Failed to create API token", http.StatusInternalServerError)
	}
}

func (h *APITokenHandler) respondError(w http.ResponseWriter, r *http.Request, err error, message string, code int) {
	if h.uiErr != nil {
		h.uiErr.Respond(w, r, err, message, code)
		return
	}
	http.Error(w, message, code)
}

func ensureTokenAuditContext(ctx context.Context, userID int64) context.Context {
	if id, ok := ctx.Value(middleware.UserContextKey).(int64); ok && id == userID {
		return ctx
	}
	return context.WithValue(ctx, middleware.UserContextKey, userID)
}
