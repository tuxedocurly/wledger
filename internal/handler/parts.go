package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/stock"
	"github.com/tuxedocurly/wledger/web/components"
	"github.com/tuxedocurly/wledger/web/pages"
)

// --- LIST & DETAIL ---

// GET /parts
func (h *Handler) HandlePartsList(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	search := r.URL.Query().Get("q")
	pageStr := r.URL.Query().Get("page")
	binStr := r.URL.Query().Get("bin")
	scroll := r.URL.Query().Get("scroll") == "true"

	page, _ := strconv.Atoi(pageStr)
	if page < 1 {
		page = 1
	}

	var binID *int64
	if binStr != "" {
		if id, err := strconv.ParseInt(binStr, 10, 64); err == nil {
			binID = &id
		}
	}

	viewParts, err := h.Parts.ListParts(r.Context(), search, page, binID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch parts", http.StatusInternalServerError)
		return
	}

	// Render logic
	if scroll {
		// If infinite scroll, return JUST the new cards (appended to bottom)
		pages.PartCards(viewParts, search, page, binID).Render(r.Context(), w)
	} else {
		// If full page load or search replacement, return the full wrapper
		pages.PartsList(user, viewParts, search, page, binID).Render(r.Context(), w)
	}
}

// GET /parts/{id}
func (h *Handler) HandlePartDetail(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	detail, err := h.Parts.GetPartDetail(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Part not found", http.StatusNotFound)
		return
	}

	pages.PartDetail(user, detail.Part, detail.Stock, detail.Links, detail.Docs, detail.Controllers).Render(r.Context(), w)
}

// -----------------------------------------------------------
// CREATE & UPDATE
// -----------------------------------------------------------

// GET /parts/new
func (h *Handler) HandlePartsNew(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	allTags, _ := h.Tags.ListAllTags(r.Context())
	pages.PartCreate(user, allTags).Render(r.Context(), w)
}

// POST /parts
func (h *Handler) HandlePartsCreate(w http.ResponseWriter, r *http.Request) {
	req, err := h.parsePartCreateRequest(r)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to parse form", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	newID, err := h.Parts.CreatePart(r.Context(), req)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			h.UIError.Respond(w, r, err, "Part already exists (check barcode)", http.StatusConflict)
		} else {
			h.UIError.Respond(w, r, err, "Failed to create part", http.StatusInternalServerError)
		}
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", newID), http.StatusSeeOther)
}

// GET /parts/{id}/edit
func (h *Handler) HandlePartEdit(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	detail, err := h.Parts.GetPartDetail(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Part not found", http.StatusNotFound)
		return
	}

	tags, _ := h.Queries.GetTagsForPart(r.Context(), int64(id))
	allTags, _ := h.Tags.ListAllTags(r.Context())

	pages.PartEdit(user, detail.Part, tags, allTags, detail.Links, detail.Docs).Render(r.Context(), w)
}

// POST /parts/{id}/update
func (h *Handler) HandlePartUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	req, err := h.parsePartUpdateRequest(r, int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to parse form", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	err = h.Parts.UpdatePart(r.Context(), req)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to update part", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", id), http.StatusSeeOther)
}

// POST /parts/{id}/delete
func (h *Handler) HandlePartDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	partID := int64(id)

	err := h.Parts.DeletePart(r.Context(), partID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to delete part", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/parts", http.StatusSeeOther)
}

// GET /parts/{id}/clone
func (h *Handler) HandlePartClone(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	partID := int64(id)

	newID, err := h.Parts.ClonePart(r.Context(), partID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to clone part", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d/edit", newID), http.StatusSeeOther)
}

// DELETE /parts/bulk
func (h *Handler) HandlePartsBulkDelete(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	if !user.CanWrite() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.UIError.Respond(w, r, err, "Failed to parse form", http.StatusBadRequest)
		return
	}

	idStrings := r.Form["ids"]
	if len(idStrings) == 0 {
		// Try to parse from JSON if form is empty (HTMX can send both depending on config)
		h.Logger.Warn("no IDs provided for bulk delete")
	}

	var ids []int64
	for _, s := range idStrings {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}

	if len(ids) == 0 {
		h.UIError.Respond(w, r, nil, "No parts selected", http.StatusBadRequest)
		return
	}

	err := h.Parts.DeleteParts(r.Context(), ids)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to delete parts", http.StatusInternalServerError)
		return
	}

	// For HTMX response:
	// Redirect to /parts to ensure a clean state
	w.Header().Set("HX-Redirect", "/parts")
	w.WriteHeader(http.StatusOK)
}

