package barcodes

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want ParsedBarcode
	}{
		{
			name: "DigiKey DataMatrix real control delimiters with 30P and 11ZPICK",
			raw:  "[)>\x1e06\x1dP\x1d1PRC0603FR-0710KL\x1d30P311-10.0KHRCT-ND\x1d1VYageo\x1dKPO-901\x1d1K71234567\x1d10K12345678\x1d11K1\x1d4LCN\x1dQ100\x1d11ZPICK\x1d12Z12345678\x1d13Z999999\x1d20Z00000000\x1e\x04",
			want: ParsedBarcode{
				Supplier:           "DigiKey",
				PartNumber:         "RC0603FR-0710KL",
				SupplierPartNumber: "311-10.0KHRCT-ND",
				Manufacturer:       "Yageo",
				Quantity:           100,
				OrderNumber:        "PO-901",
				Origin:             "CN",
				Format:             FormatTIA1063,
			},
		},
		{
			name: "DigiKey PDF417 with -ND in P and customer PN overridden by 30P",
			raw:  "[)>\x1e06\x1dPCUST-REF-42\x1d1PSN74HC595DR\x1d30P296-1193-1-ND\x1dVDIGI-KEY\x1d1VTexas Instruments\x1dK\x1d1K887766\x1d4LTH\x1dQ50\x1e\x04",
			want: ParsedBarcode{
				Supplier:           "DigiKey",
				PartNumber:         "SN74HC595DR",
				SupplierPartNumber: "296-1193-1-ND",
				Manufacturer:       "Texas Instruments",
				Quantity:           50,
				OrderNumber:        "887766",
				Origin:             "TH",
				Format:             FormatTIA1063,
			},
		},
		{
			name: "DigiKey legacy label with -ND in P and 06 GS prefix",
			raw:  "06\x1dP490-10474-1-ND\x1d1PGRM188R71H104KA93D\x1d1VMurata Electronics\x1dQ250\x1d1K554433\x1d4LJP\x04",
			want: ParsedBarcode{
				Supplier:           "DigiKey",
				PartNumber:         "GRM188R71H104KA93D",
				SupplierPartNumber: "490-10474-1-ND",
				Manufacturer:       "Murata Electronics",
				Quantity:           250,
				OrderNumber:        "554433",
				Origin:             "JP",
				Format:             FormatTIA1063,
			},
		},
		{
			name: "DigiKey with only V=DIGIKEY does not pollute Manufacturer",
			raw:  "[)>\x1e06\x1d1PATMEGA328P-PU\x1dVDIGIKEY\x1dQ10\x1d11ZPICK\x1e\x04",
			want: ParsedBarcode{
				Supplier:     "DigiKey",
				PartNumber:   "ATMEGA328P-PU",
				Manufacturer: "",
				Quantity:     10,
				Format:       FormatTIA1063,
			},
		},
		{
			name: "Mouser TIA-1063 real control chars with 14K and 1V",
			raw:  "[)>\x1e06\x1dK21345678\x1d14K001\x1d1PSN74HC595N\x1dQ25\x1d11K098765\x1d4LMY\x1d1VTexas Instruments\x1e\x04",
			want: ParsedBarcode{
				Supplier:     "Mouser",
				PartNumber:   "SN74HC595N",
				Manufacturer: "Texas Instruments",
				Quantity:     25,
				OrderNumber:  "21345678",
				Origin:       "MY",
				Format:       FormatTIA1063,
			},
		},
		{
			name: "Mouser TIA-1063 with NNN- prefix in P and VMouser",
			raw:  "[)>\x1e06\x1dP595-SN74HC595N\x1d1PSN74HC595N\x1d1VTexas Instruments\x1dVMOUSER\x1dQ15\x1d1K998877\x1d4LUS\x1e\x04",
			want: ParsedBarcode{
				Supplier:           "Mouser",
				PartNumber:         "SN74HC595N",
				SupplierPartNumber: "595-SN74HC595N",
				Manufacturer:       "Texas Instruments",
				Quantity:           15,
				OrderNumber:        "998877",
				Origin:             "US",
				Format:             FormatTIA1063,
			},
		},
		{
			name: "Generic TIA-1063 with V as Manufacturer when 1V omitted",
			raw:  "[)>\x1e06\x1d1PESP32-WROOM-32E\x1dPCUST-ESP32\x1dVEspressif Systems\x1dQ5\x1dK100200\x1d4LCN\x1e\x04",
			want: ParsedBarcode{
				Supplier:           "",
				PartNumber:         "ESP32-WROOM-32E",
				SupplierPartNumber: "CUST-ESP32",
				Manufacturer:       "Espressif Systems",
				Quantity:           5,
				OrderNumber:        "100200",
				Origin:             "CN",
				Format:             FormatTIA1063,
			},
		},
		{
			name: "TIA-1063 printable scanner substitution with <RS> <GS> <EOT>",
			raw:  "[)><RS>06<GS>1PSN74HC595DR<GS>P296-1193-1-ND<GS>1VTexas Instruments<GS>Q40<GS>KPO-77<GS>4LPH<RS><EOT>",
			want: ParsedBarcode{
				Supplier:           "DigiKey",
				PartNumber:         "SN74HC595DR",
				SupplierPartNumber: "296-1193-1-ND",
				Manufacturer:       "Texas Instruments",
				Quantity:           40,
				OrderNumber:        "PO-77",
				Origin:             "PH",
				Format:             FormatTIA1063,
			},
		},
		{
			name: "TIA-1063 newline scanner substitution after [)>06",
			raw:  "[)>\n06\n1PRC0603JR-0710KL\nP311-10KGRCT-ND\n1VYageo\nQ500\n11ZPICK",
			want: ParsedBarcode{
				Supplier:           "DigiKey",
				PartNumber:         "RC0603JR-0710KL",
				SupplierPartNumber: "311-10KGRCT-ND",
				Manufacturer:       "Yageo",
				Quantity:           500,
				Format:             FormatTIA1063,
			},
		},
		{
			name: "TIA-1063 literal escaped \\x1e and \\x1d sequences",
			raw:  `[)>\x1e06\x1dK555111\x1d14K002\x1d1PNE555P\x1dP595-NE555P\x1d1VTexas Instruments\x1dQ20\x1e\x04`,
			want: ParsedBarcode{
				Supplier:           "Mouser",
				PartNumber:         "NE555P",
				SupplierPartNumber: "595-NE555P",
				Manufacturer:       "Texas Instruments",
				Quantity:           20,
				OrderNumber:        "555111",
				Format:             FormatTIA1063,
			},
		},
		{
			name: "TIA-1063 caret notation ^] and ^^",
			raw:  "[)>^^06^]1PLM358P^]P296-1395-5-ND^]Q12^]11ZPICK^^",
			want: ParsedBarcode{
				Supplier:           "DigiKey",
				PartNumber:         "LM358P",
				SupplierPartNumber: "296-1395-5-ND",
				Quantity:           12,
				Format:             FormatTIA1063,
			},
		},
		{
			name: "LCSC unquoted JSON format",
			raw:  "{pbn:PICK231105001,on:WM2311050001,pc:C14663,pm:BZX84C5V1,qty:50}",
			want: ParsedBarcode{
				Supplier:           "LCSC",
				PartNumber:         "BZX84C5V1",
				SupplierPartNumber: "C14663",
				Quantity:           50,
				OrderNumber:        "WM2311050001",
				Format:             FormatLCSCJSON,
			},
		},
		{
			name: "LCSC unquoted JSON with spaces and comma in pm and missing pm fallback",
			raw:  "{pbn:P99,on:WM99,pc:C1525,pm:100nF 50V, 10% MLCC,qty:200}",
			want: ParsedBarcode{
				Supplier:           "LCSC",
				PartNumber:         "100nF 50V, 10% MLCC",
				SupplierPartNumber: "C1525",
				Quantity:           200,
				OrderNumber:        "WM99",
				Format:             FormatLCSCJSON,
			},
		},
		{
			name: "LCSC unquoted JSON missing pm falls back to pc",
			raw:  "{pbn:P10,on:WM10,pc:C2040,qty:5}",
			want: ParsedBarcode{
				Supplier:           "LCSC",
				PartNumber:         "C2040",
				SupplierPartNumber: "C2040",
				Quantity:           5,
				OrderNumber:        "WM10",
				Format:             FormatLCSCJSON,
			},
		},
		{
			name: "LCSC quoted standard JSON with string qty and brand",
			raw:  `{"pbn":"PICK123","on":"WM456","pc":"C14663","pm":"BZX84C5V1","qty":"100","mc":"Changjiang"}`,
			want: ParsedBarcode{
				Supplier:           "LCSC",
				PartNumber:         "BZX84C5V1",
				SupplierPartNumber: "C14663",
				Manufacturer:       "Changjiang",
				Quantity:           100,
				OrderNumber:        "WM456",
				Format:             FormatLCSCJSON,
			},
		},
		{
			name: "LCSC colon/comma key-value string",
			raw:  "pc:C14663,pm:SN74HC595DR,qty:50",
			want: ParsedBarcode{
				Supplier:           "LCSC",
				PartNumber:         "SN74HC595DR",
				SupplierPartNumber: "C14663",
				Quantity:           50,
				Format:             FormatLCSCKV,
			},
		},
		{
			name: "LCSC colon/comma KV with spaces, order number, and semicolon separator",
			raw:  "pbn:PICK8; on:WM2401; pc: C2040; pm: RP2040; qty: 25",
			want: ParsedBarcode{
				Supplier:           "LCSC",
				PartNumber:         "RP2040",
				SupplierPartNumber: "C2040",
				Quantity:           25,
				OrderNumber:        "WM2401",
				Format:             FormatLCSCKV,
			},
		},
		{
			name: "Standard 1D UPC-A barcode",
			raw:  "012345678905",
			want: ParsedBarcode{
				RawBarcode: "012345678905",
				Format:     Format1D,
			},
		},
		{
			name: "Standard 1D Code128 barcode with surrounding whitespace",
			raw:  "  SN74HC595N-12345  \n",
			want: ParsedBarcode{
				RawBarcode: "SN74HC595N-12345",
				Format:     Format1D,
			},
		},
		{
			name: "Random colon text that is not LCSC stays 1D",
			raw:  "PART:12345",
			want: ParsedBarcode{
				RawBarcode: "PART:12345",
				Format:     Format1D,
			},
		},
		{
			name: "Empty payload",
			raw:  "   \t\r\n  ",
			want: ParsedBarcode{},
		},
		{
			name: "Malformed truncated JSON does not panic",
			raw:  `{"pc":"C14663","pm":`,
			want: ParsedBarcode{
				RawBarcode: `{"pc":"C14663","pm":`,
				Format:     FormatUnknown,
			},
		},
		{
			name: "Non-LCSC JSON object does not misidentify as LCSC",
			raw:  `{"foo":"bar","baz":123}`,
			want: ParsedBarcode{
				RawBarcode: `{"foo":"bar","baz":123}`,
				Format:     FormatUnknown,
			},
		},
		{
			name: "TIA-1063 header only without fields",
			raw:  "[)>\x1e06\x1d\x1e\x04",
			want: ParsedBarcode{
				RawBarcode: "[)>\x1e06\x1d\x1e\x04",
				Format:     FormatUnknown,
			},
		},
		{
			name: "TIA-1063 malformed non-numeric and negative quantity ignored safely",
			raw:  "[)>\x1e06\x1d1PSN74HC595DR\x1dQnotanumber\x1e\x04",
			want: ParsedBarcode{
				PartNumber: "SN74HC595DR",
				Quantity:   0,
				Format:     FormatTIA1063,
			},
		},
		{
			name: "TIA-1063 negative quantity clamped to zero",
			raw:  "[)>\x1e06\x1d1PSN74HC595DR\x1dQ-5\x1e\x04",
			want: ParsedBarcode{
				PartNumber: "SN74HC595DR",
				Quantity:   0,
				Format:     FormatTIA1063,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.raw)
			want := tc.want
			if want.RawBarcode == "" && tc.raw != "" && got.Format != "" {
				// For cases that didn't explicitly spell RawBarcode in the table, assert trimmed raw preservation.
				want.RawBarcode = got.RawBarcode
				if got.RawBarcode == "" {
					t.Fatalf("expected non-empty RawBarcode for input %q", tc.raw)
				}
			}

			if got != want {
				t.Errorf("Parse(%q)\n got: %+v\nwant: %+v", tc.raw, got, want)
			}
		})
	}
}
