package wled

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// MDNSMulticastAddr is the standard mDNS IPv4 multicast destination.
	MDNSMulticastAddr = "224.0.0.251:5353"
	// WLEDServiceName is the DNS-SD service type advertised by WLED firmware.
	WLEDServiceName = "_wled._tcp.local."

	dnsTypeA    uint16 = 1
	dnsTypePTR  uint16 = 12
	dnsTypeTXT  uint16 = 16
	dnsTypeAAAA uint16 = 28
	dnsTypeSRV  uint16 = 33

	dnsClassINUnicast uint16 = 0x8001 // IN class with QU (unicast-response) bit set
)

// SRVRecord represents an mDNS SRV answer.
type SRVRecord struct {
	Port   uint16
	Target string
}

// MDNSResponseData holds parsed records from an mDNS response packet.
type MDNSResponseData struct {
	PTR  map[string][]string
	SRV  map[string]SRVRecord
	A    map[string][]string
	AAAA map[string][]string
	TXT  map[string][]string
}

// MDNSCandidate represents an unverified host/IP/port discovered via mDNS.
type MDNSCandidate struct {
	InstanceName string
	Host         string
	IP           string
	Port         int64
}

// DiscoveredDevice represents a LAN device verified to respond with valid WLED /json/info.
type DiscoveredDevice struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	IP       string `json:"ip"`
	Port     int64  `json:"port"`
	MAC      string `json:"mac"`
	LedCount int64  `json:"led_count"`
	Version  string `json:"version"`
	Arch     string `json:"arch"`
}

func newMDNSResponseData() *MDNSResponseData {
	return &MDNSResponseData{
		PTR:  make(map[string][]string),
		SRV:  make(map[string]SRVRecord),
		A:    make(map[string][]string),
		AAAA: make(map[string][]string),
		TXT:  make(map[string][]string),
	}
}

func normalizeDNSName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// BuildMDNSQuery constructs a DNS-SD PTR query packet for the given service (e.g. "_wled._tcp.local.").
func BuildMDNSQuery(service string) ([]byte, error) {
	clean := normalizeDNSName(service)
	if clean == "" {
		return nil, errors.New("service name cannot be empty")
	}

	var buf []byte
	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[4:6], 1) // QDCOUNT = 1
	buf = append(buf, header...)

	for _, label := range strings.Split(clean, ".") {
		if len(label) == 0 || len(label) > 63 {
			return nil, fmt.Errorf("invalid DNS label %q", label)
		}
		buf = append(buf, byte(len(label)))
		buf = append(buf, label...)
	}
	buf = append(buf, 0) // Root terminator

	tail := make([]byte, 4)
	binary.BigEndian.PutUint16(tail[0:2], dnsTypePTR)
	binary.BigEndian.PutUint16(tail[2:4], dnsClassINUnicast)
	buf = append(buf, tail...)

	return buf, nil
}

// ParseMDNSResponse decodes a raw mDNS UDP packet into PTR, SRV, A, AAAA, and TXT records.
func ParseMDNSResponse(pkt []byte) (*MDNSResponseData, error) {
	if len(pkt) < 12 {
		return nil, errors.New("mDNS packet shorter than 12-byte DNS header")
	}

	qdCount := int(binary.BigEndian.Uint16(pkt[4:6]))
	anCount := int(binary.BigEndian.Uint16(pkt[6:8]))
	nsCount := int(binary.BigEndian.Uint16(pkt[8:10]))
	arCount := int(binary.BigEndian.Uint16(pkt[10:12]))

	offset := 12
	for i := 0; i < qdCount; i++ {
		_, next, err := readDNSName(pkt, offset)
		if err != nil {
			return nil, fmt.Errorf("failed to parse question name: %w", err)
		}
		if next+4 > len(pkt) {
			return nil, errors.New("truncated mDNS question section")
		}
		offset = next + 4
	}

	out := newMDNSResponseData()
	totalRR := anCount + nsCount + arCount
	for i := 0; i < totalRR; i++ {
		if offset >= len(pkt) {
			break
		}
		rrName, next, err := readDNSName(pkt, offset)
		if err != nil {
			return nil, fmt.Errorf("failed to parse resource record name: %w", err)
		}
		if next+10 > len(pkt) {
			return nil, errors.New("truncated mDNS resource record header")
		}

		rrType := binary.BigEndian.Uint16(pkt[next : next+2])
		rdLen := int(binary.BigEndian.Uint16(pkt[next+8 : next+10]))
		rdataOff := next + 10
		if rdataOff+rdLen > len(pkt) {
			return nil, errors.New("truncated mDNS resource record payload")
		}
		rdata := pkt[rdataOff : rdataOff+rdLen]
		key := normalizeDNSName(rrName)

		switch rrType {
		case dnsTypePTR:
			target, _, err := readDNSName(pkt, rdataOff)
			if err == nil && target != "" {
				out.PTR[key] = appendUniqueString(out.PTR[key], normalizeDNSName(target))
			}
		case dnsTypeSRV:
			if rdLen >= 7 {
				port := binary.BigEndian.Uint16(rdata[4:6])
				target, _, err := readDNSName(pkt, rdataOff+6)
				if err == nil && target != "" {
					out.SRV[key] = SRVRecord{Port: port, Target: normalizeDNSName(target)}
				}
			}
		case dnsTypeA:
			if rdLen == 4 {
				ipStr := net.IP(rdata).String()
				out.A[key] = appendUniqueString(out.A[key], ipStr)
			}
		case dnsTypeAAAA:
			if rdLen == 16 {
				ipStr := net.IP(rdata).String()
				out.AAAA[key] = appendUniqueString(out.AAAA[key], ipStr)
			}
		case dnsTypeTXT:
			txts := parseTXTRecord(rdata)
			if len(txts) > 0 {
				out.TXT[key] = append(out.TXT[key], txts...)
			}
		}

		offset = rdataOff + rdLen
	}

	return out, nil
}

