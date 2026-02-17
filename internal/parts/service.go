package parts

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/images"
	"github.com/tuxedocurly/wledger/internal/tags"
	"github.com/tuxedocurly/wledger/web/pages"
)

type Service interface {
	CreatePart(ctx context.Context, req CreatePartRequest) (int64, error)
	UpdatePart(ctx context.Context, req UpdatePartRequest) error
	DeletePart(ctx context.Context, id int64) error
	DeleteParts(ctx context.Context, ids []int64) error
	ClonePart(ctx context.Context, id int64) (int64, error)
	GetPart(ctx context.Context, id int64) (db.Part, error)
	ListParts(ctx context.Context, search string, page int, binID *int64) ([]pages.PartView, error)
	GetPartDetail(ctx context.Context, id int64) (PartDetail, error)
}

type PartDetail struct {
	Part        db.Part
	Stock       []db.GetPartAssignmentsRow
	Links       []db.PartLink
	Docs        []db.PartDoc
	Controllers []db.Controller
}

type LinkDTO struct {
	ID    int64
	Label string
	URL   string
}

type DocUpload struct {
	File   io.Reader
	Header *multipart.FileHeader
}

type CreatePartRequest struct {
	Name              string
	Description       string
	PartNumber        string
	Manufacturer      string
	Supplier          string
	BarcodeData       string
	UnitCost          float64
	ReorderLevel      int
	MinStockThreshold int
	Image             *DocUpload
	Links             []LinkDTO
	Documents         []DocUpload
	Tags              []string
}

type UpdatePartRequest struct {
	ID                int64
	Name              string
	Description       string
	PartNumber        string
	Manufacturer      string
	Supplier          string
	BarcodeData       string
	UnitCost          float64
	ReorderLevel      int
	MinStockThreshold int
	Image             *DocUpload
	ExistingLinks     []LinkDTO
	NewLinks          []LinkDTO
	NewDocuments      []DocUpload
	Tags              []string
}

type service struct {
	database *sql.DB
	store    db.Store
	logger   *slog.Logger
	tags     tags.Service
	docs     documents.Service
}

func NewService(database *sql.DB, store db.Store, logger *slog.Logger, tags tags.Service, docs documents.Service) Service {
	return &service{
		database: database,
		store:    store,
		logger:   logger,
		tags:     tags,
		docs:     docs,
	}
}

func (s *service) GetPart(ctx context.Context, id int64) (db.Part, error) {
	return s.store.GetPart(ctx, id)
}

func (s *service) ListParts(ctx context.Context, search string, page int, binID *int64) ([]pages.PartView, error) {
	if page < 1 {
		page = 1
	}
	limit := 20
	offset := (page - 1) * limit

	var binIDParam interface{}
	if binID != nil {
		binIDParam = sql.NullInt64{Int64: *binID, Valid: true}
	} else {
		binIDParam = sql.NullInt64{Valid: false}
	}

	var viewParts []pages.PartView
	if search != "" {
		// FTS5 Search
		query := search + "*"
		rows, err := s.store.SearchParts(ctx, db.SearchPartsParams{
			Query:  sql.NullString{String: query, Valid: true},
			BinID:  binIDParam,
			Limit:  int64(limit),
			Offset: int64(offset),
		})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			viewParts = append(viewParts, pages.PartView{
				ID:            row.ID,
				Name:          row.Name,
				Description:   row.Description,
				PartNumber:    row.PartNumber,
				ImagePath:     row.ImagePath,
				IsFavorite:    row.IsFavorite,
				UnitCost:      row.UnitCost,
				TotalStock:    row.TotalStock,
				ValidStock:    row.ValidStock,
				OrphanedStock: row.OrphanedStock,
			})
		}
	} else {
		// Standard List
		rows, err := s.store.ListParts(ctx, db.ListPartsParams{
			BinID:  binIDParam,
			Limit:  int64(limit),
			Offset: int64(offset),
		})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			viewParts = append(viewParts, pages.PartView{
				ID:            row.ID,
				Name:          row.Name,
				Description:   row.Description,
				PartNumber:    row.PartNumber,
				ImagePath:     row.ImagePath,
				IsFavorite:    row.IsFavorite,
				UnitCost:      row.UnitCost,
				TotalStock:    row.TotalStock,
				ValidStock:    row.ValidStock,
				OrphanedStock: row.OrphanedStock,
			})
		}
	}
	return viewParts, nil
}

