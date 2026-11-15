package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/logger"
)

const (
	defaultMaxEntryBytes int64 = 100 << 20 // 100 MB per archive entry
	defaultMaxTotalBytes int64 = 500 << 20 // 500 MB total decompressed archive limit
)

var disallowedDocExtensions = map[string]struct{}{
	".html":  {},
	".htm":   {},
	".xhtml": {},
	".svg":   {},
	".js":    {},
	".mjs":   {},
}

type Service interface {
	Export(ctx context.Context, w io.Writer) error
	Restore(ctx context.Context, zipReader io.ReaderAt, size int64) error
}

type service struct {
	db            *sql.DB
	store         db.Store
	uploadsDir    string
	logger        *slog.Logger
	maxEntryBytes int64
	maxTotalBytes int64
	now           func() time.Time
}

func NewService(database *sql.DB, store db.Store, uploadsDir string, log *slog.Logger) Service {
	return newServiceWithLimits(database, store, uploadsDir, log, defaultMaxEntryBytes, defaultMaxTotalBytes)
}

func newServiceWithLimits(database *sql.DB, store db.Store, uploadsDir string, log *slog.Logger, maxEntryBytes, maxTotalBytes int64) *service {
	return &service{
		db:            database,
		store:         store,
		uploadsDir:    uploadsDir,
		logger:        log,
		maxEntryBytes: maxEntryBytes,
		maxTotalBytes: maxTotalBytes,
		now:           time.Now,
	}
}

func (s *service) Export(ctx context.Context, w io.Writer) error {
	s.logger.Debug("starting system backup export")

	manifest, parts, assignments, err := s.fetchBackupManifest(ctx)
	if err != nil {
		return err
	}

	zw := zip.NewWriter(w)
	closed := false
	defer func() {
		if !closed {
			_ = zw.Close()
		}
	}()

	s.logger.Debug("adding restore_data.json to backup")
	fJSON, err := zw.Create("restore_data.json")
	if err != nil {
		return fmt.Errorf("failed to create json entry: %w", err)
	}
	enc := json.NewEncoder(fJSON)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return fmt.Errorf("failed to encode json: %w", err)
	}

	s.logger.Debug("adding human_readable_parts.csv to backup")
	if err := writePartsCSV(zw, parts, assignments); err != nil {
		return err
	}

	s.logger.Debug("collecting upload files for backup", "dir", s.uploadsDir)
	if err := s.writeUploadsToZip(zw); err != nil {
		return fmt.Errorf("failed to archive uploads: %w", err)
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("failed to finalize backup zip: %w", err)
	}
	closed = true

	audit.Log(ctx, s.store, "BACKUP", "SYSTEM", 0, "Downloaded system backup", nil, nil)
	return nil
}

