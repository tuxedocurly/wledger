package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/projects"
	"github.com/tuxedocurly/wledger/web/components"
	"github.com/tuxedocurly/wledger/web/pages"
)

const maxBOMUploadBytes = 10 << 20 // 10 MiB

func (h *Handler) projectsSvc() projects.Service {
	if h.Projects == nil && h.Queries != nil {
		h.Projects = projects.NewService(h.Queries, h.WLED, h.Logger)
	}
	return h.Projects
}

func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func (h *Handler) respondProjectError(w http.ResponseWriter, r *http.Request, err error, message string, code int) {
	if h.UIError != nil {
		h.UIError.Respond(w, r, err, message, code)
		return
	}
	http.Error(w, message, code)
}

func (h *Handler) requireProjectWrite(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user := auth.GetUserFromRequest(r)
	if user.IsGuest || user.ID == 0 {
		h.respondProjectError(w, r, nil, "Unauthorized", http.StatusUnauthorized)
		return user, false
	}
	if !user.CanWrite() {
		h.respondProjectError(w, r, nil, "Forbidden", http.StatusForbidden)
		return user, false
	}
	return user, true
}

func parseRouteID(r *http.Request, param string) (int64, error) {
	raw := strings.TrimSpace(chi.URLParam(r, param))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid %s", param)
	}
	return id, nil
}

func parseOptionalInt64(raw string) (*int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	val, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || val <= 0 {
		return nil, fmt.Errorf("invalid integer ID %q", raw)
	}
	return &val, nil
}

func (h *Handler) loadPartsForSelect(ctx context.Context, user auth.User) []db.Part {
	if !user.CanWrite() || h.Queries == nil {
		return nil
	}
	allParts, err := h.Queries.GetAllParts(ctx)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("failed to fetch parts for BOM dropdown", "err", err)
		}
		return nil
	}
	return allParts
}

func toProjectDetailView(d projects.ProjectDetail) pages.ProjectDetailView {
	items := make([]pages.ProjectBOMItemView, 0, len(d.Items))
	for _, it := range d.Items {
		locs := make([]pages.ProjectBOMLocationView, 0, len(it.Locations))
		for _, loc := range it.Locations {
			locs = append(locs, pages.ProjectBOMLocationView{
				AssignmentID:   loc.AssignmentID,
				BinID:          loc.BinID,
				BinName:        loc.BinName,
				ContainerID:    loc.ContainerID,
				ContainerName:  loc.ContainerName,
				ControllerID:   loc.ControllerID,
				ControllerName: loc.ControllerName,
				Quantity:       loc.Quantity,
				IsOrphaned:     loc.IsOrphaned,
				IsStandalone:   loc.IsStandalone,
				HasLED:         loc.HasLED,
			})
		}
		items = append(items, pages.ProjectBOMItemView{
			ID:                       it.ID,
			ProjectID:                it.ProjectID,
			PartID:                   it.PartID,
			Designator:               it.Designator,
			MPN:                      it.MPN,
			Description:              it.Description,
			Notes:                    it.Notes,
			PartName:                 it.PartName,
			ResolvedPartNumber:       it.ResolvedPartNumber,
			UnitCost:                 it.UnitCost,
			QuantityPerBoard:         it.QuantityPerBoard,
			RequiredQuantity:         it.RequiredQuantity,
			AvailableStock:           it.AvailableStock,
			ShortageQuantity:         it.ShortageQuantity,
			CombinedRequiredQuantity: it.CombinedRequiredQuantity,
			CombinedShortageQuantity: it.CombinedShortageQuantity,
			HasShortage:              it.HasShortage,
			IsLinked:                 it.IsLinked,
			Locations:                locs,
			HasLocatableLED:          it.HasLocatableLED,
		})
	}

	shortages := make([]pages.ProjectShortageView, 0, len(d.Shortages))
	for _, sh := range d.Shortages {
		shortages = append(shortages, pages.ProjectShortageView{
			PartID:           sh.PartID,
			PrimaryBOMItemID: sh.PrimaryBOMItemID,
			Designators:      sh.Designators,
			DisplayName:      sh.DisplayName,
			PartNumber:       sh.PartNumber,
			QuantityPerBoard: sh.QuantityPerBoard,
			RequiredQuantity: sh.RequiredQuantity,
			AvailableStock:   sh.AvailableStock,
			ShortageQuantity: sh.ShortageQuantity,
			Unlinked:         sh.Unlinked,
		})
	}

	return pages.ProjectDetailView{
		Project:           d.Project,
		BoardCount:        d.BoardCount,
		Items:             items,
		Shortages:         shortages,
		Buildable:         d.Buildable,
		TotalCost:         d.TotalCost,
		LocatableBinCount: d.LocatableBinCount,
	}
}

