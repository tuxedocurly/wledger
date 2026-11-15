package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Runtime holds environment-configured paths, server network settings, and security options.
type Runtime struct {
	DataDir          string
	DBPath           string
	LogsDir          string
	UploadsDir       string
	UploadsImagesDir string
	UploadsDocsDir   string
	StaticDir        string
	LocalesDir       string
	Port             string
	CookieSecure     bool
	TrustProxy       bool
	TLSCert          string
	TLSKey           string
	TLSSelfSigned    bool
}

var (
	// System Paths (configurable via WLEDGER_* env vars, preserving backward-compatible defaults)
	DirData          string
	DirDatabase      string
	DirLogs          = "./app/logs"
	DirUploads       string
	DirUploadsImages string
	DirUploadsDocs   string
	DirStatic        = "./web/static"
	DirLocales       = "./locales"

	// Server & Security Settings (configurable via WLEDGER_* env vars)
	Port          string
	CookieSecure  bool
	TrustProxy    bool
	TLSCert       string
	TLSKey        string
	TLSSelfSigned bool
)

func init() {
	Load()
}

// Load reads WLEDGER_* environment variables, applies backward-compatible defaults,
// updates the package-level configuration variables, and returns the resulting Runtime config.
func Load() Runtime {
	dataDir := envOrDefault("WLEDGER_DATA_DIR", "./data")
	dbPath := strings.TrimSpace(os.Getenv("WLEDGER_DB_PATH"))
	if dbPath == "" {
		if strings.TrimSpace(os.Getenv("WLEDGER_DATA_DIR")) != "" {
			dbPath = filepath.Join(dataDir, "wledger.db")
		} else {
			dbPath = "./data/wledger.db"
		}
	}

	uploadsDir := envOrDefault("WLEDGER_UPLOADS_DIR", "./app/uploads")
	var uploadsImagesDir, uploadsDocsDir string
	if strings.TrimSpace(os.Getenv("WLEDGER_UPLOADS_DIR")) != "" {
		uploadsImagesDir = filepath.Join(uploadsDir, "images")
		uploadsDocsDir = filepath.Join(uploadsDir, "docs")
	} else {
		uploadsImagesDir = "./app/uploads/images"
		uploadsDocsDir = "./app/uploads/docs"
	}

	rt := Runtime{
		DataDir:          dataDir,
		DBPath:           dbPath,
		LogsDir:          "./app/logs",
		UploadsDir:       uploadsDir,
		UploadsImagesDir: uploadsImagesDir,
		UploadsDocsDir:   uploadsDocsDir,
		StaticDir:        "./web/static",
		LocalesDir:       "./locales",
		Port:             envOrDefault("WLEDGER_PORT", "8080"),
		CookieSecure:     envBool("WLEDGER_COOKIE_SECURE", false),
		TrustProxy:       envBool("WLEDGER_TRUST_PROXY", false),
		TLSCert:          strings.TrimSpace(os.Getenv("WLEDGER_TLS_CERT")),
		TLSKey:           strings.TrimSpace(os.Getenv("WLEDGER_TLS_KEY")),
		TLSSelfSigned:    envBool("WLEDGER_TLS_SELF_SIGNED", false),
	}

	DirData = rt.DataDir
	DirDatabase = rt.DBPath
	DirLogs = rt.LogsDir
	DirUploads = rt.UploadsDir
	DirUploadsImages = rt.UploadsImagesDir
	DirUploadsDocs = rt.UploadsDocsDir
	DirStatic = rt.StaticDir
	DirLocales = rt.LocalesDir
	Port = rt.Port
	CookieSecure = rt.CookieSecure
	TrustProxy = rt.TrustProxy
	TLSCert = rt.TLSCert
	TLSKey = rt.TLSKey
	TLSSelfSigned = rt.TLSSelfSigned

	return rt
}

func envOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

const (

	// Web URL Prefixes
	UrlPrefixStatic  = "/static/"
	UrlPrefixUploads = "/uploads/"
	UrlPrefixImages  = "/uploads/images/"
	UrlPrefixDocs    = "/uploads/docs/"

	// Upload Memory Buffer Limits
	MaxUploadSizeParts  = 100 << 20 // 100 MB
	MaxUploadSizeImport = 100 << 20 // 100 MB
	MaxUploadSizeBackup = 100 << 20 // 100 MB

	// Session Keys
	SessionKeyUserID = "user_id"
	SessionKeyRole   = "role"
)