func (s *service) fetchBackupManifest(ctx context.Context) (*Manifest, []db.Part, []db.PartAssignment, error) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, fmt.Errorf("failed to fetch settings: %w", err)
	}

	users, err := s.store.GetAllUsers(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch users: %w", err)
	}
	controllers, err := s.store.GetControllers(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch controllers: %w", err)
	}
	containers, err := s.store.GetAllContainers(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch containers: %w", err)
	}
	walls, err := s.store.GetWalls(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch walls: %w", err)
	}
	wallCards, err := s.store.GetAllWallCards(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch wall cards: %w", err)
	}
	bins, err := s.store.GetAllBins(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch bins: %w", err)
	}
	parts, err := s.store.GetAllParts(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch parts: %w", err)
	}
	assignments, err := s.store.GetAllPartAssignments(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch part assignments: %w", err)
	}
	links, err := s.store.GetAllPartLinks(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch part links: %w", err)
	}
	docs, err := s.store.GetAllPartDocs(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch part docs: %w", err)
	}
	prompts, err := s.store.GetAllPartAiPrompts(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch part ai prompts: %w", err)
	}
	logs, err := s.store.GetAllAuditLogs(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch audit logs: %w", err)
	}
	tags, err := s.store.ListAllTags(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch tags: %w", err)
	}
	partTags, err := s.store.GetAllPartTags(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch part tags: %w", err)
	}
	templates, err := s.store.GetAllInspirationTemplates(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch inspiration templates: %w", err)
	}
	flags, err := s.store.GetAllSystemFlags(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch system flags: %w", err)
	}
	projects, err := s.store.GetAllProjects(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch projects: %w", err)
	}
	bomItems, err := s.store.GetAllProjectBOMItems(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch project bom items: %w", err)
	}

	auditLogs := make([]db.AuditLog, 0, len(logs))
	for _, l := range logs {
		auditLogs = append(auditLogs, db.AuditLog{
			ID:         l.ID,
			UserID:     l.UserID,
			ActionType: l.ActionType,
			EntityType: l.EntityType,
			EntityID:   l.EntityID,
			Details:    l.Details,
			OldValue:   toJSONRaw(l.OldValue),
			NewValue:   toJSONRaw(l.NewValue),
			CreatedAt:  l.CreatedAt,
		})
	}

	manifest := &Manifest{
		Version:              ManifestVersionV2,
		ExportedAt:           s.now(),
		Settings:             settings,
		Users:                users,
		Controllers:          controllers,
		Containers:           containers,
		Walls:                walls,
		WallCards:            wallCards,
		Bins:                 bins,
		Parts:                parts,
		PartAssignments:      assignments,
		PartLinks:            links,
		PartDocs:             docs,
		PartAiPrompts:        prompts,
		Tags:                 tags,
		PartTags:             partTags,
		AuditLogs:            auditLogs,
		InspirationTemplates: templates,
		SystemFlags:          flags,
		Projects:             projects,
		ProjectBomItems:      bomItems,
	}
	return manifest, parts, assignments, nil
}

func toJSONRaw(b []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return nil
	}
	if json.Valid(trimmed) {
		return json.RawMessage(trimmed)
	}
	encoded, _ := json.Marshal(string(b))
	return json.RawMessage(encoded)
}

