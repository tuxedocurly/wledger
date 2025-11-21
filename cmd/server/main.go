// cmd/server/main.go
package main

import (
	"html/template"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"wledger/internal/background"
	"wledger/internal/features/dashboard"
	"wledger/internal/features/hardware"
	"wledger/internal/features/inspiration"
	"wledger/internal/features/inventory"
	"wledger/internal/features/parts"
	"wledger/internal/features/settings"
	"wledger/internal/features/system"
	"wledger/internal/store"
	"wledger/internal/wled"
)

func main() {
	// Ensure required directories exist
	if err := os.MkdirAll("data/uploads", 0755); err != nil {
		log.Fatal("Failed to create directories:", err)
	}

	// Init store (database)
	db, err := store.NewStore("./data/inventory.db")
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	// Init templates
	templates, err := template.ParseGlob("ui/templates/*.html")
	if err != nil {
		log.Fatal("Failed to parse templates:", err)
	}

	// Init WLED client
	wledClient := wled.NewWLEDClient()

	// Initialize feature modules
	systemHandler := system.New(db)
	hwHandler := hardware.New(db, wledClient, templates)
	settingsHandler := settings.New(db, templates)
	invHandler := inventory.New(db, templates)
	partsHandler := parts.New(db, templates)
	dashHandler := dashboard.New(db, wledClient, templates)
	inspHandler := inspiration.New(db, templates)
	bgService := background.New(db, wledClient)

	// Start background services (health checks, tag cleanup)
	go bgService.Start()

	// Setup Router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Register Routes
	// Static Files
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("./ui/static"))))
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir("./data/uploads"))))

	// Register Feature Routes
	systemHandler.RegisterRoutes(r)
	hwHandler.RegisterRoutes(r)
	settingsHandler.RegisterRoutes(r)
	invHandler.RegisterRoutes(r)
	partsHandler.RegisterRoutes(r)
	dashHandler.RegisterRoutes(r)
	inspHandler.RegisterRoutes(r)

	// Start Server
	log.Println("Starting server on :3000")
	if err := http.ListenAndServe(":3000", r); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
