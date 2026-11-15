package uierror

import (
	"log/slog"
	"net/http"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/web/components"
	"github.com/tuxedocurly/wledger/web/pages"
)

// Responder handles logging and rendering errors for the UI.
// It supports both full-page error rendering and HTMX out-of-band error toasts.
type Responder struct {
	logger *slog.Logger
}

// New creates a new UI error Responder.
func New(logger *slog.Logger) *Responder {
	return &Responder{
		logger: logger,
	}
}

// Respond standardizes error handling across handlers and middleware.
// It logs the error with context and renders a user-friendly response.
// If the request is an HTMX request, it sets HX-Reswap: none and returns an
// out-of-band Error Toast targeting #toast-container so both hx-swap="none"
// actions and targeted swaps display the error without clobbering page content.
func (r *Responder) Respond(w http.ResponseWriter, req *http.Request, err error, message string, code int) {
	if r.logger != nil {
		r.logger.Error(message, "err", err, "path", req.URL.Path, "method", req.Method)
	}

	isHTMX := req.Header.Get("HX-Request") == "true"

	if isHTMX {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("HX-Reswap", "none")
		w.WriteHeader(code)
		_ = components.Toast(components.ToastProps{
			Message: message,
			Type:    components.ToastError,
		}).Render(req.Context(), w)
		return
	}

	// Standard Response (Full Page)
	user := auth.GetUserFromRequest(req)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = pages.ErrorPage(user, message, code).Render(req.Context(), w)
}
