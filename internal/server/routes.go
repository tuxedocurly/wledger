package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Routes initializes the router and registers all endpoints
func (a *App) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Static Files
	// Note: We assume these directories exist relative to where the binary runs
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("./ui/static"))))
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir("./data/uploads"))))

	// --- Part Routes ---
	r.Get("/", a.handleShowParts)
	r.Post("/parts/search", a.handleSearchParts)
	r.Post("/parts", a.handleCreatePart)
	r.Delete("/parts/{id}", a.handleDeletePart)

	// --- Part Details Routes ---
	r.Get("/part/{id}", a.handleShowPartDetails)
	r.Post("/part/{id}/details", a.handleUpdatePartDetails)
	r.Post("/part/{id}/image/upload", a.handlePartImageUpload)
	r.Post("/part/locations", a.handleCreatePartLocation)
	r.Get("/part/location/{loc_id}", a.handleGetPartLocationRow)
	r.Get("/part/location/{loc_id}/edit", a.handleGetPartLocationEditRow)
	r.Put("/part/location/{loc_id}", a.handleUpdatePartLocation)
	r.Delete("/part/location/{loc_id}", a.handleDeletePartLocation)
	r.Post("/part/urls", a.handleAddPartURL)
	r.Delete("/part/urls/{url_id}", a.handleDeletePartURL)
	r.Post("/part/{id}/document/upload", a.handleUploadDocument)
	r.Get("/part/document/{doc_id}", a.handleDownloadDocument)
	r.Delete("/part/document/{doc_id}", a.handleDeleteDocument)
	r.Post("/part/categories", a.handleAssignCategoryToPart)
	r.Delete("/part/{part_id}/categories/{cat_id}", a.handleRemoveCategoryFromPart)

	// --- Settings Routes ---
	r.Get("/settings", a.handleShowSettings)
	r.Post("/settings/controllers", a.handleCreateController)
	r.Delete("/settings/controllers/{id}", a.handleDeleteController)
	r.Post("/settings/controllers/{id}/refresh", a.handleRefreshControllerStatus)
	r.Post("/settings/bins", a.handleCreateBin)
	r.Post("/settings/bins/bulk", a.handleCreateBinsBulk)
	r.Delete("/settings/bins/{id}", a.handleDeleteBin)
	r.Post("/settings/categories/cleanup", a.handleCleanupCategories)

	// --- Dashboard & API Routes ---
	r.Get("/dashboard", a.handleShowDashboard)
	r.Get("/inspiration", a.handleShowInspiration)
	r.Post("/api/v1/stock-status", a.handleShowStockStatus)
	r.Post("/locate/part/{id}", a.handleLocatePart)
	r.Post("/locate/stop/{id}", a.handleStopLocate)
	r.Get("/locate/button/{id}", a.handleGetLocateButton)
	r.Post("/api/v1/stop-all", a.handleStopAll)

	return r
}
