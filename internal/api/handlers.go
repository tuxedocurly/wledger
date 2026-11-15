package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/stock"
	"github.com/tuxedocurly/wledger/web/pages"
)

const (
	defaultPartsLimit = 50
	maxPartsLimit     = 500
	unboundedLimit    = 100000
)

type apiHandler struct {
	deps Dependencies
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}

func decodeJSON(r *http.Request, dst any, allowEmpty bool) error {
	if r.Body == nil {
		if allowEmpty {
			return nil
		}
		return errors.New("request body is required")
	}

	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) && allowEmpty {
			return nil
		}
		return errors.New("invalid JSON request body")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("unexpected trailing data in JSON request body")
	}
	return nil
}

func parsePositiveID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("id must be a positive integer")
	}
	return id, nil
}

func parseLimitOffset(r *http.Request) (int, int, error) {
	limit := defaultPartsLimit
	offset := 0

	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return 0, 0, errors.New("limit must be a positive integer")
		}
		if v > maxPartsLimit {
			v = maxPartsLimit
		}
		limit = v
	}

	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
		offset = v
	}

	return limit, offset, nil
}

func parseOptionalPositiveQueryID(r *http.Request, key string) (*int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return nil, errors.New(key + " must be a positive integer")
	}
	return &v, nil
}

func (h *apiHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	version := strings.TrimSpace(h.deps.Version)
	if version == "" {
		version = "v2"
	}
	writeJSON(w, http.StatusOK, HealthResponse{
		Status:  "ok",
		Version: version,
	})
}

