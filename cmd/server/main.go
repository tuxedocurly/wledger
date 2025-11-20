package main

import (
	"html/template"
	"log"
	"net/http"
	"os"

	"wledger/internal/server"
	"wledger/internal/store"
	"wledger/internal/wled"
)

func main() {
	// Ensure required directories exist to prevent issues in new deployments
	if err := os.MkdirAll("./data/uploads", 0755); err != nil {
		log.Fatal("Failed to create directories:", err)
	}

	// Init Store (Database)
	db, err := store.NewStore("./data/inventory.db")
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	// Init Templates
	templates, err := template.ParseGlob("ui/templates/*.html")
	if err != nil {
		log.Fatal("Failed to parse templates:", err)
	}

	// Init WLED Client
	wledClient := wled.NewWLEDClient()

	// Init Server Application
	// Inject dependencies into the server package
	app := server.NewApp(templates, wledClient, db)

	// Start Background Services
	// (Health checks and tag cleanup)
	go app.StartBackgroundServices()

	// Start Web Server
	log.Println("Starting server on :3000")
	if err := http.ListenAndServe(":3000", app.Routes()); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