func (d *MDNSResponseData) merge(other *MDNSResponseData) {
	if other == nil {
		return
	}
	for k, vals := range other.PTR {
		for _, v := range vals {
			d.PTR[k] = appendUniqueString(d.PTR[k], v)
		}
	}
	for k, v := range other.SRV {
		d.SRV[k] = v
	}
	for k, vals := range other.A {
		for _, v := range vals {
			d.A[k] = appendUniqueString(d.A[k], v)
		}
	}
	for k, vals := range other.AAAA {
		for _, v := range vals {
			d.AAAA[k] = appendUniqueString(d.AAAA[k], v)
		}
	}
	for k, vals := range other.TXT {
		d.TXT[k] = append(d.TXT[k], vals...)
	}
}

// ExtractCandidates correlates PTR -> SRV -> A/AAAA records into candidate endpoints.
func (d *MDNSResponseData) ExtractCandidates() []MDNSCandidate {
	if d == nil {
		return nil
	}

	var candidates []MDNSCandidate
	seen := make(map[string]bool)
	serviceKey := normalizeDNSName(WLEDServiceName)

	addCandidate := func(instName, hostName, ip string, port int64) {
		if port <= 0 {
			port = DefaultHTTPPort
		}
		if _, _, err := ValidateEndpoint(ip, sql.NullInt64{Int64: port, Valid: true}); err != nil {
			return
		}
		key := fmt.Sprintf("%s|%d", ip, port)
		if seen[key] {
			return
		}
		seen[key] = true

		display := strings.TrimSuffix(instName, "."+serviceKey)
		display = strings.TrimSuffix(display, ".")
		candidates = append(candidates, MDNSCandidate{
			InstanceName: display,
			Host:         hostName,
			IP:           ip,
			Port:         port,
		})
	}

	resolveHostIPs := func(instName, hostName string, port int64) {
		for _, ip := range d.A[hostName] {
			addCandidate(instName, hostName, ip, port)
		}
		// Fallback to IPv6 only if no IPv4 A record was provided for this host.
		if len(d.A[hostName]) == 0 {
			for _, ip := range d.AAAA[hostName] {
				addCandidate(instName, hostName, ip, port)
			}
		}
	}

	for _, inst := range d.PTR[serviceKey] {
		if srv, ok := d.SRV[inst]; ok {
			resolveHostIPs(inst, srv.Target, int64(srv.Port))
			continue
		}
		// Some responders omit SRV or split it; check direct A records under inst if any.
		resolveHostIPs(inst, inst, DefaultHTTPPort)
	}

	// Fallback: include SRV records matching _wled._tcp.local even when PTR arrived separately or was missed.
	for inst, srv := range d.SRV {
		if strings.HasSuffix(inst, "."+serviceKey) {
			resolveHostIPs(inst, srv.Target, int64(srv.Port))
		}
	}

	return candidates
}

