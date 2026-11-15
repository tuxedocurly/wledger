package wled

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func encodeUncompressedDNSName(name string) []byte {
	var out []byte
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	out = append(out, 0)
	return out
}

func appendRR(buf []byte, nameBytes []byte, rrType, rrClass uint16, ttl uint32, rdata []byte) []byte {
	buf = append(buf, nameBytes...)
	meta := make([]byte, 10)
	binary.BigEndian.PutUint16(meta[0:2], rrType)
	binary.BigEndian.PutUint16(meta[2:4], rrClass)
	binary.BigEndian.PutUint32(meta[4:8], ttl)
	binary.BigEndian.PutUint16(meta[8:10], uint16(len(rdata)))
	buf = append(buf, meta...)
	buf = append(buf, rdata...)
	return buf
}

func TestBuildMDNSQueryAndParseResponseWithVerification(t *testing.T) {
	query, err := BuildMDNSQuery(WLEDServiceName)
	if err != nil {
		t.Fatalf("BuildMDNSQuery failed: %v", err)
	}
	if len(query) < 16 || binary.BigEndian.Uint16(query[4:6]) != 1 {
		t.Fatalf("unexpected mDNS query packet header")
	}

	// Spin up mock WLED HTTP server returning /json/info
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/info" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "Shelf-WLED",
			"ver":  "0.14.4",
			"arch": "esp32",
			"mac":  "AA:BB:CC:11:22:33",
			"leds": map[string]any{"count": 144},
		})
	}))
	defer srv.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	portNum, _ := strconv.ParseUint(portStr, 10, 16)

	// Build an mDNS response packet containing:
	// - PTR _wled._tcp.local -> wled-shelf._wled._tcp.local
	// - SRV wled-shelf._wled._tcp.local -> portNum, wled-shelf.local
	// - TXT wled-shelf._wled._tcp.local -> "mac=AABBCC112233"
	// - A   wled-shelf.local -> 127.0.0.1
	pkt := make([]byte, 12)
	binary.BigEndian.PutUint16(pkt[2:4], 0x8400) // Response + Authoritative
	binary.BigEndian.PutUint16(pkt[6:8], 4)      // 4 Answers

	serviceNameOffset := len(pkt)
	ptrRdata := encodeUncompressedDNSName("wled-shelf._wled._tcp.local")
	pkt = appendRR(pkt, encodeUncompressedDNSName("_wled._tcp.local"), dnsTypePTR, 1, 120, ptrRdata)

	// Instance name with DNS compression pointer pointing back to "_wled._tcp.local"
	var compressedInst []byte
	compressedInst = append(compressedInst, byte(len("wled-shelf")))
	compressedInst = append(compressedInst, "wled-shelf"...)
	ptrWord := uint16(0xC000 | serviceNameOffset)
	compressedInst = append(compressedInst, byte(ptrWord>>8), byte(ptrWord))

	var srvRdata []byte
	srvMeta := make([]byte, 6)
	binary.BigEndian.PutUint16(srvMeta[4:6], uint16(portNum))
	srvRdata = append(srvRdata, srvMeta...)
	srvRdata = append(srvRdata, encodeUncompressedDNSName("wled-shelf.local")...)
	pkt = appendRR(pkt, compressedInst, dnsTypeSRV, 1, 120, srvRdata)

	txtEntry := append([]byte{byte(len("mac=AABBCC112233"))}, "mac=AABBCC112233"...)
	pkt = appendRR(pkt, compressedInst, dnsTypeTXT, 1, 120, txtEntry)

	pkt = appendRR(pkt, encodeUncompressedDNSName("wled-shelf.local"), dnsTypeA, 1, 120, []byte{127, 0, 0, 1})

	parsed, err := ParseMDNSResponse(pkt)
	if err != nil {
		t.Fatalf("ParseMDNSResponse failed: %v", err)
	}

	candidates := parsed.ExtractCandidates()
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d: %+v", len(candidates), candidates)
	}
	if candidates[0].IP != "127.0.0.1" || candidates[0].Port != int64(portNum) {
		t.Errorf("unexpected candidate endpoint: %+v", candidates[0])
	}

	verified := VerifyCandidates(context.Background(), NewClient(), candidates)
	if len(verified) != 1 {
		t.Fatalf("expected 1 verified WLED device, got %d", len(verified))
	}
	if verified[0].MAC != "aa:bb:cc:11:22:33" || verified[0].LedCount != 144 || verified[0].Name != "Shelf-WLED" {
		t.Errorf("unexpected verified device info: %+v", verified[0])
	}
}

