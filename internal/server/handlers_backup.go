package server

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"wledger/internal/models"
)

func (a *App) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	// Get data
	data, err := a.BackupStore.GetAllDataForBackup()
	if err != nil {
		serverError(w, r, err)
		return
	}

	// Set headers
	filename := fmt.Sprintf("wledger-backup-%s.zip", time.Now().Format("2006-01-02-150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")

	// Create zip writer
	zw := zip.NewWriter(w)
	defer zw.Close()

	// Add JSON data
	f, err := zw.Create("wledger_data.json")
	if err != nil {
		serverError(w, r, err)
		return
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		serverError(w, r, err)
		return
	}

	// Add uploaded assets (walk the data/uploads folder)
	uploadsDir := "data/uploads"
	err = filepath.Walk(uploadsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		// Get relative path (e.g., "images/1-123.jpg")
		relPath, err := filepath.Rel(uploadsDir, path)
		if err != nil {
			return err
		}

		// Store in zip as "assets/images/1-123.jpg"
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
		// Since the strem is writing, can't change the HTTP status code here. Log it instead.
		fmt.Printf("Error zipping files: %v\n", err)
	}
}

func (a *App) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	// Parse upload
	// Allow larger uploads for backups. Not sure what a good limit is...
	// TODO: See if this can be configured elsewhere globally along with other constants
	// TODO: Provide user feedback on large uploads (frontend)
	// TODO: Handle massive uploads that exceed server memory capacity & gracefully reject before upload and restore process
	// TODO: Figure out what a sensible max size here actually is/should be (500MB for now...?)
	// TODO: Idea - optional opt in statistics collection to help determine average backup sizes?

	// 500MB limit for now - it's a best guess for now
	const maxBackupSize = 500 * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupSize)
	if err := r.ParseMultipartForm(maxBackupSize); err != nil {
		clientError(w, r, http.StatusBadRequest, "Backup file too large", err)
		return
	}

	file, _, err := r.FormFile("backup_file")
	if err != nil {
		clientError(w, r, http.StatusBadRequest, "Invalid file", err)
		return
	}
	defer file.Close()

	// Save ZIP to temp file
	tempFile, err := os.CreateTemp("", "restore-*.zip")
	if err != nil {
		serverError(w, r, err)
		return
	}
	// Clean up zip later
	defer os.Remove(tempFile.Name())

	if _, err := io.Copy(tempFile, file); err != nil {
		serverError(w, r, err)
		return
	}

	// Open ZIP
	zr, err := zip.OpenReader(tempFile.Name())
	if err != nil {
		clientError(w, r, http.StatusBadRequest, "Invalid ZIP file", err)
		return
	}
	defer zr.Close()

	// Find and Parse JSON
	var backupData models.BackupData
	jsonFound := false

	for _, f := range zr.File {
		if f.Name == "wledger_data.json" {
			rc, err := f.Open()
			if err != nil {
				serverError(w, r, err)
				return
			}
			if err := json.NewDecoder(rc).Decode(&backupData); err != nil {
				rc.Close()
				clientError(w, r, http.StatusBadRequest, "Invalid backup data JSON", err)
				return
			}
			rc.Close()
			jsonFound = true
			break
		}
	}

	if !jsonFound {
		clientError(w, r, http.StatusBadRequest, "Backup JSON not found in zip", nil)
		return
	}

	// Restore database
	if err := a.BackupStore.RestoreFromBackup(backupData); err != nil {
		serverError(w, r, err)
		return
	}

	// Restore assets (Only if DB restore succeeded)
	// First clear existing uploads
	os.RemoveAll("data/uploads")
	os.MkdirAll("data/uploads", 0755)

	for _, f := range zr.File {
		// Look for files in "assets/" folder inside zip
		if filepath.Dir(f.Name) == "." {
			continue
		} // Skip root files

		// Check if file starts with "assets/"
		// Note: zip paths use forward slashes
		if len(f.Name) > 7 && f.Name[0:7] == "assets/" {
			// Remove "assets/" prefix
			relPath := f.Name[7:]
			destPath := filepath.Join("data", "uploads", relPath)

			// Create directory
			os.MkdirAll(filepath.Dir(destPath), 0755)

			// Write file
			outFile, err := os.Create(destPath)
			if err != nil {
				// Log error but keep going
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
