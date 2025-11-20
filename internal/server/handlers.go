package server

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"wledger/internal/models"
	"wledger/internal/store"

	"github.com/go-chi/chi/v5"
)

// TODO: refactor this monolithic file into smaller files by feature area

const maxUploadSize = 5 * 1024 * 1024 // 5 MB

// Error Helpers

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("Internal Server Error: %s %s: %s", r.Method, r.URL.Path, err.Error())
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

func clientError(w http.ResponseWriter, r *http.Request, status int, message string, err error) {
	if err != nil {
		log.Printf("Client Error: %s %s: %s", r.Method, r.URL.Path, err.Error())
	}
	http.Error(w, message, status)
}

// Part Handlers

func (a *App) handleShowParts(w http.ResponseWriter, r *http.Request) {
	parts, err := a.PartStore.GetParts()
	if err != nil {
		serverError(w, r, err)
		return
	}

	data := map[string]interface{}{
		"Title": "Inventory",
		"Parts": parts,
	}

	err = a.Templates.ExecuteTemplate(w, "index.html", data)
	if err != nil {
		serverError(w, r, err)
	}
}

func (a *App) handleSearchParts(w http.ResponseWriter, r *http.Request) {
	searchTerm := r.FormValue("search")

	parts, err := a.PartStore.SearchParts(searchTerm)
	if err != nil {
		serverError(w, r, err)
		return
	}

	err = a.Templates.ExecuteTemplate(w, "_parts-list.html", parts)
	if err != nil {
		serverError(w, r, err)
	}
}

func (a *App) handleCreatePart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_point"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))
	stockTracking := r.FormValue("stock_tracking_enabled") == "on"

	part := &models.Part{
		Name:          r.FormValue("name"),
		Description:   sql.NullString{String: r.FormValue("description"), Valid: true},
		PartNumber:    sql.NullString{String: r.FormValue("part_number"), Valid: true},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Manufacturer:  sql.NullString{String: r.FormValue("manufacturer"), Valid: true},
		Supplier:      sql.NullString{String: r.FormValue("supplier"), Valid: true},
		UnitCost:      sql.NullFloat64{Float64: cost, Valid: true},
		Status:        sql.NullString{String: r.FormValue("status"), Valid: true},
		StockTracking: stockTracking,
		ReorderPoint:  reorder,
		MinStock:      minStock,
	}

	if part.Name == "" {
		clientError(w, r, http.StatusBadRequest, "Part name is required", nil)
		return
	}

	if err := a.PartStore.CreatePart(part); err != nil {
		serverError(w, r, err)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleDeletePart(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if id == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid ID", nil)
		return
	}

	if err := a.PartStore.DeletePart(id); err != nil {
		serverError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// Part Details & Location Handlers

func (a *App) handleShowPartDetails(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	part, err := a.PartStore.GetPartByID(id)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Part not found", err)
		return
	}

	locations, err := a.LocStore.GetPartLocations(id)
	if err != nil {
		serverError(w, r, err)
		return
	}

	availableBins, err := a.BinStore.GetAvailableBins(id)
	if err != nil {
		serverError(w, r, err)
		return
	}

	urls, err := a.PartStore.GetURLsByPartID(id)
	if err != nil {
		serverError(w, r, err)
		return
	}

	docs, err := a.PartStore.GetDocumentsByPartID(id)
	if err != nil {
		serverError(w, r, err)
		return
	}

	assignedCategories, err := a.PartStore.GetCategoriesByPartID(id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	allCategories, err := a.PartStore.GetCategories()
	if err != nil {
		serverError(w, r, err)
		return
	}

	data := map[string]interface{}{
		"Title":              part.Name,
		"Part":               part,
		"Locations":          locations,
		"AvailableBins":      availableBins,
		"URLs":               urls,
		"Documents":          docs,
		"AssignedCategories": assignedCategories,
		"AllCategories":      allCategories,
	}

	err = a.Templates.ExecuteTemplate(w, "part-details.html", data)
	if err != nil {
		serverError(w, r, err)
	}
}

func (a *App) handleUpdatePartDetails(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if id == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid Part ID", nil)
		return
	}

	part, err := a.PartStore.GetPartByID(id)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Part not found", err)
		return
	}

	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_point"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))
	stockTracking := r.FormValue("stock_tracking_enabled") == "on"

	part.Name = r.FormValue("name")
	part.Description = sql.NullString{String: r.FormValue("description"), Valid: true}
	part.PartNumber = sql.NullString{String: r.FormValue("part_number"), Valid: true}
	part.UpdatedAt = time.Now()
	part.Manufacturer = sql.NullString{String: r.FormValue("manufacturer"), Valid: true}
	part.Supplier = sql.NullString{String: r.FormValue("supplier"), Valid: true}
	part.UnitCost = sql.NullFloat64{Float64: cost, Valid: true}
	part.Status = sql.NullString{String: r.FormValue("status"), Valid: true}
	part.StockTracking = stockTracking
	part.ReorderPoint = reorder
	part.MinStock = minStock

	if err := a.PartStore.UpdatePart(&part); err != nil {
		serverError(w, r, err)
		return
	}

	http.Redirect(w, r, "/part/"+strconv.Itoa(id), http.StatusSeeOther)
}

