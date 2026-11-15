package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sort"
	"strings"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
	"github.com/tuxedocurly/wledger/internal/wled"
)

var (
	ErrNotFound          = errors.New("project or BOM item not found")
	ErrInvalidInput      = errors.New("invalid project or BOM input")
	ErrInvalidBoardCount = errors.New("board count must be at least 1")
	ErrEmptyBOM          = errors.New("project has no BOM items")
	ErrBOMItemUnlinked   = errors.New("BOM item is not linked to an inventory part")
)

var validStatuses = map[string]bool{
	"draft":     true,
	"active":    true,
	"completed": true,
	"archived":  true,
}

// ShortageDetail describes an insufficient or unlinked BOM requirement for a build.
type ShortageDetail struct {
	PartID           *int64
	PrimaryBOMItemID int64
	Designators      string
	DisplayName      string
	PartNumber       string
	QuantityPerBoard int64
	RequiredQuantity int64
	AvailableStock   int64
	ShortageQuantity int64
	Unlinked         bool
}

// ShortageError is returned by BuildProject when one or more BOM items lack stock or a linked part.
type ShortageError struct {
	BoardCount int64
	Shortages  []ShortageDetail
}

func (e *ShortageError) Error() string {
	if len(e.Shortages) == 0 {
		return fmt.Sprintf("insufficient stock to build %d board(s)", e.BoardCount)
	}
	parts := make([]string, 0, len(e.Shortages))
	for _, s := range e.Shortages {
		label := s.DisplayName
		if label == "" {
			label = s.PartNumber
		}
		if label == "" {
			label = s.Designators
		}
		if label == "" {
			label = fmt.Sprintf("BOM item #%d", s.PrimaryBOMItemID)
		}
		if s.Unlinked {
			parts = append(parts, fmt.Sprintf("%s [unlinked] (need %d, have 0, short %d)", label, s.RequiredQuantity, s.ShortageQuantity))
		} else {
			parts = append(parts, fmt.Sprintf("%s (need %d, have %d, short %d)", label, s.RequiredQuantity, s.AvailableStock, s.ShortageQuantity))
		}
	}
	return fmt.Sprintf("insufficient stock for %d board(s): %s", e.BoardCount, strings.Join(parts, "; "))
}

// CreateProjectRequest defines inputs for creating a project.
type CreateProjectRequest struct {
	Name        string
	Description string
	Status      string
}

// UpdateProjectRequest defines inputs for updating a project.
type UpdateProjectRequest struct {
	ID          int64
	Name        string
	Description string
	Status      string
}

// AddBOMItemRequest defines inputs for adding a BOM line item to a project.
type AddBOMItemRequest struct {
	ProjectID        int64
	PartID           *int64
	Designator       string
	MPN              string
	Description      string
	QuantityPerBoard int64
	Notes            string
}

// UpdateBOMItemRequest defines inputs for updating an existing BOM line item.
type UpdateBOMItemRequest struct {
	ProjectID        int64
	ID               int64
	PartID           *int64
	Designator       string
	MPN              string
	Description      string
	QuantityPerBoard int64
	Notes            string
}

// BOMLocation summarizes a physical bin or orphaned assignment holding stock for a BOM part.
type BOMLocation struct {
	AssignmentID   int64
	BinID          int64
	BinName        string
	ContainerID    int64
	ContainerName  string
	ControllerID   int64
	ControllerName string
	Quantity       int64
	IsOrphaned     bool
	IsStandalone   bool
	HasLED         bool
}

// BOMItemDetail enriches a BOM line item with stock levels, shortages, and physical bin locations.
type BOMItemDetail struct {
	ID                       int64
	ProjectID                int64
	PartID                   *int64
	Designator               string
	MPN                      string
	Description              string
	Notes                    string
	PartName                 string
	ResolvedPartNumber       string
	UnitCost                 float64
	QuantityPerBoard         int64
	RequiredQuantity         int64
	AvailableStock           int64
	ShortageQuantity         int64
	CombinedRequiredQuantity int64
	CombinedShortageQuantity int64
	HasShortage              bool
	IsLinked                 bool
	Locations                []BOMLocation
	HasLocatableLED          bool
}

// ProjectDetail holds the full project view model for N boards.
type ProjectDetail struct {
	Project           db.Project
	BoardCount        int64
	Items             []BOMItemDetail
	Shortages         []ShortageDetail
	Buildable         bool
	TotalCost         float64
	LocatableBinCount int
}

// ImportBOMResult summarizes a CSV BOM import operation.
type ImportBOMResult struct {
	ImportedCount  int
	MatchedCount   int
	UnmatchedCount int
	Items          []db.ProjectBomItem
}

// Service manages PCB projects, BOMs, CSV imports, Pick Mode locating, and atomic builds.
type Service interface {
	ListProjects(ctx context.Context) ([]db.ListProjectsRow, error)
	GetProject(ctx context.Context, id int64) (db.Project, error)
	CreateProject(ctx context.Context, req CreateProjectRequest) (db.Project, error)
	UpdateProject(ctx context.Context, req UpdateProjectRequest) error
	DeleteProject(ctx context.Context, id int64) error

	GetProjectDetail(ctx context.Context, projectID int64, boardCount int64) (ProjectDetail, error)

	AddBOMItem(ctx context.Context, req AddBOMItemRequest) (db.ProjectBomItem, error)
	UpdateBOMItem(ctx context.Context, req UpdateBOMItemRequest) error
	LinkBOMItemPart(ctx context.Context, projectID, bomItemID int64, partID *int64) error
	DeleteBOMItem(ctx context.Context, projectID, bomItemID int64) error
	GetBOMItem(ctx context.Context, bomItemID int64) (db.ProjectBomItem, error)

	ImportCSV(ctx context.Context, projectID int64, r io.Reader) (ImportBOMResult, error)

	LocateBOM(ctx context.Context, projectID int64) error
	LocateBOMItem(ctx context.Context, bomItemID int64) error
	LocateBOMItemForProject(ctx context.Context, projectID, bomItemID int64) error

	BuildProject(ctx context.Context, projectID int64, boardCount int64) error
}