func (s *service) GetPartDetail(ctx context.Context, id int64) (PartDetail, error) {
	p, err := s.store.GetPart(ctx, id)
	if err != nil {
		return PartDetail{}, err
	}

	stock, _ := s.store.GetPartAssignments(ctx, id)
	links, _ := s.store.GetPartLinks(ctx, id)
	docs, _ := s.store.GetPartDocs(ctx, id)
	controllers, _ := s.store.GetControllers(ctx)

	return PartDetail{
		Part:        p,
		Stock:       stock,
		Links:       links,
		Docs:        docs,
		Controllers: controllers,
	}, nil
}

func (s *service) CreatePart(ctx context.Context, req CreatePartRequest) (int64, error) {
	s.logger.Debug("starting part creation", "name", req.Name, "barcode", req.BarcodeData)
	var imagePath string
	if req.Image != nil && req.Image.File != nil {
		s.logger.Debug("processing image upload", "name", req.Name)
		if mf, ok := req.Image.File.(multipart.File); ok {
			fileName, err := images.ProcessUpload(mf, req.Image.Header)
			mf.Close() // Close after processing
			if err == nil {
				imagePath = config.UrlPrefixImages + fileName
				s.logger.Debug("image uploaded successfully", "path", imagePath)
			} else {
				s.logger.Warn("failed to process image upload", "err", err)
			}
		}
	}

	var newID int64
	var uploadedDocs []string

	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		var err error
		newID, err = q.CreatePart(ctx, db.CreatePartParams{
			Name:              req.Name,
			Description:       sql.NullString{String: req.Description, Valid: req.Description != ""},
			PartNumber:        sql.NullString{String: req.PartNumber, Valid: req.PartNumber != ""},
			Manufacturer:      sql.NullString{String: req.Manufacturer, Valid: req.Manufacturer != ""},
			Supplier:          sql.NullString{String: req.Supplier, Valid: req.Supplier != ""},
			BarcodeData:       sql.NullString{String: req.BarcodeData, Valid: req.BarcodeData != ""},
			UnitCost:          sql.NullFloat64{Float64: req.UnitCost, Valid: true},
			ReorderLevel:      sql.NullInt64{Int64: int64(req.ReorderLevel), Valid: true},
			MinStockThreshold: sql.NullInt64{Int64: int64(req.MinStockThreshold), Valid: true},
			ImagePath:         sql.NullString{String: imagePath, Valid: imagePath != ""},
		})

		if err != nil {
			return err
		}
		s.logger.Debug("base part record created", "id", newID)

		for _, l := range req.Links {
			if l.URL == "" {
				continue
			}
			err = s.docs.AddLink(ctx, q, newID, l.URL, l.Label)
			if err != nil {
				s.logger.Error("failed to create part link during creation", "err", err, "part_name", req.Name)
				return fmt.Errorf("failed to create link: %w", err)
			}
		}

		for _, du := range req.Documents {
			path, err := s.docs.UploadDocument(ctx, q, newID, du.File, du.Header.Filename)
			// Close if it's a closer (it usually is if it came from multipart)
			if closer, ok := du.File.(io.Closer); ok {
				closer.Close()
			}

			if err != nil {
				return fmt.Errorf("failed to upload document %s: %w", du.Header.Filename, err)
			}
			uploadedDocs = append(uploadedDocs, path)
		}

		s.logger.Debug("syncing tags", "id", newID, "tags", req.Tags)
		if err := s.tags.SyncTags(ctx, q, newID, req.Tags); err != nil {
			return fmt.Errorf("failed to sync tags: %w", err)
		}

		summary := map[string]any{
			"id":          newID,
			"name":        req.Name,
			"part_number": req.PartNumber,
		}
		audit.Log(ctx, q, "CREATE", "PART", newID, "Created part "+req.Name, nil, summary)

		return nil
	})

	if err != nil {
		s.cleanupFiles(imagePath, uploadedDocs)
		return 0, err
	}

	return newID, nil
}

