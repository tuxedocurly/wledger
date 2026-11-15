package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	stdhtml "html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/barcodes"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/components"
)

func TestHandleParseBarcode_JSONAndForm(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	t.Run("JSON payload DigiKey TIA-1063 without DB match", func(t *testing.T) {
		raw := "[)>\x1e06\x1dP296-1193-1-ND\x1d1PSN74HC595DR\x1d1VTexas Instruments\x1dQ50\x1d1K789012\x1d4LCN\x1d11ZPICK\x1e\x04"
		bodyBytes, err := json.Marshal(map[string]string{"barcode": raw})
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/parts/parse-barcode", strings.NewReader(string(bodyBytes)))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleParseBarcode(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("expected application/json Content-Type, got %q", ct)
		}

		var got ParseBarcodeResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response failed: %v", err)
		}
		if got.Supplier != "DigiKey" {
			t.Errorf("Supplier = %q, want DigiKey", got.Supplier)
		}
		if got.PartNumber != "SN74HC595DR" {
			t.Errorf("PartNumber = %q, want SN74HC595DR", got.PartNumber)
		}
		if got.SupplierPartNumber != "296-1193-1-ND" {
			t.Errorf("SupplierPartNumber = %q, want 296-1193-1-ND", got.SupplierPartNumber)
		}
		if got.Manufacturer != "Texas Instruments" {
			t.Errorf("Manufacturer = %q, want Texas Instruments", got.Manufacturer)
		}
		if got.Quantity != 50 {
			t.Errorf("Quantity = %d, want 50", got.Quantity)
		}
		if got.OrderNumber != "789012" {
			t.Errorf("OrderNumber = %q, want 789012", got.OrderNumber)
		}
		if got.Format != barcodes.FormatTIA1063 {
			t.Errorf("Format = %q, want %q", got.Format, barcodes.FormatTIA1063)
		}
		if got.ExistingPartID != nil {
			t.Errorf("ExistingPartID = %v, want nil", *got.ExistingPartID)
		}
		if got.ExistingPartName != "" {
			t.Errorf("ExistingPartName = %q, want empty", got.ExistingPartName)
		}
	})

	t.Run("Form payload LCSC unquoted JSON", func(t *testing.T) {
		form := url.Values{}
		form.Set("barcode", "{pbn:PICK2311,on:WM23110501,pc:C14663,pm:BZX84C5V1,qty:100}")

		req := httptest.NewRequest(http.MethodPost, "/parts/parse-barcode", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		h.HandleParseBarcode(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
		}

		var got ParseBarcodeResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response failed: %v", err)
		}
		if got.Supplier != "LCSC" {
			t.Errorf("Supplier = %q, want LCSC", got.Supplier)
		}
		if got.SupplierPartNumber != "C14663" {
			t.Errorf("SupplierPartNumber = %q, want C14663", got.SupplierPartNumber)
		}
		if got.PartNumber != "BZX84C5V1" {
			t.Errorf("PartNumber = %q, want BZX84C5V1", got.PartNumber)
		}
		if got.Quantity != 100 {
			t.Errorf("Quantity = %d, want 100", got.Quantity)
		}
		if got.OrderNumber != "WM23110501" {
			t.Errorf("OrderNumber = %q, want WM23110501", got.OrderNumber)
		}
		if got.Format != barcodes.FormatLCSCJSON {
			t.Errorf("Format = %q, want %q", got.Format, barcodes.FormatLCSCJSON)
		}
	})

	t.Run("Empty barcode returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/parts/parse-barcode", strings.NewReader(`{"barcode":"   "}`))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleParseBarcode(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("Malformed JSON returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/parts/parse-barcode", strings.NewReader(`{"barcode":`))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleParseBarcode(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d: %s", rr.Code, rr.Body.String())
		}
	})
}