// Option configures optional dependencies on the projects Service.
type Option func(*service)

// WithWLEDClient supplies a WLED HTTP client so Pick Mode can batch all BOM bins per controller.
func WithWLEDClient(client *wled.Client) Option {
	return func(s *service) {
		s.wledClient = client
	}
}

type binBatchLocator interface {
	LocateBins(ctx context.Context, controllerID int64, binIDs []int64) error
}

type service struct {
	store      db.Store
	wled       wled.Service
	wledClient *wled.Client
	logger     *slog.Logger
}

// NewService creates a new projects Service.
func NewService(store db.Store, wledSvc wled.Service, logger *slog.Logger, opts ...Option) Service {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	svc := &service{
		store:  store,
		wled:   wledSvc,
		logger: logger,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	return svc
}

func normalizeStatus(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "draft", nil
	}
	if !validStatuses[s] {
		return "", fmt.Errorf("%w: invalid project status %q", ErrInvalidInput, raw)
	}
	return s, nil
}

func toNullString(s string) sql.NullString {
	trimmed := strings.TrimSpace(s)
	return sql.NullString{String: trimmed, Valid: trimmed != ""}
}

func toNullInt64Ptr(id *int64) sql.NullInt64 {
	if id == nil || *id <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *id, Valid: true}
}

func (s *service) ListProjects(ctx context.Context) ([]db.ListProjectsRow, error) {
	return s.store.ListProjects(ctx)
}

func (s *service) GetProject(ctx context.Context, id int64) (db.Project, error) {
	proj, err := s.store.GetProject(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, ErrNotFound
	}
	return proj, err
}

func (s *service) CreateProject(ctx context.Context, req CreateProjectRequest) (db.Project, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return db.Project{}, fmt.Errorf("%w: project name is required", ErrInvalidInput)
	}
	status, err := normalizeStatus(req.Status)
	if err != nil {
		return db.Project{}, err
	}

	var created db.Project
	err = s.store.ExecTx(ctx, func(q db.Querier) error {
		proj, err := q.CreateProject(ctx, db.CreateProjectParams{
			Name:        name,
			Description: toNullString(req.Description),
			Status:      status,
		})
		if err != nil {
			return err
		}
		created = proj

		audit.Log(ctx, q, "CREATE", "PROJECT", proj.ID, "Created project "+proj.Name, nil, map[string]any{
			"id":     proj.ID,
			"name":   proj.Name,
			"status": proj.Status,
		})
		return nil
	})
	if err != nil {
		return db.Project{}, err
	}
	return created, nil
}

func (s *service) UpdateProject(ctx context.Context, req UpdateProjectRequest) error {
	name := strings.TrimSpace(req.Name)
	if req.ID <= 0 || name == "" {
		return fmt.Errorf("%w: project ID and name are required", ErrInvalidInput)
	}
	status, err := normalizeStatus(req.Status)
	if err != nil {
		return err
	}

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		oldProj, err := q.GetProject(ctx, req.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		desc := toNullString(req.Description)
		if err := q.UpdateProject(ctx, db.UpdateProjectParams{
			Name:        name,
			Description: desc,
			Status:      status,
			ID:          req.ID,
		}); err != nil {
			return err
		}

		audit.Log(ctx, q, "UPDATE", "PROJECT", req.ID, "Updated project "+name,
			map[string]any{"name": oldProj.Name, "description": oldProj.Description.String, "status": oldProj.Status},
			map[string]any{"name": name, "description": desc.String, "status": status},
		)
		return nil
	})
}

func (s *service) DeleteProject(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: invalid project ID", ErrInvalidInput)
	}

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		proj, err := q.GetProject(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		if err := q.DeleteProject(ctx, id); err != nil {
			return err
		}

		audit.Log(ctx, q, "DELETE", "PROJECT", id, "Deleted project "+proj.Name,
			map[string]any{"id": proj.ID, "name": proj.Name, "status": proj.Status}, nil)
		return nil
	})
}

func (s *service) GetBOMItem(ctx context.Context, bomItemID int64) (db.ProjectBomItem, error) {
	item, err := s.store.GetProjectBOMItem(ctx, bomItemID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ProjectBomItem{}, ErrNotFound
	}
	return item, err
}