// -----------------------------------------------------------
// SUB-RESOURCES (HTMX)
// -----------------------------------------------------------

// DELETE /parts/links/{id}
func (h *Handler) HandleLinkDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	err := h.Documents.DeleteLink(r.Context(), int64(id))
	if err != nil {
		h.Logger.Error("failed to delete part link", "err", err, "link_id", id)
	}
	w.WriteHeader(http.StatusOK)
}

// DELETE /parts/docs/{id}
func (h *Handler) HandleDocDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	err := h.Documents.DeleteDocument(r.Context(), int64(id))
	if err != nil {
		h.Logger.Error("failed to delete part doc", "err", err, "doc_id", id)
	}
	w.WriteHeader(http.StatusOK)
}

// -----------------------------------------------------------
// STOCK & BINS
// -----------------------------------------------------------

func (h *Handler) HandleBinOptions(w http.ResponseWriter, r *http.Request) {
	cid, _ := strconv.Atoi(r.URL.Query().Get("controller_id"))

	containers, err := h.Queries.GetContainersByController(r.Context(), int64(cid))
	if err != nil {
		h.Logger.Error("failed to fetch containers", "err", err, "controller_id", cid)
		components.BinOptions([]db.Bin{}).Render(r.Context(), w)
		return
	}

	var allBins []db.Bin
	for _, c := range containers {
		bins, err := h.Queries.GetBinsByContainer(r.Context(), c.ID)
		if err == nil {
			allBins = append(allBins, bins...)
		}
	}

	components.BinOptions(allBins).Render(r.Context(), w)
}

