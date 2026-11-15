package router

import (
	"context"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/handler"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/web"
)

// Options configures optional router behavior such as proxy trust and upload directory overrides.
type Options struct {
	TrustProxy       bool
	UploadsDir       string
	LoginRateLimiter *middleware.IPRateLimiter
}

// New creates and configures a new chi router using default background context and runtime config.
func New(mw *middleware.Manager, sessionManager *scs.SessionManager, h *handler.Handler) *chi.Mux {
	return NewWithContext(context.Background(), mw, sessionManager, h)
}

// NewWithContext creates and configures a new chi router whose background workers are tied to ctx.
func NewWithContext(ctx context.Context, mw *middleware.Manager, sessionManager *scs.SessionManager, h *handler.Handler) *chi.Mux {
	return NewWithOptions(ctx, mw, sessionManager, h, Options{
		TrustProxy: config.TrustProxy,
		UploadsDir: config.DirUploads,
	})
}

// NewWithOptions creates and configures a new chi router with explicit runtime options.
func NewWithOptions(ctx context.Context, mw *middleware.Manager, sessionManager *scs.SessionManager, h *handler.Handler, opts Options) *chi.Mux {
	if ctx == nil {
		ctx = context.Background()
	}

	uploadsRoot := opts.UploadsDir
	if uploadsRoot == "" {
		uploadsRoot = config.DirUploads
	}

	loginLimiter := opts.LoginRateLimiter
	if loginLimiter == nil {
		loginLimiter = middleware.NewIPRateLimiter(
			ctx,
			middleware.DefaultLoginRate,
			middleware.DefaultLoginBurst,
			middleware.DefaultRateLimitStale,
		)
	}

	r := chi.NewRouter()

	// Base outer middlewares (RealIP is gated behind WLEDGER_TRUST_PROXY)
	r.Use(chimiddleware.RequestID)
	if opts.TrustProxy {
		r.Use(chimiddleware.RealIP)
	}
	r.Use(chimiddleware.Recoverer)
	r.Use(mw.RequestLogger)

	// Serve embedded static assets BEFORE SessionLoad, I18n, Authenticate, FirstRunCheck, or CSRF.
	staticFS := http.FS(web.StaticFS)
	r.Handle(config.UrlPrefixStatic+"*", http.StripPrefix(config.UrlPrefixStatic, noDirFileServer(staticFS)))

	// Application routes protected by CSRF + Session + Auth + FirstRunCheck + StockColors.
	r.Group(func(r chi.Router) {
		r.Use(csrfProtectionMiddleware())
		r.Use(sessionManager.LoadAndSave)
		r.Use(mw.I18n)
		r.Use(mw.Authenticate)
		r.Use(mw.FirstRunCheck)
		r.Use(mw.InjectStockColors)

		// -----------------------------------------------------------------
		// PUBLIC ROUTES
		// -----------------------------------------------------------------
		r.Get("/setup", h.HandleSetup)
		r.Post("/setup", h.HandleSetupPost)
		r.Get("/login", h.HandleLogin)
		r.With(loginLimiter.Middleware).Post("/login", h.HandleLoginPost)
		r.Post("/logout", h.HandleLogout)

		// -----------------------------------------------------------------
		// READ-ONLY GROUP ROUTES (Protected by RequireReadAuth + RequirePasswordChange)
		// -----------------------------------------------------------------
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireReadAuth)
			r.Use(mw.RequirePasswordChange)

			// Uploaded user assets & documents inside RequireReadAuth with hardened headers
			r.Handle(config.UrlPrefixUploads+"*", http.StripPrefix(config.UrlPrefixUploads, secureUploadsHandler(uploadsRoot)))

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
			r.Get("/hardware/containers/{id}/configure", h.HandleContainerConfigure)
			r.Post("/hardware/off", h.HandleGlobalOff)

			// Inspiration
			r.Get("/inspiration", h.HandleInspiration)
			r.Get("/inspiration/{id}/generate", h.HandleInspirationGenerate)
		})

		// -----------------------------------------------------------------
		// WRITE / PROTECTED GROUP ROUTES
		// -----------------------------------------------------------------
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

			// Wave 3/4 hook: /projects/* and /settings/tokens* routes mount here.

			// -------------------------------------------------------------
			// INVENTORY MANAGEMENT ROUTES (Editors & Admins)
			// -------------------------------------------------------------
			r.Group(func(r chi.Router) {
				r.Use(mw.RequireRole("editor", "admin"))

				// Parts CRUD
				r.Get("/parts/new", h.HandlePartsNew)
				r.With(middleware.MaxBodyBytes(config.MaxUploadSizeParts)).Post("/parts", h.HandlePartsCreate)
				r.Get("/parts/import/template", h.HandlePartsImportTemplate)
				r.With(middleware.MaxBodyBytes(config.MaxUploadSizeImport)).Post("/parts/import", h.HandlePartsImport)
				r.Handle("/parts/bulk", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost || r.Method == http.MethodDelete {
						h.HandlePartsBulkDelete(w, r)
					} else {
						w.WriteHeader(http.StatusMethodNotAllowed)
					}
				}))

				r.Get("/parts/{id}/edit", h.HandlePartEdit)
				r.With(middleware.MaxBodyBytes(config.MaxUploadSizeParts)).Post("/parts/{id}/update", h.HandlePartUpdate)
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

			// -------------------------------------------------------------
			// SYSTEM ADMINISTRATION ROUTES (Admins Only)
			// -------------------------------------------------------------
			r.Group(func(r chi.Router) {
				r.Use(mw.RequireRole("admin"))

				// Audit Logs
				r.Get("/audit-logs", h.HandleAuditLogs)

				// Hardware Configuration
				r.Post("/hardware", h.HandleHardwareCreate)
				r.Post("/hardware/discover", h.HandleHardwareDiscover)
				r.Post("/hardware/{id}/update", h.HandleControllerUpdate)
				r.Post("/hardware/{id}/delete", h.HandleHardwareDelete)
				r.Post("/hardware/{id}/grid", h.HandleHardwareGridSave)
				r.Post("/hardware/containers", h.HandleContainerCreateStandalone)
				r.Post("/hardware/containers/{id}/configure", h.HandleContainerConfigureSave)
				r.Post("/hardware/containers/{id}/delete", h.HandleContainerDelete)
				r.Post("/hardware/containers/{id}/controller", h.HandleContainerBindController)

				// System Settings Update & Backup
				r.Post("/settings", h.HandleSettingsUpdate)
				r.Get("/settings/backup/download", h.HandleBackupDownload)
				r.With(middleware.MaxBodyBytes(config.MaxUploadSizeBackup)).Post("/settings/backup/restore", func(w http.ResponseWriter, r *http.Request) {
					defer middleware.ResetSetupCache()
					h.HandleBackupRestore(w, r)
				})

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

		// Wave 3/4 hook: Bearer-authenticated /api/v1/* JSON routes mount here.
	})

	return r
}

