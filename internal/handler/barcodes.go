package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tuxedocurly/wledger/internal/barcodes"
	"github.com/tuxedocurly/wledger/internal/db"
)

const maxParseBarcodeBodyBytes = 1 << 20 // 1 MiB

type parseBarcodeRequest struct {
	Barcode string `json:"barcode"`
}

// ParseBarcodeResponse returns parsed supplier label metadata alongside any matching existing part.
type ParseBarcodeResponse struct {
	barcodes.ParsedBarcode
	ExistingPartID   *int64 `json:"existing_part_id"`
	ExistingPartName string `json:"existing_part_name"`
}

// POST /parts/parse-barcode
func (h *Handler) HandleParseBarcode(w http.ResponseWriter, r *http.Request) {
	raw, status, errMsg := extractBarcodeInput(w, r)
	if errMsg != "" {
		writeJSONError(w, status, errMsg)
		return
	}

	if strings.TrimSpace(raw) == "" {
		writeJSONError(w, http.StatusBadRequest, "barcode is required")
		return
	}

	parsed := barcodes.Parse(raw)
	existingID, existingName := h.findMatchingPart(r.Context(), parsed)

	resp := ParseBarcodeResponse{
		ParsedBarcode:    parsed,
		ExistingPartID:   existingID,
		ExistingPartName: existingName,
	}

	writeJSON(w, http.StatusOK, resp)
}

func extractBarcodeInput(w http.ResponseWriter, r *http.Request) (string, int, string) {
	if r.Body == nil {
		if q := r.URL.Query().Get("barcode"); q != "" {
			return q, 0, ""
		}
		return "", http.StatusBadRequest, "barcode is required"
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxParseBarcodeBodyBytes)
	ct := strings.ToLower(r.Header.Get("Content-Type"))

	if strings.Contains(ct, "application/json") {
		return decodeBarcodeJSON(r.Body)
	}
	if strings.Contains(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxParseBarcodeBodyBytes); err != nil {
			return "", formErrorStatus(err), "invalid multipart form payload"
		}
		return r.FormValue("barcode"), 0, ""
	}
	if strings.Contains(ct, "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			return "", formErrorStatus(err), "invalid form payload"
		}
		return r.FormValue("barcode"), 0, ""
	}

	// Fallback when Content-Type is omitted: inspect body prefix for JSON vs form.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return "", formErrorStatus(err), "failed to read request body"
	}
	trimmed := bytes.TrimSpace(bodyBytes)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return decodeBarcodeJSON(bytes.NewReader(trimmed))
	}

	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	if err := r.ParseForm(); err != nil {
		return "", http.StatusBadRequest, "invalid form payload"
	}
	return r.FormValue("barcode"), 0, ""
}

func decodeBarcodeJSON(rd io.Reader) (string, int, string) {
	var payload parseBarcodeRequest
	dec := json.NewDecoder(rd)
	if err := dec.Decode(&payload); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return "", http.StatusRequestEntityTooLarge, "request body too large"
		}
		return "", http.StatusBadRequest, "invalid JSON payload"
	}
	return payload.Barcode, 0, ""
}

func formErrorStatus(err error) int {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func (h *Handler) findMatchingPart(ctx context.Context, parsed barcodes.ParsedBarcode) (*int64, string) {
	if h == nil || h.Queries == nil {
		return nil, ""
	}

	allParts, err := h.Queries.GetAllParts(ctx)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("barcode lookup failed", "err", err)
		}
		return nil, ""
	}

	var bestPart *db.Part
	bestRank := 99

	for i := range allParts {
		p := &allParts[i]
		rank := matchPartRank(p, parsed)
		if rank > 0 && rank < bestRank {
			bestRank = rank
			bestPart = p
			if bestRank == 1 {
				break
			}
		}
	}

	if bestPart == nil {
		return nil, ""
	}
	id := bestPart.ID
	return &id, bestPart.Name
}

func matchPartRank(p *db.Part, parsed barcodes.ParsedBarcode) int {
	dbBarcode := ""
	if p.BarcodeData.Valid {
		dbBarcode = strings.TrimSpace(p.BarcodeData.String)
	}
	dbMPN := ""
	if p.PartNumber.Valid {
		dbMPN = strings.TrimSpace(p.PartNumber.String)
	}

	if parsed.RawBarcode != "" && p.BarcodeData.Valid && p.BarcodeData.String == parsed.RawBarcode {
		return 1
	}
	if parsed.RawBarcode != "" && dbBarcode != "" && strings.EqualFold(dbBarcode, parsed.RawBarcode) {
		return 2
	}
	if parsed.PartNumber != "" && dbMPN != "" && strings.EqualFold(dbMPN, parsed.PartNumber) {
		return 3
	}
	if parsed.SupplierPartNumber != "" && dbBarcode != "" && strings.EqualFold(dbBarcode, parsed.SupplierPartNumber) {
		return 4
	}
	if parsed.PartNumber != "" && dbBarcode != "" && strings.EqualFold(dbBarcode, parsed.PartNumber) {
		return 5
	}
	if parsed.PartNumber == "" && parsed.RawBarcode != "" && dbMPN != "" && strings.EqualFold(dbMPN, parsed.RawBarcode) {
		return 6
	}
	return 0
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
