package parts

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"wledger/internal/core"
	"wledger/internal/models"
)

const maxUploadSize = 5 * 1024 * 1024 // 5 MB

// Store defines the database methods this module needs
// It large because the Details page aggregates data from many tables
// It could probably be split up further but for now it's manageable
type Store interface {
	// Part Core
	GetPartByID(id int) (models.Part, error)
	GetParts() ([]models.Part, error)
	SearchParts(searchTerm string) ([]models.Part, error)
	CreatePart(p *models.Part) error
	UpdatePart(p *models.Part) error
	DeletePart(id int) error
	UpdatePartImagePath(partID int, imagePath string) error

	// Related Data (read only for details page))
	GetPartLocations(partID int) ([]models.PartLocation, error)
	GetAvailableBins(partID int) ([]models.Bin, error)

	// URLs
	GetURLsByPartID(partID int) ([]models.PartURL, error)
	CreatePartURL(partID int, url string, description string) error
	DeletePartURL(urlID int) error

	// Documents
	GetDocumentsByPartID(partID int) ([]models.PartDocument, error)
	GetDocumentByID(docID int) (models.PartDocument, error)
	CreatePartDocument(doc *models.PartDocument) error
	DeletePartDocument(docID int) error

	// Categories
	GetCategories() ([]models.Category, error)
	GetCategoriesByPartID(partID int) ([]models.Category, error)
	CreateCategory(name string) (models.Category, error)
	AssignCategoryToPart(partID int, categoryID int) error
	RemoveCategoryFromPart(partID int, categoryID int) error
}

type Handler struct {
	store     Store
	templates core.TemplateExecutor
}

func New(s Store, t core.TemplateExecutor) *Handler {
	return &Handler{store: s, templates: t}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	// Core Part Routes
	r.Get("/", h.handleShowParts)
	r.Post("/parts/search", h.handleSearchParts)
	r.Post("/parts", h.handleCreatePart)
	r.Delete("/parts/{id}", h.handleDeletePart)

	// Details page and sub-resources
	r.Get("/part/{id}", h.handleShowPartDetails)
	r.Post("/part/{id}/details", h.handleUpdatePartDetails)
	r.Post("/part/{id}/image/upload", h.handlePartImageUpload)

	// URLs
	r.Post("/part/urls", h.handleAddPartURL)
	r.Delete("/part/urls/{url_id}", h.handleDeletePartURL)

	// Documents
	r.Post("/part/{id}/document/upload", h.handleUploadDocument)
	r.Get("/part/document/{doc_id}", h.handleDownloadDocument)
	r.Delete("/part/document/{doc_id}", h.handleDeleteDocument)

	// Categories
	r.Post("/part/categories", h.handleAssignCategoryToPart)
	r.Delete("/part/{part_id}/categories/{cat_id}", h.handleRemoveCategoryFromPart)
}

// Handlers

func (h *Handler) handleShowParts(w http.ResponseWriter, r *http.Request) {
	parts, err := h.store.GetParts()
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	data := map[string]interface{}{
		"Title": "Inventory",
		"Parts": parts,
	}

	err = h.templates.ExecuteTemplate(w, "index.html", data)
	if err != nil {
		core.ServerError(w, r, err)
	}
}

func (h *Handler) handleSearchParts(w http.ResponseWriter, r *http.Request) {
	searchTerm := r.FormValue("search")

	parts, err := h.store.SearchParts(searchTerm)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	err = h.templates.ExecuteTemplate(w, "_parts-list.html", parts)
	if err != nil {
		core.ServerError(w, r, err)
	}
}

func (h *Handler) handleCreatePart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
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
		core.ClientError(w, r, http.StatusBadRequest, "Part name is required", nil)
		return
	}

	if err := h.store.CreatePart(part); err != nil {
		core.ServerError(w, r, err)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) handleDeletePart(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if id == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid ID", nil)
		return
	}

	if err := h.store.DeletePart(id); err != nil {
		core.ServerError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleShowPartDetails(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	part, err := h.store.GetPartByID(id)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Part not found", err)
		return
	}

	locations, err := h.store.GetPartLocations(id)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	availableBins, err := h.store.GetAvailableBins(id)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	urls, err := h.store.GetURLsByPartID(id)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	docs, err := h.store.GetDocumentsByPartID(id)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	assignedCategories, err := h.store.GetCategoriesByPartID(id)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}
	allCategories, err := h.store.GetCategories()
	if err != nil {
		core.ServerError(w, r, err)
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

	err = h.templates.ExecuteTemplate(w, "part-details.html", data)
	if err != nil {
		core.ServerError(w, r, err)
	}
}

func (h *Handler) handleUpdatePartDetails(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if id == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid Part ID", nil)
		return
	}

	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_point"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))
	stockTracking := r.FormValue("stock_tracking_enabled") == "on"

	part, err := h.store.GetPartByID(id)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Part not found", err)
		return
	}

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

	if err := h.store.UpdatePart(&part); err != nil {
		core.ServerError(w, r, err)
		return
	}

	http.Redirect(w, r, "/part/"+strconv.Itoa(id), http.StatusSeeOther)
}

