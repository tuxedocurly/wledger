package main

import (
	"html/template"
	"log"
	"net/http"
	"os"

	// Import our internal packages
	"wledger/internal/server"
	"wledger/internal/store"
	"wledger/internal/wled"
)

func main() {
	// 1. Ensure required directories exist
	// This prevents crashes if running in a fresh environment
	if err := os.MkdirAll("./data/uploads", 0755); err != nil {
		log.Fatal("Failed to create directories:", err)
	}

	// 2. Init Store (Database)
	// We point to the ./data folder relative to the project root
	db, err := store.NewStore("./data/inventory.db")
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	// 3. Init Templates
	// We parse from the new "ui/templates" location
	templates, err := template.ParseGlob("ui/templates/*.html")
	if err != nil {
		log.Fatal("Failed to parse templates:", err)
	}

	// 4. Init WLED Client
	wledClient := wled.NewWLEDClient()

	// 5. Init Server Application
	// We inject our dependencies (DB, Templates, WLED) into the server package
	app := server.NewApp(templates, wledClient, db)

	// 6. Start Background Services
	// (Health checks and tag cleanup)
	go app.StartBackgroundServices()

	// 7. Start Web Server
	// We ask the app for its configured Router
	log.Println("Starting server on :3000")
	if err := http.ListenAndServe(":3000", app.Routes()); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