func (s *service) AddBOMItem(ctx context.Context, req AddBOMItemRequest) (db.ProjectBomItem, error) {
	if req.ProjectID <= 0 {
		return db.ProjectBomItem{}, fmt.Errorf("%w: invalid project ID", ErrInvalidInput)
	}
	if req.QuantityPerBoard < 1 {
		return db.ProjectBomItem{}, fmt.Errorf("%w: quantity per board must be at least 1", ErrInvalidInput)
	}
	if (req.PartID == nil || *req.PartID <= 0) &&
		strings.TrimSpace(req.Designator) == "" &&
		strings.TrimSpace(req.MPN) == "" &&
		strings.TrimSpace(req.Description) == "" {
		return db.ProjectBomItem{}, fmt.Errorf("%w: specify a part, designator, MPN, or description", ErrInvalidInput)
	}

	var created db.ProjectBomItem
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		if _, err := q.GetProject(ctx, req.ProjectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}

		partID := toNullInt64Ptr(req.PartID)
		if partID.Valid {
			if _, err := q.GetPart(ctx, partID.Int64); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: referenced part %d does not exist", ErrInvalidInput, partID.Int64)
				}
				return err
			}
		}

		item, err := q.AddProjectBOMItem(ctx, db.AddProjectBOMItemParams{
			ProjectID:        req.ProjectID,
			PartID:           partID,
			Designator:       toNullString(req.Designator),
			Mpn:              toNullString(req.MPN),
			Description:      toNullString(req.Description),
			QuantityPerBoard: req.QuantityPerBoard,
			Notes:            toNullString(req.Notes),
		})
		if err != nil {
			return err
		}
		created = item

		audit.Log(ctx, q, "UPDATE", "PROJECT", req.ProjectID,
			fmt.Sprintf("Added BOM item #%d", item.ID),
			nil,
			map[string]any{"bom_item_id": item.ID, "part_id": req.PartID, "designator": item.Designator.String, "mpn": item.Mpn.String, "qty": item.QuantityPerBoard},
		)
		return nil
	})
	if err != nil {
		return db.ProjectBomItem{}, err
	}
	return created, nil
}

func (s *service) UpdateBOMItem(ctx context.Context, req UpdateBOMItemRequest) error {
	if req.ID <= 0 || req.QuantityPerBoard < 1 {
		return fmt.Errorf("%w: valid BOM item ID and quantity >= 1 required", ErrInvalidInput)
	}

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		existing, err := q.GetProjectBOMItem(ctx, req.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if req.ProjectID > 0 && existing.ProjectID != req.ProjectID {
			return ErrNotFound
		}

		partID := toNullInt64Ptr(req.PartID)
		if partID.Valid {
			if _, err := q.GetPart(ctx, partID.Int64); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: referenced part %d does not exist", ErrInvalidInput, partID.Int64)
				}
				return err
			}
		}

		if err := q.UpdateProjectBOMItem(ctx, db.UpdateProjectBOMItemParams{
			PartID:           partID,
			Designator:       toNullString(req.Designator),
			Mpn:              toNullString(req.MPN),
			Description:      toNullString(req.Description),
			QuantityPerBoard: req.QuantityPerBoard,
			Notes:            toNullString(req.Notes),
			ID:               req.ID,
		}); err != nil {
			return err
		}

		audit.Log(ctx, q, "UPDATE", "PROJECT", existing.ProjectID,
			fmt.Sprintf("Updated BOM item #%d", req.ID),
			map[string]any{"part_id": existing.PartID.Int64, "qty": existing.QuantityPerBoard},
			map[string]any{"part_id": partID.Int64, "qty": req.QuantityPerBoard},
		)
		return nil
	})
}

func (s *service) LinkBOMItemPart(ctx context.Context, projectID, bomItemID int64, partID *int64) error {
	if bomItemID <= 0 {
		return fmt.Errorf("%w: invalid BOM item ID", ErrInvalidInput)
	}

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		existing, err := q.GetProjectBOMItem(ctx, bomItemID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if projectID > 0 && existing.ProjectID != projectID {
			return ErrNotFound
		}

		nullPartID := toNullInt64Ptr(partID)
		if nullPartID.Valid {
			if _, err := q.GetPart(ctx, nullPartID.Int64); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: referenced part %d does not exist", ErrInvalidInput, nullPartID.Int64)
				}
				return err
			}
		}

		if err := q.LinkProjectBOMItemPart(ctx, db.LinkProjectBOMItemPartParams{
			PartID: nullPartID,
			ID:     bomItemID,
		}); err != nil {
			return err
		}

		audit.Log(ctx, q, "UPDATE", "PROJECT", existing.ProjectID,
			fmt.Sprintf("Linked BOM item #%d to part %d", bomItemID, nullPartID.Int64),
			map[string]any{"bom_item_id": bomItemID, "part_id": existing.PartID.Int64},
			map[string]any{"bom_item_id": bomItemID, "part_id": nullPartID.Int64},
		)
		return nil
	})
}

func (s *service) DeleteBOMItem(ctx context.Context, projectID, bomItemID int64) error {
	if bomItemID <= 0 {
		return fmt.Errorf("%w: invalid BOM item ID", ErrInvalidInput)
	}

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		existing, err := q.GetProjectBOMItem(ctx, bomItemID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if projectID > 0 && existing.ProjectID != projectID {
			return ErrNotFound
		}

		if err := q.DeleteProjectBOMItem(ctx, bomItemID); err != nil {
			return err
		}

		audit.Log(ctx, q, "UPDATE", "PROJECT", existing.ProjectID,
			fmt.Sprintf("Removed BOM item #%d", bomItemID),
			map[string]any{"bom_item_id": bomItemID, "designator": existing.Designator.String, "mpn": existing.Mpn.String},
			nil,
		)
		return nil
	})
}