// HandleProjectsList renders GET /projects.
func (h *Handler) HandleProjectsList(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	list, err := h.projectsSvc().ListProjects(r.Context())
	if err != nil {
		h.respondProjectError(w, r, err, "Failed to load projects", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ProjectsList(user, list).Render(r.Context(), w); err != nil {
		if h.Logger != nil {
			h.Logger.Error("failed to render projects list", "err", err)
		}
	}
}

// HandleProjectCreate handles POST /projects.
func (h *Handler) HandleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireProjectWrite(w, r); !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.respondProjectError(w, r, err, "Invalid form data", http.StatusBadRequest)
		return
	}

	proj, err := h.projectsSvc().CreateProject(r.Context(), projects.CreateProjectRequest{
		Name:        r.FormValue("name"),
		Description: r.FormValue("description"),
		Status:      r.FormValue("status"),
	})
	if err != nil {
		if errors.Is(err, projects.ErrInvalidInput) {
			h.respondProjectError(w, r, err, err.Error(), http.StatusBadRequest)
			return
		}
		h.respondProjectError(w, r, err, "Failed to create project", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%d", proj.ID), http.StatusSeeOther)
}

// HandleProjectDetail renders GET /projects/{id}?boards=N.
func (h *Handler) HandleProjectDetail(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	projectID, err := parseRouteID(r, "id")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid project ID", http.StatusBadRequest)
		return
	}

	boardCount := int64(1)
	if rawBoards := strings.TrimSpace(r.URL.Query().Get("boards")); rawBoards != "" {
		parsed, err := strconv.ParseInt(rawBoards, 10, 64)
		if err != nil || parsed < 1 {
			h.respondProjectError(w, r, err, "Board count must be a positive integer", http.StatusBadRequest)
			return
		}
		boardCount = parsed
	}

	var builtBoards int64
	if rawBuilt := strings.TrimSpace(r.URL.Query().Get("built")); rawBuilt != "" {
		if parsed, err := strconv.ParseInt(rawBuilt, 10, 64); err == nil && parsed > 0 {
			builtBoards = parsed
		}
	}

	detail, err := h.projectsSvc().GetProjectDetail(r.Context(), projectID, boardCount)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			h.respondProjectError(w, r, err, "Project not found", http.StatusNotFound)
			return
		}
		h.respondProjectError(w, r, err, "Failed to load project", http.StatusInternalServerError)
		return
	}

	view := toProjectDetailView(detail)
	allParts := h.loadPartsForSelect(r.Context(), user)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if isHTMXRequest(r) && r.Header.Get("HX-Target") == "project-detail-shell" {
		if err := pages.ProjectDetailContent(user, view, allParts, builtBoards).Render(r.Context(), w); err != nil {
			if h.Logger != nil {
				h.Logger.Error("failed to render project detail partial", "err", err)
			}
		}
		return
	}

	if err := pages.ProjectDetail(user, view, allParts, builtBoards).Render(r.Context(), w); err != nil {
		if h.Logger != nil {
			h.Logger.Error("failed to render project detail page", "err", err)
		}
	}
}

// HandleProjectUpdate handles POST /projects/{id}/update.
func (h *Handler) HandleProjectUpdate(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireProjectWrite(w, r); !ok {
		return
	}
	projectID, err := parseRouteID(r, "id")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid project ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.respondProjectError(w, r, err, "Invalid form data", http.StatusBadRequest)
		return
	}

	err = h.projectsSvc().UpdateProject(r.Context(), projects.UpdateProjectRequest{
		ID:          projectID,
		Name:        r.FormValue("name"),
		Description: r.FormValue("description"),
		Status:      r.FormValue("status"),
	})
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			h.respondProjectError(w, r, err, "Project not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, projects.ErrInvalidInput) {
			h.respondProjectError(w, r, err, err.Error(), http.StatusBadRequest)
			return
		}
		h.respondProjectError(w, r, err, "Failed to update project", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%d", projectID), http.StatusSeeOther)
}

// HandleProjectDelete handles POST /projects/{id}/delete.
func (h *Handler) HandleProjectDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireProjectWrite(w, r); !ok {
		return
	}
	projectID, err := parseRouteID(r, "id")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid project ID", http.StatusBadRequest)
		return
	}

	if err := h.projectsSvc().DeleteProject(r.Context(), projectID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			h.respondProjectError(w, r, err, "Project not found", http.StatusNotFound)
			return
		}
		h.respondProjectError(w, r, err, "Failed to delete project", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}

