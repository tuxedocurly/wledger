package dashboard

import (
	"context"
	"database/sql"
	"sort"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/components"
)

type Service interface {
	GetStats(ctx context.Context) (db.GetDashboardStatsRow, error)
	GetGrid(ctx context.Context) ([]components.DashboardController, error)
	GetGridByController(ctx context.Context, controllerID int64) (*components.DashboardController, error)
	GetWalls(ctx context.Context) ([]db.Wall, error)
	GetWallWithContainers(ctx context.Context, wallID int64) ([]components.DashboardContainer, error)
	GetAllWallsWithContainers(ctx context.Context) ([]components.DashboardWall, error)
	CreateWall(ctx context.Context, name, description string) (int64, error)
	UpdateWall(ctx context.Context, id int64, name, description string, containerIDs []int64) error
	DeleteWall(ctx context.Context, id int64) error
}

type service struct {
	store db.Store
}

func NewService(store db.Store) Service {
	return &service{
		store: store,
	}
}

func (s *service) GetStats(ctx context.Context) (db.GetDashboardStatsRow, error) {
	return s.store.GetDashboardStats(ctx)
}

func (s *service) GetWalls(ctx context.Context) ([]db.Wall, error) {
	return s.store.GetWalls(ctx)
}

func calcStockStatus(qty, minThresh, reorder int64) string {
	if qty <= minThresh {
		return "critical"
	}
	if qty <= reorder {
		return "low"
	}
	return "ok"
}

func sortDashboardBins(bins []components.DashboardBin) {
	sort.Slice(bins, func(i, j int) bool {
		if bins[i].GridY == bins[j].GridY {
			return bins[i].GridX < bins[j].GridX
		}
		return bins[i].GridY < bins[j].GridY
	})
}

type standaloneLookup struct {
	partsByID        map[int64]db.Part
	assignmentsByBin map[int64][]db.PartAssignment
}

func (s *service) loadStandaloneLookup(ctx context.Context) standaloneLookup {
	lookup := standaloneLookup{
		partsByID:        make(map[int64]db.Part),
		assignmentsByBin: make(map[int64][]db.PartAssignment),
	}

	parts, err := s.store.GetAllParts(ctx)
	if err == nil {
		for _, p := range parts {
			lookup.partsByID[p.ID] = p
		}
	}

	assignments, err := s.store.GetAllPartAssignments(ctx)
	if err == nil {
		for _, a := range assignments {
			if a.BinID.Valid {
				lookup.assignmentsByBin[a.BinID.Int64] = append(lookup.assignmentsByBin[a.BinID.Int64], a)
			}
		}
	}
	return lookup
}

func (s *service) buildStandaloneDashboardContainer(ctx context.Context, c db.Container, lookup standaloneLookup) components.DashboardContainer {
	binsRows, err := s.store.GetBinsByContainer(ctx, c.ID)
	var bins []components.DashboardBin
	if err == nil {
		bins = make([]components.DashboardBin, 0, len(binsRows))
		for _, b := range binsRows {
			dbin := components.DashboardBin{
				ID:       b.ID,
				Name:     b.Name,
				GridX:    int(b.GridX.Int64),
				GridY:    int(b.GridY.Int64),
				Statuses: []string{},
			}
			for _, a := range lookup.assignmentsByBin[b.ID] {
				if p, ok := lookup.partsByID[a.PartID]; ok {
					dbin.Statuses = append(dbin.Statuses, calcStockStatus(a.Quantity, p.MinStockThreshold.Int64, p.ReorderLevel.Int64))
				}
			}
			bins = append(bins, dbin)
		}
		sortDashboardBins(bins)
	}

	return components.DashboardContainer{
		ID:               c.ID,
		Name:             c.Name,
		SegmentID:        c.SegmentID,
		ControllerName:   "Standalone",
		ControllerOnline: false,
		IsStandalone:     true,
		Bins:             bins,
	}
}