func TestHandleParseBarcode_MatchesExistingPartInSQLite(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	ctx := context.Background()

	partByMPN, err := h.Queries.CreatePart(ctx, db.CreatePartParams{
		Name:         "74HC595 Shift Register",
		PartNumber:   sql.NullString{String: "SN74HC595DR", Valid: true},
		Manufacturer: sql.NullString{String: "Texas Instruments", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart MPN failed: %v", err)
	}

	rawLCSC := `{pbn:PICK10,on:WM10,pc:C14663,pm:BZX84C5V1,qty:50}`
	partByBarcode, err := h.Queries.CreatePart(ctx, db.CreatePartParams{
		Name:        "5.1V Zener Diode",
		PartNumber:  sql.NullString{String: "BZX84C5V1", Valid: true},
		BarcodeData: sql.NullString{String: rawLCSC, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart barcode failed: %v", err)
	}

	part1D, err := h.Queries.CreatePart(ctx, db.CreatePartParams{
		Name:        "UPC Test Connector",
		BarcodeData: sql.NullString{String: "012345678905", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart 1D failed: %v", err)
	}

	t.Run("Matches existing part by exact barcode_data", func(t *testing.T) {
		bodyBytes, _ := json.Marshal(map[string]string{"barcode": rawLCSC})
		req := httptest.NewRequest(http.MethodPost, "/parts/parse-barcode", strings.NewReader(string(bodyBytes)))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleParseBarcode(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rr.Code)
		}

		var got ParseBarcodeResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if got.ExistingPartID == nil || *got.ExistingPartID != partByBarcode {
			t.Fatalf("ExistingPartID = %v, want %d", got.ExistingPartID, partByBarcode)
		}
		if got.ExistingPartName != "5.1V Zener Diode" {
			t.Errorf("ExistingPartName = %q, want %q", got.ExistingPartName, "5.1V Zener Diode")
		}
	})

	t.Run("Matches existing part by case-insensitive MPN when 2D bag barcode is new", func(t *testing.T) {
		mouserRaw := "[)>\x1e06\x1dK112233\x1d14K001\x1d1Psn74hc595dr\x1dP595-SN74HC595DR\x1d1VTexas Instruments\x1dQ25\x1e\x04"
		bodyBytes, _ := json.Marshal(map[string]string{"barcode": mouserRaw})
		req := httptest.NewRequest(http.MethodPost, "/parts/parse-barcode", strings.NewReader(string(bodyBytes)))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleParseBarcode(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rr.Code)
		}

		var got ParseBarcodeResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if got.Supplier != "Mouser" {
			t.Errorf("Supplier = %q, want Mouser", got.Supplier)
		}
		if got.ExistingPartID == nil || *got.ExistingPartID != partByMPN {
			t.Fatalf("ExistingPartID = %v, want %d", got.ExistingPartID, partByMPN)
		}
		if got.ExistingPartName != "74HC595 Shift Register" {
			t.Errorf("ExistingPartName = %q, want %q", got.ExistingPartName, "74HC595 Shift Register")
		}
	})

	t.Run("Matches existing part by 1D barcode", func(t *testing.T) {
		form := url.Values{}
		form.Set("barcode", "012345678905")
		req := httptest.NewRequest(http.MethodPost, "/parts/parse-barcode", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		h.HandleParseBarcode(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rr.Code)
		}

		var got ParseBarcodeResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if got.Format != barcodes.Format1D {
			t.Errorf("Format = %q, want 1D", got.Format)
		}
		if got.ExistingPartID == nil || *got.ExistingPartID != part1D {
			t.Fatalf("ExistingPartID = %v, want %d", got.ExistingPartID, part1D)
		}
	})
}

func TestHandleParseBarcode_NilQueriesSafe(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/parts/parse-barcode", strings.NewReader(`{"barcode":"pc:C14663,pm:NE555,qty:10"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.HandleParseBarcode(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
	var got ParseBarcodeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if got.Supplier != "LCSC" || got.PartNumber != "NE555" || got.Quantity != 10 {
		t.Errorf("unexpected response: %+v", got)
	}
	if got.ExistingPartID != nil {
		t.Errorf("expected nil ExistingPartID, got %v", *got.ExistingPartID)
	}
}

func TestPartForm_BarcodeAutoFillAttributes(t *testing.T) {
	var buf bytes.Buffer
	err := components.PartForm(db.Part{}, nil, nil, nil, nil, "/parts", false).Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("render PartForm failed: %v", err)
	}
	html := buf.String()
	required := []string{
		`id="part-form"`,
		`id="name"`,
		`id="part_number"`,
		`id="manufacturer"`,
		`id="supplier"`,
		`id="barcode_data"`,
		`id="parse-barcode-btn"`,
		`id="barcode-parse-banner"`,
	}
	for _, needle := range required {
		if !strings.Contains(html, needle) {
			t.Errorf("rendered PartForm missing %s", needle)
		}
	}
	if unescaped := stdhtml.UnescapeString(html); !strings.Contains(unescaped, `startGlobalScanner('barcode_data')`) {
		t.Errorf("rendered PartForm missing startGlobalScanner('barcode_data') trigger")
	}
}