func (a *App) handlePartImageUpload(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if partID == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid Part ID", nil)
		return
	}
	part, err := a.PartStore.GetPartByID(partID)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Part not found", err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		clientError(w, r, http.StatusBadRequest, "File is too large (Max 5MB)", err)
		return
	}

	file, header, err := r.FormFile("part_image")
	if err != nil {
		clientError(w, r, http.StatusBadRequest, "Invalid file upload", err)
		return
	}
	defer file.Close()

	buff := make([]byte, 512)
	if _, err := file.Read(buff); err != nil {
		serverError(w, r, err)
		return
	}
	filetype := http.DetectContentType(buff)
	if !strings.HasPrefix(filetype, "image/") {
		clientError(w, r, http.StatusBadRequest, "Invalid file type, must be an image", nil)
		return
	}
	if _, err := file.Seek(0, 0); err != nil {
		serverError(w, r, err)
		return
	}

	ext := filepath.Ext(header.Filename)
	filename := fmt.Sprintf("%d-%d%s", partID, time.Now().UnixNano(), ext)

	relPath := path.Join("images", filename)
	absDir := filepath.Join("data", "uploads", "images")
	absPath := filepath.Join(absDir, filename)

	if err := os.MkdirAll(absDir, os.ModePerm); err != nil {
		serverError(w, r, err)
		return
	}
	dst, err := os.Create(absPath)
	if err != nil {
		serverError(w, r, err)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		serverError(w, r, err)
		return
	}

	if part.ImagePath.Valid && part.ImagePath.String != "" {
		oldPath := filepath.Join("data", "uploads", part.ImagePath.String)
		if err := os.Remove(oldPath); err != nil {
			log.Printf("Warning: failed to delete old image file %s: %v", oldPath, err)
		}
	}

	if err := a.PartStore.UpdatePartImagePath(partID, relPath); err != nil {
		serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/part/"+strconv.Itoa(partID), http.StatusSeeOther)
}

func (a *App) handleCreatePartLocation(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	partID, _ := strconv.Atoi(r.FormValue("part_id"))
	binID, _ := strconv.Atoi(r.FormValue("bin_id"))
	quantity, _ := strconv.Atoi(r.FormValue("quantity"))

	if partID == 0 || binID == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid part or bin ID", nil)
		return
	}
	if err := a.LocStore.CreatePartLocation(partID, binID, quantity); err != nil {
		serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/part/"+r.FormValue("part_id"), http.StatusSeeOther)
}

func (a *App) handleGetPartLocationRow(w http.ResponseWriter, r *http.Request) {
	locID, _ := strconv.Atoi(chi.URLParam(r, "loc_id"))
	loc, err := a.LocStore.GetPartLocationByID(locID)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Location not found", err)
		return
	}
	a.Templates.ExecuteTemplate(w, "_part-location-row.html", loc)
}

