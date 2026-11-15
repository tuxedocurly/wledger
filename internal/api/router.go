package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/apitoken"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/middleware"
)

type apiContextKey string

const principalContextKey apiContextKey = "api_token_principal"

// NewRouter constructs the JSON REST API v1 chi router (mounted under /api/v1).
func NewRouter(deps Dependencies) http.Handler {
	h := &apiHandler{deps: deps}

	r := chi.NewRouter()

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "api endpoint not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	})

	// Unauthenticated health probe for monitors and container readiness checks.
	r.Get("/health", h.handleHealth)

	// Authenticated REST v1 endpoints.
	r.Group(func(r chi.Router) {
		r.Use(bearerAuthMiddleware(deps.Tokens))

		// Parts
		r.With(requireScope(apitoken.ScopeRead)).Get("/parts", h.handleListParts)
		r.With(requireScope(apitoken.ScopeWrite)).Post("/parts", h.handleCreatePart)
		r.With(requireScope(apitoken.ScopeRead)).Get("/parts/{id}", h.handleGetPart)
		r.With(requireScope(apitoken.ScopeWrite)).Put("/parts/{id}", h.handleUpdatePart)
		r.With(requireScope(apitoken.ScopeWrite)).Delete("/parts/{id}", h.handleDeletePart)
		r.With(requireScope(apitoken.ScopeRead)).Post("/parts/{id}/locate", h.handleLocatePart)

		// Inventory alerts & stock operations
		r.With(requireScope(apitoken.ScopeRead)).Get("/low-stock", h.handleLowStock)
		r.With(requireScope(apitoken.ScopeWrite)).Post("/stock/assign", h.handleAssignStock)
		r.With(requireScope(apitoken.ScopeWrite)).Post("/stock/{assignmentID}/adjust", h.handleAdjustStock)

		// Barcodes
		r.With(requireScope(apitoken.ScopeRead)).Post("/barcodes/parse", h.handleParseBarcode)

		// Hardware & WLED
		r.With(requireScope(apitoken.ScopeRead)).Get("/containers", h.handleListContainers)
		r.With(requireScope(apitoken.ScopeRead)).Get("/controllers", h.handleListControllers)
		r.With(requireScope(apitoken.ScopeRead)).Post("/controllers/{id}/locate", h.handleLocateController)
		r.With(requireScope(apitoken.ScopeRead)).Post("/hardware/stop-locate", h.handleStopLocate)

		// Projects & BOM
		r.With(requireScope(apitoken.ScopeRead)).Get("/projects", h.handleListProjects)
		r.With(requireScope(apitoken.ScopeRead)).Post("/projects/{id}/locate", h.handleLocateProject)
		r.With(requireScope(apitoken.ScopeWrite)).Post("/projects/{id}/build", h.handleBuildProject)
	})

	return r
}

func bearerAuthMiddleware(tokens TokenAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawToken, ok := extractBearerToken(r.Header.Get("Authorization"))
			if !ok {
				writeUnauthorized(w, "missing or invalid Authorization Bearer token")
				return
			}
			if tokens == nil {
				writeError(w, http.StatusServiceUnavailable, "token authentication service unavailable")
				return
			}

			principal, err := tokens.Authenticate(r.Context(), rawToken)
			if err != nil {
				if errors.Is(err, apitoken.ErrExpiredToken) {
					writeUnauthorized(w, "api token expired")
					return
				}
				writeUnauthorized(w, "invalid api token")
				return
			}

			ctx := context.WithValue(r.Context(), principalContextKey, principal)
			ctx = auth.WithUser(ctx, principal.User)
			ctx = context.WithValue(ctx, middleware.UserContextKey, principal.User.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func requireScope(required string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := r.Context().Value(principalContextKey).(apitoken.Principal)
			if !ok {
				writeUnauthorized(w, "authentication required")
				return
			}

			if !principal.HasScope(required) {
				writeError(w, http.StatusForbidden, "insufficient token scope or role permissions")
				return
			}

			switch required {
			case apitoken.ScopeWrite:
				if !principal.User.CanWrite() {
					writeError(w, http.StatusForbidden, "write permission required")
					return
				}
			case apitoken.ScopeAdmin:
				if !principal.User.CanConfigure() {
					writeError(w, http.StatusForbidden, "admin permission required")
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func extractBearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if len(header) < len("Bearer ") {
		return "", false
	}
	if !strings.EqualFold(header[:7], "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(header[7:])
	if token == "" {
		return "", false
	}
	return token, true
}

func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="wledger-api"`)
	writeError(w, http.StatusUnauthorized, msg)
}
