package wled

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"time"
)

const (
	// DNRGBProtocolID identifies the WLED UDP Realtime DNRGB packet protocol.
	DNRGBProtocolID byte = 4
	// MaxDNRGBLEDsPerPacket is the maximum number of LEDs carried in a single DNRGB packet.
	MaxDNRGBLEDsPerPacket = 489
	// DefaultUDPPort is the standard WLED UDP realtime receive port.
	DefaultUDPPort int64 = 21324
	// DefaultUDPHoldTimeout is the hold timeout (seconds) embedded in DNRGB headers when no locate timeout is configured.
	DefaultUDPHoldTimeout byte = 255
)

// LEDRange describes a contiguous slice of LEDs starting at Start (0-based) with Count LEDs.
type LEDRange struct {
	Start int
	Count int
	RGB   [3]byte
}

// EncodeDNRGBPacket builds a single WLED DNRGB UDP packet:
// [4, timeoutSec, startHi, startLo, r0, g0, b0, ...].
func EncodeDNRGBPacket(timeoutSec byte, startIndex uint16, rgbData []byte) ([]byte, error) {
	if len(rgbData) == 0 || len(rgbData)%3 != 0 {
		return nil, fmt.Errorf("invalid DNRGB payload length %d: must be a positive multiple of 3", len(rgbData))
	}
	ledCount := len(rgbData) / 3
	if ledCount > MaxDNRGBLEDsPerPacket {
		return nil, fmt.Errorf("DNRGB packet supports at most %d LEDs, got %d", MaxDNRGBLEDsPerPacket, ledCount)
	}

	pkt := make([]byte, 4+len(rgbData))
	pkt[0] = DNRGBProtocolID
	pkt[1] = timeoutSec
	binary.BigEndian.PutUint16(pkt[2:4], startIndex)
	copy(pkt[4:], rgbData)
	return pkt, nil
}

// BuildDNRGBFramePackets turns LED ranges into a black-initialized RGB buffer and splits it into
// DNRGB UDP packets (at most 489 LEDs per packet).
func BuildDNRGBFramePackets(timeoutSec byte, totalLEDs int, ranges []LEDRange) ([][]byte, error) {
	frameLen := totalLEDs
	for _, r := range ranges {
		if r.Start < 0 || r.Count <= 0 {
			continue
		}
		if end := r.Start + r.Count; end > frameLen {
			frameLen = end
		}
	}
	if frameLen <= 0 {
		frameLen = 1
	}

	buf := make([]byte, frameLen*3)
	for _, r := range ranges {
		if r.Start < 0 || r.Count <= 0 {
			continue
		}
		for i := 0; i < r.Count; i++ {
			off := (r.Start + i) * 3
			buf[off] = r.RGB[0]
			buf[off+1] = r.RGB[1]
			buf[off+2] = r.RGB[2]
		}
	}

	var packets [][]byte
	for offset := 0; offset < frameLen; offset += MaxDNRGBLEDsPerPacket {
		chunkLEDs := frameLen - offset
		if chunkLEDs > MaxDNRGBLEDsPerPacket {
			chunkLEDs = MaxDNRGBLEDsPerPacket
		}
		startByte := offset * 3
		endByte := (offset + chunkLEDs) * 3
		pkt, err := EncodeDNRGBPacket(timeoutSec, uint16(offset), buf[startByte:endByte])
		if err != nil {
			return nil, err
		}
		packets = append(packets, pkt)
	}
	return packets, nil
}

func resolveUDPEndpoint(host string, udpPort int64) (string, int64, error) {
	cleanHost, _, err := ValidateEndpoint(host, sql.NullInt64{})
	if err != nil {
		return "", 0, err
	}
	targetPort := udpPort
	if targetPort == 0 {
		targetPort = DefaultUDPPort
	}
	if targetPort < 1 || targetPort > 65535 {
		return "", 0, fmt.Errorf("invalid UDP port %d: must be between 1 and 65535", targetPort)
	}
	return cleanHost, targetPort, nil
}

// SendDNRGB dials the controller UDP endpoint and transmits DNRGB packet(s), chunking >489 LEDs cleanly.
func SendDNRGB(ctx context.Context, host string, udpPort int64, timeoutSec byte, startIndex uint16, rgbData []byte) error {
	cleanHost, targetPort, err := resolveUDPEndpoint(host, udpPort)
	if err != nil {
		return err
	}
	if len(rgbData) == 0 || len(rgbData)%3 != 0 {
		return fmt.Errorf("invalid DNRGB payload length %d: must be a positive multiple of 3", len(rgbData))
	}

	addr := net.JoinHostPort(cleanHost, strconv.FormatInt(targetPort, 10))
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return fmt.Errorf("udp dial %s failed: %w", addr, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
	} else {
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	}

	totalLEDs := len(rgbData) / 3
	for offset := 0; offset < totalLEDs; offset += MaxDNRGBLEDsPerPacket {
		chunkLEDs := totalLEDs - offset
		if chunkLEDs > MaxDNRGBLEDsPerPacket {
			chunkLEDs = MaxDNRGBLEDsPerPacket
		}
		pkt, err := EncodeDNRGBPacket(
			timeoutSec,
			uint16(int(startIndex)+offset),
			rgbData[offset*3:(offset+chunkLEDs)*3],
		)
		if err != nil {
			return err
		}
		if _, err := conn.Write(pkt); err != nil {
			return fmt.Errorf("udp write to %s failed: %w", addr, err)
		}
	}
	return nil
}

// SendDNRGBRanges renders ranges against a black frame and transmits all DNRGB packets over UDP.
func SendDNRGBRanges(ctx context.Context, host string, udpPort int64, timeoutSec byte, totalLEDs int, ranges []LEDRange) error {
	cleanHost, targetPort, err := resolveUDPEndpoint(host, udpPort)
	if err != nil {
		return err
	}
	packets, err := BuildDNRGBFramePackets(timeoutSec, totalLEDs, ranges)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(cleanHost, strconv.FormatInt(targetPort, 10))
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return fmt.Errorf("udp dial %s failed: %w", addr, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
	} else {
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	}

	for _, pkt := range packets {
		if _, err := conn.Write(pkt); err != nil {
			return fmt.Errorf("udp write to %s failed: %w", addr, err)
		}
	}
	return nil
}
