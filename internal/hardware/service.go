package hardware

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
	"github.com/tuxedocurly/wledger/internal/wled"
)

type Service interface {
	ListControllers(ctx context.Context) ([]db.Controller, error)
	GetController(ctx context.Context, id int64) (db.Controller, error)
	CreateController(ctx context.Context, params db.CreateControllerParams) (db.CreateControllerRow, error)
	DeleteController(ctx context.Context, id int64) error
	UpdateStatus(ctx context.Context, id int64) (bool, error)
	GetBinsByController(ctx context.Context, id int64) ([]db.Bin, error)
	GetContainers(ctx context.Context, controllerID int64) ([]db.Container, error)
	SaveGrid(ctx context.Context, controllerID int64, gridDataJSON string, configJSON string) (int64, error)
}

type service struct {
	store  db.Store
	wled   *wled.Client
	logger *slog.Logger
}

func NewService(store db.Store, wledClient *wled.Client, logger *slog.Logger) Service {
	return &service{
		store:  store,
		wled:   wledClient,
		logger: logger,
	}
}

func (s *service) GetContainers(ctx context.Context, controllerID int64) ([]db.Container, error) {
	containers, err := s.store.GetContainersByController(ctx, controllerID)
	if err != nil {
		return nil, err
	}

	// Validate config JSON for each container and log if corrupted
	for _, c := range containers {
		if c.ConfigJson.Valid && c.ConfigJson.String != "" {
			var js json.RawMessage
			if err := json.Unmarshal([]byte(c.ConfigJson.String), &js); err != nil {
				s.logger.Error("corrupted container configuration detected",
					"container_id", c.ID,
					"controller_id", controllerID,
					"err", err,
					"json", c.ConfigJson.String,
				)
			}
		}
	}

	return containers, nil
}

func (s *service) ListControllers(ctx context.Context) ([]db.Controller, error) {
	return s.store.GetControllers(ctx)
}

func (s *service) GetController(ctx context.Context, id int64) (db.Controller, error) {
	return s.store.GetController(ctx, id)
}

func (s *service) CreateController(ctx context.Context, params db.CreateControllerParams) (db.CreateControllerRow, error) {
	row, err := s.store.CreateController(ctx, params)
	if err != nil {
		return row, err
	}

	// Create Default Container for the new controller
	_, err = s.store.CreateContainer(ctx, db.CreateContainerParams{
		Name:          row.Name + " (Main)",
		ControllerID:  row.ID,
		SegmentID:     0,
		PositionIndex: 0,
		ConfigJson:    sql.NullString{String: `{"type":"grid","rows":8,"cols":8}`, Valid: true},
	})
	if err != nil {
		s.logger.Error("failed to create default container for new controller", "err", err)
	}

	summary := map[string]any{
		"id":         row.ID,
		"name":       row.Name,
		"ip_address": row.IpAddress,
	}
	audit.Log(ctx, s.store, "CREATE", "HARDWARE", row.ID, "Added controller "+row.Name, nil, summary)

	return row, nil
}

func (s *service) DeleteController(ctx context.Context, id int64) error {
	// Fetch before delete for logging
	c, err := s.store.GetController(ctx, id)
	if err != nil {
		return err
	}

	summary := map[string]any{
		"id":         c.ID,
		"name":       c.Name,
		"ip_address": c.IpAddress,
	}
	audit.Log(ctx, s.store, "DELETE", "HARDWARE", id, "Deleted controller", summary, nil)

	return s.store.DeleteController(ctx, id)
}

func (s *service) UpdateStatus(ctx context.Context, id int64) (bool, error) {
	c, err := s.store.GetController(ctx, id)
	if err != nil {
		return false, err
	}

	online, _ := s.wled.Ping(ctx, c.IpAddress)

	if online != c.IsOnline.Bool {
		err := s.store.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{
			IsOnline: sql.NullBool{Bool: online, Valid: true},
			ID:       c.ID,
		})
		if err != nil {
			s.logger.Error("failed to update controller online status", "err", err, "id", c.ID, "online", online)
		}
	}

	return online, nil
}

func (s *service) GetBinsByController(ctx context.Context, id int64) ([]db.Bin, error) {
	containers, err := s.store.GetContainersByController(ctx, id)
	if err != nil {
		return nil, err
	}

	var allBins []db.Bin
	for _, c := range containers {
		bins, err := s.store.GetBinsByContainer(ctx, c.ID)
		if err == nil {
			allBins = append(allBins, bins...)
		}
	}

	return allBins, nil
}

type containerInJSON struct {
	ID            *int64                 `json:"id"`
	Name          string                 `json:"name"`
	SegmentID     int64                  `json:"segment_id"`
	PositionIndex int64                  `json:"position_index"`
	Config        mapper.ContainerConfig `json:"config"`
}

type binInJSON struct {
	ContainerIndex int    `json:"container_index"`
	X              int    `json:"x"`
	Y              int    `json:"y"`
	LedIndex       int    `json:"led_index"`
	Name           string `json:"name"`
}

