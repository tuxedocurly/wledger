package router

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/handler"
	"github.com/tuxedocurly/wledger/internal/middleware"
)

// New creates and configures a new chi router
func New(mw *middleware.Manager, sessionManager *scs.SessionManager, h *handler.Handler) *chi.Mux {
	r := chi.NewRouter()

	// Global Middlewares
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(mw.RequestLogger)
	r.Use(sessionManager.LoadAndSave)
	r.Use(mw.I18n)
	r.Use(mw.Authenticate)
	r.Use(mw.FirstRunCheck)

	// Static Files
	filesDir := http.Dir(config.DirStatic)
	r.Handle(config.UrlPrefixStatic+"*", http.StripPrefix(config.UrlPrefixStatic, http.FileServer(filesDir)))

	uploadsDir := http.Dir(config.DirUploads)
	r.Handle(config.UrlPrefixUploads+"*", http.StripPrefix(config.UrlPrefixUploads, http.FileServer(uploadsDir)))

	// -------------------------------------------------------------------------
	// PUBLIC ROUTES
	// -------------------------------------------------------------------------
	r.Get("/setup", h.HandleSetup)
	r.Post("/setup", h.HandleSetupPost)
	r.Get("/login", h.HandleLogin)
	r.Post("/login", h.HandleLoginPost)
	r.Post("/logout", h.HandleLogout)

	// -------------------------------------------------------------------------
	// READ-ONLY GROUP ROUTES (Protected by RequireReadAuth + RequirePasswordChange)
	// -------------------------------------------------------------------------
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireReadAuth)
		r.Use(mw.RequirePasswordChange)

		// Dashboard
		r.Get("/", h.HandleDashboard)

		// Parts (Read)
		r.Get("/parts", h.HandlePartsList)
		r.Get("/parts/{id}", h.HandlePartDetail)
		r.Get("/parts/bins_options", h.HandleBinOptions)
		r.Get("/parts/bin_picker", h.HandleBinPicker)

		// Locate
		r.Post("/hardware/{id}/locate", h.HandleHardwareLocate)
		r.Post("/parts/{id}/locate", h.HandlePartLocate)

		// Hardware (Read)
		r.Get("/hardware", h.HandleHardwareList)
		r.Get("/hardware/{id}/status", h.HandleHardwareStatus)
		r.Get("/hardware/{id}/grid", h.HandleHardwareGrid)
		r.Post("/hardware/off", h.HandleGlobalOff)

		// Inspiration
		r.Get("/inspiration", h.HandleInspiration)
		r.Get("/inspiration/{id}/generate", h.HandleInspirationGenerate)
	})

	// -------------------------------------------------------------------------
	// WRITE / PROTECTED GROUP ROUTES
	// -------------------------------------------------------------------------
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireAuth)
		r.Use(mw.RequirePasswordChange)

		// Force Reset
		r.Get("/force-reset", h.HandleForceReset)
		r.Post("/force-reset", h.HandleForceResetPost)

		// Settings View
		r.Get("/settings", h.HandleSettings)

		// Self-Service Password Change
		r.Post("/settings/password", h.HandleSettingsPassword)

		// -----------------------------------------------------------
		// INVENTORY MANAGEMENT ROUTES (Editors & Admins)
		// -----------------------------------------------------------
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("editor", "admin"))

			// Parts CRUD
			r.Get("/parts/new", h.HandlePartsNew)
			r.Post("/parts", h.HandlePartsCreate)
			r.Get("/parts/import/template", h.HandlePartsImportTemplate)
			r.Post("/parts/import", h.HandlePartsImport)
			r.Handle("/parts/bulk", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost || r.Method == http.MethodDelete {
					h.HandlePartsBulkDelete(w, r)
				} else {
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))

			r.Get("/parts/{id}/edit", h.HandlePartEdit)
			r.Post("/parts/{id}/update", h.HandlePartUpdate)
			r.Post("/parts/{id}/delete", h.HandlePartDelete)

			// Sub-Resources
			r.Delete("/parts/links/{id}", h.HandleLinkDelete)
			r.Delete("/parts/docs/{id}", h.HandleDocDelete)

			// Stock Management
			r.Post("/parts/{id}/assign", h.HandlePartAssign)
			r.Post("/parts/{id}/stock/{assignment_id}/adjust", h.HandlePartStockAdjust)
			r.Post("/parts/{id}/stock/{assignment_id}/move", h.HandlePartStockMove)
			r.Post("/parts/{id}/stock/{assignment_id}/delete", h.HandlePartStockRemove)

			// Inspiration CRUD
			r.Get("/inspiration/new", h.HandleInspirationNew)
			r.Post("/inspiration", h.HandleInspirationCreate)
			r.Get("/inspiration/{id}/edit", h.HandleInspirationEdit)
			r.Put("/inspiration/{id}", h.HandleInspirationUpdate)
			r.Delete("/inspiration/{id}", h.HandleInspirationDelete)
		})

		// -----------------------------------------------------------
		// SYSTEM ADMINISTRATION ROUTES (Admins Only)
		// -----------------------------------------------------------
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("admin"))

			// Audit Logs
			r.Get("/audit-logs", h.HandleAuditLogs)

			// Hardware Configuration
			r.Post("/hardware", h.HandleHardwareCreate)
			r.Post("/hardware/{id}/delete", h.HandleHardwareDelete)
			r.Post("/hardware/{id}/grid", h.HandleHardwareGridSave)

			// System Settings Update
			r.Post("/settings", h.HandleSettingsUpdate)
			r.Get("/settings/backup/download", h.HandleBackupDownload)
			r.Post("/settings/backup/restore", h.HandleBackupRestore)

			// User Management
			r.Post("/settings/users", h.HandleUserCreate)
			r.Post("/settings/users/{id}/delete", h.HandleUserDelete)
			r.Post("/settings/users/{id}/reset", h.HandleUserForceReset)

			// Wall Management
			r.Get("/walls/{id}/edit", h.HandleWallEdit)
			r.Post("/walls", h.HandleWallCreate)
			r.Post("/walls/{id}", h.HandleWallUpdate)
			r.Post("/walls/{id}/delete", h.HandleWallDelete)
		})
	})

	return r
}