func TestParseMDNSResponse_RejectsCompressionLoop(t *testing.T) {
	// Crafted packet with self-referential compression pointer at offset 12.
	pkt := make([]byte, 14)
	binary.BigEndian.PutUint16(pkt[6:8], 1) // ANCOUNT = 1
	binary.BigEndian.PutUint16(pkt[12:14], 0xC000|12)

	if _, err := ParseMDNSResponse(pkt); err == nil {
		t.Error("expected error on DNS compression pointer loop")
	}
}

func startMockMDNSResponder(t *testing.T, respond func(qname string, qtype uint16) []byte) (string, func()) {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket failed: %v", err)
	}

	var (
		wg        sync.WaitGroup
		closeOnce sync.Once
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 2048)
		for {
			n, remoteAddr, readErr := pc.ReadFrom(buf)
			if readErr != nil {
				return
			}
			if n < 16 {
				continue
			}
			qname, next, nameErr := readDNSName(buf[:n], 12)
			if nameErr != nil || next+4 > n {
				continue
			}
			qtype := binary.BigEndian.Uint16(buf[next : next+2])
			if reply := respond(normalizeDNSName(qname), qtype); len(reply) > 0 {
				_, _ = pc.WriteTo(reply, remoteAddr)
			}
		}
	}()

	stop := func() {
		closeOnce.Do(func() {
			_ = pc.Close()
			wg.Wait()
		})
	}
	t.Cleanup(stop)
	return pc.LocalAddr().String(), stop
}

func buildMDNSARecordResponse(host string, ip net.IP) []byte {
	pkt := make([]byte, 12)
	binary.BigEndian.PutUint16(pkt[2:4], 0x8400) // Response + Authoritative
	binary.BigEndian.PutUint16(pkt[6:8], 1)      // 1 Answer

	nameBytes := encodeUncompressedDNSName(host)
	if ip4 := ip.To4(); ip4 != nil {
		return appendRR(pkt, nameBytes, dnsTypeA, dnsClassIN, 120, ip4)
	}
	if ip16 := ip.To16(); ip16 != nil {
		return appendRR(pkt, nameBytes, dnsTypeAAAA, dnsClassIN, 120, ip16)
	}
	return nil
}

func TestIsMDNSHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"wled-da8eec.local", true},
		{"WLED-DA8EEC.LOCAL.", true},
		{"192.168.1.10", false},
		{"::1", false},
		{"example.com", false},
		{".local", false},
		{"localhost", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := IsMDNSHost(tc.host); got != tc.want {
			t.Errorf("IsMDNSHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestBuildMDNSQuestion_AAndPTR(t *testing.T) {
	cases := []struct {
		name      string
		qtype     uint16
		qclass    uint16
		wantType  uint16
		wantClass uint16
	}{
		{"wled-da8eec.local", dnsTypeA, dnsClassINUnicast, dnsTypeA, dnsClassINUnicast},
		{"wled-da8eec.local", dnsTypeA, dnsClassIN, dnsTypeA, dnsClassIN},
		{WLEDServiceName, dnsTypePTR, dnsClassINUnicast, dnsTypePTR, dnsClassINUnicast},
		{WLEDServiceName, dnsTypePTR, dnsClassIN, dnsTypePTR, dnsClassIN},
	}

	for _, tc := range cases {
		pkt, err := BuildMDNSQuestion(tc.name, tc.qtype, tc.qclass)
		if err != nil {
			t.Fatalf("BuildMDNSQuestion(%q) failed: %v", tc.name, err)
		}
		if len(pkt) < 16 {
			t.Fatalf("unexpected short mDNS packet length %d", len(pkt))
		}
		if qd := binary.BigEndian.Uint16(pkt[4:6]); qd != 1 {
			t.Errorf("expected QDCOUNT=1 for ESP8266 compatibility, got %d", qd)
		}
		parsedName, next, err := readDNSName(pkt, 12)
		if err != nil {
			t.Fatalf("readDNSName failed: %v", err)
		}
		if parsedName != normalizeDNSName(tc.name) {
			t.Errorf("parsed question name = %q, want %q", parsedName, normalizeDNSName(tc.name))
		}
		if next+4 != len(pkt) {
			t.Fatalf("expected 4-byte tail after question name, got %d bytes", len(pkt)-next)
		}
		gotType := binary.BigEndian.Uint16(pkt[next : next+2])
		gotClass := binary.BigEndian.Uint16(pkt[next+2 : next+4])
		if gotType != tc.wantType || gotClass != tc.wantClass {
			t.Errorf("got qtype=%d qclass=0x%04x, want qtype=%d qclass=0x%04x", gotType, gotClass, tc.wantType, tc.wantClass)
		}
	}
}

func TestResolveMDNSHost_CacheHitAndInvalidation(t *testing.T) {
	const host = "wled-da8eec.local"
	t.Cleanup(func() { InvalidateMDNSHost(host) })
	InvalidateMDNSHost(host)

	var queries atomic.Int32
	responderAddr, stopResponder := startMockMDNSResponder(t, func(qname string, qtype uint16) []byte {
		if qname == host && qtype == dnsTypeA {
			queries.Add(1)
			return buildMDNSARecordResponse(host, net.ParseIP("127.0.0.1"))
		}
		return nil
	})

	ctx := context.Background()
	ip, fromCache, err := resolveMDNSHostAtAddr(ctx, "WLED-DA8EEC.local.", responderAddr, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("initial resolveMDNSHostAtAddr failed: %v", err)
	}
	if ip != "127.0.0.1" || fromCache {
		t.Fatalf("expected fresh 127.0.0.1 resolution, got ip=%q fromCache=%v", ip, fromCache)
	}
	if queries.Load() < 1 {
		t.Errorf("expected at least 1 mDNS query sent")
	}

	// Stop the responder completely: subsequent lookup must succeed from the 60s cache.
	stopResponder()
	beforeQueries := queries.Load()

	ip, fromCache, err = resolveMDNSHostAtAddr(ctx, host, responderAddr, 80*time.Millisecond)
	if err != nil {
		t.Fatalf("cached resolveMDNSHostAtAddr failed: %v", err)
	}
	if ip != "127.0.0.1" || !fromCache {
		t.Fatalf("expected cached hit for 127.0.0.1, got ip=%q fromCache=%v", ip, fromCache)
	}
	if queries.Load() != beforeQueries {
		t.Errorf("expected cache hit not to emit UDP query")
	}

	// Evicting cache should force live query against stopped responder and fail.
	InvalidateMDNSHost(host)
	if _, _, err := resolveMDNSHostAtAddr(ctx, host, responderAddr, 60*time.Millisecond); err == nil {
		t.Error("expected resolution failure after InvalidateMDNSHost when responder is offline")
	}
}

func findClosedLoopbackCandidate(t *testing.T, port int64) string {
	t.Helper()
	candidates := []string{"127.0.0.2", "::1"}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 {
				continue
			}
			addrs, _ := iface.Addrs()
			for _, a := range addrs {
				ipNet, ok := a.(*net.IPNet)
				if !ok || ipNet.IP == nil {
					continue
				}
				if ip4 := ipNet.IP.To4(); ip4 != nil && !ip4.IsLoopback() && !ip4.IsUnspecified() {
					candidates = append(candidates, ip4.String())
				}
			}
		}
	}

	portStr := strconv.FormatInt(port, 10)
	for _, cand := range candidates {
		if _, _, err := ValidateEndpoint(cand, sql.NullInt64{}); err != nil {
			continue
		}
		start := time.Now()
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(cand, portStr), 80*time.Millisecond)
		elapsed := time.Since(start)
		if conn != nil {
			_ = conn.Close()
			continue
		}
		if err != nil && elapsed < 70*time.Millisecond {
			return cand
		}
	}
	return ""
}

func TestClient_DotLocalHTTPAndStaleCacheRecovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/info" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "WLED-DA8EEC",
			"ver":  "0.14.4",
			"arch": "esp32",
			"mac":  "AA:BB:CC:DA:8E:EC",
			"leds": map[string]any{"count": 64},
		})
	}))
	defer srv.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	portNum, _ := strconv.ParseInt(portStr, 10, 64)
	port := sql.NullInt64{Int64: portNum, Valid: true}

	// Part 1: Direct .local HTTP resolution against mock mDNS responder.
	const liveHost = "wled-da8eec.local"
	t.Cleanup(func() { InvalidateMDNSHost(liveHost) })
	InvalidateMDNSHost(liveHost)

	mdnsAddr, _ := startMockMDNSResponder(t, func(qname string, qtype uint16) []byte {
		if qname == liveHost && qtype == dnsTypeA {
			return buildMDNSARecordResponse(liveHost, net.ParseIP("127.0.0.1"))
		}
		return nil
	})

	client := newClientWithMDNSAddr(mdnsAddr)
	info, err := client.GetInfoWithPort(context.Background(), liveHost, port)
	if err != nil {
		t.Fatalf("GetInfoWithPort(%q) failed: %v", liveHost, err)
	}
	if info.Name != "WLED-DA8EEC" || info.MAC != "aa:bb:cc:da:8e:ec" || info.Leds.Count != 64 {
		t.Errorf("unexpected DeviceInfo via .local HTTP client: %+v", info)
	}

	// Part 2: Stale cached IP fails TCP connect -> automatically invalidates cache and retries fresh IP.
	staleIP := findClosedLoopbackCandidate(t, portNum)
	if staleIP == "" {
		t.Skip("no fast-refusing secondary local IP available on this host")
	}

	const staleHost = "wled-stale-cache.local"
	t.Cleanup(func() { InvalidateMDNSHost(staleHost) })
	InvalidateMDNSHost(staleHost)

	var phase atomic.Int32
	staleResponderAddr, _ := startMockMDNSResponder(t, func(qname string, qtype uint16) []byte {
		if qname != staleHost || qtype != dnsTypeA {
			return nil
		}
		if phase.Load() == 0 {
			return buildMDNSARecordResponse(staleHost, net.ParseIP(staleIP))
		}
		return buildMDNSARecordResponse(staleHost, net.ParseIP("127.0.0.1"))
	})

	// Seed the 60s cache with staleIP.
	gotIP, _, err := resolveMDNSHostAtAddr(context.Background(), staleHost, staleResponderAddr, 500*time.Millisecond)
	if err != nil || gotIP != staleIP {
		t.Fatalf("failed to seed stale cache with %q: got=%q err=%v", staleIP, gotIP, err)
	}
	phase.Store(1)

	recoveryClient := newClientWithMDNSAddr(staleResponderAddr)
	recovered, err := recoveryClient.GetInfoWithPort(context.Background(), staleHost, port)
	if err != nil {
		t.Fatalf("expected stale cache recovery to succeed, got error: %v", err)
	}
	if recovered.Name != "WLED-DA8EEC" {
		t.Errorf("unexpected info after stale cache recovery: %+v", recovered)
	}
}

