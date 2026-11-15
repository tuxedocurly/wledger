package wled

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	CacheDuration   = 5 * time.Second
	DefaultHTTPPort = int64(80)
	DefaultWipeLEDs = 5000
)

var blockedSchemes = []string{
	"http:", "https:", "file:", "ftp:", "ftps:", "ws:", "wss:",
	"gopher:", "data:", "javascript:", "ldap:", "ldaps:",
}

// DeviceInfo represents the subset of WLED /json/info fields tracked by WLedger.
type DeviceInfo struct {
	Name string `json:"name"`
	Ver  string `json:"ver"`
	Arch string `json:"arch"`
	MAC  string `json:"mac"`
	Leds struct {
		Count int64 `json:"count"`
	} `json:"leds"`
}

// WLEDSnapshot captures a controller's WLED state prior to a Locate session.
type WLEDSnapshot struct {
	State      map[string]any
	SegmentIDs []int
	LedCount   int
}

// Client handles low-level WLED JSON API communication.
type Client struct {
	httpClient *http.Client
	cache      sync.Map
}

type cachedResult struct {
	online    bool
	timestamp time.Time
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
	}
}

// NewClientWithHTTPClient creates a WLED Client using the supplied *http.Client.
func NewClientWithHTTPClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		return NewClient()
	}
	return &Client{
		httpClient: httpClient,
	}
}

// NormalizeMAC converts WLED MAC formats (e.g. "AABBCCDDEEFF", "AA-BB-...") to lowercase colon-separated "aa:bb:cc:dd:ee:ff".
func NormalizeMAC(mac string) string {
	clean := strings.TrimSpace(mac)
	clean = strings.ReplaceAll(clean, ":", "")
	clean = strings.ReplaceAll(clean, "-", "")
	clean = strings.ToLower(clean)
	if len(clean) != 12 {
		return ""
	}
	for i := 0; i < 12; i++ {
		c := clean[i]
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			return ""
		}
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		clean[0:2], clean[2:4], clean[4:6], clean[6:8], clean[8:10], clean[10:12])
}

// ValidateEndpoint validates and resolves a controller's host/IP and HTTP port, preventing SSRF/URL injection.
// It rejects URL schemes, paths, queries, fragments, userinfo, whitespace, 0.0.0.0, ::, multicast/broadcast IPs,
// and out-of-range ports, while supporting clean hostnames, IPv4/IPv6, and optional host:port notation.
func ValidateEndpoint(rawIP string, port sql.NullInt64) (string, int64, error) {
	if rawIP == "" {
		return "", 0, errors.New("controller IP/hostname cannot be empty")
	}
	if strings.ContainsAny(rawIP, " \t\r\n\v\f") {
		return "", 0, errors.New("controller IP/hostname cannot contain whitespace")
	}
	if strings.Contains(rawIP, "://") {
		return "", 0, errors.New("controller IP/hostname cannot include a URL scheme")
	}
	lowerRaw := strings.ToLower(rawIP)
	for _, scheme := range blockedSchemes {
		if strings.HasPrefix(lowerRaw, scheme) {
			return "", 0, errors.New("controller IP/hostname cannot include a URL scheme")
		}
	}
	if strings.ContainsAny(rawIP, `/\?#@%&*;"'<>!$`+"`") {
		return "", 0, errors.New("controller IP/hostname contains disallowed characters")
	}

	if port.Valid && (port.Int64 < 1 || port.Int64 > 65535) {
		return "", 0, fmt.Errorf("invalid port %d: must be between 1 and 65535", port.Int64)
	}

	host, inlinePort, hasInlinePort, err := parseHostAndOptionalPort(rawIP)
	if err != nil {
		return "", 0, err
	}
	if err := validateHost(host); err != nil {
		return "", 0, err
	}

	resolvedPort := DefaultHTTPPort
	if hasInlinePort {
		if port.Valid && port.Int64 != DefaultHTTPPort && port.Int64 != inlinePort {
			return "", 0, fmt.Errorf("conflicting ports: address port %d vs explicit port %d", inlinePort, port.Int64)
		}
		resolvedPort = inlinePort
	} else if port.Valid {
		resolvedPort = port.Int64
	}

	return host, resolvedPort, nil
}

