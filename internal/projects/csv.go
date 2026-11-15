package projects

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"
)

const maxBOMImportBytes = 10 << 20 // 10 MiB safety cap for CSV BOM uploads

// ParsedBOMRow represents one normalized line extracted from a KiCad or generic BOM CSV.
type ParsedBOMRow struct {
	RowNumber        int
	Designator       string
	ValueOrName      string
	MPN              string
	Barcode          string
	Description      string
	QuantityPerBoard int64
	Notes            string
}

// EffectiveDescription returns the description to store on the BOM item, falling back to Value/Name.
func (r ParsedBOMRow) EffectiveDescription() string {
	if r.Description != "" {
		return r.Description
	}
	return r.ValueOrName
}

// parseBOMCSV parses a KiCad or generic electronics BOM CSV stream into validated rows.
func parseBOMCSV(input io.Reader) ([]ParsedBOMRow, error) {
	if input == nil {
		return nil, fmt.Errorf("%w: empty CSV input", ErrInvalidInput)
	}

	raw, err := io.ReadAll(io.LimitReader(input, maxBOMImportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV input: %w", err)
	}
	if len(raw) > maxBOMImportBytes {
		return nil, fmt.Errorf("%w: CSV input exceeds 10MB limit", ErrInvalidInput)
	}

	// Strip UTF-8 BOM if present.
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("%w: empty CSV input", ErrInvalidInput)
	}

	reader := csv.NewReader(bytes.NewReader(raw))
	reader.Comma = detectCSVDelimiter(raw)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	headers, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: empty CSV input", ErrInvalidInput)
		}
		return nil, fmt.Errorf("%w: failed to read CSV headers: %v", ErrInvalidInput, err)
	}

	cols := mapBOMColumns(headers)
	if cols.designator < 0 && cols.value < 0 && cols.mpn < 0 && cols.description < 0 {
		return nil, fmt.Errorf("%w: could not detect BOM columns (expected Reference/Designator, Value/Name, MPN/Part Number, or Description/Comment)", ErrInvalidInput)
	}

	var rows []ParsedBOMRow
	rowNum := 1
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		rowNum++
		if err != nil {
			return nil, fmt.Errorf("%w: row %d parsing error: %v", ErrInvalidInput, rowNum, err)
		}
		if isBlankCSVRecord(record) {
			continue
		}

		row := ParsedBOMRow{
			RowNumber:   rowNum,
			Designator:  getCSVCell(record, cols.designator),
			ValueOrName: getCSVCell(record, cols.value),
			MPN:         getCSVCell(record, cols.mpn),
			Barcode:     getCSVCell(record, cols.barcode),
			Description: getCSVCell(record, cols.description),
			Notes:       getCSVCell(record, cols.notes),
		}

		if row.Designator == "" && row.ValueOrName == "" && row.MPN == "" && row.Description == "" {
			continue
		}

		qtyStr := getCSVCell(record, cols.qty)
		qty, err := resolveBOMRowQuantity(qtyStr, row.Designator)
		if err != nil {
			return nil, fmt.Errorf("%w: row %d: %v", ErrInvalidInput, rowNum, err)
		}
		row.QuantityPerBoard = qty

		rows = append(rows, row)
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: no BOM rows found in CSV", ErrInvalidInput)
	}
	return rows, nil
}

type bomColumnMap struct {
	designator  int
	value       int
	mpn         int
	barcode     int
	qty         int
	description int
	notes       int
}

func mapBOMColumns(headers []string) bomColumnMap {
	m := bomColumnMap{
		designator:  -1,
		value:       -1,
		mpn:         -1,
		barcode:     -1,
		qty:         -1,
		description: -1,
		notes:       -1,
	}

	normIdx := make(map[string]int, len(headers))
	for i, h := range headers {
		key := normalizeHeaderKey(h)
		if key == "" {
			continue
		}
		if _, exists := normIdx[key]; !exists {
			normIdx[key] = i
		}
	}

	m.designator = firstMatchingHeader(normIdx,
		"reference", "references", "ref", "designator", "designators", "refdes", "partreference", "component",
	)
	m.value = firstMatchingHeader(normIdx,
		"value", "name", "partname", "componentname", "val",
	)
	m.mpn = firstMatchingHeader(normIdx,
		"mpn", "manufacturerpartnumber", "mfgpartnumber", "mfgpn", "partnumber", "pn", "partno",
		"manufacturerpn", "manufpartnumber", "mfrpartnumber",
	)
	m.barcode = firstMatchingHeader(normIdx,
		"barcode", "barcodedata",
	)
	m.qty = firstMatchingHeader(normIdx,
		"qty", "quantity", "count", "qtyperboard", "quantityperboard", "amount",
	)
	m.description = firstMatchingHeader(normIdx,
		"description", "desc", "comment", "comments",
	)
	m.notes = firstMatchingHeader(normIdx,
		"notes", "note", "remarks",
	)

	return m
}

func normalizeHeaderKey(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func firstMatchingHeader(normIdx map[string]int, aliases ...string) int {
	for _, a := range aliases {
		if idx, ok := normIdx[a]; ok {
			return idx
		}
	}
	return -1
}

func detectCSVDelimiter(raw []byte) rune {
	// Inspect only the first non-empty line outside quotes.
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var commas, semis, tabs int
		inQuotes := false
		for i := 0; i < len(trimmed); i++ {
			switch trimmed[i] {
			case '"':
				inQuotes = !inQuotes
			case ',':
				if !inQuotes {
					commas++
				}
			case ';':
				if !inQuotes {
					semis++
				}
			case '\t':
				if !inQuotes {
					tabs++
				}
			}
		}
		if commas == 0 && semis > 0 {
			return ';'
		}
		if commas == 0 && tabs > 0 {
			return '\t'
		}
		return ','
	}
	return ','
}

func isBlankCSVRecord(record []string) bool {
	for _, cell := range record {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

func getCSVCell(record []string, idx int) string {
	if idx < 0 || idx >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[idx])
}

func resolveBOMRowQuantity(qtyStr, designator string) (int64, error) {
	if qtyStr != "" {
		clean := strings.ReplaceAll(strings.TrimSpace(qtyStr), ",", "")
		if q, err := strconv.ParseInt(clean, 10, 64); err == nil {
			if q <= 0 {
				return 0, fmt.Errorf("quantity must be greater than 0, got %d", q)
			}
			return q, nil
		}
		if f, err := strconv.ParseFloat(clean, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			if f <= 0 || math.Trunc(f) != f {
				return 0, fmt.Errorf("invalid quantity %q: must be a positive integer", qtyStr)
			}
			return int64(f), nil
		}
		return 0, fmt.Errorf("invalid quantity %q: must be a positive integer", qtyStr)
	}

	// No explicit quantity provided: count comma/semicolon-separated designators if multiple.
	if designator != "" {
		parts := strings.FieldsFunc(designator, func(r rune) bool {
			return r == ',' || r == ';'
		})
		count := int64(0)
		for _, p := range parts {
			if strings.TrimSpace(p) != "" {
				count++
			}
		}
		if count > 0 {
			return count, nil
		}
	}
	return 1, nil
}