func (h *apiHandler) handleListParts(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseLimitOffset(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	binID, err := parseOptionalPositiveQueryID(r, "bin_id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	controllerID, err := parseOptionalPositiveQueryID(r, "controller_id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))

	if h.deps.Store != nil {
		items, err := h.listPartsFromStore(r.Context(), q, tag, binID, controllerID, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list parts")
			return
		}
		writeJSON(w, http.StatusOK, PartListResponse{
			Parts:  items,
			Limit:  limit,
			Offset: offset,
			Count:  len(items),
		})
		return
	}

	if h.deps.Parts == nil {
		writeError(w, http.StatusNotImplemented, "parts service not configured")
		return
	}

	page := 1
	if limit > 0 {
		page = (offset / limit) + 1
	}
	views, err := h.deps.Parts.ListPartsFiltered(r.Context(), parts.ListPartsFilter{
		Search:       q,
		Page:         page,
		BinID:        binID,
		ControllerID: controllerID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list parts")
		return
	}

	items := make([]PartResponse, 0, len(views))
	for _, v := range views {
		items = append(items, partViewToResponse(v))
	}
	writeJSON(w, http.StatusOK, PartListResponse{
		Parts:  items,
		Limit:  limit,
		Offset: offset,
		Count:  len(items),
	})
}

func (h *apiHandler) listPartsFromStore(
	ctx context.Context,
	q, tag string,
	binID, controllerID *int64,
	limit, offset int,
) ([]PartResponse, error) {
	var binArg any
	if binID != nil {
		binArg = *binID
	}
	var ctrlArg any
	if controllerID != nil {
		ctrlArg = *controllerID
	}

	sqlLimit := int64(limit)
	sqlOffset := int64(offset)
	if tag != "" {
		// Tag filtering spans the full matched result set before applying offset/limit.
		sqlLimit = unboundedLimit
		sqlOffset = 0
	}

	var items []PartResponse
	if q != "" {
		sanitized := parts.SanitizeFTSQuery(q)
		if sanitized == "" {
			return []PartResponse{}, nil
		}
		rows, err := h.deps.Store.SearchParts(ctx, db.SearchPartsParams{
			Query:        sql.NullString{String: sanitized, Valid: true},
			BinID:        binArg,
			ControllerID: ctrlArg,
			Offset:       sqlOffset,
			Limit:        sqlLimit,
		})
		if err != nil {
			return nil, err
		}
		items = make([]PartResponse, 0, len(rows))
		for _, row := range rows {
			item := searchPartRowToResponse(row)
			if tag == "" || hasTag(item.Tags, tag) {
				items = append(items, item)
			}
		}
	} else {
		rows, err := h.deps.Store.ListParts(ctx, db.ListPartsParams{
			BinID:        binArg,
			ControllerID: ctrlArg,
			Offset:       sqlOffset,
			Limit:        sqlLimit,
		})
		if err != nil {
			return nil, err
		}
		items = make([]PartResponse, 0, len(rows))
		for _, row := range rows {
			item := listPartRowToResponse(row)
			if tag == "" || hasTag(item.Tags, tag) {
				items = append(items, item)
			}
		}
	}

	if tag == "" {
		return items, nil
	}
	if offset >= len(items) {
		return []PartResponse{}, nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end], nil
}

func (h *apiHandler) handleLowStock(w http.ResponseWriter, r *http.Request) {
	if h.deps.Store == nil {
		writeError(w, http.StatusNotImplemented, "database store not configured")
		return
	}

	limit, offset, err := parseLimitOffset(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := h.deps.Store.ListParts(r.Context(), db.ListPartsParams{
		Offset: 0,
		Limit:  unboundedLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load low-stock parts")
		return
	}

	low := make([]PartResponse, 0)
	for _, row := range rows {
		if isLowStock(row.TotalStock, row.ReorderLevel.Int64, row.MinStockThreshold.Int64) {
			low = append(low, listPartRowToResponse(row))
		}
	}

	if offset >= len(low) {
		writeJSON(w, http.StatusOK, PartListResponse{
			Parts:  []PartResponse{},
			Limit:  limit,
			Offset: offset,
			Count:  0,
		})
		return
	}
	end := offset + limit
	if end > len(low) {
		end = len(low)
	}
	page := low[offset:end]
	writeJSON(w, http.StatusOK, PartListResponse{
		Parts:  page,
		Limit:  limit,
		Offset: offset,
		Count:  len(page),
	})
}

func (h *apiHandler) handleCreatePart(w http.ResponseWriter, r *http.Request) {
	if h.deps.Parts == nil {
		writeError(w, http.StatusNotImplemented, "parts service not configured")
		return
	}

	var payload CreatePartPayload
	if err := decodeJSON(r, &payload, false); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(payload.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "part name is required")
		return
	}
	if payload.UnitCost < 0 || payload.ReorderLevel < 0 || payload.MinStockThreshold < 0 {
		writeError(w, http.StatusBadRequest, "unit_cost, reorder_level, and min_stock_threshold must be non-negative")
		return
	}

	_, links, linkErr := parseLinkPayloads(payload.Links, nil, false)
	if linkErr != nil {
		writeError(w, http.StatusBadRequest, linkErr.Error())
		return
	}

	id, err := h.deps.Parts.CreatePart(r.Context(), parts.CreatePartRequest{
		Name:              name,
		Description:       payload.Description,
		PartNumber:        strings.TrimSpace(payload.PartNumber),
		Manufacturer:      strings.TrimSpace(payload.Manufacturer),
		Supplier:          strings.TrimSpace(payload.Supplier),
		BarcodeData:       strings.TrimSpace(payload.BarcodeData),
		UnitCost:          payload.UnitCost,
		ReorderLevel:      payload.ReorderLevel,
		MinStockThreshold: payload.MinStockThreshold,
		Links:             links,
		Tags:              normalizeTagSlice(payload.Tags),
	})
	if err != nil {
		h.writePartServiceError(w, err, "failed to create part")
		return
	}

	detail, err := h.loadPartDetail(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusCreated, PartDetailResponse{
			PartResponse: PartResponse{
				ID:                id,
				Name:              name,
				Description:       payload.Description,
				PartNumber:        strings.TrimSpace(payload.PartNumber),
				Manufacturer:      strings.TrimSpace(payload.Manufacturer),
				Supplier:          strings.TrimSpace(payload.Supplier),
				BarcodeData:       strings.TrimSpace(payload.BarcodeData),
				UnitCost:          payload.UnitCost,
				ReorderLevel:      int64(payload.ReorderLevel),
				MinStockThreshold: int64(payload.MinStockThreshold),
				Tags:              normalizeTagSlice(payload.Tags),
				StockStatus:       calcStockStatus(0, int64(payload.ReorderLevel), int64(payload.MinStockThreshold)),
			},
			Assignments: []PartAssignmentResponse{},
			Links:       []PartLinkResponse{},
			Docs:        []PartDocResponse{},
		})
		return
	}
	writeJSON(w, http.StatusCreated, detail)
}

func (h *apiHandler) handleGetPart(w http.ResponseWriter, r *http.Request) {
	id, err := parsePositiveID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	detail, err := h.loadPartDetail(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "part not found")
			return
		}
		if errors.Is(err, ErrNotImplemented) {
			writeError(w, http.StatusNotImplemented, "parts service not configured")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load part")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *apiHandler) handleUpdatePart(w http.ResponseWriter, r *http.Request) {
	if h.deps.Parts == nil {
		writeError(w, http.StatusNotImplemented, "parts service not configured")
		return
	}

	id, err := parsePositiveID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var payload UpdatePartPayload
	if err := decodeJSON(r, &payload, false); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	existing, err := h.deps.Parts.GetPartDetail(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "part not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load part")
		return
	}

	req := parts.UpdatePartRequest{
		ID:                id,
		Name:              existing.Part.Name,
		Description:       existing.Part.Description.String,
		PartNumber:        existing.Part.PartNumber.String,
		Manufacturer:      existing.Part.Manufacturer.String,
		Supplier:          existing.Part.Supplier.String,
		BarcodeData:       existing.Part.BarcodeData.String,
		UnitCost:          existing.Part.UnitCost.Float64,
		ReorderLevel:      int(existing.Part.ReorderLevel.Int64),
		MinStockThreshold: int(existing.Part.MinStockThreshold.Int64),
	}

	if payload.Name != nil {
		name := strings.TrimSpace(*payload.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "part name cannot be empty")
			return
		}
		req.Name = name
	}
	if payload.Description != nil {
		req.Description = *payload.Description
	}
	if payload.PartNumber != nil {
		req.PartNumber = strings.TrimSpace(*payload.PartNumber)
	}
	if payload.Manufacturer != nil {
		req.Manufacturer = strings.TrimSpace(*payload.Manufacturer)
	}
	if payload.Supplier != nil {
		req.Supplier = strings.TrimSpace(*payload.Supplier)
	}
	if payload.BarcodeData != nil {
		req.BarcodeData = strings.TrimSpace(*payload.BarcodeData)
	}
	if payload.UnitCost != nil {
		if *payload.UnitCost < 0 {
			writeError(w, http.StatusBadRequest, "unit_cost must be non-negative")
			return
		}
		req.UnitCost = *payload.UnitCost
	}
	if payload.ReorderLevel != nil {
		if *payload.ReorderLevel < 0 {
			writeError(w, http.StatusBadRequest, "reorder_level must be non-negative")
			return
		}
		req.ReorderLevel = *payload.ReorderLevel
	}
	if payload.MinStockThreshold != nil {
		if *payload.MinStockThreshold < 0 {
			writeError(w, http.StatusBadRequest, "min_stock_threshold must be non-negative")
			return
		}
		req.MinStockThreshold = *payload.MinStockThreshold
	}

	if payload.Tags != nil {
		req.Tags = normalizeTagSlice(*payload.Tags)
	} else {
		req.Tags = h.loadPartTags(r.Context(), id, existing.Part.Tags.String)
	}

	if payload.Links != nil {
		ownedLinkIDs := make(map[int64]bool, len(existing.Links))
		for _, existingLink := range existing.Links {
			ownedLinkIDs[existingLink.ID] = true
		}
		existingLinks, newLinks, linkErr := parseLinkPayloads(*payload.Links, ownedLinkIDs, true)
		if linkErr != nil {
			writeError(w, http.StatusBadRequest, linkErr.Error())
			return
		}
		req.ExistingLinks = existingLinks
		req.NewLinks = newLinks
	} else {
		for _, l := range existing.Links {
			req.ExistingLinks = append(req.ExistingLinks, parts.LinkDTO{
				ID:    l.ID,
				Label: l.Label.String,
				URL:   l.Url,
			})
		}
	}

	if err := h.deps.Parts.UpdatePart(r.Context(), req); err != nil {
		h.writePartServiceError(w, err, "failed to update part")
		return
	}

	updated, err := h.loadPartDetail(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load updated part")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *apiHandler) handleDeletePart(w http.ResponseWriter, r *http.Request) {
	if h.deps.Parts == nil {
		writeError(w, http.StatusNotImplemented, "parts service not configured")
		return
	}

	id, err := parsePositiveID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.deps.Parts.DeletePart(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "part not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete part")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": true,
		"id":      id,
	})
}