// VerifyCandidates probes candidate endpoints via /json/info in parallel and keeps only valid WLED nodes.
func VerifyCandidates(ctx context.Context, client *Client, candidates []MDNSCandidate) []DiscoveredDevice {
	if len(candidates) == 0 || client == nil {
		return nil
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results []DiscoveredDevice
	)

	for _, cand := range candidates {
		wg.Add(1)
		go func(c MDNSCandidate) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancel()

			info, err := client.GetInfoWithPort(probeCtx, c.IP, sql.NullInt64{Int64: c.Port, Valid: true})
			if err != nil {
				return
			}

			name := strings.TrimSpace(info.Name)
			if name == "" {
				name = c.InstanceName
			}
			if name == "" {
				name = c.Host
			}

			dev := DiscoveredDevice{
				Name:     name,
				Host:     c.Host,
				IP:       c.IP,
				Port:     c.Port,
				MAC:      info.MAC,
				LedCount: info.Leds.Count,
				Version:  info.Ver,
				Arch:     info.Arch,
			}

			mu.Lock()
			results = append(results, dev)
			mu.Unlock()
		}(cand)
	}
	wg.Wait()

	// Deduplicate by MAC if available, else IP:port.
	dedup := make(map[string]DiscoveredDevice, len(results))
	var out []DiscoveredDevice
	for _, dev := range results {
		key := dev.MAC
		if key == "" {
			key = fmt.Sprintf("%s:%d", dev.IP, dev.Port)
		}
		if _, exists := dedup[key]; !exists {
			dedup[key] = dev
		}
	}
	for _, dev := range dedup {
		out = append(out, dev)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].IP < out[j].IP
	})
	return out
}

// Discover broadcasts an mDNS PTR query for _wled._tcp.local. and verifies responding hosts via /json/info.
func Discover(ctx context.Context, client *Client, timeout time.Duration) ([]DiscoveredDevice, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	query, err := BuildMDNSQuery(WLEDServiceName)
	if err != nil {
		return nil, err
	}

	mcastAddr, err := net.ResolveUDPAddr("udp4", MDNSMulticastAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve mDNS multicast address: %w", err)
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("failed to open UDP socket for mDNS discovery: %w", err)
	}
	defer conn.Close()

	if _, err := conn.WriteToUDP(query, mcastAddr); err != nil {
		return nil, fmt.Errorf("failed to send mDNS discovery query: %w", err)
	}

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetReadDeadline(deadline)

	combined := newMDNSResponseData()
	buf := make([]byte, 4096)
	for {
		if ctx.Err() != nil {
			break
		}
		n, _, readErr := conn.ReadFromUDP(buf)
		if readErr != nil {
			if netErr, ok := readErr.(net.Error); ok && netErr.Timeout() {
				break
			}
			break
		}
		parsed, parseErr := ParseMDNSResponse(buf[:n])
		if parseErr != nil {
			continue
		}
		combined.merge(parsed)
	}

	candidates := combined.ExtractCandidates()
	return VerifyCandidates(ctx, client, candidates), nil
}

func readDNSName(pkt []byte, offset int) (string, int, error) {
	var labels []string
	curr := offset
	nextOffset := -1
	jumps := 0

	for {
		if curr < 0 || curr >= len(pkt) {
			return "", 0, errors.New("DNS name out of bounds")
		}
		length := int(pkt[curr])
		if length == 0 {
			if nextOffset == -1 {
				nextOffset = curr + 1
			}
			break
		}
		if (length & 0xC0) == 0xC0 {
			if curr+1 >= len(pkt) {
				return "", 0, errors.New("truncated DNS compression pointer")
			}
			ptr := int(binary.BigEndian.Uint16(pkt[curr:curr+2]) & 0x3FFF)
			if ptr >= len(pkt) {
				return "", 0, errors.New("invalid DNS compression pointer target")
			}
			if nextOffset == -1 {
				nextOffset = curr + 2
			}
			curr = ptr
			jumps++
			if jumps > 32 {
				return "", 0, errors.New("DNS name compression pointer loop detected")
			}
			continue
		}
		if (length & 0xC0) != 0 {
			return "", 0, errors.New("unsupported DNS label encoding")
		}
		curr++
		if curr+length > len(pkt) {
			return "", 0, errors.New("DNS label exceeds packet bounds")
		}
		labels = append(labels, string(pkt[curr:curr+length]))
		curr += length
	}

	return strings.Join(labels, "."), nextOffset, nil
}

func parseTXTRecord(rdata []byte) []string {
	var entries []string
	for i := 0; i < len(rdata); {
		l := int(rdata[i])
		i++
		if i+l > len(rdata) {
			break
		}
		if l > 0 {
			entries = append(entries, string(rdata[i:i+l]))
		}
		i += l
	}
	return entries
}

func appendUniqueString(slice []string, val string) []string {
	for _, existing := range slice {
		if existing == val {
			return slice
		}
	}
	return append(slice, val)
}