func (s *service) GetWallWithContainers(ctx context.Context, wallID int64) ([]components.DashboardContainer, error) {
	rows, err := s.store.GetWallContainerBins(ctx, wallID)
	if err != nil {
		return nil, err
	}
	wallCards, err := s.store.GetWallCards(ctx, wallID)
	if err != nil {
		return nil, err
	}

	containerMap := make(map[int64]*components.DashboardContainer)
	binMap := make(map[int64]map[int64]*components.DashboardBin)
	posMap := make(map[int64]int64, len(wallCards))

	for _, card := range wallCards {
		posMap[card.ContainerID] = card.PositionIndex
	}

	for _, row := range rows {
		if _, exists := containerMap[row.ContainerID]; !exists {
			containerMap[row.ContainerID] = &components.DashboardContainer{
				ID:               row.ContainerID,
				Name:             row.ContainerName,
				SegmentID:        row.SegmentID,
				ControllerName:   row.ControllerName,
				ControllerOnline: row.IsOnline.Bool,
			}
			binMap[row.ContainerID] = make(map[int64]*components.DashboardBin)
		}
		if !row.BinID.Valid {
			continue
		}

		cBins := binMap[row.ContainerID]
		if _, exists := cBins[row.BinID.Int64]; !exists {
			cBins[row.BinID.Int64] = &components.DashboardBin{
				ID:       row.BinID.Int64,
				Name:     row.BinName.String,
				GridX:    int(row.GridX.Int64),
				GridY:    int(row.GridY.Int64),
				Statuses: []string{},
			}
		}
		if row.PartID.Valid {
			bin := cBins[row.BinID.Int64]
			bin.Statuses = append(bin.Statuses, calcStockStatus(row.Quantity.Int64, row.MinStockThreshold.Int64, row.ReorderLevel.Int64))
		}
	}

	// Include mounted standalone containers (dropped by inner JOIN on controllers in SQL query).
	hasMissing := false
	for _, card := range wallCards {
		if _, exists := containerMap[card.ContainerID]; !exists {
			hasMissing = true
			break
		}
	}
	if hasMissing {
		lookup := s.loadStandaloneLookup(ctx)
		for _, card := range wallCards {
			if _, exists := containerMap[card.ContainerID]; exists {
				continue
			}
			c, err := s.store.GetContainer(ctx, card.ContainerID)
			if err != nil {
				continue
			}
			dc := s.buildStandaloneDashboardContainer(ctx, c, lookup)
			containerMap[c.ID] = &dc
		}
	}

	results := make([]components.DashboardContainer, 0, len(containerMap))
	for cID, c := range containerMap {
		if bMap, ok := binMap[cID]; ok {
			bins := make([]components.DashboardBin, 0, len(bMap))
			for _, b := range bMap {
				bins = append(bins, *b)
			}
			sortDashboardBins(bins)
			c.Bins = bins
		}
		results = append(results, *c)
	}

	sort.Slice(results, func(i, j int) bool {
		return posMap[results[i].ID] < posMap[results[j].ID]
	})

	return results, nil
}