func (h *apiHandler) handleLocatePart(w http.ResponseWriter, r *http.Request) {
	id, err := parsePositiveID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.ensurePartExists(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "part not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to verify part")
		return
	}

	if h.deps.WLED == nil {
		writeError(w, http.StatusNotImplemented, "wled controller service not configured")
		return
	}

	if err := h.deps.WLED.LocatePart(r.Context(), id); err != nil {
		writeMappedError(w, err, "failed to locate part")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "locating",
		"part_id": id,
	})
}

func (h *apiHandler) handleAssignStock(w http.ResponseWriter, r *http.Request) {
	if h.deps.Stock == nil {
		writeError(w, http.StatusNotImplemented, "stock service not configured")
		return
	}

	var payload StockAssignPayload
	if err := decodeJSON(r, &payload, false); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if payload.PartID <= 0 || payload.BinID <= 0 {
		writeError(w, http.StatusBadRequest, "part_id and bin_id must be positive integers")
		return
	}
	if payload.Quantity <= 0 {
		writeError(w, http.StatusBadRequest, "quantity must be greater than zero")
		return
	}

	if h.deps.Store != nil {
		if _, err := h.deps.Store.GetPart(r.Context(), payload.PartID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "part not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to verify part")
			return
		}
		if _, err := h.deps.Store.GetBin(r.Context(), payload.BinID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "bin not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to verify bin")
			return
		}
	}

	err := h.deps.Stock.AssignStock(r.Context(), stock.AssignStockRequest{
		PartID:   payload.PartID,
		BinID:    payload.BinID,
		Quantity: payload.Quantity,
	})
	if err != nil {
		writeMappedError(w, err, "failed to assign stock")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "assigned",
		"part_id":  payload.PartID,
		"bin_id":   payload.BinID,
		"quantity": payload.Quantity,
	})
}