func (a *App) handleGetPartLocationEditRow(w http.ResponseWriter, r *http.Request) {
	locID, _ := strconv.Atoi(chi.URLParam(r, "loc_id"))
	loc, err := a.LocStore.GetPartLocationByID(locID)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Location not found", err)
		return
	}
	a.Templates.ExecuteTemplate(w, "_part-location-edit-row.html", loc)
}

func (a *App) handleUpdatePartLocation(w http.ResponseWriter, r *http.Request) {
	locID, _ := strconv.Atoi(chi.URLParam(r, "loc_id"))
	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	quantity, _ := strconv.Atoi(r.FormValue("quantity"))

	if err := a.LocStore.UpdatePartLocation(locID, quantity); err != nil {
		serverError(w, r, err)
		return
	}
	loc, err := a.LocStore.GetPartLocationByID(locID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	a.Templates.ExecuteTemplate(w, "_part-location-row.html", loc)
}

func (a *App) handleDeletePartLocation(w http.ResponseWriter, r *http.Request) {
	locID, _ := strconv.Atoi(chi.URLParam(r, "loc_id"))
	if err := a.LocStore.DeletePartLocation(locID); err != nil {
		serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Settings Handlers

func (a *App) handleShowSettings(w http.ResponseWriter, r *http.Request) {
	controllers, err := a.CtrlStore.GetControllers()
	if err != nil {
		serverError(w, r, err)
		return
	}
	bins, err := a.BinStore.GetBins()
	if err != nil {
		serverError(w, r, err)
		return
	}
	data := map[string]interface{}{
		"Title":       "Settings",
		"Controllers": controllers,
		"Bins":        bins,
	}
	err = a.Templates.ExecuteTemplate(w, "settings.html", data)
	if err != nil {
		serverError(w, r, err)
	}
}

func (a *App) handleCreateController(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	name := r.FormValue("name")
	ipAddress := r.FormValue("ip_address")

	if name == "" || ipAddress == "" {
		clientError(w, r, http.StatusBadRequest, "Name and IP Address are required", nil)
		return
	}
	if err := a.CtrlStore.CreateController(name, ipAddress); err != nil {
		serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (a *App) handleDeleteController(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if id == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid ID", nil)
		return
	}
	err := a.CtrlStore.DeleteController(id)
	if err != nil {
		if errors.Is(err, store.ErrForeignKeyConstraint) {
			clientError(w, r, http.StatusConflict, "Cannot delete controller: It is in use by one or more bins.", err)
		} else {
			serverError(w, r, err)
		}
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleRefreshControllerStatus(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if id == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid ID", nil)
		return
	}
	controller, err := a.CtrlStore.GetControllerByID(id)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Controller not found", err)
		return
	}
	online := a.Wled.Ping(controller.IPAddress)
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
	if err := a.CtrlStore.UpdateControllerStatus(id, status, lastSeen); err != nil {
		log.Println("Error updating controller status on refresh:", err)
	}
	updatedController, err := a.CtrlStore.GetControllerByID(id)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Controller not found", err)
		return
	}
	a.Templates.ExecuteTemplate(w, "_controller-row.html", updatedController)
}

func (a *App) handleCreateBin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	name := r.FormValue("name")
	controllerID, _ := strconv.Atoi(r.FormValue("controller_id"))
	segmentID, _ := strconv.Atoi(r.FormValue("segment_id"))
	ledIndex, _ := strconv.Atoi(r.FormValue("led_index"))

	if name == "" || controllerID == 0 {
		clientError(w, r, http.StatusBadRequest, "Name and Controller are required", nil)
		return
	}
	err := a.BinStore.CreateBin(name, controllerID, segmentID, ledIndex)
	if err != nil {
		if errors.Is(err, store.ErrUniqueConstraint) {
			clientError(w, r, http.StatusConflict, "A bin with this name already exists.", err)
		} else if errors.Is(err, store.ErrForeignKeyConstraint) {
			clientError(w, r, http.StatusBadRequest, "Invalid controller selected.", err)
		} else {
			serverError(w, r, err)
		}
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (a *App) handleCreateBinsBulk(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	controllerID, _ := strconv.Atoi(r.FormValue("controller_id"))
	segmentID, _ := strconv.Atoi(r.FormValue("segment_id"))
	ledCount, _ := strconv.Atoi(r.FormValue("led_count"))
	namePrefix := r.FormValue("name_prefix")

	if controllerID == 0 || ledCount <= 0 || namePrefix == "" {
		clientError(w, r, http.StatusBadRequest, "Controller, positive LED count, and Name Prefix are required", nil)
		return
	}
	err := a.BinStore.CreateBinsBulk(controllerID, segmentID, ledCount, namePrefix)
	if err != nil {
		if errors.Is(err, store.ErrUniqueConstraint) {
			clientError(w, r, http.StatusConflict, "One or more bin names already exist (e.g., "+namePrefix+"0).", err)
		} else {
			serverError(w, r, err)
		}
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (a *App) handleDeleteBin(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if id == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid ID", nil)
		return
	}
	if err := a.BinStore.DeleteBin(id); err != nil {
		serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Controller Handlers

func (a *App) handleGetControllerRow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	controller, err := a.CtrlStore.GetControllerByID(id)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Controller not found", err)
		return
	}
	a.Templates.ExecuteTemplate(w, "_controller-row.html", controller)
}

func (a *App) handleGetControllerEditRow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	controller, err := a.CtrlStore.GetControllerByID(id)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Controller not found", err)
		return
	}
	a.Templates.ExecuteTemplate(w, "_controller-edit-row.html", controller)
}

func (a *App) handleUpdateController(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}

	controller := &models.WLEDController{
		ID:        id,
		Name:      r.FormValue("name"),
		IPAddress: r.FormValue("ip_address"),
	}

	if controller.Name == "" || controller.IPAddress == "" {
		clientError(w, r, http.StatusBadRequest, "Name and IP are required", nil)
		return
	}

	if err := a.CtrlStore.UpdateController(controller); err != nil {
		serverError(w, r, err)
		return
	}

	// Return the updated normal row
	updated, err := a.CtrlStore.GetControllerByID(id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	a.Templates.ExecuteTemplate(w, "_controller-row.html", updated)
}

func (a *App) handleGetControllerMigrateRow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	// 1. Get the Source Controller
	source, err := a.CtrlStore.GetControllerByID(id)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Controller not found", err)
		return
	}

	// 2. Get All Controllers to find potential Targets
	all, err := a.CtrlStore.GetControllers()
	if err != nil {
		serverError(w, r, err)
		return
	}

	// 3. Filter list: Target cannot be Source
	targets := []models.WLEDController{}
	for _, c := range all {
		if c.ID != source.ID {
			targets = append(targets, c)
		}
	}

	if len(targets) == 0 {
		clientError(w, r, http.StatusConflict, "No other controllers available to migrate to.", nil)
		return
	}

	// 4. Pass data to template
	data := map[string]interface{}{
		"Source":  source,
		"Targets": targets,
	}
	a.Templates.ExecuteTemplate(w, "_controller-migrate-row.html", data)
}

func (a *App) handleMigrateController(w http.ResponseWriter, r *http.Request) {
	oldID, _ := strconv.Atoi(chi.URLParam(r, "id"))

	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}

	newID, _ := strconv.Atoi(r.FormValue("new_controller_id"))
	if oldID == 0 || newID == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid Controller IDs", nil)
		return
	}

	// Execute Migration
	if err := a.CtrlStore.MigrateBins(oldID, newID); err != nil {
		serverError(w, r, err)
		return
	}

	// Return the updated Source row (which should now have 0 bins)
	updatedSource, err := a.CtrlStore.GetControllerByID(oldID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	a.Templates.ExecuteTemplate(w, "_controller-row.html", updatedSource)
}