func writePartsCSV(zw *zip.Writer, parts []db.Part, assignments []db.PartAssignment) error {
	fCSV, err := zw.Create("human_readable_parts.csv")
	if err != nil {
		return fmt.Errorf("failed to create csv entry: %w", err)
	}

	totals := make(map[int64]int64, len(parts))
	for _, a := range assignments {
		totals[a.PartID] += a.Quantity
	}

	cw := csv.NewWriter(fCSV)
	if err := cw.Write([]string{"Name", "Description", "Part Number", "Manufacturer", "Supplier", "Unit Cost", "Reorder Level", "Min Stock", "Barcode", "Quantity"}); err != nil {
		return fmt.Errorf("failed to write csv header: %w", err)
	}
	for _, p := range parts {
		if err := cw.Write([]string{
			p.Name,
			p.Description.String,
			p.PartNumber.String,
			p.Manufacturer.String,
			p.Supplier.String,
			fmt.Sprintf("%.2f", p.UnitCost.Float64),
			fmt.Sprintf("%d", p.ReorderLevel.Int64),
			fmt.Sprintf("%d", p.MinStockThreshold.Int64),
			p.BarcodeData.String,
			fmt.Sprintf("%d", totals[p.ID]),
		}); err != nil {
			return fmt.Errorf("failed to write csv row for part %d: %w", p.ID, err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("failed to flush csv writer: %w", err)
	}
	return nil
}

func (s *service) writeUploadsToZip(zw *zip.Writer) error {
	if _, err := os.Stat(s.uploadsDir); errors.Is(err, os.ErrNotExist) {
		s.logger.Debug("uploads directory not found, skipping", "path", s.uploadsDir)
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to stat uploads directory: %w", err)
	}

	return filepath.Walk(s.uploadsDir, func(diskPath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Ignore hidden files/directories (like .restore_tmp, .uploads_bak, .git, etc.)
		if strings.HasPrefix(info.Name(), ".") && diskPath != s.uploadsDir {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		relInZip, err := filepath.Rel(s.uploadsDir, diskPath)
		if err != nil {
			return fmt.Errorf("failed to compute relative upload path: %w", err)
		}
		zipPath := path.Join("uploads", filepath.ToSlash(relInZip))

		s.logger.Debug("adding file to backup zip", "path", diskPath, "zip_path", zipPath)
		return addFileToZip(zw, zipPath, diskPath)
	})
}

func addFileToZip(zw *zip.Writer, zipPath, diskPath string) error {
	zf, err := zw.Create(zipPath)
	if err != nil {
		return err
	}

	fsFile, err := os.Open(diskPath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(zf, fsFile)
	closeErr := fsFile.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func (s *service) cleanOldTempFiles() {
	entries, err := os.ReadDir(s.uploadsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && (strings.HasPrefix(entry.Name(), ".restore_tmp_") || strings.HasPrefix(entry.Name(), ".uploads_bak_")) {
			s.logger.Info("cleaning up old temporary restore directory", "name", entry.Name())
			_ = os.RemoveAll(filepath.Join(s.uploadsDir, entry.Name()))
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

	manifest, totalExtracted, err := s.readManifestFromZip(zr)
	if err != nil {
		return err
	}

	timestamp := s.now().UnixNano()
	// Use a hidden folder inside uploadsDir to ensure same-filesystem operations
	// so os.Rename avoids cross-device link errors when uploadsDir is a Docker volume.
	tempDir := filepath.Join(s.uploadsDir, fmt.Sprintf(".restore_tmp_%d", timestamp))

	s.logger.Debug("extracting uploads to temp directory", "temp_dir", tempDir)
	defer os.RemoveAll(tempDir)

	if err := os.MkdirAll(tempDir, 0755); err != nil {
		s.logger.Error("failed to create temp restore directory", "err", err, "path", tempDir)
		return fmt.Errorf("failed to create temp restore dir: %w", err)
	}

	if err := s.extractUploads(zr, tempDir, totalExtracted); err != nil {
		return err
	}

	err = s.store.ExecTx(ctx, func(qtx db.Querier) error {
		if err := clearDatabaseTables(ctx, qtx); err != nil {
			return err
		}
		s.logger.Debug("restoring database records from manifest")
		return s.restoreData(ctx, qtx, manifest)
	})
	if err != nil {
		s.logger.Error("failed to restore database", "err", err)
		return err
	}

	// Sync runtime debug level with restored settings.
	logger.SetDebug(manifest.Settings.EnableDebugLogs.Bool)

	if err := s.swapUploadsDir(tempDir, timestamp); err != nil {
		return err
	}

	return nil
}

func (s *service) entryReadLimit(totalExtracted int64) int64 {
	remaining := s.maxTotalBytes - totalExtracted
	if remaining < 0 {
		return 0
	}
	if s.maxEntryBytes < remaining {
		return s.maxEntryBytes
	}
	return remaining
}

func (s *service) readBoundedZipEntry(f *zip.File, totalExtracted int64) ([]byte, error) {
	limit := s.entryReadLimit(totalExtracted)
	if f.UncompressedSize64 > uint64(s.maxEntryBytes) {
		return nil, fmt.Errorf("security block: zip entry %q exceeds maximum entry size (%d bytes)", f.Name, s.maxEntryBytes)
	}
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("security block: zip extraction exceeds maximum total size (%d bytes)", s.maxTotalBytes)
	}

	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open zip entry %q: %w", f.Name, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read zip entry %q: %w", f.Name, err)
	}
	if int64(len(data)) > limit {
		if s.maxEntryBytes <= s.maxTotalBytes-totalExtracted {
			return nil, fmt.Errorf("security block: zip entry %q exceeds maximum entry size (%d bytes)", f.Name, s.maxEntryBytes)
		}
		return nil, fmt.Errorf("security block: zip extraction exceeds maximum total size (%d bytes)", s.maxTotalBytes)
	}
	return data, nil
}

func (s *service) readManifestFromZip(zr *zip.Reader) (Manifest, int64, error) {
	var manifest Manifest
	for _, f := range zr.File {
		if f.Name != "restore_data.json" {
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return Manifest{}, 0, errors.New("security block: restore_data.json cannot be a symlink")
		}
		s.logger.Debug("found restore_data.json in backup")
		raw, err := s.readBoundedZipEntry(f, 0)
		if err != nil {
			return Manifest{}, 0, err
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return Manifest{}, 0, fmt.Errorf("failed to parse backup JSON: %w", err)
		}
		if manifest.Version != ManifestVersionV1 && manifest.Version != ManifestVersionV2 {
			return Manifest{}, 0, fmt.Errorf("unsupported backup version: %q", manifest.Version)
		}
		return manifest, int64(len(raw)), nil
	}
	return Manifest{}, 0, errors.New("invalid Backup: restore_data.json missing")
}

func resolveUploadTarget(tempDir, entryName string) (targetPath, relPath string, isUpload bool, err error) {
	normalized := strings.ReplaceAll(entryName, "\\", "/")
	if normalized == "uploads" || normalized == "uploads/" {
		return "", "", false, nil
	}
	if !strings.HasPrefix(normalized, "uploads/") {
		return "", "", false, nil
	}

	rawRel := strings.TrimPrefix(normalized, "uploads/")
	cleanTempDir := filepath.Clean(tempDir)
	cleanTarget := filepath.Clean(filepath.Join(cleanTempDir, filepath.FromSlash(rawRel)))

	rel, relErr := filepath.Rel(cleanTempDir, cleanTarget)
	if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", "", false, fmt.Errorf("security block: zip entry %q attempts path traversal outside restore dir", entryName)
	}

	return cleanTarget, rel, true, nil
}

func isDisallowedDocUpload(relPath string) (string, bool) {
	relSlash := strings.ToLower(filepath.ToSlash(relPath))
	if relSlash != "docs" && !strings.HasPrefix(relSlash, "docs/") {
		return "", false
	}
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(relSlash)))
	_, blocked := disallowedDocExtensions[ext]
	return ext, blocked
}

func (s *service) extractUploads(zr *zip.Reader, tempDir string, totalExtracted int64) error {
	for _, f := range zr.File {
		targetPath, relPath, isUpload, err := resolveUploadTarget(tempDir, f.Name)
		if err != nil {
			s.logger.Warn("security block on backup entry", "entry", f.Name, "err", err)
			return err
		}
		if !isUpload {
			continue
		}

		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("security block: symlink entry %q is not allowed", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("failed to create temp subdir: %w", err)
			}
			continue
		}

		if ext, blocked := isDisallowedDocUpload(relPath); blocked {
			return fmt.Errorf("security block: disallowed document extension %q in %q", ext, f.Name)
		}

		s.logger.Debug("extracting file", "zip_path", f.Name, "target_path", targetPath)
		written, err := s.extractSingleUploadFile(f, targetPath, totalExtracted)
		if err != nil {
			return err
		}
		totalExtracted += written
	}
	return nil
}