func (s *service) UpdatePart(ctx context.Context, req UpdatePartRequest) error {
	s.logger.Debug("starting part update", "id", req.ID, "name", req.Name)
	oldPart, err := s.store.GetPart(ctx, req.ID)
	if err != nil {
		return fmt.Errorf("part not found: %w", err)
	}

	newImagePath := oldPart.ImagePath.String
	uploadedNewImage := false
	if req.Image != nil && req.Image.File != nil {
		s.logger.Debug("processing new image upload", "id", req.ID)
		if mf, ok := req.Image.File.(multipart.File); ok {
			fileName, err := images.ProcessUpload(mf, req.Image.Header)
			mf.Close() // Close after processing
			if err == nil {
				newImagePath = config.UrlPrefixImages + fileName
				uploadedNewImage = true
				s.logger.Debug("new image uploaded", "id", req.ID, "path", newImagePath)
			} else {
				s.logger.Warn("failed to process new image upload", "err", err)
			}
		}
	}

	var uploadedDocs []string

	err = s.store.ExecTx(ctx, func(q db.Querier) error {
		err := q.UpdatePart(ctx, db.UpdatePartParams{
			Name:              req.Name,
			Description:       sql.NullString{String: req.Description, Valid: req.Description != ""},
			PartNumber:        sql.NullString{String: req.PartNumber, Valid: req.PartNumber != ""},
			Manufacturer:      sql.NullString{String: req.Manufacturer, Valid: req.Manufacturer != ""},
			Supplier:          sql.NullString{String: req.Supplier, Valid: req.Supplier != ""},
			BarcodeData:       sql.NullString{String: req.BarcodeData, Valid: req.BarcodeData != ""},
			UnitCost:          sql.NullFloat64{Float64: req.UnitCost, Valid: true},
			ReorderLevel:      sql.NullInt64{Int64: int64(req.ReorderLevel), Valid: true},
			MinStockThreshold: sql.NullInt64{Int64: int64(req.MinStockThreshold), Valid: true},
			ImagePath:         sql.NullString{String: newImagePath, Valid: newImagePath != ""},
			ID:                req.ID,
		})

		if err != nil {
			return err
		}

		for _, l := range req.ExistingLinks {
			if l.ID == 0 || l.URL == "" {
				continue
			}
			s.logger.Debug("updating existing link", "id", req.ID, "link_id", l.ID, "url", l.URL)
			err = q.UpdatePartLink(ctx, db.UpdatePartLinkParams{
				Url:   l.URL,
				Label: sql.NullString{String: l.Label, Valid: l.Label != ""},
				ID:    l.ID,
			})
			if err != nil {
				return fmt.Errorf("failed to update link: %w", err)
			}
		}

		for _, l := range req.NewLinks {
			if l.URL == "" {
				continue
			}
			err = s.docs.AddLink(ctx, q, req.ID, l.URL, l.Label)
			if err != nil {
				return fmt.Errorf("failed to create link: %w", err)
			}
		}

		for _, du := range req.NewDocuments {
			path, err := s.docs.UploadDocument(ctx, q, req.ID, du.File, du.Header.Filename)
			// Close if it's a closer
			if closer, ok := du.File.(io.Closer); ok {
				closer.Close()
			}

			if err != nil {
				return fmt.Errorf("failed to upload document %s: %w", du.Header.Filename, err)
			}
			uploadedDocs = append(uploadedDocs, path)
		}

		if err := s.tags.SyncTags(ctx, q, req.ID, req.Tags); err != nil {
			return fmt.Errorf("failed to sync tags: %w", err)
		}

		// Calculate Diff
		oldDiff := make(map[string]any)
		newDiff := make(map[string]any)

		if oldPart.Name != req.Name {
			oldDiff["name"] = oldPart.Name
			newDiff["name"] = req.Name
		}
		if oldPart.Description.String != req.Description {
			oldDiff["description"] = oldPart.Description.String
			newDiff["description"] = req.Description
		}
		if oldPart.PartNumber.String != req.PartNumber {
			oldDiff["part_number"] = oldPart.PartNumber.String
			newDiff["part_number"] = req.PartNumber
		}
		if oldPart.Manufacturer.String != req.Manufacturer {
			oldDiff["manufacturer"] = oldPart.Manufacturer.String
			newDiff["manufacturer"] = req.Manufacturer
		}
		if oldPart.Supplier.String != req.Supplier {
			oldDiff["supplier"] = oldPart.Supplier.String
			newDiff["supplier"] = req.Supplier
		}
		if oldPart.BarcodeData.String != req.BarcodeData {
			oldDiff["barcode_data"] = oldPart.BarcodeData.String
			newDiff["barcode_data"] = req.BarcodeData
		}
		if oldPart.UnitCost.Float64 != req.UnitCost {
			oldDiff["unit_cost"] = oldPart.UnitCost.Float64
			newDiff["unit_cost"] = req.UnitCost
		}
		if int(oldPart.ReorderLevel.Int64) != req.ReorderLevel {
			oldDiff["reorder_level"] = oldPart.ReorderLevel.Int64
			newDiff["reorder_level"] = req.ReorderLevel
		}
		if int(oldPart.MinStockThreshold.Int64) != req.MinStockThreshold {
			oldDiff["min_stock"] = oldPart.MinStockThreshold.Int64
			newDiff["min_stock"] = req.MinStockThreshold
		}

		if len(oldDiff) > 0 {
			audit.Log(ctx, q, "UPDATE", "PART", req.ID, "Updated details", oldDiff, newDiff)
		}

		return nil
	})

	if err != nil {
		s.cleanupFiles(uploadedNewImage, newImagePath, uploadedDocs)
		return fmt.Errorf("failed to update part: %w", err)
	}

	if uploadedNewImage && oldPart.ImagePath.Valid {
		images.DeleteByWebPath(oldPart.ImagePath.String)
	}

	return nil
}

