package inventory

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"wledger/internal/core"
	"wledger/internal/models"
	"wledger/internal/store"
)

// Store defines the database methods this module needs
// It combines Bin and Location methods.
type Store interface {
	// Bin methods
	GetBinByID(id int) (models.Bin, error)
	GetControllers() ([]models.WLEDController, error) // Needed for the dropdown
	CreateBin(name string, controllerID, segmentID, ledIndex int) error
	CreateBinsBulk(controllerID, segmentID, ledCount int, namePrefix string) error
	UpdateBin(b *models.Bin) error
	DeleteBin(id int) error

	// Location methods
	CreatePartLocation(partID, binID, quantity int) error
	GetPartLocationByID(locationID int) (models.PartLocation, error)
	UpdatePartLocation(locationID, quantity int) error
	DeletePartLocation(locationID int) error
}

type Handler struct {
	store     Store
	templates core.TemplateExecutor
}

func New(s Store, t core.TemplateExecutor) *Handler {
	return &Handler{store: s, templates: t}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	// Bin management
	r.Post("/settings/bins", h.handleCreateBin)
	r.Post("/settings/bins/bulk", h.handleCreateBinsBulk)
	r.Delete("/settings/bins/{id}", h.handleDeleteBin)
	r.Get("/settings/bins/{id}", h.handleGetBinRow)
	r.Get("/settings/bins/{id}/edit", h.handleGetBinEditRow)
	r.Put("/settings/bins/{id}", h.handleUpdateBin)

	// Part management
	r.Post("/part/locations", h.handleCreatePartLocation)
	r.Get("/part/location/{loc_id}", h.handleGetPartLocationRow)
	r.Get("/part/location/{loc_id}/edit", h.handleGetPartLocationEditRow)
	r.Put("/part/location/{loc_id}", h.handleUpdatePartLocation)
	r.Delete("/part/location/{loc_id}", h.handleDeletePartLocation)
}

// Bin Handlers

func (h *Handler) handleCreateBin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	name := r.FormValue("name")
	controllerID, _ := strconv.Atoi(r.FormValue("controller_id"))
	segmentID, _ := strconv.Atoi(r.FormValue("segment_id"))
	ledIndex, _ := strconv.Atoi(r.FormValue("led_index"))

	if name == "" || controllerID == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Name and Controller are required", nil)
		return
	}
	err := h.store.CreateBin(name, controllerID, segmentID, ledIndex)
	if err != nil {
		if errors.Is(err, store.ErrUniqueConstraint) {
			core.ClientError(w, r, http.StatusConflict, "A bin with this name already exists.", err)
		} else if errors.Is(err, store.ErrForeignKeyConstraint) {
			core.ClientError(w, r, http.StatusBadRequest, "Invalid controller selected.", err)
		} else {
			core.ServerError(w, r, err)
		}
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) handleCreateBinsBulk(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	controllerID, _ := strconv.Atoi(r.FormValue("controller_id"))
	segmentID, _ := strconv.Atoi(r.FormValue("segment_id"))
	ledCount, _ := strconv.Atoi(r.FormValue("led_count"))
	namePrefix := r.FormValue("name_prefix")

	if controllerID == 0 || ledCount <= 0 || namePrefix == "" {
		core.ClientError(w, r, http.StatusBadRequest, "Controller, positive LED count, and Name Prefix are required", nil)
		return
	}
	err := h.store.CreateBinsBulk(controllerID, segmentID, ledCount, namePrefix)
	if err != nil {
		if errors.Is(err, store.ErrUniqueConstraint) {
			core.ClientError(w, r, http.StatusConflict, "One or more bin names already exist (e.g., "+namePrefix+"0).", err)
		} else {
			core.ServerError(w, r, err)
		}
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) handleDeleteBin(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if id == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid ID", nil)
		return
	}
	if err := h.store.DeleteBin(id); err != nil {
		core.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleGetBinRow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	bin, err := h.store.GetBinByID(id)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Bin not found", err)
		return
	}
	h.templates.ExecuteTemplate(w, "_bin-row.html", bin)
}

func (h *Handler) handleGetBinEditRow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	bin, err := h.store.GetBinByID(id)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Bin not found", err)
		return
	}

	controllers, _ := h.store.GetControllers()

	data := map[string]interface{}{
		"Bin":         bin,
		"Controllers": controllers,
	}
	h.templates.ExecuteTemplate(w, "_bin-edit-row.html", data)
}

func (h *Handler) handleUpdateBin(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}

	bin := &models.Bin{
		ID:               id,
		Name:             r.FormValue("name"),
		WLEDControllerID: 0,
		WLEDSegmentID:    0,
		LEDIndex:         0,
	}

	bin.WLEDControllerID, _ = strconv.Atoi(r.FormValue("controller_id"))
	bin.WLEDSegmentID, _ = strconv.Atoi(r.FormValue("segment_id"))
	bin.LEDIndex, _ = strconv.Atoi(r.FormValue("led_index"))

	if bin.Name == "" || bin.WLEDControllerID == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Name and Controller are required", nil)
		return
	}

	if err := h.store.UpdateBin(bin); err != nil {
		if errors.Is(err, store.ErrUniqueConstraint) {
			core.ClientError(w, r, http.StatusConflict, "Bin name already exists", err)
		} else {
			core.ServerError(w, r, err)
		}
		return
	}

	updated, _ := h.store.GetBinByID(id)
	h.templates.ExecuteTemplate(w, "_bin-row.html", updated)
}

// Part (stock) & Location Handlers

func (h *Handler) handleCreatePartLocation(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	partID, _ := strconv.Atoi(r.FormValue("part_id"))
	binID, _ := strconv.Atoi(r.FormValue("bin_id"))
	quantity, _ := strconv.Atoi(r.FormValue("quantity"))

	if partID == 0 || binID == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid part or bin ID", nil)
		return
	}
	if err := h.store.CreatePartLocation(partID, binID, quantity); err != nil {
		core.ServerError(w, r, err)
		return
	}
	http.Redirect(w, r, "/part/"+r.FormValue("part_id"), http.StatusSeeOther)
}

func (h *Handler) handleGetPartLocationRow(w http.ResponseWriter, r *http.Request) {
	locID, _ := strconv.Atoi(chi.URLParam(r, "loc_id"))
	loc, err := h.store.GetPartLocationByID(locID)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Location not found", err)
		return
	}
	h.templates.ExecuteTemplate(w, "_part-location-row.html", loc)
}

func (h *Handler) handleGetPartLocationEditRow(w http.ResponseWriter, r *http.Request) {
	locID, _ := strconv.Atoi(chi.URLParam(r, "loc_id"))
	loc, err := h.store.GetPartLocationByID(locID)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Location not found", err)
		return
	}
	h.templates.ExecuteTemplate(w, "_part-location-edit-row.html", loc)
}

func (h *Handler) handleUpdatePartLocation(w http.ResponseWriter, r *http.Request) {
	locID, _ := strconv.Atoi(chi.URLParam(r, "loc_id"))
	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	quantity, _ := strconv.Atoi(r.FormValue("quantity"))

	if err := h.store.UpdatePartLocation(locID, quantity); err != nil {
		core.ServerError(w, r, err)
		return
	}
	loc, err := h.store.GetPartLocationByID(locID)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}
	h.templates.ExecuteTemplate(w, "_part-location-row.html", loc)
}

func (h *Handler) handleDeletePartLocation(w http.ResponseWriter, r *http.Request) {
	locID, _ := strconv.Atoi(chi.URLParam(r, "loc_id"))
	if err := h.store.DeletePartLocation(locID); err != nil {
		core.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