// Bin Handlers

func (a *App) handleGetBinRow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	// NOTE: You need to add GetBinByID to your store! (See below)
	bin, err := a.BinStore.GetBinByID(id)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Bin not found", err)
		return
	}
	a.Templates.ExecuteTemplate(w, "_bin-row.html", bin)
}

func (a *App) handleGetBinEditRow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	bin, err := a.BinStore.GetBinByID(id)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Bin not found", err)
		return
	}

	// controllers for the dropdown
	controllers, _ := a.CtrlStore.GetControllers()

	data := map[string]interface{}{
		"Bin":         bin,
		"Controllers": controllers,
	}
	a.Templates.ExecuteTemplate(w, "_bin-edit-row.html", data)
}

func (a *App) handleUpdateBin(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
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
		clientError(w, r, http.StatusBadRequest, "Name and Controller are required", nil)
		return
	}

	if err := a.BinStore.UpdateBin(bin); err != nil {
		if errors.Is(err, store.ErrUniqueConstraint) {
			clientError(w, r, http.StatusConflict, "Bin name already exists", err)
		} else {
			serverError(w, r, err)
		}
		return
	}

	// Return updated row
	updated, _ := a.BinStore.GetBinByID(id)
	a.Templates.ExecuteTemplate(w, "_bin-row.html", updated)
}