func (s *service) DeletePart(ctx context.Context, id int64) error {
	p, err := s.store.GetPart(ctx, id)
	if err != nil {
		return err
	}

	if p.ImagePath.Valid {
		images.DeleteByWebPath(p.ImagePath.String)
	}

	docs, err := s.store.GetPartDocs(ctx, id)
	if err == nil {
		for _, doc := range docs {
			if strings.HasPrefix(doc.FilePath, config.UrlPrefixUploads) {
				relPath := strings.TrimPrefix(doc.FilePath, config.UrlPrefixUploads)
				diskPath := filepath.Join(config.DirUploads, relPath)
				os.Remove(diskPath)
			}
		}
	}

	summary := map[string]any{
		"id":          p.ID,
		"name":        p.Name,
		"part_number": p.PartNumber.String,
	}
	audit.Log(ctx, s.store, "DELETE", "PART", id, "Deleted part", summary, nil)
	return s.store.DeletePart(ctx, id)
}

func (s *service) ClonePart(ctx context.Context, id int64) (int64, error) {
	p, err := s.store.GetPart(ctx, id)
	if err != nil {
		return 0, err
	}

	links, err := s.store.GetPartLinks(ctx, id)
	if err != nil {
		return 0, err
	}

	tags, err := s.store.GetTagsForPart(ctx, id)
	if err != nil {
		return 0, err
	}
	tagNames := make([]string, len(tags))
	for i, t := range tags {
		tagNames[i] = t.Name
	}

	var newID int64
	err = s.store.ExecTx(ctx, func(q db.Querier) error {
		var err error
		newID, err = q.CreatePart(ctx, db.CreatePartParams{
			Name:              p.Name + " (Copy)",
			Description:       p.Description,
			PartNumber:        p.PartNumber,
			Manufacturer:      p.Manufacturer,
			Supplier:          p.Supplier,
			BarcodeData:       p.BarcodeData,
			UnitCost:          p.UnitCost,
			ReorderLevel:      p.ReorderLevel,
			MinStockThreshold: p.MinStockThreshold,
			ImagePath:         p.ImagePath,
		})
		if err != nil {
			return err
		}

		for _, l := range links {
			if err := s.docs.AddLink(ctx, q, newID, l.Url, l.Label.String); err != nil {
				return err
			}
		}

		if err := s.tags.SyncTags(ctx, q, newID, tagNames); err != nil {
			return err
		}

		summary := map[string]any{
			"id":          newID,
			"name":        p.Name,
			"part_number": p.PartNumber.String,
		}
		audit.Log(ctx, q, "CREATE", "PART", newID, "Cloned part from "+p.Name, nil, summary)
		return nil
	})
	return newID, err
}

func (s *service) DeleteParts(ctx context.Context, ids []int64) error {
	return s.store.ExecTx(ctx, func(q db.Querier) error {
		for _, id := range ids {
			// Get part for audit and cleanup
			p, err := q.GetPart(ctx, id)
			if err != nil {
				return err
			}

			// Delete files (non-transactional, but better than leaving them)
			if p.ImagePath.Valid {
				images.DeleteByWebPath(p.ImagePath.String)
			}
			docs, _ := q.GetPartDocs(ctx, id)
			for _, doc := range docs {
				if strings.HasPrefix(doc.FilePath, config.UrlPrefixUploads) {
					relPath := strings.TrimPrefix(doc.FilePath, config.UrlPrefixUploads)
					diskPath := filepath.Join(config.DirUploads, relPath)
					os.Remove(diskPath)
				}
			}

			summary := map[string]any{
				"id":          p.ID,
				"name":        p.Name,
				"part_number": p.PartNumber.String,
			}
			audit.Log(ctx, q, "DELETE", "PART", id, "Deleted part (Bulk)", summary, nil)

			if err := q.DeletePart(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *service) cleanupFiles(args ...interface{}) {
	var imagePath string
	var docs []string
	var uploadedNewImage bool

	for _, arg := range args {
		switch v := arg.(type) {
		case bool:
			uploadedNewImage = v
		case string:
			imagePath = v
		case []string:
			docs = v
		}
	}

	if (uploadedNewImage || imagePath != "") && imagePath != "" {
		images.DeleteByWebPath(imagePath)
	}
	for _, p := range docs {
		rel := strings.TrimPrefix(p, config.UrlPrefixUploads)
		relPath := filepath.Join(config.DirUploads, rel)
		os.Remove(relPath)
	}
}