func TestUDP_DotLocalSendDNRGB(t *testing.T) {
	const host = "wled-udp-test.local"
	t.Cleanup(func() { InvalidateMDNSHost(host) })
	InvalidateMDNSHost(host)

	udpRecv, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket failed: %v", err)
	}
	defer udpRecv.Close()
	udpPort := int64(udpRecv.LocalAddr().(*net.UDPAddr).Port)

	mdnsAddr, _ := startMockMDNSResponder(t, func(qname string, qtype uint16) []byte {
		if qname == host && qtype == dnsTypeA {
			return buildMDNSARecordResponse(host, net.ParseIP("127.0.0.1"))
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := sendDNRGBAtMDNS(ctx, host, udpPort, 10, 0, []byte{255, 0, 128}, mdnsAddr); err != nil {
		t.Fatalf("sendDNRGBAtMDNS failed: %v", err)
	}

	_ = udpRecv.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 64)
	n, _, err := udpRecv.ReadFrom(buf)
	if err != nil {
		t.Fatalf("failed to read DNRGB packet: %v", err)
	}
	want := []byte{4, 10, 0, 0, 255, 0, 128}
	if !bytes.Equal(buf[:n], want) {
		t.Errorf("got DNRGB packet %v, want %v", buf[:n], want)
	}
}

func TestDiscoverAtAddr_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/info" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "WLED-Bench",
			"ver":  "0.14.4",
			"arch": "esp8266",
			"mac":  "AA:BB:CC:11:22:33",
			"leds": map[string]any{"count": 72},
		})
	}))
	defer srv.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	portNum, _ := strconv.ParseUint(portStr, 10, 16)

	mdnsAddr, _ := startMockMDNSResponder(t, func(qname string, qtype uint16) []byte {
		if qname != "_wled._tcp.local" || qtype != dnsTypePTR {
			return nil
		}
		pkt := make([]byte, 12)
		binary.BigEndian.PutUint16(pkt[2:4], 0x8400)
		binary.BigEndian.PutUint16(pkt[6:8], 3)

		instName := encodeUncompressedDNSName("wled-bench._wled._tcp.local")
		pkt = appendRR(pkt, encodeUncompressedDNSName("_wled._tcp.local"), dnsTypePTR, dnsClassIN, 120, instName)

		var srvRdata []byte
		srvMeta := make([]byte, 6)
		binary.BigEndian.PutUint16(srvMeta[4:6], uint16(portNum))
		srvRdata = append(srvRdata, srvMeta...)
		srvRdata = append(srvRdata, encodeUncompressedDNSName("wled-bench.local")...)
		pkt = appendRR(pkt, instName, dnsTypeSRV, dnsClassIN, 120, srvRdata)

		pkt = appendRR(pkt, encodeUncompressedDNSName("wled-bench.local"), dnsTypeA, dnsClassIN, 120, []byte{127, 0, 0, 1})
		return pkt
	})

	devices, err := discoverAtAddr(context.Background(), NewClient(), mdnsAddr, 250*time.Millisecond)
	if err != nil {
		t.Fatalf("discoverAtAddr failed: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 discovered device, got %d: %+v", len(devices), devices)
	}
	if devices[0].IP != "127.0.0.1" || devices[0].Port != int64(portNum) || devices[0].MAC != "aa:bb:cc:11:22:33" || devices[0].Name != "WLED-Bench" {
		t.Errorf("unexpected discovered device: %+v", devices[0])
	}
}