func (s *service) extractSingleUploadFile(f *zip.File, targetPath string, totalExtracted int64) (int64, error) {
	limit := s.entryReadLimit(totalExtracted)
	if f.UncompressedSize64 > uint64(s.maxEntryBytes) {
		return 0, fmt.Errorf("security block: zip entry %q exceeds maximum entry size (%d bytes)", f.Name, s.maxEntryBytes)
	}
	if f.UncompressedSize64 > uint64(limit) {
		return 0, fmt.Errorf("security block: zip extraction exceeds maximum total size (%d bytes)", s.maxTotalBytes)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return 0, fmt.Errorf("failed to create temp subdir: %w", err)
	}

	outFile, err := os.Create(targetPath)
	if err != nil {
		return 0, fmt.Errorf("failed to create temp file: %w", err)
	}

	rc, err := f.Open()
	if err != nil {
		_ = outFile.Close()
		return 0, fmt.Errorf("failed to open zip file entry: %w", err)
	}

	written, copyErr := io.Copy(outFile, io.LimitReader(rc, limit+1))
	rcErr := rc.Close()
	closeErr := outFile.Close()

	if copyErr != nil {
		return 0, fmt.Errorf("failed to write temp file: %w", copyErr)
	}
	if rcErr != nil {
		return 0, fmt.Errorf("failed to close zip entry: %w", rcErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("failed to finalize temp file: %w", closeErr)
	}
	if written > limit {
		if s.maxEntryBytes <= s.maxTotalBytes-totalExtracted {
			return 0, fmt.Errorf("security block: zip entry %q exceeds maximum entry size (%d bytes)", f.Name, s.maxEntryBytes)
		}
		return 0, fmt.Errorf("security block: zip extraction exceeds maximum total size (%d bytes)", s.maxTotalBytes)
	}
	return written, nil
}

func (s *service) swapUploadsDir(tempDir string, timestamp int64) error {
	s.logger.Debug("swapping upload contents", "dir", s.uploadsDir)

	backupDir := filepath.Join(s.uploadsDir, fmt.Sprintf(".uploads_bak_%d", timestamp))
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup dir: %w", err)
	}
	defer os.RemoveAll(backupDir)

	moveContents := func(src, dst string) error {
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
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

	if err := moveContents(s.uploadsDir, backupDir); err != nil {
		return fmt.Errorf("failed to move current uploads to backup: %w", err)
	}

	if err := moveContents(tempDir, s.uploadsDir); err != nil {
		s.logger.Error("failed to move new uploads to live, attempting rollback", "err", err)
		if rbErr := moveContents(backupDir, s.uploadsDir); rbErr != nil {
			s.logger.Error("FATAL: rollback failed", "err", rbErr)
		}
		return fmt.Errorf("failed to swap new uploads: %w", err)
	}
	return nil
}

func clearDatabaseTables(ctx context.Context, qtx db.Querier) error {
	type clearStep struct {
		name string
		fn   func(context.Context) error
	}
	// Ordered child-before-parent to satisfy foreign key constraints.
	steps := []clearStep{
		{"sessions", qtx.ClearSessions},
		{"api tokens", qtx.ClearAPITokens},
		{"audit logs", qtx.ClearAuditLogs},
		{"project bom items", qtx.ClearProjectBOMItems},
		{"projects", qtx.ClearProjects},
		{"part tags", qtx.ClearPartTags},
		{"tags", qtx.ClearTags},
		{"part assignments", qtx.ClearPartAssignments},
		{"part docs", qtx.ClearPartDocs},
		{"part links", qtx.ClearPartLinks},
		{"part ai prompts", qtx.ClearPartAiPrompts},
		{"parts", qtx.ClearParts},
		{"bins", qtx.ClearBins},
		{"wall cards", qtx.ClearWallCards},
		{"walls", qtx.ClearWalls},
		{"containers", qtx.ClearContainers},
		{"controllers", qtx.ClearControllers},
		{"users", qtx.ClearUsers},
		{"inspiration templates", qtx.ClearInspirationTemplates},
		{"system flags", qtx.ClearSystemFlags},
	}
	for _, step := range steps {
		if err := step.fn(ctx); err != nil {
			return fmt.Errorf("failed to clear %s: %w", step.name, err)
		}
	}
	return nil
}

func normalizeAuditValue(v any) (any, error) {
	switch val := v.(type) {
	case nil:
		return nil, nil
	case []byte:
		if len(val) == 0 {
			return nil, nil
		}
		return val, nil
	case json.RawMessage:
		if len(val) == 0 {
			return nil, nil
		}
		return []byte(val), nil
	case string:
		if val == "" {
			return nil, nil
		}
		return []byte(val), nil
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
}

func defaultControllerForRestore(c db.Controller) db.RestoreControllerParams {
	if !c.UseUdp.Valid {
		c.UseUdp = sql.NullBool{Bool: false, Valid: true}
	}
	if !c.UdpPort.Valid {
		c.UdpPort = sql.NullInt64{Int64: 21324, Valid: true}
	}
	if !c.RestoreState.Valid {
		c.RestoreState = sql.NullBool{Bool: true, Valid: true}
	}
	if !c.IdlePresetID.Valid {
		c.IdlePresetID = sql.NullInt64{Int64: 0, Valid: true}
	}
	return db.RestoreControllerParams(c)
}

func (s *service) restoreData(ctx context.Context, qtx db.Querier, manifest Manifest) error {
	locateColorByStock := manifest.Settings.LocateColorByStock
	if !locateColorByStock.Valid {
		locateColorByStock = sql.NullBool{Bool: false, Valid: true}
	}
	enableControllerPing := manifest.Settings.EnableControllerPing
	if !enableControllerPing.Valid {
		enableControllerPing = sql.NullBool{Bool: true, Valid: true}
	}
	pingInterval := manifest.Settings.ControllerPingIntervalSeconds
	if !pingInterval.Valid || pingInterval.Int64 <= 0 {
		pingInterval = sql.NullInt64{Int64: 600, Valid: true}
	}

	if err := qtx.RestoreSettings(ctx, db.RestoreSettingsParams{
		RequireAuthForRead:            manifest.Settings.RequireAuthForRead,
		LocateTimeoutSeconds:          manifest.Settings.LocateTimeoutSeconds,
		EnableLocateTimeout:           manifest.Settings.EnableLocateTimeout,
		EnableDebugLogs:               manifest.Settings.EnableDebugLogs,
		ColorLocate:                   manifest.Settings.ColorLocate,
		ColorStockOk:                  manifest.Settings.ColorStockOk,
		ColorStockLow:                 manifest.Settings.ColorStockLow,
		ColorStockCritical:            manifest.Settings.ColorStockCritical,
		CreatedAt:                     manifest.Settings.CreatedAt,
		UpdatedAt:                     manifest.Settings.UpdatedAt,
		InspirationSeedsApplied:       manifest.Settings.InspirationSeedsApplied,
		LocateColorByStock:            locateColorByStock,
		EnableControllerPing:          enableControllerPing,
		ControllerPingIntervalSeconds: pingInterval,
	}); err != nil {
		return fmt.Errorf("settings restore: %w", err)
	}

	if err := s.restoreEntities(ctx, qtx, manifest); err != nil {
		return err
	}
	return s.restoreV2AndAudit(ctx, qtx, manifest)
}

func (s *service) restoreEntities(ctx context.Context, qtx db.Querier, manifest Manifest) error {
	for _, u := range manifest.Users {
		if err := qtx.RestoreUser(ctx, db.RestoreUserParams(u)); err != nil {
			return fmt.Errorf("user restore: %w", err)
		}
	}
	for _, c := range manifest.Controllers {
		if err := qtx.RestoreController(ctx, defaultControllerForRestore(c)); err != nil {
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
	return nil
}

func (s *service) restoreV2AndAudit(ctx context.Context, qtx db.Querier, manifest Manifest) error {
	for _, t := range manifest.InspirationTemplates {
		if err := qtx.RestoreInspirationTemplate(ctx, db.RestoreInspirationTemplateParams(t)); err != nil {
			return fmt.Errorf("inspiration template restore: %w", err)
		}
	}
	for _, f := range manifest.SystemFlags {
		if err := qtx.RestoreSystemFlag(ctx, db.RestoreSystemFlagParams(f)); err != nil {
			return fmt.Errorf("system flag restore: %w", err)
		}
	}
	for _, pr := range manifest.Projects {
		if err := qtx.RestoreProject(ctx, db.RestoreProjectParams(pr)); err != nil {
			return fmt.Errorf("project restore: %w", err)
		}
	}
	for _, bi := range manifest.ProjectBomItems {
		if err := qtx.RestoreProjectBOMItem(ctx, db.RestoreProjectBOMItemParams(bi)); err != nil {
			return fmt.Errorf("project bom item restore: %w", err)
		}
	}
	for _, l := range manifest.AuditLogs {
		oldVal, err := normalizeAuditValue(l.OldValue)
		if err != nil {
			return fmt.Errorf("audit log old_value normalize: %w", err)
		}
		newVal, err := normalizeAuditValue(l.NewValue)
		if err != nil {
			return fmt.Errorf("audit log new_value normalize: %w", err)
		}
		if err := qtx.RestoreAuditLog(ctx, db.RestoreAuditLogParams{
			ID:         l.ID,
			UserID:     l.UserID,
			ActionType: l.ActionType,
			EntityType: l.EntityType,
			EntityID:   l.EntityID,
			Details:    l.Details,
			OldValue:   oldVal,
			NewValue:   newVal,
			CreatedAt:  l.CreatedAt,
		}); err != nil {
			return fmt.Errorf("audit log restore: %w", err)
		}
	}
	return nil
}