func (h *Handler) handlePartImageUpload(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if partID == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid Part ID", nil)
		return
	}
	part, err := h.store.GetPartByID(partID)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Part not found", err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "File is too large (Max 5MB)", err)
		return
	}

	file, header, err := r.FormFile("part_image")
	if err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid file upload", err)
		return
	}
	defer file.Close()

	buff := make([]byte, 512)
	if _, err := file.Read(buff); err != nil {
		core.ServerError(w, r, err)
		return
	}
	filetype := http.DetectContentType(buff)
	if !strings.HasPrefix(filetype, "image/") {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid file type, must be an image", nil)
		return
	}
	if _, err := file.Seek(0, 0); err != nil {
		core.ServerError(w, r, err)
		return
	}

	ext := filepath.Ext(header.Filename)
	filename := fmt.Sprintf("%d-%d%s", partID, time.Now().UnixNano(), ext)

	relPath := path.Join("images", filename)
	absDir := filepath.Join("data", "uploads", "images")
	absPath := filepath.Join(absDir, filename)

	if err := os.MkdirAll(absDir, os.ModePerm); err != nil {
		core.ServerError(w, r, err)
		return
	}
	dst, err := os.Create(absPath)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		core.ServerError(w, r, err)
		return
	}

	if part.ImagePath.Valid && part.ImagePath.String != "" {
		oldPath := filepath.Join("data", "uploads", part.ImagePath.String)
		if err := os.Remove(oldPath); err != nil {
			fmt.Printf("Warning: failed to delete old image file %s: %v\n", oldPath, err)
		}
	}

	if err := h.store.UpdatePartImagePath(partID, relPath); err != nil {
		core.ServerError(w, r, err)
		return
	}
	http.Redirect(w, r, "/part/"+strconv.Itoa(partID), http.StatusSeeOther)
}

func (h *Handler) handleAddPartURL(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	partID, _ := strconv.Atoi(r.FormValue("part_id"))
	url := r.FormValue("url")
	desc := r.FormValue("description")

	if partID == 0 || url == "" {
		core.ClientError(w, r, http.StatusBadRequest, "Part ID and URL are required", nil)
		return
	}
	if err := h.store.CreatePartURL(partID, url, desc); err != nil {
		core.ServerError(w, r, err)
		return
	}
	http.Redirect(w, r, "/part/"+r.FormValue("part_id"), http.StatusSeeOther)
}

func (h *Handler) handleDeletePartURL(w http.ResponseWriter, r *http.Request) {
	urlID, _ := strconv.Atoi(chi.URLParam(r, "url_id"))
	if urlID == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid URL ID", nil)
		return
	}
	if err := h.store.DeletePartURL(urlID); err != nil {
		core.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if partID == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid Part ID", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "File is too large (Max 5MB)", err)
		return
	}
	file, header, err := r.FormFile("part_document")
	if err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid file upload", err)
		return
	}
	defer file.Close()
	filename := fmt.Sprintf("%d-%d-%s", partID, time.Now().UnixNano(), header.Filename)
	relPath := path.Join("documents", filename)
	absDir := filepath.Join("data", "uploads", "documents")
	absPath := filepath.Join(absDir, filename)

	if err := os.MkdirAll(absDir, os.ModePerm); err != nil {
		core.ServerError(w, r, err)
		return
	}
	dst, err := os.Create(absPath)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		core.ServerError(w, r, err)
		return
	}
	doc := &models.PartDocument{
		PartID:      partID,
		Filename:    header.Filename,
		Filepath:    relPath,
		Description: sql.NullString{String: r.FormValue("description"), Valid: true},
		Mimetype:    header.Header.Get("Content-Type"),
	}
	if err := h.store.CreatePartDocument(doc); err != nil {
		core.ServerError(w, r, err)
		os.Remove(absPath)
		return
	}
	http.Redirect(w, r, "/part/"+strconv.Itoa(partID), http.StatusSeeOther)
}

func (h *Handler) handleDownloadDocument(w http.ResponseWriter, r *http.Request) {
	docID, _ := strconv.Atoi(chi.URLParam(r, "doc_id"))
	if docID == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid Document ID", nil)
		return
	}
	doc, err := h.store.GetDocumentByID(docID)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Document not found", err)
		return
	}
	w.Header().Set("Content-Type", doc.Mimetype)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+doc.Filename+"\"")
	http.ServeFile(w, r, filepath.Join("data", "uploads", doc.Filepath))
}

func (h *Handler) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	docID, _ := strconv.Atoi(chi.URLParam(r, "doc_id"))
	if docID == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid Document ID", nil)
		return
	}
	doc, err := h.store.GetDocumentByID(docID)
	if err != nil {
		core.ClientError(w, r, http.StatusNotFound, "Document not found", err)
		return
	}
	if err := h.store.DeletePartDocument(docID); err != nil {
		core.ServerError(w, r, err)
		return
	}
	if err := os.Remove(filepath.Join("data", "uploads", doc.Filepath)); err != nil {
		fmt.Printf("Warning: failed to delete document file %s: %v\n", doc.Filepath, err)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleAssignCategoryToPart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Bad Request", err)
		return
	}
	partID, _ := strconv.Atoi(r.FormValue("part_id"))
	categoryName := r.FormValue("category_name")

	if partID == 0 || categoryName == "" {
		core.ClientError(w, r, http.StatusBadRequest, "Part ID and Category Name are required", nil)
		return
	}
	category, err := h.store.CreateCategory(categoryName)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}
	if err := h.store.AssignCategoryToPart(partID, category.ID); err != nil {
		core.ServerError(w, r, err)
		return
	}
	http.Redirect(w, r, "/part/"+r.FormValue("part_id"), http.StatusSeeOther)
}

func (h *Handler) handleRemoveCategoryFromPart(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "part_id"))
	categoryID, _ := strconv.Atoi(chi.URLParam(r, "cat_id"))

	if partID == 0 || categoryID == 0 {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid Part or Category ID", nil)
		return
	}
	if err := h.store.RemoveCategoryFromPart(partID, categoryID); err != nil {
		core.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