// URL Handlers

func (a *App) handleAddPartURL(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	partID, _ := strconv.Atoi(r.FormValue("part_id"))
	url := r.FormValue("url")
	desc := r.FormValue("description")

	if partID == 0 || url == "" {
		clientError(w, r, http.StatusBadRequest, "Part ID and URL are required", nil)
		return
	}
	if err := a.PartStore.CreatePartURL(partID, url, desc); err != nil {
		serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/part/"+r.FormValue("part_id"), http.StatusSeeOther)
}

func (a *App) handleDeletePartURL(w http.ResponseWriter, r *http.Request) {
	urlID, _ := strconv.Atoi(chi.URLParam(r, "url_id"))
	if urlID == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid URL ID", nil)
		return
	}
	if err := a.PartStore.DeletePartURL(urlID); err != nil {
		serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Document Handlers

func (a *App) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if partID == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid Part ID", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		clientError(w, r, http.StatusBadRequest, "File is too large (Max 5MB)", err)
		return
	}
	file, header, err := r.FormFile("part_document")
	if err != nil {
		clientError(w, r, http.StatusBadRequest, "Invalid file upload", err)
		return
	}
	defer file.Close()
	filename := fmt.Sprintf("%d-%d-%s", partID, time.Now().UnixNano(), header.Filename)
	relPath := path.Join("documents", filename)
	absDir := filepath.Join("data", "uploads", "documents")
	absPath := filepath.Join(absDir, filename)

	if err := os.MkdirAll(absDir, os.ModePerm); err != nil {
		serverError(w, r, err)
		return
	}
	dst, err := os.Create(absPath)
	if err != nil {
		serverError(w, r, err)
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		serverError(w, r, err)
		return
	}
	doc := &models.PartDocument{
		PartID:      partID,
		Filename:    header.Filename,
		Filepath:    relPath,
		Description: sql.NullString{String: r.FormValue("description"), Valid: true},
		Mimetype:    header.Header.Get("Content-Type"),
	}
	if err := a.PartStore.CreatePartDocument(doc); err != nil {
		serverError(w, r, err)
		os.Remove(absPath)
		return
	}
	http.Redirect(w, r, "/part/"+strconv.Itoa(partID), http.StatusSeeOther)
}

func (a *App) handleDownloadDocument(w http.ResponseWriter, r *http.Request) {
	docID, _ := strconv.Atoi(chi.URLParam(r, "doc_id"))
	if docID == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid Document ID", nil)
		return
	}
	doc, err := a.PartStore.GetDocumentByID(docID)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Document not found", err)
		return
	}
	w.Header().Set("Content-Type", doc.Mimetype)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+doc.Filename+"\"")
	http.ServeFile(w, r, filepath.Join("data", "uploads", doc.Filepath))
}

