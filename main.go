package main

import (
	"html/template"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// App holds all application dependencies
type App struct {
	templates *template.Template
	wled      WLEDClientInterface
	partStore PartStore
	locStore  LocationStore
	binStore  BinStore
	ctrlStore ControllerStore
	dashStore DashboardStore
}

func main() {
	store, err := NewStore("./data/inventory.db")
	if err != nil {
		log.Fatal(err)
	}

	templates, err := template.ParseGlob("templates/*.html")
	if err != nil {
		log.Fatal("Failed to parse templates:", err)
	}

	wledClient := NewWLEDClient()

	app := &App{
		templates: templates,
		wled:      wledClient,
		partStore: store,
		locStore:  store,
		binStore:  store,
		ctrlStore: store,
		dashStore: store,
	}

	go app.startBackgroundServices()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir("./uploads"))))

	r.Get("/", app.handleShowParts)
	r.Post("/parts/search", app.handleSearchParts)
	r.Post("/parts", app.handleCreatePart)
	r.Delete("/parts/{id}", app.handleDeletePart)

	r.Get("/part/{id}", app.handleShowPartDetails)
	r.Post("/part/{id}/details", app.handleUpdatePartDetails)
	r.Post("/part/{id}/image/upload", app.handlePartImageUpload)
	r.Post("/part/locations", app.handleCreatePartLocation)
	r.Get("/part/location/{loc_id}", app.handleGetPartLocationRow)
	r.Get("/part/location/{loc_id}/edit", app.handleGetPartLocationEditRow)
	r.Put("/part/location/{loc_id}", app.handleUpdatePartLocation)
	r.Delete("/part/location/{loc_id}", app.handleDeletePartLocation)
	r.Post("/part/urls", app.handleAddPartURL)
	r.Delete("/part/urls/{url_id}", app.handleDeletePartURL)
	r.Post("/part/{id}/document/upload", app.handleUploadDocument)
	r.Get("/part/document/{doc_id}", app.handleDownloadDocument)
	r.Delete("/part/document/{doc_id}", app.handleDeleteDocument)
	r.Post("/part/categories", app.handleAssignCategoryToPart)
	r.Delete("/part/{part_id}/categories/{cat_id}", app.handleRemoveCategoryFromPart)

	r.Get("/settings", app.handleShowSettings)
	r.Post("/settings/controllers", app.handleCreateController)
	r.Delete("/settings/controllers/{id}", app.handleDeleteController)
	r.Post("/settings/controllers/{id}/refresh", app.handleRefreshControllerStatus)
	r.Post("/settings/bins", app.handleCreateBin)
	r.Post("/settings/bins/bulk", app.handleCreateBinsBulk)
	r.Delete("/settings/bins/{id}", app.handleDeleteBin)
	r.Post("/settings/categories/cleanup", app.handleCleanupCategories)

	r.Get("/dashboard", app.handleShowDashboard)
	r.Post("/api/v1/stock-status", app.handleShowStockStatus)
	r.Post("/locate/part/{id}", app.handleLocatePart)
	r.Post("/locate/stop/{id}", app.handleStopLocate)
	r.Get("/locate/button/{id}", app.handleGetLocateButton)
	r.Post("/api/v1/stop-all", app.handleStopAll)

	r.Get("/inspiration", app.handleShowInspiration)

	log.Println("Starting server on :3000")
	if err := http.ListenAndServe(":3000", r); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
