package documents

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// setupTestDB creates an in memory DB and applies the schema using db.Migrate
func setupTestDB(t *testing.T) (*sql.DB, db.Store, func()) {
	// Open in-memory DB
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Apply migrations automatically
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	// Create store helper
	s := db.NewStore(conn)

	// return cleanup function
	return conn, s, func() {
		conn.Close()
	}
}

func TestDocumentService(t *testing.T) {
	// Setup templ dir to handle file uploads
	wd, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(wd)

	_, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(s, logger)
	ctx := context.Background()

	// Create Part
	partID, err := s.CreatePart(ctx, db.CreatePartParams{Name: "Doc Part"})
	if err != nil {
		t.Fatalf("create part failed: %v", err)
	}

	// Test AddLink
	err = svc.AddLink(ctx, s, partID, "http://google.com", "Google")
	if err != nil {
		t.Fatalf("AddLink failed: %v", err)
	}

	links, err := s.GetPartLinks(ctx, partID)
	if err != nil {
		t.Fatalf("GetPartLinks failed: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("expected 1 link, got %d", len(links))
	} else if links[0].Url != "http://google.com" {
		t.Errorf("unexpected url: %s", links[0].Url)
	}

	// Test DeleteLink
	err = svc.DeleteLink(ctx, links[0].ID)
	if err != nil {
		t.Fatalf("DeleteLink failed: %v", err)
	}
	links, _ = s.GetPartLinks(ctx, partID)
	if len(links) != 0 {
		t.Errorf("expected 0 links, got %d", len(links))
	}

	// Test UploadDocument
	content := "hello world"
	reader := strings.NewReader(content)
	path, err := svc.UploadDocument(ctx, s, partID, reader, "test.txt")
	if err != nil {
		t.Fatalf("UploadDocument failed: %v", err)
	}

	// Verify file path prefix
	if !strings.HasPrefix(path, "/uploads/docs/") {
		t.Errorf("unexpected path prefix: %s", path)
	}

	// Verify DB
	docs, err := s.GetPartDocs(ctx, partID)
	if err != nil {
		t.Fatalf("GetPartDocs failed: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0].FilePath != path {
		t.Errorf("db path mismatch: %s vs %s", docs[0].FilePath, path)
	}

	// Verify File on Disk
	// path is like /uploads/docs/test_123.txt
	// config.DirUploads is ./app/uploads
	// config.UrlPrefixUploads is /uploads/
	relPath := strings.TrimPrefix(path, "/uploads/") // docs/test_123.txt
	diskPath := filepath.Join("app/uploads", relPath)

	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		t.Errorf("file not found on disk: %s", diskPath)
	}

	// Test DeleteDocument
	err = svc.DeleteDocument(ctx, docs[0].ID)
	if err != nil {
		t.Fatalf("DeleteDocument failed: %v", err)
	}

	docs, _ = s.GetPartDocs(ctx, partID)
	if len(docs) != 0 {
		t.Errorf("expected 0 docs, got %d", len(docs))
	}

	if _, err := os.Stat(diskPath); !os.IsNotExist(err) {
		t.Errorf("file should be deleted: %s", diskPath)
	}
}

func TestDocumentService_UploadValidation(t *testing.T) {
	wd, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(wd)

	_, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	ctx := context.Background()
	svc := NewService(s, slog.New(slog.DiscardHandler))

	partID, err := s.CreatePart(ctx, db.CreatePartParams{Name: "ValPart"})
	if err != nil {
		t.Fatalf("failed to create part: %v", err)
	}

	var pngBuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.Black)
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("failed to encode png: %v", err)
	}

	tests := []struct {
		name     string
		filename string
		data     []byte
		wantErr  bool
	}{
		{name: "reject_html", filename: "evil.html", data: []byte("<html><body>xss</body></html>"), wantErr: true},
		{name: "reject_htm", filename: "evil.htm", data: []byte("<html><body>xss</body></html>"), wantErr: true},
		{name: "reject_svg", filename: "evil.svg", data: []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"), wantErr: true},
		{name: "reject_xml", filename: "evil.xml", data: []byte("<?xml version='1.0'?><a/>"), wantErr: true},
		{name: "reject_js", filename: "evil.js", data: []byte("alert(1)"), wantErr: true},
		{name: "reject_html_spoofed_as_txt", filename: "spoof.txt", data: []byte("<html><script>alert(1)</script></html>"), wantErr: true},
		{name: "reject_xml_spoofed_as_json", filename: "spoof.json", data: []byte("<?xml version='1.0'?><root/>"), wantErr: true},
		{name: "reject_plaintext_as_pdf", filename: "fake.pdf", data: []byte("not a pdf file"), wantErr: true},
		{name: "reject_binary_as_txt", filename: "bin.txt", data: []byte{0x00, 0x01, 0x02, 0x03}, wantErr: true},
		{name: "allow_pdf", filename: "datasheet.pdf", data: []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n"), wantErr: false},
		{name: "allow_txt", filename: "notes.txt", data: []byte("resistor notes"), wantErr: false},
		{name: "allow_md", filename: "readme.md", data: []byte("# Datasheet Notes\nDetails here.\n"), wantErr: false},
		{name: "allow_csv", filename: "bom.csv", data: []byte("part,qty\nR1,10\n"), wantErr: false},
		{name: "allow_json", filename: "spec.json", data: []byte(`{"voltage":"3.3V"}`), wantErr: false},
		{name: "allow_png", filename: "pinout.png", data: pngBuf.Bytes(), wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, err := svc.UploadDocument(ctx, s, partID, bytes.NewReader(tc.data), tc.filename)
			if (err != nil) != tc.wantErr {
				t.Fatalf("UploadDocument(%s) err = %v, wantErr %v", tc.filename, err, tc.wantErr)
			}
			if !tc.wantErr && !strings.HasPrefix(path, "/uploads/docs/") {
				t.Fatalf("expected /uploads/docs/ prefix, got %q", path)
			}
		})
	}

	// Ensure no rejected extensions were written to disk
	entries, err := os.ReadDir("app/uploads/docs")
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(entries) != 6 {
		t.Fatalf("expected exactly 6 valid saved files, got %d", len(entries))
	}
}