// HandleProjectBOMAdd handles POST /projects/{id}/bom.
func (h *Handler) HandleProjectBOMAdd(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireProjectWrite(w, r); !ok {
		return
	}
	projectID, err := parseRouteID(r, "id")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid project ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.respondProjectError(w, r, err, "Invalid form data", http.StatusBadRequest)
		return
	}

	qty, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("quantity_per_board")), 10, 64)
	if err != nil || qty <= 0 {
		h.respondProjectError(w, r, err, "Quantity per board must be a positive integer", http.StatusBadRequest)
		return
	}
	partID, err := parseOptionalInt64(r.FormValue("part_id"))
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid part ID", http.StatusBadRequest)
		return
	}

	_, err = h.projectsSvc().AddBOMItem(r.Context(), projects.AddBOMItemRequest{
		ProjectID:        projectID,
		PartID:           partID,
		Designator:       r.FormValue("designator"),
		MPN:              r.FormValue("mpn"),
		Description:      r.FormValue("description"),
		QuantityPerBoard: qty,
		Notes:            r.FormValue("notes"),
	})
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			h.respondProjectError(w, r, err, "Project not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, projects.ErrInvalidInput) {
			h.respondProjectError(w, r, err, err.Error(), http.StatusBadRequest)
			return
		}
		h.respondProjectError(w, r, err, "Failed to add BOM item", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%d", projectID), http.StatusSeeOther)
}

// HandleProjectBOMImport handles POST /projects/{id}/bom/import (multipart file upload or pasted CSV).
func (h *Handler) HandleProjectBOMImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireProjectWrite(w, r); !ok {
		return
	}
	projectID, err := parseRouteID(r, "id")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid project ID", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBOMUploadBytes)

	var csvReader io.Reader
	var uploaded io.Closer
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxBOMUploadBytes); err != nil {
			h.respondProjectError(w, r, err, "Failed to parse uploaded BOM file", http.StatusBadRequest)
			return
		}
		file, header, fileErr := r.FormFile("file")
		if fileErr == nil && file != nil {
			if header == nil || header.Filename != "" || header.Size > 0 {
				csvReader = file
				uploaded = file
			} else {
				_ = file.Close()
			}
		}
		if csvReader == nil {
			if pasted := strings.TrimSpace(r.FormValue("csv_data")); pasted != "" {
				csvReader = strings.NewReader(pasted)
			}
		}
	} else {
		if err := r.ParseForm(); err != nil {
			h.respondProjectError(w, r, err, "Invalid form data", http.StatusBadRequest)
			return
		}
		if pasted := strings.TrimSpace(r.FormValue("csv_data")); pasted != "" {
			csvReader = strings.NewReader(pasted)
		} else if strings.Contains(contentType, "text/csv") || strings.Contains(contentType, "text/plain") {
			raw, readErr := io.ReadAll(r.Body)
			if readErr == nil && len(bytes.TrimSpace(raw)) > 0 {
				csvReader = bytes.NewReader(raw)
			}
		}
	}
	if uploaded != nil {
		defer func() { _ = uploaded.Close() }()
	}

	if csvReader == nil {
		msg := "Please upload a BOM CSV file or paste CSV contents"
		if isHTMXRequest(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = components.ProjectBOMImportResult(projectID, 0, 0, 0, msg).Render(r.Context(), w)
			return
		}
		h.respondProjectError(w, r, nil, msg, http.StatusBadRequest)
		return
	}

	res, err := h.projectsSvc().ImportCSV(r.Context(), projectID, csvReader)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, projects.ErrNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, projects.ErrInvalidInput) {
			status = http.StatusBadRequest
		}
		if isHTMXRequest(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(status)
			_ = components.ProjectBOMImportResult(projectID, 0, 0, 0, err.Error()).Render(r.Context(), w)
			return
		}
		h.respondProjectError(w, r, err, err.Error(), status)
		return
	}

	if isHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.ProjectBOMImportResult(projectID, res.ImportedCount, res.MatchedCount, res.UnmatchedCount, "").Render(r.Context(), w)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%d", projectID), http.StatusSeeOther)
}

// HandleProjectBOMLink handles POST /projects/{id}/bom/{itemID}/link.
func (h *Handler) HandleProjectBOMLink(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireProjectWrite(w, r); !ok {
		return
	}
	projectID, err := parseRouteID(r, "id")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid project ID", http.StatusBadRequest)
		return
	}
	itemID, err := parseRouteID(r, "itemID")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid BOM item ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.respondProjectError(w, r, err, "Invalid form data", http.StatusBadRequest)
		return
	}

	partID, err := parseOptionalInt64(r.FormValue("part_id"))
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid part ID", http.StatusBadRequest)
		return
	}

	if err := h.projectsSvc().LinkBOMItemPart(r.Context(), projectID, itemID, partID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			h.respondProjectError(w, r, err, "BOM item not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, projects.ErrInvalidInput) {
			h.respondProjectError(w, r, err, err.Error(), http.StatusBadRequest)
			return
		}
		h.respondProjectError(w, r, err, "Failed to link BOM item", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%d", projectID), http.StatusSeeOther)
}

// HandleProjectBOMDelete handles POST /projects/{id}/bom/{itemID}/delete.
func (h *Handler) HandleProjectBOMDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireProjectWrite(w, r); !ok {
		return
	}
	projectID, err := parseRouteID(r, "id")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid project ID", http.StatusBadRequest)
		return
	}
	itemID, err := parseRouteID(r, "itemID")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid BOM item ID", http.StatusBadRequest)
		return
	}

	if err := h.projectsSvc().DeleteBOMItem(r.Context(), projectID, itemID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			h.respondProjectError(w, r, err, "BOM item not found", http.StatusNotFound)
			return
		}
		h.respondProjectError(w, r, err, "Failed to delete BOM item", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%d", projectID), http.StatusSeeOther)
}