func (s *service) GetProjectDetail(ctx context.Context, projectID int64, boardCount int64) (ProjectDetail, error) {
	if boardCount < 1 {
		boardCount = 1
	}

	proj, err := s.store.GetProject(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectDetail{}, ErrNotFound
	}
	if err != nil {
		return ProjectDetail{}, err
	}

	bomRows, err := s.store.GetProjectBOMWithStock(ctx, db.GetProjectBOMWithStockParams{
		BoardCount: boardCount,
		ProjectID:  projectID,
	})
	if err != nil {
		return ProjectDetail{}, err
	}

	shortageRows, err := s.store.GetProjectShortages(ctx, db.GetProjectShortagesParams{
		BoardCount: boardCount,
		ProjectID:  projectID,
	})
	if err != nil {
		return ProjectDetail{}, err
	}

	// Preload locations per distinct linked part ID and compute combined per-part demand.
	locationsByPart := make(map[int64][]BOMLocation)
	combinedReqByPart := make(map[int64]int64)
	availByPart := make(map[int64]int64)
	locatableBins := make(map[int64]bool)

	for _, r := range bomRows {
		if !r.PartID.Valid {
			continue
		}
		pid := r.PartID.Int64
		combinedReqByPart[pid] += r.RequiredQuantity
		availByPart[pid] = r.AvailableStock

		if _, loaded := locationsByPart[pid]; !loaded {
			assignments, err := s.store.GetPartAssignments(ctx, pid)
			if err != nil {
				return ProjectDetail{}, fmt.Errorf("failed to load locations for part %d: %w", pid, err)
			}
			locs := make([]BOMLocation, 0, len(assignments))
			for _, a := range assignments {
				loc := BOMLocation{
					AssignmentID:   a.ID,
					BinID:          a.BinID.Int64,
					BinName:        a.BinName.String,
					ContainerID:    a.ContainerID.Int64,
					ContainerName:  a.ContainerName.String,
					ControllerID:   a.ControllerID.Int64,
					ControllerName: a.ControllerName.String,
					Quantity:       a.Quantity,
					IsOrphaned:     !a.BinID.Valid || !a.BinName.Valid,
					IsStandalone:   a.BinID.Valid && !a.ControllerID.Valid,
					HasLED:         mapper.IsAssignmentLocatable(a.BinID.Valid, a.LedIndex.Valid, a.GridX.Valid, a.GridY.Valid, a.ControllerID.Valid, a.ControllerIp, a.ContainerConfig),
				}
				if loc.HasLED && loc.Quantity > 0 {
					locatableBins[loc.BinID] = true
				}
				locs = append(locs, loc)
			}
			locationsByPart[pid] = locs
		}
	}

	items := make([]BOMItemDetail, 0, len(bomRows))
	var totalCost float64
	for _, r := range bomRows {
		var pidPtr *int64
		var locs []BOMLocation
		combinedReq := r.RequiredQuantity
		combinedShort := r.ShortageQuantity
		hasLED := false

		if r.PartID.Valid {
			pid := r.PartID.Int64
			pidPtr = &pid
			locs = locationsByPart[pid]
			combinedReq = combinedReqByPart[pid]
			if combinedReq > availByPart[pid] {
				combinedShort = combinedReq - availByPart[pid]
			} else {
				combinedShort = 0
			}
			for _, l := range locs {
				if l.HasLED && l.Quantity > 0 {
					hasLED = true
					break
				}
			}
		}

		if r.UnitCost.Valid {
			totalCost += r.UnitCost.Float64 * float64(r.RequiredQuantity)
		}

		hasShortage := !r.PartID.Valid || r.ShortageQuantity > 0 || combinedShort > 0

		items = append(items, BOMItemDetail{
			ID:                       r.ID,
			ProjectID:                r.ProjectID,
			PartID:                   pidPtr,
			Designator:               r.Designator.String,
			MPN:                      r.Mpn.String,
			Description:              r.Description.String,
			Notes:                    r.Notes.String,
			PartName:                 r.PartName.String,
			ResolvedPartNumber:       r.ResolvedPartNumber.String,
			UnitCost:                 r.UnitCost.Float64,
			QuantityPerBoard:         r.QuantityPerBoard,
			RequiredQuantity:         r.RequiredQuantity,
			AvailableStock:           r.AvailableStock,
			ShortageQuantity:         r.ShortageQuantity,
			CombinedRequiredQuantity: combinedReq,
			CombinedShortageQuantity: combinedShort,
			HasShortage:              hasShortage,
			IsLinked:                 r.PartID.Valid,
			Locations:                locs,
			HasLocatableLED:          hasLED,
		})
	}

	shortages := make([]ShortageDetail, 0, len(shortageRows))
	for _, sr := range shortageRows {
		var pidPtr *int64
		if sr.PartID.Valid {
			pid := sr.PartID.Int64
			pidPtr = &pid
		}
		shortages = append(shortages, ShortageDetail{
			PartID:           pidPtr,
			PrimaryBOMItemID: sr.PrimaryBomItemID,
			Designators:      sr.Designators,
			DisplayName:      sr.DisplayName,
			PartNumber:       sr.PartNumber,
			QuantityPerBoard: sr.QuantityPerBoard,
			RequiredQuantity: sr.RequiredQuantity,
			AvailableStock:   sr.AvailableStock,
			ShortageQuantity: sr.ShortageQuantity,
			Unlinked:         !sr.PartID.Valid,
		})
	}

	return ProjectDetail{
		Project:           proj,
		BoardCount:        boardCount,
		Items:             items,
		Shortages:         shortages,
		Buildable:         len(items) > 0 && len(shortages) == 0,
		TotalCost:         totalCost,
		LocatableBinCount: len(locatableBins),
	}, nil
}