func parseHostAndOptionalPort(raw string) (host string, port int64, hasPort bool, err error) {
	if ip := net.ParseIP(raw); ip != nil {
		return raw, 0, false, nil
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		inner := raw[1 : len(raw)-1]
		if ip := net.ParseIP(inner); ip != nil {
			return inner, 0, false, nil
		}
		return "", 0, false, errors.New("invalid bracketed IPv6 address")
	}
	if strings.Contains(raw, ":") {
		h, pStr, splitErr := net.SplitHostPort(raw)
		if splitErr != nil {
			return "", 0, false, fmt.Errorf("invalid host/port %q: %w", raw, splitErr)
		}
		if h == "" || pStr == "" {
			return "", 0, false, errors.New("host and port must both be non-empty")
		}
		for i := 0; i < len(pStr); i++ {
			if pStr[i] < '0' || pStr[i] > '9' {
				return "", 0, false, fmt.Errorf("invalid port %q", pStr)
			}
		}
		p, parseErr := strconv.ParseInt(pStr, 10, 64)
		if parseErr != nil || p < 1 || p > 65535 {
			return "", 0, false, fmt.Errorf("invalid port %q: must be between 1 and 65535", pStr)
		}
		return h, p, true, nil
	}
	return raw, 0, false, nil
}

func validateHost(host string) error {
	if host == "" {
		return errors.New("host cannot be empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() {
			return fmt.Errorf("unspecified IP address %q is not allowed", host)
		}
		if ip.IsMulticast() {
			return fmt.Errorf("multicast IP address %q is not allowed", host)
		}
		if ip.Equal(net.IPv4bcast) {
			return fmt.Errorf("broadcast IP address %q is not allowed", host)
		}
		return nil
	}

	if strings.Contains(host, ":") {
		return fmt.Errorf("invalid IPv6/host %q", host)
	}
	if len(host) > 253 {
		return errors.New("hostname exceeds maximum length of 253 characters")
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return fmt.Errorf("invalid hostname %q", host)
	}

	labels := strings.Split(host, ".")
	allNumericOrHex := true
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("invalid hostname label %q", label)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("hostname label %q cannot begin or end with '-'", label)
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			valid := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-'
			if !valid {
				return fmt.Errorf("hostname contains invalid character %q", ch)
			}
		}
		if !isNumericOrHexLiteral(label) {
			allNumericOrHex = false
		}
	}
	if allNumericOrHex {
		return fmt.Errorf("obfuscated numeric/hex IP hostname %q is not allowed", host)
	}
	return nil
}