func (h *apiHandler) handleAdjustStock(w http.ResponseWriter, r *http.Request) {
	if h.deps.Stock == nil {
		writeError(w, http.StatusNotImplemented, "stock service not configured")
		return
	}

	assignmentID, err := parsePositiveID(chi.URLParam(r, "assignmentID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var payload StockAdjustPayload
	if err := decodeJSON(r, &payload, false); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if payload.Delta == 0 {
		writeError(w, http.StatusBadRequest, "delta must be non-zero")
		return
	}

	if err := h.deps.Stock.AdjustStock(r.Context(), assignmentID, payload.Delta); err != nil {
		writeMappedError(w, err, "failed to adjust stock")
		return
	}

	resp := map[string]any{
		"status":        "adjusted",
		"assignment_id": assignmentID,
		"delta":         payload.Delta,
	}
	if h.deps.Store != nil {
		if updated, err := h.deps.Store.GetAssignment(r.Context(), assignmentID); err == nil {
			resp["quantity"] = updated.Quantity
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *apiHandler) handleParseBarcode(w http.ResponseWriter, r *http.Request) {
	var payload BarcodeParsePayload
	if err := decodeJSON(r, &payload, false); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	raw := strings.TrimSpace(payload.Barcode)
	if raw == "" {
		writeError(w, http.StatusBadRequest, "barcode is required")
		return
	}

	if h.deps.Barcodes == nil {
		writeError(w, http.StatusNotImplemented, "barcode parser not configured")
		return
	}

	parsed, err := h.deps.Barcodes.ParseBarcode(r.Context(), payload.Barcode)
	if err != nil {
		writeMappedError(w, err, "failed to parse barcode")
		return
	}
	if parsed.RawBarcode == "" {
		parsed.RawBarcode = raw
	}
	if parsed.ExistingPartID == nil && h.deps.Store != nil {
		h.enrichExistingPartMatch(r.Context(), &parsed)
	}

	writeJSON(w, http.StatusOK, parsed)
}

func (h *apiHandler) handleListContainers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.loadContainers(r.Context())
	if err != nil {
		if errors.Is(err, ErrNotImplemented) {
			writeError(w, http.StatusNotImplemented, "hardware service not configured")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to list containers")
		return
	}

	out := make([]ContainerResponse, 0, len(rows))
	for _, c := range rows {
		item := ContainerResponse{
			ID:            c.ID,
			Name:          c.Name,
			SegmentID:     c.SegmentID,
			PositionIndex: c.PositionIndex,
			Bins:          []BinResponse{},
		}
		if c.ControllerID.Valid {
			v := c.ControllerID.Int64
			item.ControllerID = &v
		}
		if c.ControllerName.Valid {
			item.ControllerName = c.ControllerName.String
		}
		if c.ControllerIp.Valid {
			item.ControllerIP = c.ControllerIp.String
		}
		if c.ControllerIsOnline.Valid {
			v := c.ControllerIsOnline.Bool
			item.ControllerIsOnline = &v
		}

		bins, err := h.loadContainerBins(r.Context(), c.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load container bins")
			return
		}
		for _, b := range bins {
			item.Bins = append(item.Bins, binToResponse(b))
		}
		out = append(out, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"containers": out,
		"count":      len(out),
	})
}

func (h *apiHandler) handleListControllers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.loadControllers(r.Context())
	if err != nil {
		if errors.Is(err, ErrNotImplemented) {
			writeError(w, http.StatusNotImplemented, "hardware service not configured")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to list controllers")
		return
	}

	out := make([]ControllerResponse, 0, len(rows))
	for _, ctrl := range rows {
		out = append(out, controllerToResponse(ctrl))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"controllers": out,
		"count":       len(out),
	})
}

func (h *apiHandler) handleLocateController(w http.ResponseWriter, r *http.Request) {
	controllerID, err := parsePositiveID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.deps.WLED == nil {
		writeError(w, http.StatusNotImplemented, "wled controller service not configured")
		return
	}

	var payload LocateControllerPayload
	if err := decodeJSON(r, &payload, true); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var binID int64
	if payload.BinID != nil {
		if *payload.BinID <= 0 {
			writeError(w, http.StatusBadRequest, "bin_id must be a positive integer")
			return
		}
		binID = *payload.BinID
	} else if rawQueryBin := strings.TrimSpace(r.URL.Query().Get("bin_id")); rawQueryBin != "" {
		v, err := parsePositiveID(rawQueryBin)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		binID = v
	}

	if err := h.ensureControllerExists(r.Context(), controllerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "controller not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to verify controller")
		return
	}

	if binID == 0 && (h.deps.Hardware != nil || h.deps.Store != nil) {
		resolved, resolveErr := h.resolveFirstControllerBin(r.Context(), controllerID)
		if resolveErr != nil {
			writeMappedError(w, resolveErr, "controller has no mapped bins to locate")
			return
		}
		binID = resolved
	} else if binID > 0 && h.deps.Store != nil {
		if _, err := h.deps.Store.GetBin(r.Context(), binID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "bin not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to verify bin")
			return
		}
	}

	if err := h.deps.WLED.LocateBin(r.Context(), controllerID, binID); err != nil {
		writeMappedError(w, err, "failed to locate controller")
		return
	}

	resp := map[string]any{
		"status":        "locating",
		"controller_id": controllerID,
	}
	if binID > 0 {
		resp["bin_id"] = binID
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *apiHandler) handleStopLocate(w http.ResponseWriter, r *http.Request) {
	if h.deps.WLED == nil {
		writeError(w, http.StatusNotImplemented, "wled controller service not configured")
		return
	}

	if err := h.deps.WLED.GlobalOff(r.Context()); err != nil {
		writeMappedError(w, err, "failed to stop locate")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "stopped",
	})
}

func (h *apiHandler) handleListProjects(w http.ResponseWriter, r *http.Request) {
	if h.deps.Projects != nil {
		projects, err := h.deps.Projects.ListProjects(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list projects")
			return
		}
		if projects == nil {
			projects = []ProjectSummary{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"projects": projects,
			"count":    len(projects),
		})
		return
	}

	if h.deps.Store == nil {
		writeError(w, http.StatusNotImplemented, "project service not configured")
		return
	}

	rows, err := h.deps.Store.ListProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list projects")
		return
	}

	out := make([]ProjectSummary, 0, len(rows))
	for _, row := range rows {
		summary := ProjectSummary{
			ID:           row.ID,
			Name:         row.Name,
			Description:  row.Description.String,
			Status:       row.Status,
			BOMItemCount: row.BomItemCount,
		}
		if row.CreatedAt.Valid {
			t := row.CreatedAt.Time.UTC()
			summary.CreatedAt = &t
		}
		if row.UpdatedAt.Valid {
			t := row.UpdatedAt.Time.UTC()
			summary.UpdatedAt = &t
		}
		out = append(out, summary)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"projects": out,
		"count":    len(out),
	})
}

