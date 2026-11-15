package importer

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// PartImportRow represents a single row from the CSV
type PartImportRow struct {
	RowNumber         int
	Name              string
	Description       string
	PartNumber        string
	Manufacturer      string
	Supplier          string
	UnitCost          float64
	ReorderLevel      int
	MinStockThreshold int
	BarcodeData       string
	InitialQuantity   int
	Tags              []string
	Links             []string
	ControllerIP      string
	ContainerName     string
	BinName           string
	SegmentID         *int
	LEDIndex          *int
}

// Validate checks the business rules for a single row
func (r PartImportRow) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("row %d: 'Name' is required", r.RowNumber)
	}
	if r.UnitCost < 0 {
		return fmt.Errorf("row %d: 'Unit Cost' cannot be negative", r.RowNumber)
	}
	if r.InitialQuantity < 0 {
		return fmt.Errorf("row %d: 'Quantity' cannot be negative", r.RowNumber)
	}
	if r.ReorderLevel < 0 {
		return fmt.Errorf("row %d: 'Reorder Level' cannot be negative", r.RowNumber)
	}
	if r.MinStockThreshold < 0 {
		return fmt.Errorf("row %d: 'Min Stock' cannot be negative", r.RowNumber)
	}
	for _, link := range r.Links {
		u, err := url.ParseRequestURI(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("row %d: invalid URL: %s", r.RowNumber, link)
		}
	}

	// Location Validation
	hasIP := r.ControllerIP != ""
	hasContainer := r.ContainerName != ""
	hasBinName := r.BinName != ""
	hasSeg := r.SegmentID != nil
	hasLed := r.LEDIndex != nil

	if hasIP {
		if !hasSeg {
			return fmt.Errorf("row %d: 'Segment ID' is required when specifying location", r.RowNumber)
		}
		if !hasLed {
			return fmt.Errorf("row %d: 'LED Index' is required when specifying location", r.RowNumber)
		}
	} else if hasContainer {
		if !hasBinName && !hasLed {
			return fmt.Errorf("row %d: 'Bin Name' or 'LED Index' is required when specifying container", r.RowNumber)
		}
	} else if hasBinName {
		return fmt.Errorf("row %d: 'Container' is required when specifying bin name", r.RowNumber)
	} else if hasSeg || hasLed {
		return fmt.Errorf("row %d: 'Controller IP' is required when specifying location", r.RowNumber)
	}

	return nil
}