func isNumericOrHexLiteral(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(s), "0x") && len(s) > 2 {
		for i := 2; i < len(s); i++ {
			c := s[i]
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func buildEndpointBaseURL(rawIP string, port sql.NullInt64) (string, string, error) {
	host, resolvedPort, err := ValidateEndpoint(rawIP, port)
	if err != nil {
		return "", "", err
	}
	hostPort := net.JoinHostPort(host, strconv.FormatInt(resolvedPort, 10))
	return "http://" + hostPort, hostPort, nil
}

// Ping checks if a controller is online (using optional host:port or default port 80).
func (c *Client) Ping(ctx context.Context, ip string) (bool, error) {
	return c.PingWithPort(ctx, ip, sql.NullInt64{})
}

// PingWithPort checks if a controller is online on the resolved host and port.
func (c *Client) PingWithPort(ctx context.Context, ip string, port sql.NullInt64) (bool, error) {
	baseURL, cacheKey, err := buildEndpointBaseURL(ip, port)
	if err != nil {
		return false, err
	}

	if val, ok := c.cache.Load(cacheKey); ok {
		entry := val.(cachedResult)
		if time.Since(entry.timestamp) < CacheDuration {
			return entry.online, nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/json/info", nil)
	if err != nil {
		return false, err
	}

	resp, err := c.httpClient.Do(req)
	isOnline := false
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			isOnline = true
		}
	}

	c.cache.Store(cacheKey, cachedResult{
		online:    isOnline,
		timestamp: time.Now(),
	})

	return isOnline, nil
}

// GetInfoWithPort fetches and decodes WLED /json/info for the controller.
func (c *Client) GetInfoWithPort(ctx context.Context, ip string, port sql.NullInt64) (DeviceInfo, error) {
	baseURL, cacheKey, err := buildEndpointBaseURL(ip, port)
	if err != nil {
		return DeviceInfo{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/json/info", nil)
	if err != nil {
		return DeviceInfo{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.cache.Store(cacheKey, cachedResult{online: false, timestamp: time.Now()})
		return DeviceInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.cache.Store(cacheKey, cachedResult{online: false, timestamp: time.Now()})
		return DeviceInfo{}, fmt.Errorf("wled /json/info returned status %d", resp.StatusCode)
	}

	var info DeviceInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return DeviceInfo{}, fmt.Errorf("failed to decode /json/info: %w", err)
	}
	info.MAC = NormalizeMAC(info.MAC)

	c.cache.Store(cacheKey, cachedResult{online: true, timestamp: time.Now()})
	return info, nil
}

// GetSnapshotWithPort fetches /json and extracts the controller state, segment IDs, and LED count.
// Returns (nil, nil) if the endpoint returns an empty/non-JSON response (e.g., minimal test doubles).
func (c *Client) GetSnapshotWithPort(ctx context.Context, ip string, port sql.NullInt64) (*WLEDSnapshot, error) {
	baseURL, _, err := buildEndpointBaseURL(ip, port)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/json", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("wled /json returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}

	var raw struct {
		State map[string]any `json:"state"`
		Info  *DeviceInfo    `json:"info"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil
	}
	if raw.State == nil {
		return nil, nil
	}

	snap := &WLEDSnapshot{
		State: raw.State,
	}
	if raw.Info != nil {
		snap.LedCount = int(raw.Info.Leds.Count)
	}
	snap.SegmentIDs = extractSegmentIDs(raw.State)
	return snap, nil
}

func extractSegmentIDs(state map[string]any) []int {
	rawSegs, ok := state["seg"]
	if !ok || rawSegs == nil {
		return nil
	}
	slice, ok := rawSegs.([]any)
	if !ok {
		return nil
	}
	var ids []int
	seen := make(map[int]bool)
	for idx, item := range slice {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		segID := idx
		if idVal, hasID := m["id"]; hasID {
			if f, ok := idVal.(float64); ok {
				segID = int(f)
			}
		}
		if !seen[segID] {
			seen[segID] = true
			ids = append(ids, segID)
		}
	}
	sort.Ints(ids)
	return ids
}

// SetState updates the WLED state with a custom payload on default/inline port.
func (c *Client) SetState(ctx context.Context, ip string, payload map[string]any) error {
	return c.SetStateWithPort(ctx, ip, sql.NullInt64{}, payload)
}

// SetStateWithPort updates the WLED state via POST /json/state using the validated endpoint.
func (c *Client) SetStateWithPort(ctx context.Context, ip string, port sql.NullInt64, payload map[string]any) error {
	baseURL, _, err := buildEndpointBaseURL(ip, port)
	if err != nil {
		return err
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/json/state", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("wled returned status: %d", resp.StatusCode)
	}
	return nil
}

// LightUp uses the individual LED ('i') API to light up a single range.
func (c *Client) LightUp(ctx context.Context, ip string, segmentID int, index int, count int, hexColor string) error {
	return c.LightUpWithPort(ctx, ip, sql.NullInt64{}, segmentID, index, count, hexColor)
}

// LightUpWithPort illuminates a single range on the specified controller port.
func (c *Client) LightUpWithPort(ctx context.Context, ip string, port sql.NullInt64, segmentID int, index int, count int, hexColor string) error {
	if count < 1 {
		count = 1
	}
	rgb, err := HexToRGBArray(hexColor)
	if err != nil {
		rgb = [3]byte{255, 255, 255}
	}
	return c.LightRangesWithPort(ctx, ip, port, 0, []int{segmentID}, map[int][]LEDRange{
		segmentID: {
			{Start: index, Count: count, RGB: rgb},
		},
	})
}

// LightRangesWithPort batches all located LED ranges per segment into one /json/state payload,
// forces solid black background ("fx": 0) on active segments to avoid orange bleed, and turns off non-located segments.
func (c *Client) LightRangesWithPort(ctx context.Context, ip string, port sql.NullInt64, totalLEDs int, knownSegIDs []int, bySeg map[int][]LEDRange) error {
	wipeCount := totalLEDs
	for _, ranges := range bySeg {
		for _, r := range ranges {
			if end := r.Start + r.Count; end > wipeCount {
				wipeCount = end
			}
		}
	}
	if wipeCount <= 0 {
		wipeCount = DefaultWipeLEDs
	}

	segSet := make(map[int]bool)
	for _, id := range knownSegIDs {
		segSet[id] = true
	}
	for id := range bySeg {
		segSet[id] = true
	}
	if len(segSet) == 0 {
		segSet[0] = true
	}

	allSegIDs := make([]int, 0, len(segSet))
	for id := range segSet {
		allSegIDs = append(allSegIDs, id)
	}
	sort.Ints(allSegIDs)

	segPayloads := make([]map[string]any, 0, len(allSegIDs))
	for _, segID := range allSegIDs {
		ranges := bySeg[segID]
		if len(ranges) == 0 {
			segPayloads = append(segPayloads, map[string]any{
				"id":  segID,
				"on":  false,
				"fx":  0,
				"col": [][]int{{0, 0, 0}},
				"i":   []any{0, wipeCount, []int{0, 0, 0}},
			})
			continue
		}

		// Wipe the segment black first so non-located pixels do not bleed WLED boot orange.
		ind := make([]any, 0, 3+len(ranges)*3)
		ind = append(ind, 0, wipeCount, []int{0, 0, 0})
		for _, r := range ranges {
			if r.Count < 1 {
				continue
			}
			ind = append(ind, r.Start, r.Start+r.Count, []int{int(r.RGB[0]), int(r.RGB[1]), int(r.RGB[2])})
		}

		segPayloads = append(segPayloads, map[string]any{
			"id":  segID,
			"on":  true,
			"bri": 255,
			"fx":  0,
			"col": [][]int{{0, 0, 0}},
			"i":   ind,
		})
	}

	payload := map[string]any{
		"on":   true,
		"bri":  255,
		"live": false,
		"tt":   0,
		"seg":  segPayloads,
	}
	return c.SetStateWithPort(ctx, ip, port, payload)
}

// RestoreStateWithPort reapplies a pre-locate WLED snapshot, unfreezing segments when appropriate.
func (c *Client) RestoreStateWithPort(ctx context.Context, ip string, port sql.NullInt64, snap *WLEDSnapshot) error {
	if snap == nil || snap.State == nil {
		return nil
	}
	raw, err := json.Marshal(snap.State)
	if err != nil {
		return err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}

	payload["live"] = false
	payload["tt"] = 0

	if rawSegs, ok := payload["seg"].([]any); ok {
		for _, s := range rawSegs {
			m, ok := s.(map[string]any)
			if !ok {
				continue
			}
			// WLED's individual-LED API sets frz=true automatically; clear it on restore unless originally frozen.
			if frz, _ := m["frz"].(bool); !frz {
				m["frz"] = false
			}
		}
	}

	return c.SetStateWithPort(ctx, ip, port, payload)
}

// Clear resets segment 0 to black and powers the controller off.
func (c *Client) Clear(ctx context.Context, ip string) error {
	return c.ClearWithPort(ctx, ip, sql.NullInt64{})
}

// ClearWithPort clears segment 0 on the given port and turns the controller off.
func (c *Client) ClearWithPort(ctx context.Context, ip string, port sql.NullInt64) error {
	return c.ClearSegmentsWithPort(ctx, ip, port, 0, nil)
}

// ClearSegmentsWithPort wipes all specified segments (always including segment 0) to black,
// unfreezes individual LEDs, and sends {"on": false} so WLED PSU relays power down.
func (c *Client) ClearSegmentsWithPort(ctx context.Context, ip string, port sql.NullInt64, ledCount int, segmentIDs []int) error {
	wipeCount := ledCount
	if wipeCount <= 0 {
		wipeCount = DefaultWipeLEDs
	}

	segSet := map[int]bool{0: true}
	for _, id := range segmentIDs {
		segSet[id] = true
	}
	allSegIDs := make([]int, 0, len(segSet))
	for id := range segSet {
		allSegIDs = append(allSegIDs, id)
	}
	sort.Ints(allSegIDs)

	segPayloads := make([]map[string]any, 0, len(allSegIDs))
	for _, id := range allSegIDs {
		segPayloads = append(segPayloads, map[string]any{
			"id":  id,
			"on":  false,
			"frz": false,
			"fx":  0,
			"col": [][]int{{0, 0, 0}},
			"i":   []any{0, wipeCount, []int{0, 0, 0}},
		})
	}

	payload := map[string]any{
		"on":   false,
		"live": false,
		"tt":   0,
		"seg":  segPayloads,
	}
	return c.SetStateWithPort(ctx, ip, port, payload)
}

// HexToRGB converts a hex string (#RRGGBB or RRGGBB) to an [r, g, b] int slice.
func HexToRGB(hex string) ([]int, error) {
	rgb, err := HexToRGBArray(hex)
	if err != nil {
		return nil, err
	}
	return []int{int(rgb[0]), int(rgb[1]), int(rgb[2])}, nil
}

// HexToRGBArray converts a hex string (#RRGGBB or RRGGBB) to a [3]byte array.
func HexToRGBArray(hex string) ([3]byte, error) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 {
		return [3]byte{}, fmt.Errorf("invalid hex length")
	}

	val, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return [3]byte{}, err
	}

	return [3]byte{
		byte(val >> 16),
		byte((val >> 8) & 0xFF),
		byte(val & 0xFF),
	}, nil
}