func (s *service) SaveGrid(ctx context.Context, controllerID int64, gridDataJSON string, configJSON string) (int64, error) {
	var inputBins []binInJSON
	if err := json.Unmarshal([]byte(gridDataJSON), &inputBins); err != nil {
		return 0, fmt.Errorf("invalid grid json: %w", err)
	}

	var inputContainers []containerInJSON
	if err := json.Unmarshal([]byte(configJSON), &inputContainers); err != nil {
		return 0, fmt.Errorf("invalid config json: %w", err)
	}

	var totalLedCount int64 = 0

	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		// Sync Containers
		existingContainers, err := q.GetContainersByController(ctx, controllerID)
		if err != nil {
			return err
		}

		containerIDMap := make(map[int]int64) // Index in input -> DB ID
		updatedIDs := make(map[int64]bool)

		for i, c := range inputContainers {
			configBytes, _ := json.Marshal(c.Config)
			configStr := string(configBytes)

			if c.ID != nil {
				// Update existing
				err := q.UpdateContainerConfig(ctx, db.UpdateContainerConfigParams{
					ID:            *c.ID,
					Name:          c.Name,
					SegmentID:     c.SegmentID,
					PositionIndex: int64(i), // Use loop index as position
					ConfigJson:    sql.NullString{String: configStr, Valid: true},
				})
				if err != nil {
					return fmt.Errorf("failed to update container %d: %w", *c.ID, err)
				}
				containerIDMap[i] = *c.ID
				updatedIDs[*c.ID] = true
			} else {
				// Create new
				newID, err := q.CreateContainer(ctx, db.CreateContainerParams{
					Name:          c.Name,
					ControllerID:  controllerID,
					SegmentID:     c.SegmentID,
					PositionIndex: int64(i), // Use loop index as position
					ConfigJson:    sql.NullString{String: configStr, Valid: true},
				})
				if err != nil {
					return fmt.Errorf("failed to create container: %w", err)
				}
				containerIDMap[i] = newID
			}
		}

		// Delete containers that are no longer present
		for _, ec := range existingContainers {
			if !updatedIDs[ec.ID] {
				err := q.DeleteContainer(ctx, ec.ID)
				if err != nil {
					return fmt.Errorf("failed to delete container %d: %w", ec.ID, err)
				}
			}
		}

		// Sync Bins
		for i := range inputContainers {
			dbID := containerIDMap[i]

			// Get all input bins for THIS container
			var containerInputBins []binInJSON
			for _, b := range inputBins {
				if b.ContainerIndex == i {
					containerInputBins = append(containerInputBins, b)
				}
			}

			// Get existing bins from DB to preserve IDs (and part assignments)
			existingBins, err := q.GetBinsByContainer(ctx, dbID)
			if err != nil {
				return err
			}

			// Clear all LED indices for this container to avoid unique constraint violations during swap/resize
			// SQLite UNIQUE(container_id, led_index) allows multiple NULLs
			err = q.ClearContainerBinLedIndices(ctx, dbID)
			if err != nil {
				return fmt.Errorf("failed to clear bin indices: %w", err)
			}

			// Map existing bins by Position (X,Y)
			existingByPos := make(map[string]db.Bin)
			for _, bin := range existingBins {
				if bin.GridX.Valid && bin.GridY.Valid {
					key := fmt.Sprintf("%d,%d", bin.GridX.Int64, bin.GridY.Int64)
					existingByPos[key] = bin
				}
			}

			processedBinIDs := make(map[int64]bool)

			for _, b := range containerInputBins {
				key := fmt.Sprintf("%d,%d", b.X, b.Y)

				if existing, found := existingByPos[key]; found {
					// Update existing bin
					err := q.UpdateBin(ctx, db.UpdateBinParams{
						ID:       existing.ID,
						Name:     b.Name,
						LedIndex: sql.NullInt64{Int64: int64(b.LedIndex), Valid: true},
						Width:    sql.NullInt64{Int64: 1, Valid: true},
						GridX:    sql.NullInt64{Int64: int64(b.X), Valid: true},
						GridY:    sql.NullInt64{Int64: int64(b.Y), Valid: true},
					})
					if err != nil {
						return err
					}
					processedBinIDs[existing.ID] = true
				} else {
					// Create new bin
					_, err := q.CreateBin(ctx, db.CreateBinParams{
						Name:        b.Name,
						ContainerID: dbID,
						LedIndex:    sql.NullInt64{Int64: int64(b.LedIndex), Valid: true},
						Width:       sql.NullInt64{Int64: 1, Valid: true},
						GridX:       sql.NullInt64{Int64: int64(b.X), Valid: true},
						GridY:       sql.NullInt64{Int64: int64(b.Y), Valid: true},
					})
					if err != nil {
						return err
					}
				}

				if int64(b.LedIndex) >= totalLedCount {
					totalLedCount = int64(b.LedIndex) + 1
				}
			}

			// Delete orphans (bins that were in DB but not in input)
			for _, bin := range existingBins {
				if !processedBinIDs[bin.ID] {
					err := q.DeleteBin(ctx, bin.ID)
					if err != nil {
						return err
					}
				}
			}
		}

		// Audit Log
		audit.Log(ctx, q, "UPDATE", "HARDWARE", controllerID, "Updated LED Grid Layout",
			nil,
			map[string]any{"led_count": totalLedCount})

		return nil
	})

	return totalLedCount, err
}