func (h *apiHandler) handleLocateProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := parsePositiveID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.deps.Projects != nil {
		if err := h.deps.Projects.LocateProject(r.Context(), projectID); err != nil {
			writeMappedError(w, err, "failed to locate project parts")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "locating",
			"project_id": projectID,
		})
		return
	}

	if h.deps.Store == nil || h.deps.WLED == nil {
		writeError(w, http.StatusNotImplemented, "project locate service not configured")
		return
	}

	if _, err := h.deps.Store.GetProject(r.Context(), projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load project")
		return
	}

	items, err := h.deps.Store.ListProjectBOMItems(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project BOM")
		return
	}

	seenParts := make(map[int64]bool)
	located := 0
	for _, item := range items {
		if !item.PartID.Valid || seenParts[item.PartID.Int64] {
			continue
		}
		seenParts[item.PartID.Int64] = true
		if err := h.deps.WLED.LocatePart(r.Context(), item.PartID.Int64); err != nil {
			writeMappedError(w, err, "failed to locate BOM part")
			return
		}
		located++
	}
	if located == 0 {
		writeError(w, http.StatusBadRequest, "project has no linked inventory parts to locate")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "locating",
		"project_id":    projectID,
		"located_parts": located,
	})
}

func (h *apiHandler) handleBuildProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := parsePositiveID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var payload ProjectBuildPayload
	if err := decodeJSON(r, &payload, true); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	boardCount := 1
	if payload.BoardCount != nil {
		if *payload.BoardCount <= 0 {
			writeError(w, http.StatusBadRequest, "board_count must be a positive integer")
			return
		}
		boardCount = *payload.BoardCount
	}

	if h.deps.Projects == nil {
		writeError(w, http.StatusNotImplemented, "project build service not configured")
		return
	}

	result, err := h.deps.Projects.BuildProject(r.Context(), projectID, boardCount)
	if err != nil {
		if errors.Is(err, ErrProjectShortage) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeMappedError(w, err, "failed to build project")
		return
	}
	if result.ProjectID == 0 {
		result.ProjectID = projectID
	}
	if result.BoardCount == 0 {
		result.BoardCount = boardCount
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *apiHandler) loadPartDetail(ctx context.Context, id int64) (PartDetailResponse, error) {
	if h.deps.Parts != nil {
		detail, err := h.deps.Parts.GetPartDetail(ctx, id)
		if err != nil {
			return PartDetailResponse{}, err
		}
		tags := h.loadPartTags(ctx, id, detail.Part.Tags.String)
		return partDetailToResponse(detail, tags), nil
	}

	if h.deps.Store == nil {
		return PartDetailResponse{}, ErrNotImplemented
	}

	p, err := h.deps.Store.GetPart(ctx, id)
	if err != nil {
		return PartDetailResponse{}, err
	}
	assignments, err := h.deps.Store.GetPartAssignments(ctx, id)
	if err != nil {
		return PartDetailResponse{}, err
	}
	links, err := h.deps.Store.GetPartLinks(ctx, id)
	if err != nil {
		return PartDetailResponse{}, err
	}
	docs, err := h.deps.Store.GetPartDocs(ctx, id)
	if err != nil {
		return PartDetailResponse{}, err
	}
	tags := h.loadPartTags(ctx, id, p.Tags.String)

	return partDetailToResponse(parts.PartDetail{
		Part:  p,
		Stock: assignments,
		Links: links,
		Docs:  docs,
	}, tags), nil
}

