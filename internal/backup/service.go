package backup

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
)

type Service interface {
	Export(ctx context.Context, w io.Writer) error
	Restore(ctx context.Context, zipReader io.ReaderAt, size int64) error
}

type service struct {
	db         *sql.DB
	store      db.Store
	uploadsDir string
	logger     *slog.Logger
}

func NewService(database *sql.DB, store db.Store, uploadsDir string, logger *slog.Logger) Service {
	return &service{
		db:         database,
		store:      store,
		uploadsDir: uploadsDir,
		logger:     logger,
	}
}

func (s *service) Export(ctx context.Context, w io.Writer) error {
	s.logger.Debug("starting system backup export")
	// Fetch Data
	settings, err := s.store.GetSettings(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to fetch settings: %w", err)
	}

	users, _ := s.store.GetAllUsers(ctx)
	controllers, _ := s.store.GetControllers(ctx)
	containers, _ := s.store.GetAllContainers(ctx)
	walls, _ := s.store.GetWalls(ctx)
	wallCards, _ := s.store.GetAllWallCards(ctx)
	bins, _ := s.store.GetAllBins(ctx)
	parts, _ := s.store.GetAllParts(ctx)
	assignments, _ := s.store.GetAllPartAssignments(ctx)
	links, _ := s.store.GetAllPartLinks(ctx)
	docs, _ := s.store.GetAllPartDocs(ctx)
	prompts, _ := s.store.GetAllPartAiPrompts(ctx)
	logs, _ := s.store.GetAllAuditLogs(ctx)
	var auditLogs []db.AuditLog
	for _, l := range logs {
		auditLogs = append(auditLogs, db.AuditLog{
			ID:         l.ID,
			UserID:     l.UserID,
			ActionType: l.ActionType,
			EntityType: l.EntityType,
			EntityID:   l.EntityID,
			Details:    l.Details,
			OldValue:   json.RawMessage(l.OldValue),
			NewValue:   json.RawMessage(l.NewValue),
			CreatedAt:  l.CreatedAt,
		})
	}
	tags, _ := s.store.ListAllTags(ctx)
	partTags, _ := s.store.GetAllPartTags(ctx)

	manifest := Manifest{
		Version:         "1.0",
		ExportedAt:      time.Now(),
		Settings:        settings,
		Users:           users,
		Controllers:     controllers,
		Containers:      containers,
		Walls:           walls,
		WallCards:       wallCards,
		Bins:            bins,
		Parts:           parts,
		PartAssignments: assignments,
		PartLinks:       links,
		PartDocs:        docs,
		PartAiPrompts:   prompts,
		Tags:            tags,
		PartTags:        partTags,
		AuditLogs:       auditLogs,
	}

	zw := zip.NewWriter(w)
	defer zw.Close()

	// Add restore_data.json
	s.logger.Debug("adding restore_data.json to backup")
	fJson, err := zw.Create("restore_data.json")
	if err != nil {
		return fmt.Errorf("failed to create json entry: %w", err)
	}
	enc := json.NewEncoder(fJson)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return fmt.Errorf("failed to encode json: %w", err)
	}

	// add human_readable_parts.csv
	s.logger.Debug("adding human_readable_parts.csv to backup")
	fCsv, err := zw.Create("human_readable_parts.csv")
	if err == nil {
		cw := csv.NewWriter(fCsv)
		cw.Write([]string{"Name", "Description", "Part Number", "Manufacturer", "Supplier", "Unit Cost", "Reorder Level", "Min Stock", "Barcode", "Quantity"})
		for _, p := range parts {
			var total int64
			for _, a := range assignments {
				if a.PartID == p.ID {
					total += a.Quantity
				}
			}
			cw.Write([]string{
				p.Name,
				p.Description.String,
				p.PartNumber.String,
				p.Manufacturer.String,
				p.Supplier.String,
				fmt.Sprintf("%.2f", p.UnitCost.Float64),
				fmt.Sprintf("%d", p.ReorderLevel.Int64),
				fmt.Sprintf("%d", p.MinStockThreshold.Int64),
				p.BarcodeData.String,
				fmt.Sprintf("%d", total),
			})
		}
		cw.Flush()
	}

	// Add Uploads
	s.logger.Debug("collecting upload files for backup", "dir", s.uploadsDir)
	err = filepath.Walk(s.uploadsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// If uploads dir doesn't exist, just skip
			if os.IsNotExist(err) {
				s.logger.Debug("uploads directory not found, skipping", "path", s.uploadsDir)
				return nil
			}
			return err
		}

		// Ignore hidden files/directories (like .restore_tmp, .uploads_bak, .git, etc.)
		if strings.HasPrefix(info.Name(), ".") && path != s.uploadsDir {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		relInZip, _ := filepath.Rel(s.uploadsDir, path)
		zipPath := filepath.Join("uploads", relInZip)

		s.logger.Debug("adding file to backup zip", "path", path, "zip_path", zipPath)
		zf, err := zw.Create(zipPath)
		if err != nil {
			return err
		}

		fsFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fsFile.Close()

		_, err = io.Copy(zf, fsFile)
		return err
	})

	if err != nil {
		s.logger.Error("backup failed to zip uploads", "err", err)
		// return nil if zip succeeds
		// TODO: implement better handling of this case
	}

	// Log audit
	audit.Log(ctx, s.store, "BACKUP", "SYSTEM", 0, "Downloaded system backup", nil, nil)
	return nil
}

