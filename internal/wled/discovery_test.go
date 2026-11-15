package wled

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
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