func (a *App) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	docID, _ := strconv.Atoi(chi.URLParam(r, "doc_id"))
	if docID == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid Document ID", nil)
		return
	}
	doc, err := a.PartStore.GetDocumentByID(docID)
	if err != nil {
		clientError(w, r, http.StatusNotFound, "Document not found", err)
		return
	}
	if err := a.PartStore.DeletePartDocument(docID); err != nil {
		serverError(w, r, err)
		return
	}
	if err := os.Remove(filepath.Join("data", "uploads", doc.Filepath)); err != nil {
		log.Printf("Warning: failed to delete document file %s: %v", doc.Filepath, err)
	}
	w.WriteHeader(http.StatusOK)
}

// Category Handlers

func (a *App) handleAssignCategoryToPart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		clientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	partID, _ := strconv.Atoi(r.FormValue("part_id"))
	categoryName := r.FormValue("category_name")

	if partID == 0 || categoryName == "" {
		clientError(w, r, http.StatusBadRequest, "Part ID and Category Name are required", nil)
		return
	}
	category, err := a.PartStore.CreateCategory(categoryName)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if err := a.PartStore.AssignCategoryToPart(partID, category.ID); err != nil {
		serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/part/"+r.FormValue("part_id"), http.StatusSeeOther)
}

func (a *App) handleRemoveCategoryFromPart(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "part_id"))
	categoryID, _ := strconv.Atoi(chi.URLParam(r, "cat_id"))

	if partID == 0 || categoryID == 0 {
		clientError(w, r, http.StatusBadRequest, "Invalid Part or Category ID", nil)
		return
	}
	if err := a.PartStore.RemoveCategoryFromPart(partID, categoryID); err != nil {
		serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleCleanupCategories(w http.ResponseWriter, r *http.Request) {
	if err := a.PartStore.CleanupOrphanedCategories(); err != nil {
		serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// Dashboard & WLED Handlers

func (a *App) handleShowDashboard(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title": "Stock Dashboard",
	}
	err := a.Templates.ExecuteTemplate(w, "dashboard.html", data)
	if err != nil {
		serverError(w, r, err)
	}
}

func (a *App) handleShowStockStatus(w http.ResponseWriter, r *http.Request) {
	level := r.FormValue("level")
	allBinsForStop, err := a.DashStore.GetAllBinLocationsForStopAll()
	if err != nil {
		serverError(w, r, err)
		return
	}
	type ledPayload map[string]map[int][]interface{}
	stopPayloads := make(ledPayload)
	colorBlack := "000000"
	for _, bin := range allBinsForStop {
		if stopPayloads[bin.IP] == nil {
			stopPayloads[bin.IP] = make(map[int][]interface{})
		}
		stopPayloads[bin.IP][bin.SegID] = append(
			stopPayloads[bin.IP][bin.SegID],
			bin.LEDIndex, colorBlack,
		)
	}
	for ip, segments := range stopPayloads {
		wledSegments := []models.WLEDSegment{}
		for segID, iPayload := range segments {
			wledSegments = append(wledSegments, models.WLEDSegment{ID: segID, On: true, I: iPayload})
		}
		state := models.WLEDState{Segments: wledSegments}
		if err := a.Wled.SendCommand(ip, state); err != nil {
			log.Println("Dashboard (Clear): Failed to send WLED 'off' command to", ip, ":", err)
		}
	}
	allBins, err := a.DashStore.GetDashboardBinData()
	if err != nil {
		serverError(w, r, err)
		return
	}
	colorRed := "FF0000"
	colorYellow := "FFFF00"
	colorGreen := "00FF00"
	payloads := make(ledPayload)
	binsLit := 0
	for _, bin := range allBins {
		var color string
		if bin.BinQuantity <= bin.MinStock {
			color = colorRed
		} else if bin.BinQuantity <= bin.ReorderPoint {
			color = colorYellow
		} else {
			color = colorGreen
		}
		if level == "critical" && color != colorRed {
			continue
		}
		if level == "attention" && color == colorGreen {
			continue
		}
		if payloads[bin.BinIP] == nil {
			payloads[bin.BinIP] = make(map[int][]interface{})
		}
		payloads[bin.BinIP][bin.BinSegmentID] = append(
			payloads[bin.BinIP][bin.BinSegmentID],
			bin.BinLEDIndex, color,
		)
		binsLit++
	}
	for ip, segments := range payloads {
		wledSegments := []models.WLEDSegment{}
		for segID, iPayload := range segments {
			wledSegments = append(wledSegments, models.WLEDSegment{ID: segID, On: true, I: iPayload})
		}
		state := models.WLEDState{Segments: wledSegments}
		if err := a.Wled.SendCommand(ip, state); err != nil {
			log.Println("Dashboard: Failed to send WLED command to", ip, ":", err)
		}
	}
	fmt.Fprintf(w, "<strong>Success!</strong> Lit %d bins.", binsLit)
}

func (a *App) handleLocatePart(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	locations, err := a.DashStore.GetPartLocationsForLocate(partID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	type ledMap map[string]map[int][]int
	ledsByController := make(ledMap)
	for _, loc := range locations {
		if ledsByController[loc.IP] == nil {
			ledsByController[loc.IP] = make(map[int][]int)
		}
		ledsByController[loc.IP][loc.SegID] = append(ledsByController[loc.IP][loc.SegID], loc.LEDIndex)
	}
	color := "FF0000"
	for ip, segments := range ledsByController {
		wledSegments := []models.WLEDSegment{}
		for segID, ledIndices := range segments {
			iPayload := []interface{}{}
			for _, ledIndex := range ledIndices {
				iPayload = append(iPayload, ledIndex, color)
			}
			wledSegments = append(wledSegments, models.WLEDSegment{ID: segID, On: true, I: iPayload})
		}
		state := models.WLEDState{Segments: wledSegments}
		if err := a.Wled.SendCommand(ip, state); err != nil {
			log.Println("Failed to send WLED command to", ip, ":", err)
			part := models.Part{ID: partID}
			a.Templates.ExecuteTemplate(w, "_locate-start-button.html", part)
			return
		}
	}
	part := models.Part{ID: partID}
	a.Templates.ExecuteTemplate(w, "_locate-stop-button.html", part)
}

func (a *App) handleStopLocate(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	locations, err := a.DashStore.GetPartLocationsForStop(partID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	type ledMap map[string]map[int][]int
	ledsByController := make(ledMap)
	for _, loc := range locations {
		if ledsByController[loc.IP] == nil {
			ledsByController[loc.IP] = make(map[int][]int)
		}
		ledsByController[loc.IP][loc.SegID] = append(ledsByController[loc.IP][loc.SegID], loc.LEDIndex)
	}
	color := "000000"
	for ip, segments := range ledsByController {
		wledSegments := []models.WLEDSegment{}
		for segID, ledIndices := range segments {
			iPayload := []interface{}{}
			for _, ledIndex := range ledIndices {
				iPayload = append(iPayload, ledIndex, color)
			}
			wledSegments = append(wledSegments, models.WLEDSegment{ID: segID, On: true, I: iPayload})
		}
		state := models.WLEDState{Segments: wledSegments}
		if err := a.Wled.SendCommand(ip, state); err != nil {
			log.Println("Failed to send WLED 'off' command to", ip, ":", err)
		}
	}
	part := models.Part{ID: partID}
	a.Templates.ExecuteTemplate(w, "_locate-start-button.html", part)
}

func (a *App) handleStopAll(w http.ResponseWriter, r *http.Request) {
	locations, err := a.DashStore.GetAllBinLocationsForStopAll()
	if err != nil {
		serverError(w, r, err)
		return
	}
	type ledMap map[string]map[int][]int
	ledsByController := make(ledMap)
	for _, loc := range locations {
		if ledsByController[loc.IP] == nil {
			ledsByController[loc.IP] = make(map[int][]int)
		}
		ledsByController[loc.IP][loc.SegID] = append(ledsByController[loc.IP][loc.SegID], loc.LEDIndex)
	}
	color := "000000"
	for ip, segments := range ledsByController {
		wledSegments := []models.WLEDSegment{}
		for segID, ledIndices := range segments {
			iPayload := []interface{}{}
			for _, ledIndex := range ledIndices {
				iPayload = append(iPayload, ledIndex, color)
			}
			wledSegments = append(wledSegments, models.WLEDSegment{ID: segID, On: true, I: iPayload})
		}
		state := models.WLEDState{Segments: wledSegments}
		if err := a.Wled.SendCommand(ip, state); err != nil {
			log.Println("StopAll: Failed to send WLED 'off' command to", ip, ":", err)
		}
	}
	w.Header().Set("HX-Trigger", "resetLocateButtons")
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleGetLocateButton(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	part := models.Part{ID: id}
	a.Templates.ExecuteTemplate(w, "_locate-start-button.html", part)
}

// handleShowInspiration generates a prompt for an LLM
func (a *App) handleShowInspiration(w http.ResponseWriter, r *http.Request) {
	// Get all parts from the store. This already has TotalQuantity
	parts, err := a.PartStore.GetParts()
	if err != nil {
		serverError(w, r, err)
		return
	}

	// Build the inventory list string
	var inventoryList strings.Builder
	for _, part := range parts {
		if part.TotalQuantity > 0 {
			// Format as: "- Part Name (Part Number): X in stock"
			inventoryList.WriteString(fmt.Sprintf("- %s", part.Name))
			if part.PartNumber.Valid {
				inventoryList.WriteString(fmt.Sprintf(" (%s)", part.PartNumber.String))
			}
			inventoryList.WriteString(fmt.Sprintf(": %d in stock\n", part.TotalQuantity))
		}
	}

	// Define the base prompt
	basePrompt := `### Project Idea Generator Prompt
Generate 3 - 5 project ideas based on the provided [INVENTORY LIST] following all instructions.

### Objective
The goal of this prompt is to generate innovative and practical project ideas, focusing primarily on maximizing the use of a user-provided list of existing components. The generated projects must be feasible with the materials on hand and should inspire the user.

### Role
You are an expert project ideator and hardware hacker. Your task is to analyze a provided inventory list and generate a list of creative, functional, and engaging project ideas that utilize the components listed.

### Context
The user will provide an inventory list detailing the components they currently have in stock. This list will include the component's name and the quantity available. The user is looking for inspiration for new projects and wants to ensure the ideas are highly relevant to their existing materials.

### Instructions

1.  **Analyze the Inventory:** Thoroughly review the provided \[INVENTORY LIST\] for key components, quantities, and potential synergistic pairings (e.g., microcontrollers, sensors, actuators, power sources).
2.  **Generate Ideas:** Create a list of 3-5 distinct project ideas.
    *   **Priority:** Focus on projects that utilize the most valuable or most numerous components in the inventory.
    *   **Feasibility:** Ensure the projects are realistically achievable *primarily* with the listed parts. If a common, easily obtainable component (e.g., standard wire, basic resistor) is needed, it can be mentioned, but the core functionality must rely on the provided inventory.
3.  **Structure the Output:** For each project idea, provide the following mandatory sections:
    *   **Project Title:** A concise and compelling name.
    *   **Concept Summary:** A brief, engaging description of what the project does and its utility or entertainment value.
    *   **Key Components Used:** A list of essential components from the user's \[INVENTORY LIST\] required for this project. State the required quantity for each.
    *   **Next Steps/Inspiration:** A suggestion for how the user could expand or modify the project if they were to acquire two to three additional, specific components (e.g., "Add a Wi-Fi module for remote control").
4.  **Tone:** Enthusiastic, practical, and inspiring.

### Constraint
Do not suggest projects that require highly specialized or expensive components *not* present in the \[INVENTORY LIST\]. The ideas must be centered around the given materials.

***

### User Input:
#### INVENTORY LIST
`

	// Combine the prompt and the list
	finalPrompt := basePrompt + inventoryList.String()

	// Render the template
	data := map[string]interface{}{
		"Title":  "Project Inspiration",
		"Prompt": finalPrompt,
	}

	err = a.Templates.ExecuteTemplate(w, "inspiration.html", data)
	if err != nil {
		serverError(w, r, err)
	}
}
