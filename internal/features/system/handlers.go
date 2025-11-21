package system

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"

	"wledger/internal/core"
	"wledger/internal/models"
)

// Store defines the specific database methods this module needs.
type Store interface {
	GetAllDataForBackup() (models.BackupData, error)
	RestoreFromBackup(data models.BackupData) error
	CleanupOrphanedCategories() error
}

type Handler struct {
	store     Store
	uploadDir string
}

// New creates a new System handler
func New(s Store, uploadDir string) *Handler {
	return &Handler{
		store:     s,
		uploadDir: uploadDir,
	}

}

// RegisterRoutes defines the URLs for this module
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/settings/categories/cleanup", h.handleCleanupCategories)
	r.Get("/settings/backup/download", h.handleDownloadBackup)
	r.Post("/settings/backup/restore", h.handleRestoreBackup)
}

// Handlers

func (h *Handler) handleCleanupCategories(w http.ResponseWriter, r *http.Request) {
	if err := h.store.CleanupOrphanedCategories(); err != nil {
		core.ServerError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	data, err := h.store.GetAllDataForBackup()
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	filename := fmt.Sprintf("wledger-backup-%s.zip", time.Now().Format("2006-01-02-150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")

	zw := zip.NewWriter(w)
	defer zw.Close()

	f, err := zw.Create("wledger_data.json")
	if err != nil {
		core.ServerError(w, r, err)
		return
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		core.ServerError(w, r, err)
		return
	}

	// Walk the data/uploads folder
	uploadsDir := h.uploadDir
	// Ensure dir exists to avoid walk error if empty
	os.MkdirAll(uploadsDir, 0755)

	err = filepath.Walk(uploadsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		relPath, err := filepath.Rel(uploadsDir, path)
		if err != nil {
			return err
		}

		zipPath := filepath.Join("assets", relPath)
		zipFile, err := zw.Create(zipPath)
		if err != nil {
			return err
		}

		fsFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fsFile.Close()

		_, err = io.Copy(zipFile, fsFile)
		return err
	})

	if err != nil {
		// Log only as stream has started
		fmt.Printf("Error zipping files: %v\n", err)
	}
}

func (h *Handler) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	const maxBackupSize = 50 * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupSize)
	if err := r.ParseMultipartForm(maxBackupSize); err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Backup file too large", err)
		return
	}

	file, _, err := r.FormFile("backup_file")
	if err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid file", err)
		return
	}
	defer file.Close()

	tempFile, err := os.CreateTemp("", "restore-*.zip")
	if err != nil {
		core.ServerError(w, r, err)
		return
	}
	defer os.Remove(tempFile.Name())

	if _, err := io.Copy(tempFile, file); err != nil {
		core.ServerError(w, r, err)
		return
	}

	zr, err := zip.OpenReader(tempFile.Name())
	if err != nil {
		core.ClientError(w, r, http.StatusBadRequest, "Invalid ZIP file", err)
		return
	}
	defer zr.Close()

	var backupData models.BackupData
	jsonFound := false

	for _, f := range zr.File {
		if f.Name == "wledger_data.json" {
			rc, err := f.Open()
			if err != nil {
				core.ServerError(w, r, err)
				return
			}
			if err := json.NewDecoder(rc).Decode(&backupData); err != nil {
				rc.Close()
				core.ClientError(w, r, http.StatusBadRequest, "Invalid backup data JSON", err)
				return
			}
			rc.Close()
			jsonFound = true
			break
		}
	}

	if !jsonFound {
		core.ClientError(w, r, http.StatusBadRequest, "Backup JSON not found in zip", nil)
		return
	}

	if err := h.store.RestoreFromBackup(backupData); err != nil {
		core.ServerError(w, r, err)
		return
	}

	// Clear existing uploads and restore new ones
	os.RemoveAll(h.uploadDir)
	os.MkdirAll(h.uploadDir, 0755)

	for _, f := range zr.File {
		if filepath.Dir(f.Name) == "." {
			continue
		}

		if len(f.Name) > 7 && f.Name[0:7] == "assets/" {
			relPath := f.Name[7:]
			destPath := filepath.Join(h.uploadDir, relPath)

			os.MkdirAll(filepath.Dir(destPath), 0755)

			outFile, err := os.Create(destPath)
			if err != nil {
				fmt.Printf("Error extracting file %s: %v\n", f.Name, err)
				continue
			}
			rc, _ := f.Open()
			io.Copy(outFile, rc)
			outFile.Close()
			rc.Close()
		}
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