func (h *apiHandler) loadPartTags(ctx context.Context, partID int64, fallbackCSV string) []string {
	if h.deps.Store != nil {
		if rows, err := h.deps.Store.GetTagsForPart(ctx, partID); err == nil {
			tags := make([]string, 0, len(rows))
			for _, t := range rows {
				tags = append(tags, t.Name)
			}
			return tags
		}
	}
	return splitTagCSV(fallbackCSV)
}

func (h *apiHandler) ensurePartExists(ctx context.Context, id int64) error {
	if h.deps.Parts != nil {
		_, err := h.deps.Parts.GetPart(ctx, id)
		return err
	}
	if h.deps.Store != nil {
		_, err := h.deps.Store.GetPart(ctx, id)
		return err
	}
	return nil
}

func (h *apiHandler) ensureControllerExists(ctx context.Context, id int64) error {
	if h.deps.Hardware != nil {
		_, err := h.deps.Hardware.GetController(ctx, id)
		return err
	}
	if h.deps.Store != nil {
		_, err := h.deps.Store.GetController(ctx, id)
		return err
	}
	return nil
}

func (h *apiHandler) resolveFirstControllerBin(ctx context.Context, controllerID int64) (int64, error) {
	var bins []db.Bin
	var err error
	if h.deps.Hardware != nil {
		bins, err = h.deps.Hardware.GetBinsByController(ctx, controllerID)
	} else if h.deps.Store != nil {
		containers, cErr := h.deps.Store.GetContainersByController(ctx, sql.NullInt64{Int64: controllerID, Valid: true})
		if cErr != nil {
			return 0, cErr
		}
		for _, cont := range containers {
			cBins, bErr := h.deps.Store.GetBinsByContainer(ctx, cont.ID)
			if bErr != nil {
				return 0, bErr
			}
			bins = append(bins, cBins...)
		}
	}
	if err != nil {
		return 0, err
	}
	if len(bins) == 0 {
		return 0, errors.New("controller has no configured bins to locate")
	}
	for _, b := range bins {
		if b.LedIndex.Valid {
			return b.ID, nil
		}
	}
	return bins[0].ID, nil
}

func (h *apiHandler) loadContainers(ctx context.Context) ([]db.GetAllContainersWithControllerRow, error) {
	if h.deps.Hardware != nil {
		return h.deps.Hardware.ListAllContainersWithController(ctx)
	}
	if h.deps.Store != nil {
		return h.deps.Store.GetAllContainersWithController(ctx)
	}
	return nil, ErrNotImplemented
}

func (h *apiHandler) loadContainerBins(ctx context.Context, containerID int64) ([]db.Bin, error) {
	if h.deps.Hardware != nil {
		return h.deps.Hardware.GetBinsByContainer(ctx, containerID)
	}
	if h.deps.Store != nil {
		return h.deps.Store.GetBinsByContainer(ctx, containerID)
	}
	return nil, nil
}

func (h *apiHandler) loadControllers(ctx context.Context) ([]db.Controller, error) {
	if h.deps.Hardware != nil {
		return h.deps.Hardware.ListControllers(ctx)
	}
	if h.deps.Store != nil {
		return h.deps.Store.GetControllers(ctx)
	}
	return nil, ErrNotImplemented
}

func (h *apiHandler) enrichExistingPartMatch(ctx context.Context, res *ParsedBarcodeResult) {
	allParts, err := h.deps.Store.GetAllParts(ctx)
	if err != nil {
		return
	}

	raw := strings.TrimSpace(res.RawBarcode)
	mpn := strings.TrimSpace(res.PartNumber)
	spn := strings.TrimSpace(res.SupplierPartNumber)

	for _, p := range allParts {
		if raw != "" && p.BarcodeData.Valid && strings.EqualFold(strings.TrimSpace(p.BarcodeData.String), raw) {
			id := p.ID
			res.ExistingPartID = &id
			res.ExistingPartName = p.Name
			return
		}
	}
	for _, p := range allParts {
		if !p.PartNumber.Valid {
			continue
		}
		existingPN := strings.TrimSpace(p.PartNumber.String)
		if existingPN == "" {
			continue
		}
		if (mpn != "" && strings.EqualFold(existingPN, mpn)) || (spn != "" && strings.EqualFold(existingPN, spn)) {
			id := p.ID
			res.ExistingPartID = &id
			res.ExistingPartName = p.Name
			return
		}
	}
}

func (h *apiHandler) writePartServiceError(w http.ResponseWriter, err error, fallbackMsg string) {
	lower := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, sql.ErrNoRows) || strings.Contains(lower, "not found"):
		writeError(w, http.StatusNotFound, err.Error())
	case strings.Contains(lower, "unique constraint"):
		writeError(w, http.StatusConflict, "part with duplicate barcode already exists")
	case strings.Contains(lower, "required") || strings.Contains(lower, "invalid"):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, fallbackMsg)
	}
}