func (s *service) GetAllWallsWithContainers(ctx context.Context) ([]components.DashboardWall, error) {
	walls, err := s.store.GetWalls(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := s.store.GetAllWallContainerBins(ctx)
	if err != nil {
		return nil, err
	}

	allCards, err := s.store.GetAllWallCards(ctx)
	if err != nil {
		return nil, err
	}

	wallMap := make(map[int64]*components.DashboardWall, len(walls))
	wallContainerMap := make(map[int64]map[int64]*components.DashboardContainer, len(walls))
	binMap := make(map[int64]map[int64]*components.DashboardBin)
	posMap := make(map[int64]map[int64]int64, len(walls))

	for _, w := range walls {
		wallMap[w.ID] = &components.DashboardWall{
			ID:          w.ID,
			Name:        w.Name,
			Description: w.Description.String,
			Containers:  []components.DashboardContainer{},
		}
		wallContainerMap[w.ID] = make(map[int64]*components.DashboardContainer)
		posMap[w.ID] = make(map[int64]int64)
	}

	for _, card := range allCards {
		if _, ok := posMap[card.WallID]; ok {
			posMap[card.WallID][card.ContainerID] = card.PositionIndex
		}
	}

	for _, row := range rows {
		if _, ok := wallMap[row.WallID]; !ok {
			continue
		}

		cMap := wallContainerMap[row.WallID]
		if _, exists := cMap[row.ContainerID]; !exists {
			cMap[row.ContainerID] = &components.DashboardContainer{
				ID:               row.ContainerID,
				Name:             row.ContainerName,
				SegmentID:        row.SegmentID,
				ControllerName:   row.ControllerName,
				ControllerOnline: row.IsOnline.Bool,
			}
			if _, ok := binMap[row.ContainerID]; !ok {
				binMap[row.ContainerID] = make(map[int64]*components.DashboardBin)
			}
		}

		if !row.BinID.Valid {
			continue
		}

		cBins := binMap[row.ContainerID]
		if _, exists := cBins[row.BinID.Int64]; !exists {
			cBins[row.BinID.Int64] = &components.DashboardBin{
				ID:       row.BinID.Int64,
				Name:     row.BinName.String,
				GridX:    int(row.GridX.Int64),
				GridY:    int(row.GridY.Int64),
				Statuses: []string{},
			}
		}
		if row.PartID.Valid {
			bin := cBins[row.BinID.Int64]
			bin.Statuses = append(bin.Statuses, calcStockStatus(row.Quantity.Int64, row.MinStockThreshold.Int64, row.ReorderLevel.Int64))
		}
	}

	// Hydrate standalone containers mounted on any wall.
	var missingCards []db.WallCard
	for _, card := range allCards {
		cMap, ok := wallContainerMap[card.WallID]
		if !ok {
			continue
		}
		if _, exists := cMap[card.ContainerID]; !exists {
			missingCards = append(missingCards, card)
		}
	}

	standaloneBuilt := make(map[int64]components.DashboardContainer)
	if len(missingCards) > 0 {
		lookup := s.loadStandaloneLookup(ctx)
		for _, card := range missingCards {
			dc, cached := standaloneBuilt[card.ContainerID]
			if !cached {
				c, err := s.store.GetContainer(ctx, card.ContainerID)
				if err != nil {
					continue
				}
				dc = s.buildStandaloneDashboardContainer(ctx, c, lookup)
				standaloneBuilt[card.ContainerID] = dc
			}
			dcCopy := dc
			wallContainerMap[card.WallID][card.ContainerID] = &dcCopy
		}
	}

	result := make([]components.DashboardWall, 0, len(walls))
	for _, w := range walls {
		dashboardWall := wallMap[w.ID]
		containers := make([]components.DashboardContainer, 0, len(wallContainerMap[w.ID]))
		for cID, c := range wallContainerMap[w.ID] {
			if bMap, ok := binMap[cID]; ok {
				bins := make([]components.DashboardBin, 0, len(bMap))
				for _, b := range bMap {
					bins = append(bins, *b)
				}
				sortDashboardBins(bins)
				c.Bins = bins
			}
			containers = append(containers, *c)
		}

		wallPosMap := posMap[w.ID]
		sort.Slice(containers, func(i, j int) bool {
			return wallPosMap[containers[i].ID] < wallPosMap[containers[j].ID]
		})

		dashboardWall.Containers = containers
		result = append(result, *dashboardWall)
	}

	return result, nil
}

func (s *service) DeleteWall(ctx context.Context, id int64) error {
	return s.store.DeleteWall(ctx, id)
}

func (s *service) CreateWall(ctx context.Context, name, description string) (int64, error) {
	return s.store.CreateWall(ctx, db.CreateWallParams{
		Name:        name,
		Description: sql.NullString{String: description, Valid: description != ""},
	})
}

func (s *service) UpdateWall(ctx context.Context, id int64, name, description string, containerIDs []int64) error {
	return s.store.ExecTx(ctx, func(q db.Querier) error {
		if err := q.UpdateWall(ctx, db.UpdateWallParams{
			ID:          id,
			Name:        name,
			Description: sql.NullString{String: description, Valid: description != ""},
		}); err != nil {
			return err
		}

		if err := q.DeleteWallCardsByWallID(ctx, id); err != nil {
			return err
		}

		for i, cID := range containerIDs {
			if err := q.AddContainerToWall(ctx, db.AddContainerToWallParams{
				WallID:        id,
				ContainerID:   cID,
				PositionIndex: int64(i),
				ConfigJson:    sql.NullString{Valid: false},
			}); err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *service) GetGrid(ctx context.Context) ([]components.DashboardController, error) {
	gridRows, err := s.store.GetDashboardGrid(ctx)
	if err != nil {
		return nil, err
	}

	flat := make([]gridRowData, 0, len(gridRows))
	for _, r := range gridRows {
		flat = append(flat, gridRowData{
			ControllerID:      r.ControllerID,
			ControllerName:    r.ControllerName,
			IsOnline:          r.IsOnline,
			ContainerID:       r.ContainerID,
			ContainerName:     r.ContainerName,
			SegmentID:         r.SegmentID,
			BinID:             r.BinID,
			BinName:           r.BinName,
			GridX:             r.GridX,
			GridY:             r.GridY,
			PartID:            r.PartID,
			Quantity:          r.Quantity,
			ReorderLevel:      r.ReorderLevel,
			MinStockThreshold: r.MinStockThreshold,
		})
	}
	controllers := buildDashboardGridView(flat)

	// Append standalone containers (that have bins) as a "Standalone Storage" section in legacy fallback view.
	standaloneList, err := s.store.GetStandaloneContainers(ctx)
	if err == nil && len(standaloneList) > 0 {
		lookup := s.loadStandaloneLookup(ctx)
		var standaloneCards []components.DashboardContainer
		for _, c := range standaloneList {
			dc := s.buildStandaloneDashboardContainer(ctx, c, lookup)
			if len(dc.Bins) > 0 {
				standaloneCards = append(standaloneCards, dc)
			}
		}
		if len(standaloneCards) > 0 {
			controllers = append(controllers, components.DashboardController{
				ID:         0,
				Name:       "Standalone Storage",
				Containers: standaloneCards,
			})
		}
	}

	return controllers, nil
}

func (s *service) GetGridByController(ctx context.Context, controllerID int64) (*components.DashboardController, error) {
	rows, err := s.store.GetDashboardGridByController(ctx, controllerID)
	if err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}

	flat := make([]gridRowData, 0, len(rows))
	for _, r := range rows {
		flat = append(flat, gridRowData{
			ControllerID:      r.ControllerID,
			ControllerName:    r.ControllerName,
			IsOnline:          r.IsOnline,
			ContainerID:       r.ContainerID,
			ContainerName:     r.ContainerName,
			SegmentID:         r.SegmentID,
			BinID:             r.BinID,
			BinName:           r.BinName,
			GridX:             r.GridX,
			GridY:             r.GridY,
			PartID:            r.PartID,
			Quantity:          r.Quantity,
			ReorderLevel:      r.ReorderLevel,
			MinStockThreshold: r.MinStockThreshold,
		})
	}
	controllers := buildDashboardGridView(flat)
	if len(controllers) == 0 {
		return nil, sql.ErrNoRows
	}

	return &controllers[0], nil
}

type gridRowData struct {
	ControllerID      int64
	ControllerName    string
	IsOnline          sql.NullBool
	ContainerID       int64
	ContainerName     string
	SegmentID         int64
	BinID             int64
	BinName           string
	GridX             sql.NullInt64
	GridY             sql.NullInt64
	PartID            sql.NullInt64
	Quantity          sql.NullInt64
	ReorderLevel      sql.NullInt64
	MinStockThreshold sql.NullInt64
}

func buildDashboardGridView(rows []gridRowData) []components.DashboardController {
	ctrlMap := make(map[int64]*components.DashboardController)
	ctrlContainerMap := make(map[int64]map[int64]*components.DashboardContainer)
	containerBinMap := make(map[int64]map[int64]*components.DashboardBin)

	for _, row := range rows {
		if _, exists := ctrlMap[row.ControllerID]; !exists {
			ctrlMap[row.ControllerID] = &components.DashboardController{
				ID:   row.ControllerID,
				Name: row.ControllerName,
			}
			ctrlContainerMap[row.ControllerID] = make(map[int64]*components.DashboardContainer)
		}

		cContainers := ctrlContainerMap[row.ControllerID]
		if _, exists := cContainers[row.ContainerID]; !exists {
			cContainers[row.ContainerID] = &components.DashboardContainer{
				ID:               row.ContainerID,
				Name:             row.ContainerName,
				SegmentID:        row.SegmentID,
				ControllerName:   row.ControllerName,
				ControllerOnline: row.IsOnline.Bool,
			}
			containerBinMap[row.ContainerID] = make(map[int64]*components.DashboardBin)
		}

		cBins := containerBinMap[row.ContainerID]
		if _, exists := cBins[row.BinID]; !exists {
			cBins[row.BinID] = &components.DashboardBin{
				ID:       row.BinID,
				Name:     row.BinName,
				GridX:    int(row.GridX.Int64),
				GridY:    int(row.GridY.Int64),
				Statuses: []string{},
			}
		}

		if row.PartID.Valid {
			bin := cBins[row.BinID]
			bin.Statuses = append(bin.Statuses, calcStockStatus(row.Quantity.Int64, row.MinStockThreshold.Int64, row.ReorderLevel.Int64))
		}
	}

	controllers := make([]components.DashboardController, 0, len(ctrlMap))
	for cID, ctrl := range ctrlMap {
		containers := make([]components.DashboardContainer, 0, len(ctrlContainerMap[cID]))
		for contID, cont := range ctrlContainerMap[cID] {
			bins := make([]components.DashboardBin, 0, len(containerBinMap[contID]))
			for _, b := range containerBinMap[contID] {
				bins = append(bins, *b)
			}
			sortDashboardBins(bins)
			cont.Bins = bins
			containers = append(containers, *cont)
		}

		sort.Slice(containers, func(i, j int) bool {
			if containers[i].SegmentID == containers[j].SegmentID {
				return containers[i].Name < containers[j].Name
			}
			return containers[i].SegmentID < containers[j].SegmentID
		})

		ctrl.Containers = containers
		controllers = append(controllers, *ctrl)
	}

	sort.Slice(controllers, func(i, j int) bool {
		return controllers[i].Name < controllers[j].Name
	})

	return controllers
}
