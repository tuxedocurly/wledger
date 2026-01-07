package config

const (
	// System Paths (Relative to project root)
	DirData          = "./data"
	DirDatabase      = "./data/wledger.db"
	DirLogs		     = "./app/logs"
	DirUploads       = "./app/uploads"
	DirUploadsImages = "./app/uploads/images"
	DirUploadsDocs   = "./app/uploads/docs"
	DirStatic        = "./web/static"
	DirLocales       = "./locales"

	// Web URL Prefixes
	UrlPrefixStatic  = "/static/"
	UrlPrefixUploads = "/uploads/"
	UrlPrefixImages  = "/uploads/images/"
	UrlPrefixDocs    = "/uploads/docs/"

	// Upload Limits (Bytes)
	MaxUploadSizeParts  = 100 << 20 // 100 MB
	MaxUploadSizeImport = 100 << 20 // 100 MB
	MaxUploadSizeBackup = 100 << 20 // 100 MB

	// Session Keys
	SessionKeyUserID = "user_id"
	SessionKeyRole   = "role"
)
