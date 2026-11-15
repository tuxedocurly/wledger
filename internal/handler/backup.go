package handler

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/web/components"
)

// GET /settings/backup/download
func (h *Handler) HandleBackupDownload(w http.ResponseWriter, r *http.Request) {
	// Admin Only
	user := auth.GetUserFromRequest(r)
	if !user.IsAdmin() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusForbidden)
		return
	}

	// Build archive in a temporary file before committing HTTP download headers
	// so export errors return a clean HTTP error rather than a corrupt ZIP stream.
	tmpFile, err := os.CreateTemp("", "wledger_backup_*.zip")
	if err != nil {
		h.Logger.Error("failed to create temp backup file", "err", err)
		h.UIError.Respond(w, r, err, "Failed to generate backup", http.StatusInternalServerError)
		return
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	if err := h.Backup.Export(r.Context(), tmpFile); err != nil {
		h.Logger.Error("failed to generate backup", "err", err)
		h.UIError.Respond(w, r, err, "Failed to generate backup", http.StatusInternalServerError)
		return
	}

	info, err := tmpFile.Stat()
	if err != nil {
		h.Logger.Error("failed to stat temp backup file", "err", err)
		h.UIError.Respond(w, r, err, "Failed to generate backup", http.StatusInternalServerError)
		return
	}
	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		h.Logger.Error("failed to rewind temp backup file", "err", err)
		h.UIError.Respond(w, r, err, "Failed to generate backup", http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("wledger_backup_%s.zip", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))

	if _, err := io.Copy(w, tmpFile); err != nil {
		h.Logger.Error("failed to stream backup archive", "err", err)
	}
}

// POST /settings/backup/restore
func (h *Handler) HandleBackupRestore(w http.ResponseWriter, r *http.Request) {
	// Admin Only
	user := auth.GetUserFromRequest(r)
	if !user.IsAdmin() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, config.MaxUploadSizeBackup+(1<<20))

	// Parse Upload
	err := r.ParseMultipartForm(config.MaxUploadSizeBackup) // 100 MB memory buffer
	if err != nil {
		h.Logger.Error("failed to parse multipart form for backup restore", "err", err)
		components.ImportResult(false, "Upload too large or invalid: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Clean up files after request
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("backup_file")
	if err != nil {
		h.Logger.Error("failed to get backup file from request", "err", err)
		components.ImportResult(false, "No file provided", nil).Render(r.Context(), w)
		return
	}
	defer file.Close()

	// Execute Restore via Service
	if err := h.Backup.Restore(r.Context(), file, header.Size); err != nil {
		h.Logger.Error("restore failed", "err", err)
		components.ImportResult(false, "Restore failed: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Invalidate active SCS session cookie before refreshing client UI.
	if h.Session != nil {
		_ = h.Session.Destroy(r.Context())
	}
	w.Header().Set("HX-Refresh", "true")

	// Force Logout / Success
	components.ImportResult(true, "System restored successfully. You will be logged out.", nil).Render(r.Context(), w)
}
