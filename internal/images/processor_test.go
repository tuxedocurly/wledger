package images

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type bytesMultipartFile struct {
	*bytes.Reader
}

func (bytesMultipartFile) Close() error { return nil }

func fakePNGHeader(width, height uint32) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 13)
	buf.Write(lenBuf[:])

	var chunk bytes.Buffer
	chunk.WriteString("IHDR")
	var dimBuf [4]byte
	binary.BigEndian.PutUint32(dimBuf[:], width)
	chunk.Write(dimBuf[:])
	binary.BigEndian.PutUint32(dimBuf[:], height)
	chunk.Write(dimBuf[:])
	chunk.Write([]byte{8, 2, 0, 0, 0})

	buf.Write(chunk.Bytes())

	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc32.ChecksumIEEE(chunk.Bytes()))
	buf.Write(crcBuf[:])
	return buf.Bytes()
}

func TestProcessUpload_RejectsDecompressionBombs(t *testing.T) {
	tests := []struct {
		name   string
		width  uint32
		height uint32
		errSub string
	}{
		{name: "width_over_8192", width: 8193, height: 1, errSub: "exceed maximum 8192"},
		{name: "height_over_8192", width: 1, height: 8193, errSub: "exceed maximum 8192"},
		{name: "total_pixels_over_36M", width: 7000, height: 7000, errSub: "pixel count"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hdrBytes := fakePNGHeader(tc.width, tc.height)
			cfg, _, err := image.DecodeConfig(bytes.NewReader(hdrBytes))
			if err != nil {
				t.Fatalf("test PNG header itself failed DecodeConfig: %v", err)
			}
			if cfg.Width != int(tc.width) || cfg.Height != int(tc.height) {
				t.Fatalf("decoded config %dx%d != %dx%d", cfg.Width, cfg.Height, tc.width, tc.height)
			}

			file := bytesMultipartFile{Reader: bytes.NewReader(hdrBytes)}
			_, err = ProcessUpload(file, &multipart.FileHeader{Filename: "bomb.png"})
			if err == nil {
				t.Fatal("expected error rejecting oversized image")
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("unexpected error %q, wanted substring %q", err.Error(), tc.errSub)
			}
		})
	}
}

func TestProcessUpload_ValidAndUnsupported(t *testing.T) {
	wd, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(wd)

	if err := Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if _, err := ProcessUpload(bytesMultipartFile{bytes.NewReader([]byte("<svg/>"))}, &multipart.FileHeader{Filename: "icon.svg"}); err == nil {
		t.Fatal("expected error for unsupported .svg extension")
	}

	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(0, 0, color.White)
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode png: %v", err)
	}

	fileName, err := ProcessUpload(bytesMultipartFile{bytes.NewReader(buf.Bytes())}, &multipart.FileHeader{Filename: "part.png"})
	if err != nil {
		t.Fatalf("ProcessUpload valid png failed: %v", err)
	}
	if !strings.HasSuffix(fileName, ".jpg") {
		t.Fatalf("expected .jpg output filename, got %q", fileName)
	}

	base := strings.TrimSuffix(fileName, ".jpg")
	if _, err := os.Stat(filepath.Join("app/uploads/images", fileName)); err != nil {
		t.Fatalf("expected saved main image: %v", err)
	}
	if _, err := os.Stat(filepath.Join("app/uploads/images", base+"_thumb.jpg")); err != nil {
		t.Fatalf("expected saved thumb image: %v", err)
	}
}
