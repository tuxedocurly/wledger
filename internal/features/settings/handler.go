package settings

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"wledger/internal/core"
	"wledger/internal/models"
)

// Store defines the read-only methods needed to render the settings page
type Store interface {
	GetControllers() ([]models.WLEDController, error)
	GetBins() ([]models.Bin, error)
}

type Handler struct {
	store     Store
	templates core.TemplateExecutor
}

func New(s Store, t core.TemplateExecutor) *Handler {
	return &Handler{store: s, templates: t}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/settings", h.handleShowSettings)
}

// Handlers

func (h *Handler) handleShowSettings(w http.ResponseWriter, r *http.Request) {
	// Fetch controllers from hardware domain
	controllers, err := h.store.GetControllers()
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	// Fetch bins from inventory domain
	bins, err := h.store.GetBins()
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	// Render the composite view
	data := map[string]interface{}{
		"Title":       "Settings",
		"Controllers": controllers,
		"Bins":        bins,
	}

	err = h.templates.ExecuteTemplate(w, "settings.html", data)
	if err != nil {
		core.ServerError(w, r, err)
	}
}
