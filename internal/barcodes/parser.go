// Package barcodes parses supplier 2D bag barcodes (TIA-1063 / ANSI MH10.8.2
// used by DigiKey and Mouser, plus LCSC JSON/KV QR formats) and standard 1D codes.
package barcodes

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	FormatTIA1063  = "TIA-1063"
	FormatLCSCJSON = "LCSC-JSON"
	FormatLCSCKV   = "LCSC-KV"
	Format1D       = "1D"
	FormatUnknown  = "UNKNOWN"
)

// ParsedBarcode holds structured metadata extracted from a scanned or pasted
// supplier label or 1D barcode.
type ParsedBarcode struct {
	Supplier           string `json:"supplier"`
	PartNumber         string `json:"part_number"`
	SupplierPartNumber string `json:"supplier_part_number"`
	Manufacturer       string `json:"manufacturer"`
	Quantity           int    `json:"quantity"`
	OrderNumber        string `json:"order_number"`
	Origin             string `json:"origin,omitempty"`
	RawBarcode         string `json:"raw_barcode"`
	Format             string `json:"format"`
}

var (
	mouserPNPattern = regexp.MustCompile(`^\d{2,3}-[A-Za-z0-9._/+=-]*[A-Za-z][A-Za-z0-9._/+=-]*$`)
	kvIdentPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\s*:`)
)

// Parse inspects raw barcode input and extracts supplier, MPN, supplier PN,
// manufacturer, quantity, and order metadata across TIA-1063, LCSC, and 1D formats.
func Parse(raw string) ParsedBarcode {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ParsedBarcode{}
	}

	if looksLikeTIA1063(trimmed) {
		if parsed, ok := parseTIA1063(trimmed); ok {
			return parsed
		}
		return ParsedBarcode{
			RawBarcode: trimmed,
			Format:     FormatUnknown,
		}
	}

	if strings.HasPrefix(trimmed, "{") {
		if strings.HasSuffix(trimmed, "}") {
			if parsed, ok := parseLCSCObject(trimmed); ok {
				return parsed
			}
		}
		return ParsedBarcode{
			RawBarcode: trimmed,
			Format:     FormatUnknown,
		}
	}

	if parsed, ok := parseLCSCKV(trimmed); ok {
		return parsed
	}

	if hasControlChars(trimmed) || strings.ContainsAny(trimmed, "\r\n") {
		return ParsedBarcode{
			RawBarcode: trimmed,
			Format:     FormatUnknown,
		}
	}

	return ParsedBarcode{
		RawBarcode: trimmed,
		Format:     Format1D,
	}
}

func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return true
		}
	}
	return false
}

func looksLikeTIA1063(s string) bool {
	if strings.ContainsAny(s, "\x1d\x1e\x04\u241d\u241e\u2404") {
		return true
	}
	if strings.HasPrefix(s, "[)>") {
		return true
	}
	upper := strings.ToUpper(s)
	if strings.Contains(upper, "<GS>") || strings.Contains(upper, "[GS]") || strings.Contains(upper, "{GS}") {
		return true
	}
	if strings.Contains(s, `\x1d`) || strings.Contains(s, `\x1e`) || strings.Contains(s, `\u001d`) || strings.Contains(s, `\u001e`) {
		return true
	}
	if strings.Contains(s, "^]") || strings.Contains(s, "^^") {
		return true
	}
	for _, prefix := range []string{"06\n", "06\r", "06\t", "05\n", "05\r", "05\t"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func normalizeTIA1063(raw string) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	replacer := strings.NewReplacer(
		"<RS>", "\x1e", "<rs>", "\x1e", "[RS]", "\x1e", "{RS}", "\x1e", "␞", "\x1e", "^^", "\x1e", `\x1e`, "\x1e", `\X1E`, "\x1e", `\u001e`, "\x1e", `\u001E`, "\x1e",
		"<GS>", "\x1d", "<gs>", "\x1d", "[GS]", "\x1d", "{GS}", "\x1d", "␝", "\x1d", "^]", "\x1d", `\x1d`, "\x1d", `\X1D`, "\x1d", `\u001d`, "\x1d", `\u001D`, "\x1d",
		"<EOT>", "\x04", "<eot>", "\x04", "[EOT]", "\x04", "{EOT}", "\x04", "␄", "\x04", "^D", "\x04", `\x04`, "\x04", `\X04`, "\x04", `\u0004`, "\x04",
		"<FS>", "\x1c", "[FS]", "\x1c",
		"\n", "\x1d",
		"\t", "\x1d",
	)
	return replacer.Replace(s)
}

func parseTIA1063(raw string) (ParsedBarcode, bool) {
	norm := normalizeTIA1063(raw)
	norm = strings.TrimPrefix(norm, "[)>")
	norm = strings.TrimRight(norm, "\x04\x1e\x1d \t\r\n")

	// Split ISO 15434 record sections on RS (\x1e) and collect GS (\x1d) fields.
	var tokens []string
	sections := strings.Split(norm, "\x1e")
	for _, sec := range sections {
		sec = strings.Trim(sec, "\x1d\x1c\x04")
		if sec == "" {
			continue
		}
		// Strip leading format indicator when scanner dropped delimiter right after "[)>".
		for _, fmtCode := range []string{"06", "05", "12"} {
			if sec == fmtCode {
				sec = ""
				break
			}
			if strings.HasPrefix(sec, fmtCode+"\x1d") {
				sec = strings.TrimPrefix(sec, fmtCode+"\x1d")
				break
			}
		}
		if sec == "" {
			continue
		}
		parts := strings.Split(sec, "\x1d")
		tokens = append(tokens, parts...)
	}

	fields := make(map[string]string)
	var ordered []struct {
		id  string
		val string
	}

	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" || tok == "06" || tok == "05" || tok == "12" {
			continue
		}
		id, val, ok := splitDataIdentifier(tok)
		if !ok {
			continue
		}
		ordered = append(ordered, struct {
			id  string
			val string
		}{id: id, val: val})
		// Keep first non-empty occurrence unless overwriting an empty slot.
		if existing, exists := fields[id]; !exists || existing == "" {
			fields[id] = val
		}
	}

	if len(ordered) == 0 {
		return ParsedBarcode{}, false
	}

	res := ParsedBarcode{
		RawBarcode: raw,
		Format:     FormatTIA1063,
	}

	res.PartNumber = fields["1P"]
	res.Origin = fields["4L"]
	res.Quantity = parseQuantity(fields["Q"])

	// Customer / Supplier PN: prefer DigiKey 30P (Supplier PN) when present, else P.
	if pn30P := fields["30P"]; pn30P != "" {
		res.SupplierPartNumber = pn30P
	} else {
		res.SupplierPartNumber = fields["P"]
	}

	// Order / PO number: K (Customer PO) or 1K (Sales Order).
	if k := fields["K"]; k != "" && !strings.EqualFold(k, "N/A") {
		res.OrderNumber = k
	} else if k1 := fields["1K"]; k1 != "" {
		res.OrderNumber = k1
	}

	// Manufacturer: prefer 1V, fall back to V if V isn't purely a distributor marker.
	v1 := fields["1V"]
	v := fields["V"]
	if v1 != "" {
		res.Manufacturer = v1
	} else if v != "" && !isDistributorMarker(v) {
		res.Manufacturer = v
	}

	res.Supplier = detectTIA1063Supplier(fields, res.SupplierPartNumber, raw)
	return res, true
}

// splitDataIdentifier splits an ANSI MH10.8.2 token "<digits><LETTER><value>"
// into its data identifier and value.
func splitDataIdentifier(tok string) (string, string, bool) {
	i := 0
	for i < len(tok) && tok[i] >= '0' && tok[i] <= '9' {
		i++
	}
	if i >= len(tok) {
		return "", "", false
	}
	letter := tok[i]
	if letter < 'A' || letter > 'Z' {
		return "", "", false
	}
	return tok[:i+1], strings.TrimSpace(tok[i+1:]), true
}

func isDistributorMarker(v string) bool {
	u := strings.ToUpper(strings.TrimSpace(v))
	return strings.Contains(u, "DIGIKEY") ||
		strings.Contains(u, "DIGI-KEY") ||
		strings.Contains(u, "MOUSER")
}

func detectTIA1063Supplier(fields map[string]string, supplierPN, raw string) string {
	upperRaw := strings.ToUpper(raw)

	// 1. Explicit vendor identification in V, 1V, or payload.
	for _, candidate := range []string{fields["V"], fields["1V"], upperRaw} {
		u := strings.ToUpper(candidate)
		if strings.Contains(u, "DIGI-KEY") || strings.Contains(u, "DIGIKEY") {
			return "DigiKey"
		}
		if strings.Contains(u, "MOUSER") {
			return "Mouser"
		}
	}

	// 2. DigiKey structural markers: 30P, 11ZPICK, -ND suffix, or 11Z+12Z/13Z/20Z block.
	if fields["30P"] != "" || strings.EqualFold(fields["11Z"], "PICK") {
		return "DigiKey"
	}
	for _, pn := range []string{supplierPN, fields["P"], fields["30P"]} {
		if strings.HasSuffix(strings.ToUpper(strings.TrimSpace(pn)), "-ND") {
			return "DigiKey"
		}
	}
	dkZCount := 0
	for _, zKey := range []string{"11Z", "12Z", "13Z", "20Z"} {
		if _, ok := fields[zKey]; ok {
			dkZCount++
		}
	}
	if dkZCount >= 2 {
		return "DigiKey"
	}

	// 3. Mouser structural markers: 14K line item or NNN-... Mouser PN format.
	if _, has14K := fields["14K"]; has14K {
		return "Mouser"
	}
	if supplierPN != "" && mouserPNPattern.MatchString(supplierPN) {
		return "Mouser"
	}

	return ""
}

func parseLCSCObject(raw string) (ParsedBarcode, bool) {
	// Try strict JSON first.
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		kv := make(map[string]string, len(obj))
		for k, v := range obj {
			kv[strings.ToLower(strings.TrimSpace(k))] = stringifyJSONValue(v)
		}
		return buildLCSCResult(kv, raw, FormatLCSCJSON)
	}

	// Fall back to LCSC relaxed object format with unquoted keys/values:
	// e.g. {pbn:PICK123,on:WM123,pc:C14663,pm:SN74HC595DR,qty:50}
	inner := strings.TrimSpace(raw)
	if len(inner) < 2 || inner[0] != '{' || inner[len(inner)-1] != '}' {
		return ParsedBarcode{}, false
	}
	inner = strings.TrimSpace(inner[1 : len(inner)-1])
	if inner == "" {
		return ParsedBarcode{}, false
	}

	kv, ok := parseDelimitedKVPairs(inner, ',')
	if !ok {
		return ParsedBarcode{}, false
	}
	return buildLCSCResult(kv, raw, FormatLCSCJSON)
}

func parseLCSCKV(raw string) (ParsedBarcode, bool) {
	sep := byte(',')
	if !strings.ContainsRune(raw, ',') && strings.ContainsRune(raw, ';') {
		sep = ';'
	}
	kv, ok := parseDelimitedKVPairs(raw, sep)
	if !ok {
		return ParsedBarcode{}, false
	}
	return buildLCSCResult(kv, raw, FormatLCSCKV)
}

func parseDelimitedKVPairs(input string, sep byte) (map[string]string, bool) {
	pairs := splitKVPairs(input, sep)
	if len(pairs) == 0 {
		return nil, false
	}

	kv := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		colon := strings.IndexByte(pair, ':')
		if colon <= 0 {
			return nil, false
		}
		k := strings.ToLower(unquoteToken(pair[:colon]))
		v := unquoteToken(pair[colon+1:])
		if k == "" {
			return nil, false
		}
		kv[k] = v
	}
	return kv, true
}

// splitKVPairs splits on sep outside quotes, and for commas only splits when
// the upcoming segment starts with "<key>:" so unquoted commas inside values
// like "10k, 1%" do not corrupt subsequent fields.
func splitKVPairs(s string, sep byte) []string {
	var out []string
	var quote byte
	escaped := false
	start := 0

	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			if c == '\\' {
				escaped = true
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == sep {
			rest := strings.TrimSpace(s[i+1:])
			if sep != ',' || rest == "" || kvIdentPattern.MatchString(rest) {
				part := strings.TrimSpace(s[start:i])
				if part != "" {
					out = append(out, part)
				}
				start = i + 1
			}
		}
	}

	if tail := strings.TrimSpace(s[start:]); tail != "" {
		out = append(out, tail)
	}
	return out
}

func unquoteToken(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			inner := s[1 : len(s)-1]
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			inner = strings.ReplaceAll(inner, `\'`, `'`)
			inner = strings.ReplaceAll(inner, `\\`, `\`)
			return strings.TrimSpace(inner)
		}
	}
	return s
}

func stringifyJSONValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

func buildLCSCResult(kv map[string]string, raw, format string) (ParsedBarcode, bool) {
	pc := kv["pc"]
	pm := kv["pm"]
	pbn := kv["pbn"]
	on := kv["on"]
	qtyRaw := kv["qty"]
	if qtyRaw == "" {
		qtyRaw = kv["q"]
	}

	// Verify this actually looks like an LCSC payload rather than arbitrary JSON/KV text.
	hasLCSCKeys := pc != "" ||
		(pbn != "" && (on != "" || pm != "" || qtyRaw != "")) ||
		(pm != "" && qtyRaw != "")
	if !hasLCSCKeys {
		return ParsedBarcode{}, false
	}

	// For non-JSON KV strings, avoid misclassifying random single "foo:bar" 1D codes.
	if format == FormatLCSCKV && len(kv) < 2 && !isLCSCCode(pc) {
		return ParsedBarcode{}, false
	}

	partNumber := pm
	if partNumber == "" {
		partNumber = pc
	}

	mfr := kv["mc"]
	if mfr == "" {
		mfr = kv["mfr"]
	}
	if mfr == "" {
		mfr = kv["manufacturer"]
	}
	if mfr == "" {
		mfr = kv["brand"]
	}

	return ParsedBarcode{
		Supplier:           "LCSC",
		PartNumber:         partNumber,
		SupplierPartNumber: pc,
		Manufacturer:       mfr,
		Quantity:           parseQuantity(qtyRaw),
		OrderNumber:        on,
		RawBarcode:         raw,
		Format:             format,
	}, true
}

func isLCSCCode(s string) bool {
	if len(s) < 2 || (s[0] != 'C' && s[0] != 'c') {
		return false
	}
	for _, r := range s[1:] {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func parseQuantity(raw string) int {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n > 0 {
			return n
		}
		return 0
	}
	// Accept clean whole-number float strings such as "50.0" or "50 pcs".
	if fields := strings.Fields(s); len(fields) > 0 {
		first := fields[0]
		if n, err := strconv.Atoi(first); err == nil && n > 0 {
			return n
		}
		if f, err := strconv.ParseFloat(first, 64); err == nil && f > 0 && f == float64(int(f)) {
			return int(f)
		}
	}
	return 0
}