func (s *service) ImportCSV(ctx context.Context, projectID int64, r io.Reader) (ImportBOMResult, error) {
	if projectID <= 0 {
		return ImportBOMResult{}, fmt.Errorf("%w: invalid project ID", ErrInvalidInput)
	}

	parsedRows, err := parseBOMCSV(r)
	if err != nil {
		return ImportBOMResult{}, err
	}

	var result ImportBOMResult
	err = s.store.ExecTx(ctx, func(q db.Querier) error {
		proj, err := q.GetProject(ctx, projectID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		allParts, err := q.GetAllParts(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch inventory parts for BOM matching: %w", err)
		}
		sort.Slice(allParts, func(i, j int) bool { return allParts[i].ID < allParts[j].ID })

		result.Items = make([]db.ProjectBomItem, 0, len(parsedRows))
		for _, row := range parsedRows {
			matchedPartID := matchInventoryPart(allParts, row)
			if matchedPartID.Valid {
				result.MatchedCount++
			} else {
				result.UnmatchedCount++
			}

			item, err := q.AddProjectBOMItem(ctx, db.AddProjectBOMItemParams{
				ProjectID:        projectID,
				PartID:           matchedPartID,
				Designator:       toNullString(row.Designator),
				Mpn:              toNullString(row.MPN),
				Description:      toNullString(row.EffectiveDescription()),
				QuantityPerBoard: row.QuantityPerBoard,
				Notes:            toNullString(row.Notes),
			})
			if err != nil {
				return fmt.Errorf("row %d: failed to insert BOM item: %w", row.RowNumber, err)
			}
			result.Items = append(result.Items, item)
			result.ImportedCount++
		}

		audit.Log(ctx, q, "IMPORT", "PROJECT", projectID,
			fmt.Sprintf("Imported %d BOM items into %s (%d matched, %d unmatched)",
				result.ImportedCount, proj.Name, result.MatchedCount, result.UnmatchedCount),
			nil,
			map[string]any{"imported": result.ImportedCount, "matched": result.MatchedCount, "unmatched": result.UnmatchedCount},
		)
		return nil
	})
	if err != nil {
		return ImportBOMResult{}, err
	}
	return result, nil
}

// matchInventoryPart attempts to auto-link a BOM row to an inventory part in strict priority order:
// 1. part_number exact case-insensitive match
// 2. barcode_data match
// 3. exact name match
func matchInventoryPart(allParts []db.Part, row ParsedBOMRow) sql.NullInt64 {
	// 1. part_number exact case-insensitive match
	if mpn := strings.TrimSpace(row.MPN); mpn != "" {
		for _, p := range allParts {
			if p.PartNumber.Valid && strings.EqualFold(strings.TrimSpace(p.PartNumber.String), mpn) {
				return sql.NullInt64{Int64: p.ID, Valid: true}
			}
		}
	}

	// 2. barcode_data match (check Barcode column, MPN, Value/Name)
	barcodeCandidates := nonEmptyUnique([]string{row.Barcode, row.MPN, row.ValueOrName})
	for _, cand := range barcodeCandidates {
		for _, p := range allParts {
			if p.BarcodeData.Valid && strings.TrimSpace(p.BarcodeData.String) == cand {
				return sql.NullInt64{Int64: p.ID, Valid: true}
			}
		}
		for _, p := range allParts {
			if p.BarcodeData.Valid && strings.EqualFold(strings.TrimSpace(p.BarcodeData.String), cand) {
				return sql.NullInt64{Int64: p.ID, Valid: true}
			}
		}
	}

	// 3. exact name match (check Value/Name first, then Description)
	nameCandidates := nonEmptyUnique([]string{row.ValueOrName, row.Description})
	for _, cand := range nameCandidates {
		for _, p := range allParts {
			if strings.TrimSpace(p.Name) == cand {
				return sql.NullInt64{Int64: p.ID, Valid: true}
			}
		}
		for _, p := range allParts {
			if strings.EqualFold(strings.TrimSpace(p.Name), cand) {
				return sql.NullInt64{Int64: p.ID, Valid: true}
			}
		}
	}

	return sql.NullInt64{}
}

func nonEmptyUnique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func (s *service) LocateBOM(ctx context.Context, projectID int64) error {
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return err
	}

	bomItems, err := s.store.ListProjectBOMItems(ctx, projectID)
	if err != nil {
		return fmt.Errorf("failed to list project BOM items: %w", err)
	}

	partIDs := make([]int64, 0)
	seenParts := make(map[int64]bool)
	for _, item := range bomItems {
		if !item.PartID.Valid || item.PartID.Int64 <= 0 || seenParts[item.PartID.Int64] {
			continue
		}
		seenParts[item.PartID.Int64] = true
		partIDs = append(partIDs, item.PartID.Int64)
	}
	if len(partIDs) == 0 {
		return nil
	}

	byCtrl := make(map[int64][]ctrlLocateItem)
	ctrlBinSeen := make(map[int64]map[int64]bool)
	locatableParts := make([]int64, 0, len(partIDs))

	for _, pid := range partIDs {
		part, err := s.store.GetPart(ctx, pid)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return fmt.Errorf("failed to load part %d: %w", pid, err)
		}
		assignments, err := s.store.GetPartAssignments(ctx, pid)
		if err != nil {
			return fmt.Errorf("failed to load assignments for part %d: %w", pid, err)
		}

		partHasLocatable := false
		for _, a := range assignments {
			if a.Quantity <= 0 {
				continue
			}
			if !mapper.IsAssignmentLocatable(a.BinID.Valid, a.LedIndex.Valid, a.GridX.Valid, a.GridY.Valid, a.ControllerID.Valid, a.ControllerIp, a.ContainerConfig) {
				continue
			}
			partHasLocatable = true
			ctrlID := a.ControllerID.Int64
			if ctrlBinSeen[ctrlID] == nil {
				ctrlBinSeen[ctrlID] = make(map[int64]bool)
			}
			byCtrl[ctrlID] = append(byCtrl[ctrlID], ctrlLocateItem{
				part:       part,
				assignment: a,
			})
			ctrlBinSeen[ctrlID][a.BinID.Int64] = true
		}
		if partHasLocatable {
			locatableParts = append(locatableParts, pid)
		}
	}

	if len(byCtrl) == 0 {
		s.logger.Info("no positive-stock WLED bins found for project BOM", "project_id", projectID)
		return nil
	}

	// Prefer batched multi-bin illumination via wled.Client so all BOM bins on a controller stay lit simultaneously.
	if s.wledClient != nil {
		return s.locateBatchedItems(ctx, byCtrl)
	}

	if s.wled == nil {
		return errors.New("WLED service not configured")
	}

	// If wled.Service provides a LocateBins extension, batch per controller.
	if batcher, ok := s.wled.(binBatchLocator); ok {
		ctrlIDs := sortedMapKeys(byCtrl)
		var errs []error
		for _, ctrlID := range ctrlIDs {
			binIDs := sortedBoolKeys(ctrlBinSeen[ctrlID])
			if err := batcher.LocateBins(ctx, ctrlID, binIDs); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	// Fallback: invoke wled.Service.LocatePart per unique locatable part.
	sort.Slice(locatableParts, func(i, j int) bool { return locatableParts[i] < locatableParts[j] })
	var errs []error
	for _, pid := range locatableParts {
		if err := s.wled.LocatePart(ctx, pid); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *service) LocateBOMItem(ctx context.Context, bomItemID int64) error {
	return s.LocateBOMItemForProject(ctx, 0, bomItemID)
}

func (s *service) LocateBOMItemForProject(ctx context.Context, projectID, bomItemID int64) error {
	item, err := s.store.GetProjectBOMItem(ctx, bomItemID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if projectID > 0 && item.ProjectID != projectID {
		return ErrNotFound
	}
	if !item.PartID.Valid || item.PartID.Int64 <= 0 {
		return ErrBOMItemUnlinked
	}

	if s.wled != nil {
		return s.wled.LocatePart(ctx, item.PartID.Int64)
	}
	if s.wledClient != nil {
		part, err := s.store.GetPart(ctx, item.PartID.Int64)
		if err != nil {
			return err
		}
		assignments, err := s.store.GetPartAssignments(ctx, item.PartID.Int64)
		if err != nil {
			return err
		}
		byCtrl := make(map[int64][]ctrlLocateItem)
		for _, a := range assignments {
			if a.Quantity <= 0 || !mapper.IsAssignmentLocatable(a.BinID.Valid, a.LedIndex.Valid, a.GridX.Valid, a.GridY.Valid, a.ControllerID.Valid, a.ControllerIp, a.ContainerConfig) {
				continue
			}
			byCtrl[a.ControllerID.Int64] = append(byCtrl[a.ControllerID.Int64], ctrlLocateItem{
				part:       part,
				assignment: a,
			})
		}
		if len(byCtrl) == 0 {
			return nil
		}
		return s.locateBatchedItems(ctx, byCtrl)
	}
	return errors.New("WLED service not configured")
}

type ctrlLocateItem struct {
	part       db.Part
	assignment db.GetPartAssignmentsRow
}

func (s *service) locateBatchedItems(ctx context.Context, byCtrl map[int64][]ctrlLocateItem) error {
	settings, _ := s.store.GetSettings(ctx)
	ctrlIDs := make([]int64, 0, len(byCtrl))
	for id := range byCtrl {
		ctrlIDs = append(ctrlIDs, id)
	}
	sort.Slice(ctrlIDs, func(i, j int) bool { return ctrlIDs[i] < ctrlIDs[j] })

	var errs []error
	for _, ctrlID := range ctrlIDs {
		ctrl, err := s.store.GetController(ctx, ctrlID)
		if err != nil {
			errs = append(errs, fmt.Errorf("controller %d not found: %w", ctrlID, err))
			continue
		}
		containers, err := s.store.GetContainersByController(ctx, sql.NullInt64{Int64: ctrlID, Valid: true})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to fetch containers for controller %d: %w", ctrlID, err))
			continue
		}

		type binColor struct {
			ranges []mapper.MappedRange
			rank   int
			hex    string
		}
		binEntries := make(map[int64]binColor)
		var binOrder []int64

		for _, entry := range byCtrl[ctrlID] {
			a := entry.assignment
			bin := db.Bin{
				ID:          a.BinID.Int64,
				ContainerID: a.ContainerID.Int64,
				LedIndex:    a.LedIndex,
				Width:       a.Width,
				GridX:       a.GridX,
				GridY:       a.GridY,
			}
			ranges, mapErr := mapper.ResolveBinRanges(containers, bin)
			if mapErr != nil {
				s.logger.Error("mapping failed for BOM locate", "bin_id", bin.ID, "err", mapErr)
				continue
			}

			hexColor := fallbackHex(settings.ColorLocate, "#0000FF")
			rank := 0
			if settings.LocateColorByStock.Bool {
				hexColor, rank = resolvePartStockHealthColor(settings, entry.part, a.Quantity)
			}

			if prev, exists := binEntries[bin.ID]; exists {
				if rank > prev.rank {
					prev.rank = rank
					prev.hex = hexColor
					binEntries[bin.ID] = prev
				}
				continue
			}

			binOrder = append(binOrder, bin.ID)
			binEntries[bin.ID] = binColor{
				ranges: ranges,
				rank:   rank,
				hex:    hexColor,
			}
		}

		if len(binOrder) == 0 {
			continue
		}
		sort.Slice(binOrder, func(i, j int) bool { return binOrder[i] < binOrder[j] })

		bySeg := make(map[int][]wled.LEDRange)
		segSet := map[int]bool{0: true}
		ledCount := int(ctrl.LedCount)
		for _, c := range containers {
			segSet[int(c.SegmentID)] = true
			if c.ConfigJson.Valid && c.ConfigJson.String != "" {
				var cfg mapper.ContainerConfig
				if jsonErr := json.Unmarshal([]byte(c.ConfigJson.String), &cfg); jsonErr == nil {
					if ext := int(mapper.ContainerMaxLEDExtent(cfg)); ext > ledCount {
						ledCount = ext
					}
				}
			}
		}

		for _, bid := range binOrder {
			bc := binEntries[bid]
			rgb, rgbErr := wled.HexToRGBArray(bc.hex)
			if rgbErr != nil {
				rgb = [3]byte{0, 0, 255}
			}
			for _, r := range bc.ranges {
				segID := int(r.SegmentID)
				start := int(r.StartIndex)
				width := int(r.Width)
				bySeg[segID] = append(bySeg[segID], wled.LEDRange{
					Start: start,
					Count: width,
					RGB:   rgb,
				})
				segSet[segID] = true
				if end := start + width; end > ledCount {
					ledCount = end
				}
			}
		}

		knownSegs := make([]int, 0, len(segSet))
		for id := range segSet {
			knownSegs = append(knownSegs, id)
		}
		sort.Ints(knownSegs)

		var sendErr error
		if ctrl.UseUdp.Bool {
			udpTimeout := byte(wled.DefaultUDPHoldTimeout)
			if settings.EnableLocateTimeout.Bool && settings.LocateTimeoutSeconds.Int64 > 0 {
				secs := settings.LocateTimeoutSeconds.Int64
				if secs > 254 {
					secs = 254
				}
				udpTimeout = byte(secs)
			}
			var udpRanges []wled.LEDRange
			for _, segID := range knownSegs {
				udpRanges = append(udpRanges, bySeg[segID]...)
			}
			udpPort := ctrl.UdpPort.Int64
			if !ctrl.UdpPort.Valid || udpPort <= 0 {
				udpPort = wled.DefaultUDPPort
			}
			sendErr = wled.SendDNRGBRanges(ctx, ctrl.IpAddress, udpPort, udpTimeout, ledCount, udpRanges)
		} else {
			sendErr = s.wledClient.LightRangesWithPort(ctx, ctrl.IpAddress, ctrl.Port, ledCount, knownSegs, bySeg)
		}
		if sendErr != nil {
			errs = append(errs, fmt.Errorf("controller %s (%s): %w", ctrl.Name, ctrl.IpAddress, sendErr))
		}
	}

	return errors.Join(errs...)
}

func (s *service) BuildProject(ctx context.Context, projectID int64, boardCount int64) error {
	if projectID <= 0 {
		return fmt.Errorf("%w: invalid project ID", ErrInvalidInput)
	}
	if boardCount < 1 {
		return ErrInvalidBoardCount
	}

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		proj, err := q.GetProject(ctx, projectID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		bomRows, err := q.GetProjectBOMWithStock(ctx, db.GetProjectBOMWithStockParams{
			BoardCount: boardCount,
			ProjectID:  projectID,
		})
		if err != nil {
			return err
		}
		if len(bomRows) == 0 {
			return ErrEmptyBOM
		}

		type partDemand struct {
			partID      int64
			perBoard    int64
			required    int64
			displayName string
			partNumber  string
			designators []string
			primaryID   int64
		}

		demands := make(map[int64]*partDemand)
		partOrder := make([]int64, 0, len(bomRows))
		var shortages []ShortageDetail

		for _, row := range bomRows {
			if row.QuantityPerBoard <= 0 {
				return fmt.Errorf("%w: BOM item #%d has non-positive quantity %d", ErrInvalidInput, row.ID, row.QuantityPerBoard)
			}
			if boardCount > math.MaxInt64/row.QuantityPerBoard {
				return fmt.Errorf("%w: required quantity overflow", ErrInvalidBoardCount)
			}
			reqQty := row.QuantityPerBoard * boardCount

			if !row.PartID.Valid {
				label := row.Description.String
				if label == "" {
					label = row.Mpn.String
				}
				shortages = append(shortages, ShortageDetail{
					PrimaryBOMItemID: row.ID,
					Designators:      row.Designator.String,
					DisplayName:      label,
					PartNumber:       row.Mpn.String,
					QuantityPerBoard: row.QuantityPerBoard,
					RequiredQuantity: reqQty,
					AvailableStock:   0,
					ShortageQuantity: reqQty,
					Unlinked:         true,
				})
				continue
			}

			pid := row.PartID.Int64
			d, exists := demands[pid]
			if !exists {
				displayName := row.PartName.String
				if displayName == "" {
					displayName = row.Description.String
				}
				partNo := row.ResolvedPartNumber.String
				if partNo == "" {
					partNo = row.Mpn.String
				}
				d = &partDemand{
					partID:      pid,
					displayName: displayName,
					partNumber:  partNo,
					primaryID:   row.ID,
				}
				demands[pid] = d
				partOrder = append(partOrder, pid)
			}
			if d.perBoard > math.MaxInt64-row.QuantityPerBoard || d.required > math.MaxInt64-reqQty {
				return fmt.Errorf("%w: required quantity overflow", ErrInvalidBoardCount)
			}
			d.perBoard += row.QuantityPerBoard
			d.required += reqQty
			if row.Designator.String != "" {
				d.designators = append(d.designators, row.Designator.String)
			}
		}

		assignmentsByPart := make(map[int64][]db.GetPartAssignmentsRow, len(partOrder))
		for _, pid := range partOrder {
			d := demands[pid]
			assignments, err := q.GetPartAssignments(ctx, pid)
			if err != nil {
				return fmt.Errorf("failed to fetch assignments for part %d: %w", pid, err)
			}

			var available int64
			for _, a := range assignments {
				if a.Quantity > 0 {
					available += a.Quantity
				}
			}

			if available < d.required {
				pidCopy := pid
				shortages = append(shortages, ShortageDetail{
					PartID:           &pidCopy,
					PrimaryBOMItemID: d.primaryID,
					Designators:      strings.Join(d.designators, ", "),
					DisplayName:      d.displayName,
					PartNumber:       d.partNumber,
					QuantityPerBoard: d.perBoard,
					RequiredQuantity: d.required,
					AvailableStock:   available,
					ShortageQuantity: d.required - available,
				})
			}
			assignmentsByPart[pid] = assignments
		}

		if len(shortages) > 0 {
			return &ShortageError{
				BoardCount: boardCount,
				Shortages:  shortages,
			}
		}

		// Deduct deterministically across assignments: non-orphaned ordered by (BinID, ID), then orphaned by ID.
		deductedSummary := make([]map[string]any, 0, len(partOrder))
		for _, pid := range partOrder {
			d := demands[pid]
			assignments := assignmentsByPart[pid]
			sort.Slice(assignments, func(i, j int) bool {
				iValid := assignments[i].BinID.Valid
				jValid := assignments[j].BinID.Valid
				if iValid != jValid {
					return iValid
				}
				if assignments[i].BinID.Int64 != assignments[j].BinID.Int64 {
					return assignments[i].BinID.Int64 < assignments[j].BinID.Int64
				}
				return assignments[i].ID < assignments[j].ID
			})

			remaining := d.required
			for _, a := range assignments {
				if remaining <= 0 {
					break
				}
				if a.Quantity <= 0 {
					continue
				}

				deduct := a.Quantity
				if remaining < deduct {
					deduct = remaining
				}
				newQty := a.Quantity - deduct

				if err := q.UpdatePartAssignmentQuantityByID(ctx, db.UpdatePartAssignmentQuantityByIDParams{
					Quantity: newQty,
					ID:       a.ID,
				}); err != nil {
					return fmt.Errorf("failed to deduct stock for part %d assignment %d: %w", pid, a.ID, err)
				}

				var binVal any
				if a.BinID.Valid {
					binVal = a.BinID.Int64
				}
				audit.Log(ctx, q, "STOCK_DEC", "PART", pid,
					fmt.Sprintf("Built %d board(s) of project %s (-%d)", boardCount, proj.Name, deduct),
					map[string]any{
						"assignment_id": a.ID,
						"bin_id":        binVal,
						"quantity":      a.Quantity,
						"project_id":    projectID,
					},
					map[string]any{
						"assignment_id": a.ID,
						"bin_id":        binVal,
						"quantity":      newQty,
						"project_id":    projectID,
						"board_count":   boardCount,
					},
				)

				remaining -= deduct
			}

			if remaining != 0 {
				return fmt.Errorf("internal stock deduction error: %d remaining for part %d", remaining, pid)
			}

			deductedSummary = append(deductedSummary, map[string]any{
				"part_id":  pid,
				"deducted": d.required,
			})
		}

		audit.Log(ctx, q, "BUILD", "PROJECT", projectID,
			fmt.Sprintf("Built %d board(s) of project %s", boardCount, proj.Name),
			map[string]any{"project_id": projectID, "board_count": boardCount},
			map[string]any{"project_id": projectID, "board_count": boardCount, "parts": deductedSummary},
		)
		return nil
	})
}

func resolvePartStockHealthColor(settings db.Setting, part db.Part, qty int64) (string, int) {
	minThresh := int64(0)
	if part.MinStockThreshold.Valid {
		minThresh = part.MinStockThreshold.Int64
	}
	reorder := int64(0)
	if part.ReorderLevel.Valid {
		reorder = part.ReorderLevel.Int64
	}
	if qty <= minThresh {
		return fallbackHex(settings.ColorStockCritical, "#EF4444"), 3
	}
	if qty <= reorder {
		return fallbackHex(settings.ColorStockLow, "#F59E0B"), 2
	}
	return fallbackHex(settings.ColorStockOk, "#10B981"), 1
}

func fallbackHex(ns sql.NullString, def string) string {
	if ns.Valid && ns.String != "" {
		return ns.String
	}
	return def
}

func sortedMapKeys[V any](m map[int64]V) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedBoolKeys(m map[int64]bool) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
