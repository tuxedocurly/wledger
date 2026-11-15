package wled

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestEncodeDNRGBPacket(t *testing.T) {
	pkt, err := EncodeDNRGBPacket(12, 300, []byte{255, 16, 32, 10, 20, 30})
	if err != nil {
		t.Fatalf("EncodeDNRGBPacket failed: %v", err)
	}

	// [4, timeoutSec, startHi, startLo, r0, g0, b0, r1, g1, b1]
	if len(pkt) != 10 {
		t.Fatalf("expected packet length 10, got %d", len(pkt))
	}
	if pkt[0] != 4 || pkt[1] != 12 {
		t.Errorf("unexpected header bytes [0..1]: %v", pkt[:2])
	}
	if start := binary.BigEndian.Uint16(pkt[2:4]); start != 300 {
		t.Errorf("expected start index 300, got %d", start)
	}
	if !bytes.Equal(pkt[4:], []byte{255, 16, 32, 10, 20, 30}) {
		t.Errorf("unexpected RGB payload: %v", pkt[4:])
	}

	if _, err := EncodeDNRGBPacket(5, 0, []byte{1, 2}); err == nil {
		t.Error("expected error for payload not divisible by 3")
	}
	tooMany := make([]byte, (MaxDNRGBLEDsPerPacket+1)*3)
	if _, err := EncodeDNRGBPacket(5, 0, tooMany); err == nil {
		t.Error("expected error when exceeding MaxDNRGBLEDsPerPacket")
	}
}

func TestSendDNRGB_LoopbackPacketAndChunking(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket failed: %v", err)
	}
	defer pc.Close()

	udpAddr := pc.LocalAddr().(*net.UDPAddr)

	// Send 495 LEDs (> 489 max per packet) to verify clean 2-packet chunking.
	const totalLEDs = 495
	rgb := make([]byte, totalLEDs*3)
	rgb[0], rgb[1], rgb[2] = 0xAA, 0xBB, 0xCC
	rgb[489*3], rgb[489*3+1], rgb[489*3+2] = 0x11, 0x22, 0x33

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := SendDNRGB(ctx, "127.0.0.1", int64(udpAddr.Port), 15, 0, rgb); err != nil {
		t.Fatalf("SendDNRGB failed: %v", err)
	}

	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf1 := make([]byte, 2048)
	n1, _, err := pc.ReadFrom(buf1)
	if err != nil {
		t.Fatalf("failed to read first DNRGB packet: %v", err)
	}
	buf2 := make([]byte, 2048)
	n2, _, err := pc.ReadFrom(buf2)
	if err != nil {
		t.Fatalf("failed to read second DNRGB packet: %v", err)
	}

	p1 := buf1[:n1]
	p2 := buf2[:n2]
	if p1[0] != 4 || p1[1] != 15 || binary.BigEndian.Uint16(p1[2:4]) != 0 || len(p1) != 4+489*3 {
		t.Errorf("unexpected first chunk: header=%v len=%d", p1[:4], len(p1))
	}
	if p2[0] != 4 || p2[1] != 15 || binary.BigEndian.Uint16(p2[2:4]) != 489 || len(p2) != 4+6*3 {
		t.Errorf("unexpected second chunk: header=%v len=%d", p2[:4], len(p2))
	}
	if !bytes.Equal(p2[4:7], []byte{0x11, 0x22, 0x33}) {
		t.Errorf("unexpected chunk 2 pixel 0 RGB: %v", p2[4:7])
	}
}

func TestService_LocateBin_UDPRealtimeDNRGB(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket failed: %v", err)
	}
	defer pc.Close()
	udpAddr := pc.LocalAddr().(*net.UDPAddr)

	dbConn, err := db.Open("file:wled_udp_service_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	store := db.NewStore(dbConn)
	ctx := context.Background()
	_ = store.InitSettings(ctx)
	_ = store.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate: sql.NullString{String: "#123456", Valid: true},
	})
	_ = store.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
		EnableLocateTimeout:  sql.NullBool{Bool: true, Valid: true},
		LocateTimeoutSeconds: sql.NullInt64{Int64: 10, Valid: true},
	})

	ctrlRow, err := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "UDP Ctrl",
		IpAddress: "127.0.0.1",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}

	err = store.UpdateController(ctx, db.UpdateControllerParams{
		ID:           ctrlRow.ID,
		Name:         "UDP Ctrl",
		IpAddress:    "127.0.0.1",
		Port:         sql.NullInt64{Int64: 80, Valid: true},
		UseUdp:       sql.NullBool{Bool: true, Valid: true},
		UdpPort:      sql.NullInt64{Int64: int64(udpAddr.Port), Valid: true},
		RestoreState: sql.NullBool{Bool: false, Valid: true},
		IdlePresetID: sql.NullInt64{Int64: 0, Valid: true},
	})
	if err != nil {
		t.Fatalf("update controller UDP settings: %v", err)
	}

	contID, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Cont",
		ControllerID: sql.NullInt64{Int64: ctrlRow.ID, Valid: true},
		SegmentID:    0,
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}

	binID, err := store.CreateBin(ctx, db.CreateBinParams{
		Name:        "WideBin",
		ContainerID: contID,
		LedIndex:    sql.NullInt64{Int64: 2, Valid: true},
		Width:       sql.NullInt64{Int64: 2, Valid: true},
	})
	if err != nil {
		t.Fatalf("create bin: %v", err)
	}

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.LocateBin(ctx, ctrlRow.ID, binID); err != nil {
		t.Fatalf("LocateBin UDP failed: %v", err)
	}

	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 512)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom UDP failed: %v", err)
	}
	pkt := buf[:n]

	// Expected frame: LEDs 0..3 -> [0]=black, [1]=black, [2]=#123456, [3]=#123456
	if len(pkt) != 4+4*3 {
		t.Fatalf("expected 16-byte DNRGB packet for 4 LEDs, got %d bytes", len(pkt))
	}
	if pkt[0] != 4 || pkt[1] != 10 {
		t.Errorf("expected DNRGB header [4, 10], got %v", pkt[:2])
	}
	if start := binary.BigEndian.Uint16(pkt[2:4]); start != 0 {
		t.Errorf("expected startIdx 0, got %d", start)
	}
	// LED 2 and LED 3 should be 0x12, 0x34, 0x56; LED 0 and 1 should be 0, 0, 0
	if !bytes.Equal(pkt[4:10], []byte{0, 0, 0, 0, 0, 0}) {
		t.Errorf("expected LEDs 0..1 black, got %v", pkt[4:10])
	}
	if !bytes.Equal(pkt[10:16], []byte{0x12, 0x34, 0x56, 0x12, 0x34, 0x56}) {
		t.Errorf("expected LEDs 2..3 #123456, got %v", pkt[10:16])
	}
}
