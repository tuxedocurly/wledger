package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
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
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func main() {
	// Logger init
	log := logger.New(true)
	log.Info("Starting WLEDger V2...")

	// i18n init
	if err := i18n.Init(); err != nil {
		log.Error("Failed to initialize i18n", "error", err)
		os.Exit(1)
	}

	// Database
	os.MkdirAll(config.DirData, 0755)
	database, err := db.Open(config.DirDatabase)
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

	// Session Manager
	sessionManager := scs.New()
	sessionManager.Store = auth.NewStore(store)
	sessionManager.Lifetime = 24 * time.Hour
	sessionManager.Cookie.Persist = true
	sessionManager.Cookie.SameSite = http.SameSiteLaxMode
	sessionManager.Cookie.Secure = false // TODO: Set to true in prod

	// WLED client & service
	wledClient := wled.NewClient()
	wledService := wled.NewService(store, wledClient, log)

	// UI Error Responder
	uiErrorResponder := uierror.New(log)

	// Backup Service
	backupService := backup.NewService(database, store, config.DirUploads, log)

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
	// Seed the initial inspiration templates
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

	// Middleware Manager
	mw := middleware.New(store, sessionManager, log, uiErrorResponder)

	// Router Setup
	r := router.New(mw, sessionManager, h)

	// Start Server
	port := "8080"
	log.Info("Server listening", "url", "http://localhost:"+port)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful Shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c

	log.Info("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}