func (s *service) cleanOldTempFiles() {
	entries, err := os.ReadDir(s.uploadsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && (strings.HasPrefix(entry.Name(), ".restore_tmp_") || strings.HasPrefix(entry.Name(), ".uploads_bak_")) {
			s.logger.Info("cleaning up old temporary restore directory", "name", entry.Name())
			os.RemoveAll(filepath.Join(s.uploadsDir, entry.Name()))
		}
	}
}

func (s *service) Restore(ctx context.Context, zipReader io.ReaderAt, size int64) error {
	s.logger.Debug("starting system restore", "size", size)
	s.cleanOldTempFiles()

	zr, err := zip.NewReader(zipReader, size)
	if err != nil {
		return fmt.Errorf("invalid ZIP file: %w", err)
	}

	// Validation: Find & Parse JSON Manifest
	var manifest Manifest
	var manifestFound bool

	for _, f := range zr.File {
		if f.Name == "restore_data.json" {
			s.logger.Debug("found restore_data.json in backup")
			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("failed to open restore_data.json: %w", err)
			}
			if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
				rc.Close()
				return fmt.Errorf("failed to parse backup JSON: %w", err)
			}
			rc.Close()
			manifestFound = true
			break
		}
	}

	if !manifestFound {
		return errors.New("invalid Backup: restore_data.json missing")
	}

	// Preparation: Extract Uploads to Temp Directory
	timestamp := time.Now().UnixNano()
	// Use a hidden folder inside uploadsDir to ensure same-filesystem operations
	// This avoids "cross-device link" errors when uploadsDir is a Docker volume
	tempDir := filepath.Join(s.uploadsDir, fmt.Sprintf(".restore_tmp_%d", timestamp))

	s.logger.Debug("extracting uploads to temp directory", "temp_dir", tempDir)
	defer os.RemoveAll(tempDir)

	if err := os.MkdirAll(tempDir, 0755); err != nil {
		s.logger.Error("failed to create temp restore directory", "err", err, "path", tempDir)
		return fmt.Errorf("failed to create temp restore dir: %w", err)
	}

	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "uploads/") && !f.FileInfo().IsDir() {
			relPath := strings.TrimPrefix(f.Name, "uploads/")
			targetPath := filepath.Join(tempDir, relPath)

			// Security check
			if !strings.HasPrefix(filepath.Clean(targetPath), tempDir) {
				s.logger.Warn("security block: zip entry attempts to escape temp directory", "entry", f.Name)
				continue
			}

			s.logger.Debug("extracting file", "zip_path", f.Name, "target_path", targetPath)
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				s.logger.Error("failed to create temp restore subdirectory", "err", err, "path", filepath.Dir(targetPath))
				return fmt.Errorf("failed to create temp subdir: %w", err)
			}

			outFile, err := os.Create(targetPath)
			if err != nil {
				s.logger.Error("failed to create temp restore file", "err", err, "path", targetPath)
				return fmt.Errorf("failed to create temp file: %w", err)
			}

			rc, err := f.Open()
			if err != nil {
				outFile.Close()
				s.logger.Error("failed to open zip file entry for restore", "err", err, "name", f.Name)
				return fmt.Errorf("failed to open zip file entry: %w", err)
			}
			_, err = io.Copy(outFile, rc)
			rc.Close()
			outFile.Close()
			if err != nil {
				s.logger.Error("failed to write temp restore file", "err", err, "path", targetPath)
				return fmt.Errorf("failed to write temp file: %w", err)
			}
		}
	}

	// Database Restore Transaction
	err = s.store.ExecTx(ctx, func(qtx db.Querier) error {
		s.logger.Debug("clearing existing database records")
		// Order matters for Foreign Keys
		if err := qtx.ClearAuditLogs(ctx); err != nil {
			return fmt.Errorf("failed to clear audit logs: %w", err)
		}
		if err := qtx.ClearPartTags(ctx); err != nil {
			return fmt.Errorf("failed to clear part tags: %w", err)
		}
		if err := qtx.ClearTags(ctx); err != nil {
			return fmt.Errorf("failed to clear tags: %w", err)
		}
		if err := qtx.ClearPartAssignments(ctx); err != nil {
			return fmt.Errorf("failed to clear part assignments: %w", err)
		}
		if err := qtx.ClearPartDocs(ctx); err != nil {
			return fmt.Errorf("failed to clear part docs: %w", err)
		}
		if err := qtx.ClearPartLinks(ctx); err != nil {
			return fmt.Errorf("failed to clear part links: %w", err)
		}
		if err := qtx.ClearPartAiPrompts(ctx); err != nil {
			return fmt.Errorf("failed to clear part ai prompts: %w", err)
		}
		if err := qtx.ClearParts(ctx); err != nil {
			return fmt.Errorf("failed to clear parts: %w", err)
		}
		if err := qtx.ClearBins(ctx); err != nil {
			return fmt.Errorf("failed to clear bins: %w", err)
		}
		if err := qtx.ClearWallCards(ctx); err != nil {
			return fmt.Errorf("failed to clear wall cards: %w", err)
		}
		if err := qtx.ClearWalls(ctx); err != nil {
			return fmt.Errorf("failed to clear walls: %w", err)
		}
		if err := qtx.ClearContainers(ctx); err != nil {
			return fmt.Errorf("failed to clear containers: %w", err)
		}
		if err := qtx.ClearControllers(ctx); err != nil {
			return fmt.Errorf("failed to clear controllers: %w", err)
		}
		if err := qtx.ClearUsers(ctx); err != nil {
			return fmt.Errorf("failed to clear users: %w", err)
		}

		s.logger.Debug("restoring database records from manifest")
		return s.restoreData(ctx, qtx, manifest)
	})

	if err != nil {
		s.logger.Error("failed to restore database", "err", err)
		return err
	}

	// Atomic Swap of Assets (Modified for Volume compatibility)
	s.logger.Debug("swapping upload contents", "dir", s.uploadsDir)

	// Create backup folder inside uploadsDir
	backupDir := filepath.Join(s.uploadsDir, fmt.Sprintf(".uploads_bak_%d", timestamp))
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup dir: %w", err)
	}
	defer os.RemoveAll(backupDir)

	// Helper to move contents
	moveContents := func(src, dst string) error {
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			// Skip the special directories we created
			if entry.Name() == filepath.Base(tempDir) || entry.Name() == filepath.Base(backupDir) {
				continue
			}
			srcPath := filepath.Join(src, entry.Name())
			dstPath := filepath.Join(dst, entry.Name())
			if err := os.Rename(srcPath, dstPath); err != nil {
				return err
			}
		}
		return nil
	}

	// Move current contents to backup
	if err := moveContents(s.uploadsDir, backupDir); err != nil {
		return fmt.Errorf("failed to move current uploads to backup: %w", err)
	}

	// Move new contents from temp to live
	if err := moveContents(tempDir, s.uploadsDir); err != nil {
		s.logger.Error("failed to move new uploads to live, attempting rollback", "err", err)
		// Rollback
		if rbErr := moveContents(backupDir, s.uploadsDir); rbErr != nil {
			s.logger.Error("FATAL: rollback failed", "err", rbErr)
		}
		return fmt.Errorf("failed to swap new uploads: %w", err)
	}

	return nil
}

