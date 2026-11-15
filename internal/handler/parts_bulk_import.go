package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/importer"
	"github.com/tuxedocurly/wledger/web/components"
)

// GET /parts/import/template
func (h *Handler) HandlePartsImportTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment;filename=wledger_import_template.csv")
	w.Write([]byte("Name,Description,Part Number,Manufacturer,Supplier,Unit Cost,Reorder Level,Min Stock,Barcode,Quantity,Tags,Links,Controller IP,Segment ID,LED Index,Container,Bin Name\n"))
	w.Write([]byte("Example Part,Resistor 10-K,MPN123,TI,DigiKey,0.05,50,10,12345678,100,Resistor|SMD,https://example.com/resistor,192.168.1.100,0,5,,\n"))
}

// POST /parts/import
func (h *Handler) HandlePartsImport(w http.ResponseWriter, r *http.Request) {
	// Authorization
	user := auth.GetUserFromRequest(r)
	if !user.CanWrite() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusForbidden)
		return
	}

	// Parse Form
	err := r.ParseMultipartForm(config.MaxUploadSizeImport) // 100 MB
	if err != nil {
		h.Logger.Error("failed to parse multipart form for parts import", "err", err)
		components.ImportResult(false, "Failed to parse form: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	defer r.MultipartForm.RemoveAll()

	// Get Input (File takes precedence over text)
	var rows []importer.PartImportRow

	file, _, err := r.FormFile("file")
	if err == nil {
		defer file.Close()
		rows, err = importer.ParsePartsCSV(file)
	} else {
		// Try raw text
		raw := r.FormValue("raw_text")
		if strings.TrimSpace(raw) == "" {
			components.ImportResult(false, "No data provided. Upload a file or paste text.", nil).Render(r.Context(), w)
			return
		}
		rows, err = importer.ParsePartsCSV(strings.NewReader(raw))
	}

	if err != nil {
		components.ImportResult(false, "Parsing Error: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	if len(rows) == 0 {
		components.ImportResult(false, "No valid rows found in input.", nil).Render(r.Context(), w)
		return
	}

	count := 0
	err = h.Queries.ExecTx(r.Context(), func(q db.Querier) error {
		for _, row := range rows {
			// Insert Part
			partID, err := q.CreatePart(r.Context(), db.CreatePartParams{
				Name:              row.Name,
				Description:       sql.NullString{String: row.Description, Valid: row.Description != ""},
				PartNumber:        sql.NullString{String: row.PartNumber, Valid: row.PartNumber != ""},
				Manufacturer:      sql.NullString{String: row.Manufacturer, Valid: row.Manufacturer != ""},
				Supplier:          sql.NullString{String: row.Supplier, Valid: row.Supplier != ""},
				UnitCost:          sql.NullFloat64{Float64: row.UnitCost, Valid: true},
				ReorderLevel:      sql.NullInt64{Int64: int64(row.ReorderLevel), Valid: true},
				MinStockThreshold: sql.NullInt64{Int64: int64(row.MinStockThreshold), Valid: true},
				BarcodeData:       sql.NullString{String: row.BarcodeData, Valid: row.BarcodeData != ""},
			})

			if err != nil {
				h.Logger.Error("failed to create part during import", "err", err, "row", row.RowNumber)
				returnErr := fmt.Errorf("Row %d Error: %v", row.RowNumber, err)
				if strings.Contains(err.Error(), "UNIQUE constraint") {
					returnErr = fmt.Errorf("Row %d Error: Duplicate Barcode '%s'", row.RowNumber, row.BarcodeData)
				}
				return returnErr
			}

			// Sync Tags
			if len(row.Tags) > 0 {
				if err := h.Tags.SyncTags(r.Context(), q, partID, row.Tags); err != nil {
					h.Logger.Error("failed to sync tags during import", "err", err, "row", row.RowNumber)
					return fmt.Errorf("Row %d Error saving tags: %v", row.RowNumber, err)
				}
			}

			// Add Links
			for _, linkURL := range row.Links {
				label := ""
				if u, err := url.Parse(linkURL); err == nil {
					label = u.Hostname()
				}
				if err := h.Documents.AddLink(r.Context(), q, partID, linkURL, label); err != nil {
					h.Logger.Error("failed to add link during import", "err", err, "row", row.RowNumber, "url", linkURL)
					return fmt.Errorf("Row %d Error saving link %s: %v", row.RowNumber, linkURL, err)
				}
			}

			count++

			// Resolve Bin ID if location is provided
			binID, err := h.resolveImportBinID(r.Context(), q, row)
			if err != nil {
				return err
			}

			// Insert Stock if Quantity > 0
			if row.InitialQuantity > 0 {
				err = q.CreatePartAssignment(r.Context(), db.CreatePartAssignmentParams{
					PartID:   partID,
					BinID:    binID,
					Quantity: int64(row.InitialQuantity),
				})
				if err != nil {
					h.Logger.Error("failed to create part assignment during import", "err", err, "row", row.RowNumber)
					return fmt.Errorf("Row %d Error saving stock: %v", row.RowNumber, err)
				}
			}
		}

		// Audit Log
		audit.Log(r.Context(), q, "IMPORT", "PARTS", 0, fmt.Sprintf("Bulk imported %d parts", count), nil, nil)
		return nil
	})

	if err != nil {
		components.ImportResult(false, err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Success Response
	msg := fmt.Sprintf("Successfully imported %d parts.", count)
	components.ImportResult(true, msg, nil).Render(r.Context(), w)
}

func (h *Handler) resolveImportBinID(ctx context.Context, q db.Querier, row importer.PartImportRow) (sql.NullInt64, error) {
	if row.ControllerIP != "" {
		resolvedBinID, err := q.GetBinByLocation(ctx, db.GetBinByLocationParams{
			IpAddress: row.ControllerIP,
			SegmentID: int64(*row.SegmentID),
			LedIndex:  sql.NullInt64{Int64: int64(*row.LEDIndex), Valid: true},
		})
		if err != nil {
			h.Logger.Error("failed to resolve bin location during import", "err", err, "row", row.RowNumber, "ip", row.ControllerIP, "seg", *row.SegmentID, "led", *row.LEDIndex)
			if err == sql.ErrNoRows {
				return sql.NullInt64{}, fmt.Errorf("Row %d Error: Location not found (IP: %s, Seg: %d, LED: %d)", row.RowNumber, row.ControllerIP, *row.SegmentID, *row.LEDIndex)
			}
			return sql.NullInt64{}, fmt.Errorf("Row %d Error resolving location: %v", row.RowNumber, err)
		}
		return sql.NullInt64{Int64: resolvedBinID, Valid: true}, nil
	}

	if row.ContainerName == "" {
		return sql.NullInt64{}, nil
	}

	containers, err := q.GetStandaloneContainers(ctx)
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("Row %d Error loading standalone containers: %v", row.RowNumber, err)
	}

	var matchedContainer *db.Container
	for i := range containers {
		if strings.EqualFold(containers[i].Name, row.ContainerName) {
			if row.SegmentID == nil || containers[i].SegmentID == int64(*row.SegmentID) {
				matchedContainer = &containers[i]
				break
			}
		}
	}
	if matchedContainer == nil {
		return sql.NullInt64{}, fmt.Errorf("Row %d Error: Location not found (Standalone container '%s')", row.RowNumber, row.ContainerName)
	}

	bins, err := q.GetBinsByContainer(ctx, matchedContainer.ID)
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("Row %d Error loading bins for container '%s': %v", row.RowNumber, row.ContainerName, err)
	}

	for _, b := range bins {
		if row.BinName != "" && strings.EqualFold(b.Name, row.BinName) {
			return sql.NullInt64{Int64: b.ID, Valid: true}, nil
		}
		if row.LEDIndex != nil {
			if (b.LedIndex.Valid && b.LedIndex.Int64 == int64(*row.LEDIndex)) || b.Name == strconv.Itoa(*row.LEDIndex) {
				return sql.NullInt64{Int64: b.ID, Valid: true}, nil
			}
		}
	}

	return sql.NullInt64{}, fmt.Errorf("Row %d Error: Location not found in standalone container '%s'", row.RowNumber, row.ContainerName)
}
