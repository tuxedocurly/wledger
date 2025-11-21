package hardware

import (
	"database/sql"
	"log" // Ensure this is imported
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"wledger/internal/core"
	"wledger/internal/models"
)

// ... (Interfaces and Structs unchanged) ...
type Store interface {
	GetControllerByID(id int) (models.WLEDController, error)
	GetControllers() ([]models.WLEDController, error)
	CreateController(name, ipAddress string) error
	UpdateController(c *models.WLEDController) error
	DeleteController(id int) error
	UpdateControllerStatus(id int, status string, lastSeen sql.NullTime) error
	MigrateBins(oldControllerID, newControllerID int) error
	GetBins() ([]models.Bin, error)
}

type WLEDClient interface {
	Ping(ipAddress string) bool
}

type Handler struct {
	store     Store
	wled      WLEDClient
	templates core.TemplateExecutor
}

func New(s Store, w WLEDClient, t core.TemplateExecutor) *Handler {
	return &Handler{store: s, wled: w, templates: t}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/settings", h.handleShowSettings)
	r.Post("/settings/controllers", h.handleCreateController)
	r.Delete("/settings/controllers/{id}", h.handleDeleteController)
	r.Post("/settings/controllers/{id}/refresh", h.handleRefreshControllerStatus)

	r.Get("/settings/controllers/{id}", h.handleGetControllerRow)
	r.Get("/settings/controllers/{id}/edit", h.handleGetControllerEditRow)
	r.Put("/settings/controllers/{id}", h.handleUpdateController)
	r.Get("/settings/controllers/{id}/migrate", h.handleGetControllerMigrateRow)
	r.Post("/settings/controllers/{id}/migrate", h.handleMigrateController)
}

// Handlers

func (h *Handler) handleShowSettings(w http.ResponseWriter, r *http.Request) {
	controllers, err := h.store.GetControllers()
	if err != nil {
		core.ServerError(w, r, err)
		return
	}
	bins, err := h.store.GetBins()
	if err != nil {
		core.ServerError(w, r, err)
		return
	}
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

func (h *Handler) handleCreateController(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	name := r.FormValue("name")
	ipAddress := r.FormValue("ip_address")

	if name == "" || ipAddress == "" {
		core.ClientError(w, r, http.StatusBadRequest, "Name and IP Address are required", nil)
		return
	}

	if err := h.store.CreateController(name, ipAddress); err != nil {
		core.ServerError(w, r, err)
		return
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) handleDeleteController(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if id == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid ID", nil)
		return
	}

	err := h.store.DeleteController(id)
	if err != nil {
		if err.Error() == "foreign key constraint violation" {
			core.ClientError(w, r, http.StatusConflict, "Cannot delete controller: It is in use by one or more bins.", err)
		} else {
			core.ServerError(w, r, err)
		}
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleRefreshControllerStatus(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if id == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid ID", nil)
		return
	}

	controller, err := h.store.GetControllerByID(id)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Controller not found", err)
		return
	}

	online := h.wled.Ping(controller.IPAddress)
	var status string
	var lastSeen sql.NullTime
	if online {
		status = "online"
		lastSeen.Time = time.Now()
		lastSeen.Valid = true
	} else {
		status = "offline"
		lastSeen.Valid = false
	}

	if err := h.store.UpdateControllerStatus(id, status, lastSeen); err != nil {
		// Log error but do NOT return 500 response
		log.Printf("Error updating controller status: %v", err)
	}

	updatedController, err := h.store.GetControllerByID(id)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Controller not found", err)
		return
	}
	h.templates.ExecuteTemplate(w, "_controller-row.html", updatedController)
}

func (h *Handler) handleGetControllerRow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	controller, err := h.store.GetControllerByID(id)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Controller not found", err)
		return
	}
	h.templates.ExecuteTemplate(w, "_controller-row.html", controller)
}

func (h *Handler) handleGetControllerEditRow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	controller, err := h.store.GetControllerByID(id)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Controller not found", err)
		return
	}
	h.templates.ExecuteTemplate(w, "_controller-edit-row.html", controller)
}

func (h *Handler) handleUpdateController(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}

	controller := &models.WLEDController{
		ID:        id,
		Name:      r.FormValue("name"),
		IPAddress: r.FormValue("ip_address"),
	}

	if controller.Name == "" || controller.IPAddress == "" {
		core.ClientError(w, r, http.StatusBadRequest, "Name and IP are required", nil)
		return
	}

	if err := h.store.UpdateController(controller); err != nil {
		core.ServerError(w, r, err)
		return
	}

	updated, err := h.store.GetControllerByID(id)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}
	h.templates.ExecuteTemplate(w, "_controller-row.html", updated)
}

func (h *Handler) handleGetControllerMigrateRow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	source, err := h.store.GetControllerByID(id)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Controller not found", err)
		return
	}

	all, err := h.store.GetControllers()
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	targets := []models.WLEDController{}
	for _, c := range all {
		if c.ID != source.ID {
			targets = append(targets, c)
		}
	}

	if len(targets) == 0 {
		core.ClientError(w, r, http.StatusConflict, "No other controllers available to migrate to.", nil)
		return
	}

	data := map[string]interface{}{
		"Source":  source,
		"Targets": targets,
	}
	h.templates.ExecuteTemplate(w, "_controller-migrate-row.html", data)
}

func (h *Handler) handleMigrateController(w http.ResponseWriter, r *http.Request) {
	oldID, _ := strconv.Atoi(chi.URLParam(r, "id"))

	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}

	newID, _ := strconv.Atoi(r.FormValue("new_controller_id"))
	if oldID == 0 || newID == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid Controller IDs", nil)
		return
	}

	if err := h.store.MigrateBins(oldID, newID); err != nil {
		core.ServerError(w, r, err)
		return
	}

	updatedSource, err := h.store.GetControllerByID(oldID)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}
	h.templates.ExecuteTemplate(w, "_controller-row.html", updatedSource)
}