func csrfProtectionMiddleware() func(http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	return func(next http.Handler) http.Handler {
		protected := protection.Handler(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/v1/") && hasBearerToken(r) {
				next.ServeHTTP(w, r)
				return
			}
			protected.ServeHTTP(w, r)
		})
	}
}

func hasBearerToken(r *http.Request) bool {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authHeader) <= len("Bearer ") {
		return false
	}
	return strings.EqualFold(authHeader[:len("Bearer ")], "Bearer ") && strings.TrimSpace(authHeader[len("Bearer "):]) != ""
}

func noDirFileServer(fsys http.FileSystem) http.Handler {
	fileServer := http.FileServer(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleanPath := path.Clean("/" + r.URL.Path)
		if _, ok := openNonDir(fsys, cleanPath); !ok {
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func secureUploadsHandler(uploadsRoot string) http.Handler {
	fsys := http.Dir(uploadsRoot)
	fileServer := http.FileServer(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleanPath := path.Clean("/" + r.URL.Path)
		if _, ok := openNonDir(fsys, cleanPath); !ok {
			http.NotFound(w, r)
			return
		}

		if strings.HasPrefix(cleanPath, "/docs/") {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")

			if !isInlineDocExtension(path.Ext(cleanPath)) {
				w.Header().Set("Content-Disposition", formatAttachmentHeader(path.Base(cleanPath)))
			}
		} else if strings.HasPrefix(cleanPath, "/images/") {
			w.Header().Set("X-Content-Type-Options", "nosniff")
		}

		fileServer.ServeHTTP(w, r)
	})
}

func openNonDir(fsys http.FileSystem, cleanPath string) (fs.FileInfo, bool) {
	if cleanPath == "/" {
		return nil, false
	}
	f, err := fsys.Open(cleanPath)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return nil, false
	}
	return info, true
}

func isInlineDocExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".pdf", ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return true
	default:
		return false
	}
}

func formatAttachmentHeader(filename string) string {
	disp := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if disp == "" {
		return "attachment"
	}
	return disp
}