// HandleProjectLocate handles POST /projects/{id}/locate ("Pick Mode").
func (h *Handler) HandleProjectLocate(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseRouteID(r, "id")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid project ID", http.StatusBadRequest)
		return
	}

	if err := h.projectsSvc().LocateBOM(r.Context(), projectID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			h.respondProjectError(w, r, err, "Project not found", http.StatusNotFound)
			return
		}
		h.respondProjectError(w, r, err, "Failed to locate BOM parts: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if isHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.Toast(components.ToastProps{
			Message: "Pick Mode active: illuminated all positive-stock BOM bins.",
			Type:    components.ToastSuccess,
		}).Render(r.Context(), w)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%d", projectID), http.StatusSeeOther)
}

// HandleProjectBOMItemLocate handles POST /projects/{id}/bom/{itemID}/locate.
func (h *Handler) HandleProjectBOMItemLocate(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseRouteID(r, "id")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid project ID", http.StatusBadRequest)
		return
	}
	itemID, err := parseRouteID(r, "itemID")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid BOM item ID", http.StatusBadRequest)
		return
	}

	if err := h.projectsSvc().LocateBOMItemForProject(r.Context(), projectID, itemID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			h.respondProjectError(w, r, err, "BOM item not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, projects.ErrBOMItemUnlinked) {
			h.respondProjectError(w, r, err, err.Error(), http.StatusBadRequest)
			return
		}
		h.respondProjectError(w, r, err, "Failed to locate BOM item: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if isHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.Toast(components.ToastProps{
			Message: "Part bin(s) illuminated.",
			Type:    components.ToastSuccess,
		}).Render(r.Context(), w)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%d", projectID), http.StatusSeeOther)
}

// HandleProjectBuild handles POST /projects/{id}/build.
func (h *Handler) HandleProjectBuild(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireProjectWrite(w, r)
	if !ok {
		return
	}
	projectID, err := parseRouteID(r, "id")
	if err != nil {
		h.respondProjectError(w, r, err, "Invalid project ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.respondProjectError(w, r, err, "Invalid form data", http.StatusBadRequest)
		return
	}

	boardCount := int64(1)
	rawBoards := strings.TrimSpace(r.FormValue("board_count"))
	if rawBoards == "" {
		rawBoards = strings.TrimSpace(r.FormValue("boards"))
	}
	if rawBoards != "" {
		parsed, err := strconv.ParseInt(rawBoards, 10, 64)
		if err != nil || parsed < 1 {
			h.respondProjectError(w, r, err, "Board count must be a positive integer", http.StatusBadRequest)
			return
		}
		boardCount = parsed
	}

	if err := h.projectsSvc().BuildProject(r.Context(), projectID, boardCount); err != nil {
		var shortErr *projects.ShortageError
		switch {
		case errors.Is(err, projects.ErrNotFound):
			h.respondProjectError(w, r, err, "Project not found", http.StatusNotFound)
		case errors.As(err, &shortErr):
			h.respondProjectError(w, r, err, err.Error(), http.StatusConflict)
		case errors.Is(err, projects.ErrInvalidBoardCount),
			errors.Is(err, projects.ErrEmptyBOM),
			errors.Is(err, projects.ErrInvalidInput):
			h.respondProjectError(w, r, err, err.Error(), http.StatusBadRequest)
		default:
			h.respondProjectError(w, r, err, "Failed to build project: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if isHTMXRequest(r) {
		detail, err := h.projectsSvc().GetProjectDetail(r.Context(), projectID, boardCount)
		if err != nil {
			h.respondProjectError(w, r, err, "Build completed, but failed to refresh view", http.StatusInternalServerError)
			return
		}
		allParts := h.loadPartsForSelect(r.Context(), user)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.ProjectDetailContent(user, toProjectDetailView(detail), allParts, boardCount).Render(r.Context(), w)
		_ = components.Toast(components.ToastProps{
			Message: fmt.Sprintf("Built %d board(s) and deducted inventory stock.", boardCount),
			Type:    components.ToastSuccess,
		}).Render(r.Context(), w)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%d?boards=%d&built=%d", projectID, boardCount, boardCount), http.StatusSeeOther)
}