// ParsePartsCSV reads a CSV stream and returns typed rows or errors
func ParsePartsCSV(input io.Reader) ([]PartImportRow, error) {
	reader := csv.NewReader(input)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	// Read Header
	headers, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("empty file")
		}
		return nil, fmt.Errorf("failed to read headers: %w", err)
	}

	// Map headers to column indices
	headerMap := make(map[string]int)
	for i, h := range headers {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(h), " ", ""))
		normalized = strings.ReplaceAll(normalized, "_", "") // "part_number" -> "partnumber"
		headerMap[normalized] = i
	}

	// Verify required header
	if _, ok := headerMap["name"]; !ok {
		return nil, fmt.Errorf("missing required column: 'Name' (Did you include the header row?)")
	}

	var rows []PartImportRow
	rowNum := 1 // Header is 1

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("row %d: parsing error: %w", rowNum+1, err)
		}
		rowNum++

		row := PartImportRow{RowNumber: rowNum}

		// Helper to get string value
		getString := func(key string) string {
			if idx, ok := headerMap[key]; ok && idx < len(record) {
				return strings.TrimSpace(record[idx])
			}
			return ""
		}

		// Helper to find which alias is present
		findColumn := func(aliases ...string) string {
			for _, key := range aliases {
				if _, ok := headerMap[key]; ok {
					return key
				}
			}
			return ""
		}

		// Helper to get float from a specific known key
		parseColumnFloat := func(key string) (float64, error) {
			s := getString(key)
			if s == "" {
				return 0, nil
			}
			// Sanitize
			s = strings.TrimPrefix(s, "$")
			s = strings.ReplaceAll(s, ",", "")
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid number for '%s': %s", key, s)
			}
			return f, nil
		}

		// Helper to get int from a specific known key
		parseColumnInt := func(key string) (int, error) {
			s := getString(key)
			if s == "" {
				return 0, nil
			}
			s = strings.ReplaceAll(s, ",", "")
			i, err := strconv.Atoi(s)
			if err != nil {
				f, fErr := strconv.ParseFloat(s, 64)
				if fErr == nil {
					return int(f), nil
				}
				return 0, fmt.Errorf("invalid integer for '%s': %s", key, s)
			}
			return i, nil
		}

		// Map Fields
		row.Name = getString("name")
		row.Description = getString("description")
		
		if key := findColumn("partnumber", "mpn"); key != "" {
			row.PartNumber = getString(key)
		}

		row.Manufacturer = getString("manufacturer")
		row.Supplier = getString("supplier")
		row.BarcodeData = getString("barcode")

		// Numeric Fields
		if key := findColumn("unitcost", "cost"); key != "" {
			if val, err := parseColumnFloat(key); err != nil {
				return nil, fmt.Errorf("row %d: %w", rowNum, err)
			} else {
				row.UnitCost = val
			}
		}

		if key := findColumn("reorderlevel", "reorder"); key != "" {
			if val, err := parseColumnInt(key); err != nil {
				return nil, fmt.Errorf("row %d: %w", rowNum, err)
			} else {
				row.ReorderLevel = val
			}
		}

		if key := findColumn("minstock", "min"); key != "" {
			if val, err := parseColumnInt(key); err != nil {
				return nil, fmt.Errorf("row %d: %w", rowNum, err)
			} else {
				row.MinStockThreshold = val
			}
		}

		if key := findColumn("quantity", "qty", "stock"); key != "" {
			if val, err := parseColumnInt(key); err != nil {
				return nil, fmt.Errorf("row %d: %w", rowNum, err)
			} else {
				row.InitialQuantity = val
			}
		}

		// Location Fields
		if key := findColumn("container", "containername"); key != "" {
			row.ContainerName = getString(key)
		}

		if key := findColumn("controllerip", "ipaddress", "ip"); key != "" {
			ip := getString(key)
			if strings.EqualFold(ip, "standalone") && row.ContainerName != "" {
				ip = ""
			}
			row.ControllerIP = ip
		}

		if key := findColumn("binname"); key != "" {
			row.BinName = getString(key)
		}

		if key := findColumn("segmentid", "segment", "seg"); key != "" {
			s := getString(key)
			if s != "" {
				if val, err := parseColumnInt(key); err != nil {
					return nil, fmt.Errorf("row %d: %w", rowNum, err)
				} else {
					row.SegmentID = &val
				}
			}
		}

		if key := findColumn("ledindex", "led", "binindex", "bin"); key != "" {
			s := getString(key)
			if s != "" {
				if val, err := parseColumnInt(key); err != nil {
					if key == "bin" && row.ControllerIP == "" && row.ContainerName != "" {
						row.BinName = s
					} else {
						return nil, fmt.Errorf("row %d: %w", rowNum, err)
					}
				} else {
					row.LEDIndex = &val
				}
			}
		}

		// Parse Multi-value fields (Tags & Links)
		if key := findColumn("tags"); key != "" {
			s := getString(key)
			if s != "" {
				parts := strings.Split(s, "|")
				for _, p := range parts {
					trimmed := strings.TrimSpace(p)
					if trimmed != "" {
						row.Tags = append(row.Tags, trimmed)
					}
				}
			}
		}

		if key := findColumn("links", "urls"); key != "" {
			s := getString(key)
			if s != "" {
				parts := strings.Split(s, "|")
				for _, p := range parts {
					trimmed := strings.TrimSpace(p)
					if trimmed != "" {
						row.Links = append(row.Links, trimmed)
					}
				}
			}
		}

		// Validate Domain Rules
		if err := row.Validate(); err != nil {
			return nil, err
		}

		rows = append(rows, row)
	}

	return rows, nil
}
