package config

import (
	"path/filepath"
	"testing"
)

func TestLoad_DefaultsAndEnvOverrides(t *testing.T) {
	t.Cleanup(func() {
		Load()
	})

	envVars := []string{
		"WLEDGER_DATA_DIR",
		"WLEDGER_DB_PATH",
		"WLEDGER_UPLOADS_DIR",
		"WLEDGER_PORT",
		"WLEDGER_COOKIE_SECURE",
		"WLEDGER_TRUST_PROXY",
		"WLEDGER_TLS_CERT",
		"WLEDGER_TLS_KEY",
		"WLEDGER_TLS_SELF_SIGNED",
	}
	for _, k := range envVars {
		t.Setenv(k, "")
	}

	rt := Load()
	if rt.DataDir != "./data" || DirData != "./data" {
		t.Fatalf("expected default DataDir ./data, got %q / %q", rt.DataDir, DirData)
	}
	if rt.DBPath != "./data/wledger.db" || DirDatabase != "./data/wledger.db" {
		t.Fatalf("expected default DBPath ./data/wledger.db, got %q / %q", rt.DBPath, DirDatabase)
	}
	if rt.UploadsDir != "./app/uploads" || DirUploads != "./app/uploads" {
		t.Fatalf("expected default UploadsDir ./app/uploads, got %q", rt.UploadsDir)
	}
	if rt.UploadsImagesDir != "./app/uploads/images" || rt.UploadsDocsDir != "./app/uploads/docs" {
		t.Fatalf("unexpected default upload subdirs: %q %q", rt.UploadsImagesDir, rt.UploadsDocsDir)
	}
	if rt.Port != "8080" || Port != "8080" {
		t.Fatalf("expected default Port 8080, got %q", rt.Port)
	}
	if rt.CookieSecure || rt.TrustProxy || rt.TLSSelfSigned {
		t.Fatalf("expected bool defaults to be false, got %+v", rt)
	}
	if rt.TLSCert != "" || rt.TLSKey != "" {
		t.Fatalf("expected empty TLS paths, got %q %q", rt.TLSCert, rt.TLSKey)
	}

	// Derive DB path from WLEDGER_DATA_DIR and subdirs from WLEDGER_UPLOADS_DIR
	t.Setenv("WLEDGER_DATA_DIR", "/var/lib/wledger")
	t.Setenv("WLEDGER_UPLOADS_DIR", "/srv/uploads")
	t.Setenv("WLEDGER_PORT", "9090")
	t.Setenv("WLEDGER_COOKIE_SECURE", "true")
	t.Setenv("WLEDGER_TRUST_PROXY", "1")
	t.Setenv("WLEDGER_TLS_CERT", "/etc/ssl/cert.pem")
	t.Setenv("WLEDGER_TLS_KEY", "/etc/ssl/key.pem")
	t.Setenv("WLEDGER_TLS_SELF_SIGNED", "true")

	rt = Load()
	if rt.DataDir != "/var/lib/wledger" || DirData != "/var/lib/wledger" {
		t.Fatalf("expected overridden DataDir, got %q", rt.DataDir)
	}
	if want := filepath.Join("/var/lib/wledger", "wledger.db"); rt.DBPath != want || DirDatabase != want {
		t.Fatalf("expected derived DBPath %q, got %q / %q", want, rt.DBPath, DirDatabase)
	}
	if want := filepath.Join("/srv/uploads", "images"); rt.UploadsImagesDir != want || DirUploadsImages != want {
		t.Fatalf("expected derived UploadsImagesDir %q, got %q", want, rt.UploadsImagesDir)
	}
	if want := filepath.Join("/srv/uploads", "docs"); rt.UploadsDocsDir != want || DirUploadsDocs != want {
		t.Fatalf("expected derived UploadsDocsDir %q, got %q", want, rt.UploadsDocsDir)
	}
	if rt.Port != "9090" || !rt.CookieSecure || !rt.TrustProxy || !rt.TLSSelfSigned {
		t.Fatalf("unexpected overrides: %+v", rt)
	}
	if rt.TLSCert != "/etc/ssl/cert.pem" || rt.TLSKey != "/etc/ssl/key.pem" {
		t.Fatalf("unexpected TLS overrides: %q %q", rt.TLSCert, rt.TLSKey)
	}

	// Explicit WLEDGER_DB_PATH wins over WLEDGER_DATA_DIR, invalid bool safely falls back
	t.Setenv("WLEDGER_DB_PATH", "/custom/my.db")
	t.Setenv("WLEDGER_COOKIE_SECURE", "not-a-bool")
	rt = Load()
	if rt.DBPath != "/custom/my.db" || DirDatabase != "/custom/my.db" {
		t.Fatalf("expected explicit DBPath /custom/my.db, got %q", rt.DBPath)
	}
	if rt.CookieSecure {
		t.Fatalf("expected invalid bool to fall back to false")
	}
}
