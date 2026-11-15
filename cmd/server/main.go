package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/backup"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/dashboard"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/handler"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/i18n"
	"github.com/tuxedocurly/wledger/internal/images"
	"github.com/tuxedocurly/wledger/internal/inspiration"
	"github.com/tuxedocurly/wledger/internal/logger"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/router"
	settingsServicePkg "github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/internal/stock"
	"github.com/tuxedocurly/wledger/internal/tags"
	"github.com/tuxedocurly/wledger/internal/tlsutil"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

const sessionCleanupInterval = 15 * time.Minute

func main() {
	// Logger init
	log := logger.New(true)
	log.Info("Starting WLEDger V2...")

	cfg := config.Load()

	// i18n init
	if err := i18n.Init(); err != nil {
		log.Error("Failed to initialize i18n", "error", err)
		os.Exit(1)
	}

	// Database
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Error("Failed to create data directory", "error", err)
		os.Exit(1)
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Error("Failed to open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Apply Migrations
	if err := db.Migrate(database); err != nil {
		log.Error("Failed to migrate database", "error", err)
		os.Exit(1)
	}

	// Initialize store
	store := db.NewStore(database)

	// Run Data Migrations
	if err := hardware.MigrateLegacyLedIndices(context.Background(), store, log); err != nil {
		log.Error("Failed to run hardware data migrations", "error", err)
		os.Exit(1)
	}

	// Ensure Settings exist
	if err := store.InitSettings(context.Background()); err != nil {
		log.Error("Failed to initialize settings", "error", err)
	}

	// Fetch Settings to set Log Level
	settings, err := store.GetSettings(context.Background())
	if err == nil {
		logger.SetDebug(settings.EnableDebugLogs.Bool)
	} else {
		log.Error("Failed to fetch settings for log level", "error", err)
	}

	// TLS setup (explicit cert/key or persisted ECDSA self-signed certificate)
	tlsCfg, tlsEnabled, err := tlsutil.ConfigureTLS(cfg)
	if err != nil {
		log.Error("Failed to configure TLS", "error", err)
		os.Exit(1)
	}

	// Shutdown context for signals and background workers
	appCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	// Session Manager
	sessionStore := auth.NewStore(store)
	sessionManager := scs.New()
	sessionManager.Store = sessionStore
	sessionManager.Lifetime = 24 * time.Hour
	sessionManager.Cookie.Persist = true
	sessionManager.Cookie.SameSite = http.SameSiteLaxMode
	sessionManager.Cookie.Secure = cfg.CookieSecure || tlsEnabled

	go runSessionCleanup(appCtx, sessionStore, log)

	// WLED client & service
	wledClient := wled.NewClient()
	wledService := wled.NewService(store, wledClient, log)

	// UI Error Responder
	uiErrorResponder := uierror.New(log)

	// Backup Service
	backupService := backup.NewService(database, store, cfg.UploadsDir, log)

	// Tags Service
	tagsService := tags.NewService(database, store)

	// Documents Service
	docsService := documents.NewService(store, log)

	// Stock Service
	stockService := stock.NewService(store, log)

	// Parts Service
	partsService := parts.NewService(database, store, log, tagsService, docsService)

	// Hardware Service
	hardwareService := hardware.NewService(store, wledClient, log)

	// Settings Service
	settingsService := settingsServicePkg.NewService(store)

	// Audit Service
	auditService := audit.NewService(store)

	// Dashboard Service
	dashboardService := dashboard.NewService(store)

	// Inspiration Service
	inspirationService := inspiration.NewService(store)
	if err := inspirationService.SeedTemplates(context.Background()); err != nil {
		log.Error("Failed to seed inspiration templates", "error", err)
	}

	// Initialize Image Processor
	if err := images.Init(); err != nil {
		log.Error("Failed to init images", "error", err)
		os.Exit(1)
	}

	// Handler instantiation
	h := handler.New(
		log,
		store,
		sessionManager,
		wledService,
		database,
		backupService,
		partsService,
		tagsService,
		inspirationService,
		uiErrorResponder,
		hardwareService,
		settingsService,
		auditService,
		dashboardService,
		stockService,
		docsService,
	)

	// Middleware Manager & Router Setup
	mw := middleware.New(store, sessionManager, log, uiErrorResponder)
	r := router.NewWithOptions(appCtx, mw, sessionManager, h, router.Options{
		TrustProxy: cfg.TrustProxy,
		UploadsDir: cfg.UploadsDir,
	})

	listenAddr := normalizeListenAddr(cfg.Port)
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
	}
	log.Info("Server listening", "url", scheme+"://localhost"+listenAddr, "tls", tlsEnabled)

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           r,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErrCh := make(chan error, 1)
	go func() {
		var serveErr error
		if tlsEnabled {
			serveErr = srv.ListenAndServeTLS("", "")
		} else {
			serveErr = srv.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErrCh <- serveErr
		}
	}()

	select {
	case serveErr := <-serverErrCh:
		log.Error("Server failed", "error", serveErr)
		stopSignals()
		_ = database.Close()
		os.Exit(1)
	case <-appCtx.Done():
	}

	log.Info("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("Server shutdown error", "error", err)
	}
}

func runSessionCleanup(ctx context.Context, store *auth.SQLCStore, log interface{ Warn(string, ...any) }) {
	ticker := time.NewTicker(sessionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := store.CleanupExpired(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("failed to clean up expired sessions", "error", err)
			}
		}
	}
}

func normalizeListenAddr(port string) string {
	p := strings.TrimSpace(port)
	if p == "" {
		return ":8080"
	}
	if strings.Contains(p, ":") {
		return p
	}
	return ":" + p
}