func writeMappedError(w http.ResponseWriter, err error, fallbackMsg string) {
	lower := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, sql.ErrNoRows) || strings.Contains(lower, "not found"):
		writeError(w, http.StatusNotFound, err.Error())
	case strings.Contains(lower, "shortage") ||
		strings.Contains(lower, "insufficient") ||
		strings.Contains(lower, "negative") ||
		strings.Contains(lower, "unique constraint"):
		writeError(w, http.StatusConflict, err.Error())
	case strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "required") ||
		strings.Contains(lower, "must be") ||
		strings.Contains(lower, "no configured bins") ||
		strings.Contains(lower, "no linked") ||
		strings.Contains(lower, "no physical") ||
		strings.Contains(lower, "no assigned"):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, fallbackMsg+": "+err.Error())
	}
}

func listPartRowToResponse(row db.ListPartsRow) PartResponse {
	resp := PartResponse{
		ID:                row.ID,
		Name:              row.Name,
		Description:       row.Description.String,
		PartNumber:        row.PartNumber.String,
		Manufacturer:      row.Manufacturer.String,
		Supplier:          row.Supplier.String,
		BarcodeData:       row.BarcodeData.String,
		UnitCost:          row.UnitCost.Float64,
		ReorderLevel:      row.ReorderLevel.Int64,
		MinStockThreshold: row.MinStockThreshold.Int64,
		ImagePath:         row.ImagePath.String,
		IsFavorite:        row.IsFavorite.Bool,
		Tags:              splitTagCSV(row.Tags.String),
		TotalStock:        row.TotalStock,
		ValidStock:        row.ValidStock,
		OrphanedStock:     row.OrphanedStock,
		StockStatus:       calcStockStatus(row.TotalStock, row.ReorderLevel.Int64, row.MinStockThreshold.Int64),
	}
	resp.CreatedAt = nullTimePtr(row.CreatedAt)
	resp.UpdatedAt = nullTimePtr(row.UpdatedAt)
	return resp
}

func searchPartRowToResponse(row db.SearchPartsRow) PartResponse {
	resp := PartResponse{
		ID:                row.ID,
		Name:              row.Name,
		Description:       row.Description.String,
		PartNumber:        row.PartNumber.String,
		Manufacturer:      row.Manufacturer.String,
		Supplier:          row.Supplier.String,
		BarcodeData:       row.BarcodeData.String,
		UnitCost:          row.UnitCost.Float64,
		ReorderLevel:      row.ReorderLevel.Int64,
		MinStockThreshold: row.MinStockThreshold.Int64,
		ImagePath:         row.ImagePath.String,
		IsFavorite:        row.IsFavorite.Bool,
		Tags:              splitTagCSV(row.Tags.String),
		TotalStock:        row.TotalStock,
		ValidStock:        row.ValidStock,
		OrphanedStock:     row.OrphanedStock,
		StockStatus:       calcStockStatus(row.TotalStock, row.ReorderLevel.Int64, row.MinStockThreshold.Int64),
	}
	resp.CreatedAt = nullTimePtr(row.CreatedAt)
	resp.UpdatedAt = nullTimePtr(row.UpdatedAt)
	return resp
}

func partViewToResponse(v pages.PartView) PartResponse {
	return PartResponse{
		ID:            v.ID,
		Name:          v.Name,
		Description:   v.Description.String,
		PartNumber:    v.PartNumber.String,
		UnitCost:      v.UnitCost.Float64,
		ImagePath:     v.ImagePath.String,
		IsFavorite:    v.IsFavorite.Bool,
		Tags:          []string{},
		TotalStock:    v.TotalStock,
		ValidStock:    v.ValidStock,
		OrphanedStock: v.OrphanedStock,
		StockStatus:   "ok",
	}
}