func (s *service) restoreData(ctx context.Context, qtx db.Querier, manifest Manifest) error {
	// Settings
	err := qtx.RestoreSettings(ctx, db.RestoreSettingsParams{
		RequireAuthForRead:   manifest.Settings.RequireAuthForRead,
		LocateTimeoutSeconds: manifest.Settings.LocateTimeoutSeconds,
		EnableLocateTimeout:  manifest.Settings.EnableLocateTimeout,
		ColorLocate:          manifest.Settings.ColorLocate,
		ColorStockOk:         manifest.Settings.ColorStockOk,
		ColorStockLow:        manifest.Settings.ColorStockLow,
		ColorStockCritical:   manifest.Settings.ColorStockCritical,
		CreatedAt:            manifest.Settings.CreatedAt,
		UpdatedAt:            manifest.Settings.UpdatedAt,
	})
	if err != nil {
		return fmt.Errorf("settings restore: %w", err)
	}

	for _, u := range manifest.Users {
		if err := qtx.RestoreUser(ctx, db.RestoreUserParams(u)); err != nil {
			return fmt.Errorf("user restore: %w", err)
		}
	}
	for _, c := range manifest.Controllers {
		if err := qtx.RestoreController(ctx, db.RestoreControllerParams(c)); err != nil {
			return fmt.Errorf("controller restore: %w", err)
		}
	}
	for _, c := range manifest.Containers {
		if err := qtx.RestoreContainer(ctx, db.RestoreContainerParams{
			ID:            c.ID,
			Name:          c.Name,
			ControllerID:  c.ControllerID,
			SegmentID:     c.SegmentID,
			ConfigJson:    c.ConfigJson,
			PositionIndex: c.PositionIndex,
			CreatedAt:     c.CreatedAt,
			UpdatedAt:     c.UpdatedAt,
		}); err != nil {
			return fmt.Errorf("container restore: %w", err)
		}
	}
	for _, w := range manifest.Walls {
		if err := qtx.RestoreWall(ctx, db.RestoreWallParams{
			ID:          w.ID,
			Name:        w.Name,
			Description: w.Description,
			CreatedAt:   w.CreatedAt,
			UpdatedAt:   w.UpdatedAt,
		}); err != nil {
			return fmt.Errorf("wall restore: %w", err)
		}
	}
	for _, wc := range manifest.WallCards {
		if err := qtx.RestoreWallCard(ctx, db.RestoreWallCardParams{
			ID:            wc.ID,
			WallID:        wc.WallID,
			ContainerID:   wc.ContainerID,
			PositionIndex: wc.PositionIndex,
			ConfigJson:    wc.ConfigJson,
		}); err != nil {
			return fmt.Errorf("wall_card restore: %w", err)
		}
	}
	for _, b := range manifest.Bins {
		if err := qtx.RestoreBin(ctx, db.RestoreBinParams(b)); err != nil {
			return fmt.Errorf("bin restore: %w", err)
		}
	}
	for _, p := range manifest.Parts {
		if err := qtx.RestorePart(ctx, db.RestorePartParams{
			ID:                p.ID,
			Name:              p.Name,
			Description:       p.Description,
			PartNumber:        p.PartNumber,
			Manufacturer:      p.Manufacturer,
			Supplier:          p.Supplier,
			UnitCost:          p.UnitCost,
			ReorderLevel:      p.ReorderLevel,
			MinStockThreshold: p.MinStockThreshold,
			ImagePath:         p.ImagePath,
			BarcodeData:       p.BarcodeData,
			IsFavorite:        p.IsFavorite,
			Tags:              p.Tags,
			CreatedAt:         p.CreatedAt,
			UpdatedAt:         p.UpdatedAt,
		}); err != nil {
			return fmt.Errorf("part restore: %w", err)
		}
	}
	for _, a := range manifest.PartAssignments {
		if err := qtx.RestorePartAssignment(ctx, db.RestorePartAssignmentParams(a)); err != nil {
			return fmt.Errorf("assignment restore: %w", err)
		}
	}
	for _, l := range manifest.PartLinks {
		if err := qtx.RestorePartLink(ctx, db.RestorePartLinkParams(l)); err != nil {
			return fmt.Errorf("link restore: %w", err)
		}
	}
	for _, d := range manifest.PartDocs {
		if err := qtx.RestorePartDoc(ctx, db.RestorePartDocParams(d)); err != nil {
			return fmt.Errorf("doc restore: %w", err)
		}
	}
	for _, p := range manifest.PartAiPrompts {
		if err := qtx.RestorePartAiPrompt(ctx, db.RestorePartAiPromptParams(p)); err != nil {
			return fmt.Errorf("prompt restore: %w", err)
		}
	}
	for _, t := range manifest.Tags {
		if err := qtx.RestoreTag(ctx, db.RestoreTagParams{
			ID:   t.ID,
			Name: t.Name,
		}); err != nil {
			return fmt.Errorf("tag restore: %w", err)
		}
	}
	for _, pt := range manifest.PartTags {
		if err := qtx.RestorePartTag(ctx, db.RestorePartTagParams(pt)); err != nil {
			return fmt.Errorf("part_tag restore: %w", err)
		}
	}
	for _, l := range manifest.AuditLogs {
		if err := qtx.RestoreAuditLog(ctx, db.RestoreAuditLogParams(l)); err != nil {
			return fmt.Errorf("audit log restore: %w", err)
		}
	}
	return nil
}