func (h *Handler) HandleBinPicker(w http.ResponseWriter, r *http.Request) {
	cidStr := r.URL.Query().Get("controller_id")
	cid, _ := strconv.ParseInt(cidStr, 10, 64)

	if cid == 0 {
		h.Logger.Error("HandleBinPicker: invalid or missing controller_id")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	grid, err := h.Dashboard.GetGridByController(r.Context(), cid)
	if err != nil {
		h.Logger.Error("failed to fetch bin picker grid", "err", err, "controller_id", cid)
		// Return empty state component if not found or error
		components.BinPickerGrid(components.DashboardController{}).Render(r.Context(), w)
		return
	}

	components.BinPickerGrid(*grid).Render(r.Context(), w)
}

func (h *Handler) HandlePartAssign(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	binID, _ := strconv.Atoi(r.FormValue("bin_id"))
	qty, _ := strconv.Atoi(r.FormValue("quantity"))

	if binID == 0 || qty <= 0 {
		h.UIError.Respond(w, r, nil, "Invalid input: positive quantity and valid bin required", http.StatusBadRequest)
		return
	}

	err := h.Stock.AssignStock(r.Context(), stock.AssignStockRequest{
		PartID:   int64(partID),
		BinID:    int64(binID),
		Quantity: qty,
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to assign stock", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (h *Handler) HandlePartStockMove(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	assignmentID, _ := strconv.Atoi(chi.URLParam(r, "assignment_id"))
	targetBinID, _ := strconv.Atoi(r.FormValue("bin_id"))

	if targetBinID == 0 {
		h.UIError.Respond(w, r, nil, "Invalid target bin", http.StatusBadRequest)
		return
	}

	err := h.Stock.MoveStock(r.Context(), stock.MoveStockRequest{
		PartID:       int64(partID),
		AssignmentID: int64(assignmentID),
		TargetBinID:  int64(targetBinID),
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to move stock", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (h *Handler) HandlePartStockRemove(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	assignmentID, _ := strconv.Atoi(chi.URLParam(r, "assignment_id"))

	err := h.Stock.RemoveStock(r.Context(), stock.RemoveStockRequest{
		PartID:       int64(partID),
		AssignmentID: int64(assignmentID),
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to remove stock", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (h *Handler) HandlePartStockAdjust(w http.ResponseWriter, r *http.Request) {
	// partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	assignmentID, _ := strconv.Atoi(chi.URLParam(r, "assignment_id"))
	delta, _ := strconv.Atoi(r.URL.Query().Get("delta"))

	if delta == 0 {
		h.UIError.Respond(w, r, nil, "Invalid delta", http.StatusBadRequest)
		return
	}

	err := h.Stock.AdjustStock(r.Context(), int64(assignmentID), delta)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to adjust stock", http.StatusInternalServerError)
		return
	}

	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))

	// If HTMX request, render just the row
	if r.Header.Get("HX-Request") == "true" {
		stock, err := h.Queries.GetPartAssignments(r.Context(), int64(partID))
		if err != nil {
			http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
			return
		}

		var targetRow db.GetPartAssignmentsRow
		found := false
		for _, s := range stock {
			if s.ID == int64(assignmentID) {
				targetRow = s
				found = true
				break
			}
		}

		if !found {
			http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
			return
		}

		controllers, _ := h.Queries.GetControllers(r.Context())
		user := auth.GetUserFromRequest(r)

		pages.StockRow(int64(partID), targetRow, user, controllers).Render(r.Context(), w)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

// POST /parts/{id}/locate
func (h *Handler) HandlePartLocate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	err := h.WLED.LocatePart(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Locate failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// --- HELPERS ---

func (h *Handler) parsePartCreateRequest(r *http.Request) (parts.CreatePartRequest, error) {
	err := r.ParseMultipartForm(config.MaxUploadSizeParts)
	if err != nil {
		return parts.CreatePartRequest{}, err
	}

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_level"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))

	req := parts.CreatePartRequest{
		Name:              r.FormValue("name"),
		Description:       r.FormValue("description"),
		PartNumber:        r.FormValue("part_number"),
		Manufacturer:      r.FormValue("manufacturer"),
		Supplier:          r.FormValue("supplier"),
		BarcodeData:       r.FormValue("barcode_data"),
		UnitCost:          cost,
		ReorderLevel:      reorder,
		MinStockThreshold: minStock,
		Tags:              h.parseFormTags(r.FormValue("tags")),
		Links:             h.parseFormLinks(r, ""),
		Documents:         h.parseFormDocuments(r),
	}

	// Handle Image
	if file, header, err := r.FormFile("image"); err == nil {
		req.Image = &parts.DocUpload{File: file, Header: header}
	}

	return req, nil
}

func (h *Handler) parsePartUpdateRequest(r *http.Request, id int64) (parts.UpdatePartRequest, error) {
	err := r.ParseMultipartForm(config.MaxUploadSizeParts)
	if err != nil {
		return parts.UpdatePartRequest{}, err
	}

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_level"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))

	req := parts.UpdatePartRequest{
		ID:                id,
		Name:              r.FormValue("name"),
		Description:       r.FormValue("description"),
		PartNumber:        r.FormValue("part_number"),
		Manufacturer:      r.FormValue("manufacturer"),
		Supplier:          r.FormValue("supplier"),
		BarcodeData:       r.FormValue("barcode_data"),
		UnitCost:          cost,
		ReorderLevel:      reorder,
		MinStockThreshold: minStock,
		Tags:              h.parseFormTags(r.FormValue("tags")),
		ExistingLinks:     h.parseFormLinks(r, "existing_"),
		NewLinks:          h.parseFormLinks(r, ""),
		NewDocuments:      h.parseFormDocuments(r),
	}

	// Handle Image
	if file, header, err := r.FormFile("image"); err == nil {
		req.Image = &parts.DocUpload{File: file, Header: header}
	}

	return req, nil
}

func (h *Handler) parseFormTags(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func (h *Handler) parseFormLinks(r *http.Request, prefix string) []parts.LinkDTO {
	var links []parts.LinkDTO
	ids := r.PostForm[prefix+"link_ids[]"]
	labels := r.PostForm[prefix+"link_labels[]"]
	urls := r.PostForm[prefix+"link_urls[]"]

	for i, u := range urls {
		if u == "" {
			continue
		}
		link := parts.LinkDTO{URL: u}
		if i < len(labels) {
			link.Label = labels[i]
		}
		if i < len(ids) {
			lid, _ := strconv.Atoi(ids[i])
			link.ID = int64(lid)
		}
		links = append(links, link)
	}
	return links
}

func (h *Handler) parseFormDocuments(r *http.Request) []parts.DocUpload {
	var uploads []parts.DocUpload
	if r.MultipartForm == nil || r.MultipartForm.File == nil {
		return nil
	}

	files := r.MultipartForm.File["documents"]
	for _, fh := range files {
		f, err := fh.Open()
		if err == nil {
			uploads = append(uploads, parts.DocUpload{
				File:   f,
				Header: fh,
			})
		}
	}
	return uploads
}