func partDetailToResponse(detail parts.PartDetail, tags []string) PartDetailResponse {
	var totalStock, validStock, orphanedStock int64
	assignments := make([]PartAssignmentResponse, 0, len(detail.Stock))
	for _, s := range detail.Stock {
		totalStock += s.Quantity
		if s.BinID.Valid {
			validStock += s.Quantity
		} else {
			orphanedStock += s.Quantity
		}
		item := PartAssignmentResponse{
			ID:             s.ID,
			BinName:        s.BinName.String,
			ContainerName:  s.ContainerName.String,
			ControllerName: s.ControllerName.String,
			ControllerIP:   s.ControllerIp.String,
			Quantity:       s.Quantity,
		}
		if s.BinID.Valid {
			v := s.BinID.Int64
			item.BinID = &v
		}
		if s.ContainerID.Valid {
			v := s.ContainerID.Int64
			item.ContainerID = &v
		}
		if s.ControllerID.Valid {
			v := s.ControllerID.Int64
			item.ControllerID = &v
		}
		if s.LedIndex.Valid {
			v := s.LedIndex.Int64
			item.LedIndex = &v
		}
		if s.Width.Valid {
			v := s.Width.Int64
			item.Width = &v
		}
		assignments = append(assignments, item)
	}

	links := make([]PartLinkResponse, 0, len(detail.Links))
	for _, l := range detail.Links {
		links = append(links, PartLinkResponse{
			ID:    l.ID,
			Label: l.Label.String,
			URL:   l.Url,
		})
	}

	docs := make([]PartDocResponse, 0, len(detail.Docs))
	for _, d := range detail.Docs {
		docs = append(docs, PartDocResponse{
			ID:       d.ID,
			FileName: d.FileName,
			FilePath: d.FilePath,
		})
	}

	if tags == nil {
		tags = []string{}
	}

	p := detail.Part
	return PartDetailResponse{
		PartResponse: PartResponse{
			ID:                p.ID,
			Name:              p.Name,
			Description:       p.Description.String,
			PartNumber:        p.PartNumber.String,
			Manufacturer:      p.Manufacturer.String,
			Supplier:          p.Supplier.String,
			BarcodeData:       p.BarcodeData.String,
			UnitCost:          p.UnitCost.Float64,
			ReorderLevel:      p.ReorderLevel.Int64,
			MinStockThreshold: p.MinStockThreshold.Int64,
			ImagePath:         p.ImagePath.String,
			IsFavorite:        p.IsFavorite.Bool,
			Tags:              tags,
			TotalStock:        totalStock,
			ValidStock:        validStock,
			OrphanedStock:     orphanedStock,
			StockStatus:       calcStockStatus(totalStock, p.ReorderLevel.Int64, p.MinStockThreshold.Int64),
			CreatedAt:         nullTimePtr(p.CreatedAt),
			UpdatedAt:         nullTimePtr(p.UpdatedAt),
		},
		Assignments: assignments,
		Links:       links,
		Docs:        docs,
	}
}

func binToResponse(b db.Bin) BinResponse {
	resp := BinResponse{
		ID:          b.ID,
		Name:        b.Name,
		ContainerID: b.ContainerID,
	}
	if b.LedIndex.Valid {
		v := b.LedIndex.Int64
		resp.LedIndex = &v
	}
	if b.Width.Valid {
		v := b.Width.Int64
		resp.Width = &v
	}
	if b.GridX.Valid {
		v := b.GridX.Int64
		resp.GridX = &v
	}
	if b.GridY.Valid {
		v := b.GridY.Int64
		resp.GridY = &v
	}
	return resp
}

func controllerToResponse(c db.Controller) ControllerResponse {
	return ControllerResponse{
		ID:         c.ID,
		Name:       c.Name,
		IPAddress:  c.IpAddress,
		Port:       c.Port.Int64,
		MacAddress: c.MacAddress.String,
		IsOnline:   c.IsOnline.Bool,
		LedCount:   c.LedCount,
		UseUDP:     c.UseUdp.Bool,
		UDPPort:    c.UdpPort.Int64,
	}
}

func calcStockStatus(totalStock, reorderLevel, minThreshold int64) string {
	if minThreshold > 0 && totalStock <= minThreshold {
		return "critical"
	}
	if reorderLevel > 0 && totalStock <= reorderLevel {
		return "low"
	}
	return "ok"
}

func isLowStock(totalStock, reorderLevel, minThreshold int64) bool {
	if minThreshold > 0 && totalStock <= minThreshold {
		return true
	}
	if reorderLevel > 0 && totalStock <= reorderLevel {
		return true
	}
	return false
}

func splitTagCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	return normalizeTagSlice(parts)
}

func normalizeTagSlice(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, trimmed)
	}
	return out
}

func hasTag(tags []string, wanted string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, wanted) {
			return true
		}
	}
	return false
}

func nullTimePtr(ts sql.NullTime) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

func parseLinkPayloads(raw []LinkPayload, ownedLinkIDs map[int64]bool, allowExisting bool) ([]parts.LinkDTO, []parts.LinkDTO, error) {
	var existingLinks []parts.LinkDTO
	newLinks := make([]parts.LinkDTO, 0, len(raw))

	for _, l := range raw {
		if allowExisting {
			if l.ID < 0 {
				return nil, nil, errors.New("link id must be a positive integer")
			}
			if l.ID > 0 && !ownedLinkIDs[l.ID] {
				return nil, nil, errors.New("link id does not belong to this part")
			}
		}

		rawURL := strings.TrimSpace(l.URL)
		if rawURL == "" {
			if allowExisting && l.ID > 0 {
				return nil, nil, errors.New("link url is required when updating an existing link")
			}
			continue
		}

		validURL, err := validateLinkURL(rawURL)
		if err != nil {
			return nil, nil, err
		}

		dto := parts.LinkDTO{
			Label: strings.TrimSpace(l.Label),
			URL:   validURL,
		}
		if allowExisting && l.ID > 0 {
			dto.ID = l.ID
			existingLinks = append(existingLinks, dto)
		} else {
			newLinks = append(newLinks, dto)
		}
	}

	return existingLinks, newLinks, nil
}

func validateLinkURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("invalid link url")
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Host == "" {
		return "", errors.New("link url must use http or https with a valid host")
	}
	return raw, nil
}
