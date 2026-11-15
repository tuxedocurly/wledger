package hardware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
	"github.com/tuxedocurly/wledger/internal/wled"
)

var (
	// ErrContainerOwnership is returned when a grid save references a container not owned by the target context.
	ErrContainerOwnership = errors.New("container ownership mismatch")
	// ErrInvalidEndpoint is returned when a controller IP/hostname or port fails validation.
	ErrInvalidEndpoint = errors.New("invalid controller endpoint")
)

type Service interface {
	ListControllers(ctx context.Context) ([]db.Controller, error)
	GetController(ctx context.Context, id int64) (db.Controller, error)
	CreateController(ctx context.Context, params db.CreateControllerParams) (db.CreateControllerRow, error)
	UpdateController(ctx context.Context, params db.UpdateControllerParams) error
	DeleteController(ctx context.Context, id int64) error
	UpdateStatus(ctx context.Context, id int64) (bool, error)
	DiscoverControllers(ctx context.Context) (DiscoveryResult, error)
	ReconcileDiscoveredDevices(ctx context.Context, devices []wled.DiscoveredDevice) (DiscoveryResult, error)

	GetBinsByController(ctx context.Context, id int64) ([]db.Bin, error)
	GetContainers(ctx context.Context, controllerID int64) ([]db.Container, error)
	SaveGrid(ctx context.Context, controllerID int64, gridDataJSON string, configJSON string) (int64, error)

	ListStandaloneContainers(ctx context.Context) ([]db.Container, error)
	ListAllContainersWithController(ctx context.Context) ([]db.GetAllContainersWithControllerRow, error)
	GetContainer(ctx context.Context, id int64) (db.Container, error)
	GetBinsByContainer(ctx context.Context, containerID int64) ([]db.Bin, error)
	CreateStandaloneContainer(ctx context.Context, name string, rows, cols int) (int64, error)
	DeleteContainer(ctx context.Context, containerID int64) error
	UpdateContainerController(ctx context.Context, containerID int64, controllerID sql.NullInt64) error
	SaveStandaloneGrid(ctx context.Context, containerID int64, gridDataJSON string, configJSON string) (int64, error)
}

// DiscoveredControllerMatch describes one mDNS-verified WLED node matched against WLedger's database.
type DiscoveredControllerMatch struct {
	Device         wled.DiscoveredDevice `json:"device"`
	Status         string                `json:"status"` // "new", "online", "ip_updated"
	ControllerID   int64                 `json:"controller_id,omitempty"`
	ControllerName string                `json:"controller_name,omitempty"`
	PreviousIP     string                `json:"previous_ip,omitempty"`
}

// DiscoveryResult contains all mDNS-discovered WLED devices after MAC/IP reconciliation.
type DiscoveryResult struct {
	Devices []DiscoveredControllerMatch `json:"devices"`
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

func validateControllerEndpoint(rawIP string, port sql.NullInt64) (string, int64, error) {
	host, resolvedPort, err := wled.ValidateEndpoint(rawIP, port)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %v", ErrInvalidEndpoint, err)
	}
	return host, resolvedPort, nil
}

func (s *service) GetContainers(ctx context.Context, controllerID int64) ([]db.Container, error) {
	containers, err := s.store.GetContainersByController(ctx, sql.NullInt64{Int64: controllerID, Valid: true})
	if err != nil {
		return nil, err
	}
	s.validateContainerConfigs(containers)
	return containers, nil
}

func (s *service) ListStandaloneContainers(ctx context.Context) ([]db.Container, error) {
	containers, err := s.store.GetStandaloneContainers(ctx)
	if err != nil {
		return nil, err
	}
	s.validateContainerConfigs(containers)
	return containers, nil
}

func (s *service) ListAllContainersWithController(ctx context.Context) ([]db.GetAllContainersWithControllerRow, error) {
	return s.store.GetAllContainersWithController(ctx)
}

func (s *service) GetContainer(ctx context.Context, id int64) (db.Container, error) {
	return s.store.GetContainer(ctx, id)
}

func (s *service) GetBinsByContainer(ctx context.Context, containerID int64) ([]db.Bin, error) {
	return s.store.GetBinsByContainer(ctx, containerID)
}

func (s *service) validateContainerConfigs(containers []db.Container) {
	for _, c := range containers {
		if c.ConfigJson.Valid && c.ConfigJson.String != "" {
			var js json.RawMessage
			if err := json.Unmarshal([]byte(c.ConfigJson.String), &js); err != nil {
				s.logger.Error("corrupted container configuration detected",
					"container_id", c.ID,
					"err", err,
					"json", c.ConfigJson.String,
				)
			}
		}
	}
}

func (s *service) ListControllers(ctx context.Context) ([]db.Controller, error) {
	return s.store.GetControllers(ctx)
}

func (s *service) GetController(ctx context.Context, id int64) (db.Controller, error) {
	return s.store.GetController(ctx, id)
}

func (s *service) CreateController(ctx context.Context, params db.CreateControllerParams) (db.CreateControllerRow, error) {
	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" {
		return db.CreateControllerRow{}, errors.New("controller name cannot be empty")
	}

	cleanHost, resolvedPort, err := validateControllerEndpoint(params.IpAddress, params.Port)
	if err != nil {
		return db.CreateControllerRow{}, err
	}
	params.IpAddress = cleanHost
	params.Port = sql.NullInt64{Int64: resolvedPort, Valid: true}

	row, err := s.store.CreateController(ctx, params)
	if err != nil {
		return row, err
	}

	// Create default container for the new controller.
	_, err = s.store.CreateContainer(ctx, db.CreateContainerParams{
		Name:          row.Name + " (Main)",
		ControllerID:  sql.NullInt64{Int64: row.ID, Valid: true},
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
		"port":       resolvedPort,
	}
	audit.Log(ctx, s.store, "CREATE", "HARDWARE", row.ID, "Added controller "+row.Name, nil, summary)

	return row, nil
}

func (s *service) UpdateController(ctx context.Context, params db.UpdateControllerParams) error {
	existing, err := s.store.GetController(ctx, params.ID)
	if err != nil {
		return err
	}

	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" {
		return errors.New("controller name cannot be empty")
	}

	cleanHost, resolvedPort, err := validateControllerEndpoint(params.IpAddress, params.Port)
	if err != nil {
		return err
	}
	params.IpAddress = cleanHost
	params.Port = sql.NullInt64{Int64: resolvedPort, Valid: true}

	if !params.UdpPort.Valid || params.UdpPort.Int64 == 0 {
		params.UdpPort = sql.NullInt64{Int64: wled.DefaultUDPPort, Valid: true}
	} else if params.UdpPort.Int64 < 1 || params.UdpPort.Int64 > 65535 {
		return fmt.Errorf("%w: invalid UDP port %d", ErrInvalidEndpoint, params.UdpPort.Int64)
	}

	if !params.IdlePresetID.Valid || params.IdlePresetID.Int64 < 0 {
		params.IdlePresetID = sql.NullInt64{Int64: 0, Valid: true}
	}

	if !params.UseUdp.Valid {
		params.UseUdp = sql.NullBool{Bool: false, Valid: true}
	}
	if !params.RestoreState.Valid {
		params.RestoreState = sql.NullBool{Bool: false, Valid: true}
	}

	if err := s.store.UpdateController(ctx, params); err != nil {
		return err
	}

	oldVal := map[string]any{
		"name":           existing.Name,
		"ip_address":     existing.IpAddress,
		"port":           existing.Port.Int64,
		"use_udp":        existing.UseUdp.Bool,
		"udp_port":       existing.UdpPort.Int64,
		"restore_state":  existing.RestoreState.Bool,
		"idle_preset_id": existing.IdlePresetID.Int64,
	}
	newVal := map[string]any{
		"name":           params.Name,
		"ip_address":     params.IpAddress,
		"port":           params.Port.Int64,
		"use_udp":        params.UseUdp.Bool,
		"udp_port":       params.UdpPort.Int64,
		"restore_state":  params.RestoreState.Bool,
		"idle_preset_id": params.IdlePresetID.Int64,
	}
	audit.Log(ctx, s.store, "UPDATE", "HARDWARE", params.ID, "Updated controller "+params.Name, oldVal, newVal)
	return nil
}

func (s *service) DeleteController(ctx context.Context, id int64) error {
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

	info, infoErr := s.wled.GetInfoWithPort(ctx, c.IpAddress, c.Port)
	if infoErr == nil {
		mac := c.MacAddress
		if info.MAC != "" {
			mac = sql.NullString{String: info.MAC, Valid: true}
		}
		err := s.store.UpdateControllerInfo(ctx, db.UpdateControllerInfoParams{
			IsOnline:   sql.NullBool{Bool: true, Valid: true},
			LedCount:   info.Leds.Count,
			MacAddress: mac,
			ID:         c.ID,
		})
		if err != nil {
			s.logger.Error("failed to update controller info", "err", err, "id", c.ID)
		}
		return true, nil
	}

	// Offline: ONLY update is_online so existing led_count and mac_address are preserved.
	if err := s.store.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{
		IsOnline: sql.NullBool{Bool: false, Valid: true},
		ID:       c.ID,
	}); err != nil {
		s.logger.Error("failed to update controller offline status", "err", err, "id", c.ID)
	}

	return false, nil
}

func (s *service) DiscoverControllers(ctx context.Context) (DiscoveryResult, error) {
	devices, err := wled.Discover(ctx, s.wled, 2*time.Second)
	if err != nil {
		return DiscoveryResult{}, err
	}
	return s.ReconcileDiscoveredDevices(ctx, devices)
}

func (s *service) ReconcileDiscoveredDevices(ctx context.Context, devices []wled.DiscoveredDevice) (DiscoveryResult, error) {
	controllers, err := s.store.GetControllers(ctx)
	if err != nil {
		return DiscoveryResult{}, err
	}

	byHostPort := make(map[string]db.Controller, len(controllers))
	for _, c := range controllers {
		p := c.Port.Int64
		if !c.Port.Valid || p <= 0 {
			p = wled.DefaultHTTPPort
		}
		byHostPort[fmt.Sprintf("%s:%d", c.IpAddress, p)] = c
	}

	result := DiscoveryResult{
		Devices: make([]DiscoveredControllerMatch, 0, len(devices)),
	}

	for _, dev := range devices {
		dev.MAC = wled.NormalizeMAC(dev.MAC)
		port := dev.Port
		if port <= 0 {
			port = wled.DefaultHTTPPort
		}
		dev.Port = port

		if dev.MAC != "" {
			existing, macErr := s.store.GetControllerByMac(ctx, sql.NullString{String: dev.MAC, Valid: true})
			if macErr == nil {
				oldPort := existing.Port.Int64
				if !existing.Port.Valid || oldPort <= 0 {
					oldPort = wled.DefaultHTTPPort
				}
				ipChanged := existing.IpAddress != dev.IP || oldPort != port

				if ipChanged {
					prevIP := existing.IpAddress
					updErr := s.store.UpdateController(ctx, db.UpdateControllerParams{
						Name:         existing.Name,
						IpAddress:    dev.IP,
						Port:         sql.NullInt64{Int64: port, Valid: true},
						UseUdp:       existing.UseUdp,
						UdpPort:      existing.UdpPort,
						RestoreState: existing.RestoreState,
						IdlePresetID: existing.IdlePresetID,
						ID:           existing.ID,
					})
					if updErr == nil {
						_ = s.store.UpdateControllerInfo(ctx, db.UpdateControllerInfoParams{
							IsOnline:   sql.NullBool{Bool: true, Valid: true},
							LedCount:   dev.LedCount,
							MacAddress: sql.NullString{String: dev.MAC, Valid: true},
							ID:         existing.ID,
						})
						audit.Log(ctx, s.store, "UPDATE", "HARDWARE", existing.ID,
							fmt.Sprintf("Auto-healed DHCP IP for %s (%s -> %s)", existing.Name, prevIP, dev.IP),
							map[string]any{"ip_address": prevIP, "port": oldPort},
							map[string]any{"ip_address": dev.IP, "port": port, "mac": dev.MAC},
						)
						result.Devices = append(result.Devices, DiscoveredControllerMatch{
							Device:         dev,
							Status:         "ip_updated",
							ControllerID:   existing.ID,
							ControllerName: existing.Name,
							PreviousIP:     prevIP,
						})
						continue
					}
				}

				_ = s.store.UpdateControllerInfo(ctx, db.UpdateControllerInfoParams{
					IsOnline:   sql.NullBool{Bool: true, Valid: true},
					LedCount:   dev.LedCount,
					MacAddress: sql.NullString{String: dev.MAC, Valid: true},
					ID:         existing.ID,
				})
				result.Devices = append(result.Devices, DiscoveredControllerMatch{
					Device:         dev,
					Status:         "online",
					ControllerID:   existing.ID,
					ControllerName: existing.Name,
				})
				continue
			}
		}

		// Fallback match by IP:port when MAC was not yet stored on an existing controller.
		if existing, ok := byHostPort[fmt.Sprintf("%s:%d", dev.IP, port)]; ok {
			macVal := existing.MacAddress
			if dev.MAC != "" {
				macVal = sql.NullString{String: dev.MAC, Valid: true}
			}
			_ = s.store.UpdateControllerInfo(ctx, db.UpdateControllerInfoParams{
				IsOnline:   sql.NullBool{Bool: true, Valid: true},
				LedCount:   dev.LedCount,
				MacAddress: macVal,
				ID:         existing.ID,
			})
			result.Devices = append(result.Devices, DiscoveredControllerMatch{
				Device:         dev,
				Status:         "online",
				ControllerID:   existing.ID,
				ControllerName: existing.Name,
			})
			continue
		}

		result.Devices = append(result.Devices, DiscoveredControllerMatch{
			Device: dev,
			Status: "new",
		})
	}

	return result, nil
}

func (s *service) CreateStandaloneContainer(ctx context.Context, name string, rows, cols int) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("container name cannot be empty")
	}
	if rows < 1 {
		rows = 4
	}
	if cols < 1 {
		cols = 4
	}

	cfgBytes, err := json.Marshal(mapper.ContainerConfig{
		Type: "grid",
		Rows: rows,
		Cols: cols,
	})
	if err != nil {
		return 0, err
	}

	id, err := s.store.CreateContainer(ctx, db.CreateContainerParams{
		Name:          name,
		ControllerID:  sql.NullInt64{Valid: false},
		SegmentID:     0,
		ConfigJson:    sql.NullString{String: string(cfgBytes), Valid: true},
		PositionIndex: 0,
	})
	if err != nil {
		return 0, err
	}

	audit.Log(ctx, s.store, "CREATE", "HARDWARE", id, "Created standalone container "+name, nil, map[string]any{
		"id":   id,
		"name": name,
		"rows": rows,
		"cols": cols,
	})
	return id, nil
}

func (s *service) DeleteContainer(ctx context.Context, containerID int64) error {
	c, err := s.store.GetContainer(ctx, containerID)
	if err != nil {
		return err
	}

	audit.Log(ctx, s.store, "DELETE", "HARDWARE", containerID, "Deleted container "+c.Name, map[string]any{
		"id":            c.ID,
		"name":          c.Name,
		"controller_id": c.ControllerID,
	}, nil)
	return s.store.DeleteContainer(ctx, containerID)
}

func (s *service) UpdateContainerController(ctx context.Context, containerID int64, controllerID sql.NullInt64) error {
	c, err := s.store.GetContainer(ctx, containerID)
	if err != nil {
		return err
	}

	if controllerID.Valid {
		if _, err := s.store.GetController(ctx, controllerID.Int64); err != nil {
			return fmt.Errorf("target controller not found: %w", err)
		}
	}

	if err := s.store.UpdateContainerController(ctx, db.UpdateContainerControllerParams{
		ControllerID: controllerID,
		ID:           containerID,
	}); err != nil {
		return err
	}

	audit.Log(ctx, s.store, "UPDATE", "HARDWARE", containerID, "Updated container controller attachment",
		map[string]any{"controller_id": c.ControllerID},
		map[string]any{"controller_id": controllerID})
	return nil
}

func (s *service) GetBinsByController(ctx context.Context, id int64) ([]db.Bin, error) {
	containers, err := s.store.GetContainersByController(ctx, sql.NullInt64{Int64: id, Valid: true})
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
	LedIndex       *int   `json:"led_index"`
	Width          int    `json:"width"`
	Name           string `json:"name"`
}

func (s *service) SaveGrid(ctx context.Context, controllerID int64, gridDataJSON string, configJSON string) (int64, error) {
	inputBins, inputContainers, err := decodeGridPayloads(gridDataJSON, configJSON)
	if err != nil {
		return 0, err
	}

	var totalLedCount int64

	err = s.store.ExecTx(ctx, func(q db.Querier) error {
		existingContainers, err := q.GetContainersByController(ctx, sql.NullInt64{Int64: controllerID, Valid: true})
		if err != nil {
			return err
		}

		ownedIDs := make(map[int64]bool, len(existingContainers))
		for _, ec := range existingContainers {
			ownedIDs[ec.ID] = true
		}

		containerIDMap := make(map[int]int64, len(inputContainers))
		updatedIDs := make(map[int64]bool, len(inputContainers))

		for i, c := range inputContainers {
			configBytes, _ := json.Marshal(c.Config)
			configStr := string(configBytes)

			if c.ID != nil {
				if !ownedIDs[*c.ID] {
					return fmt.Errorf("%w: container %d does not belong to controller %d", ErrContainerOwnership, *c.ID, controllerID)
				}
				if updatedIDs[*c.ID] {
					return fmt.Errorf("%w: duplicate container %d in save payload", ErrContainerOwnership, *c.ID)
				}

				err := q.UpdateContainerConfig(ctx, db.UpdateContainerConfigParams{
					ID:            *c.ID,
					Name:          c.Name,
					SegmentID:     c.SegmentID,
					PositionIndex: int64(i),
					ConfigJson:    sql.NullString{String: configStr, Valid: true},
				})
				if err != nil {
					return fmt.Errorf("failed to update container %d: %w", *c.ID, err)
				}
				containerIDMap[i] = *c.ID
				updatedIDs[*c.ID] = true
			} else {
				newID, err := q.CreateContainer(ctx, db.CreateContainerParams{
					Name:          c.Name,
					ControllerID:  sql.NullInt64{Int64: controllerID, Valid: true},
					SegmentID:     c.SegmentID,
					PositionIndex: int64(i),
					ConfigJson:    sql.NullString{String: configStr, Valid: true},
				})
				if err != nil {
					return fmt.Errorf("failed to create container: %w", err)
				}
				containerIDMap[i] = newID
			}
		}

		for _, ec := range existingContainers {
			if !updatedIDs[ec.ID] {
				if err := q.DeleteContainer(ctx, ec.ID); err != nil {
					return fmt.Errorf("failed to delete container %d: %w", ec.ID, err)
				}
			}
		}

		for i := range inputContainers {
			containerLEDMax, err := syncContainerBins(ctx, q, containerIDMap[i], binsForContainer(inputBins, i))
			if err != nil {
				return err
			}
			if containerLEDMax > totalLedCount {
				totalLedCount = containerLEDMax
			}
		}

		audit.Log(ctx, q, "UPDATE", "HARDWARE", controllerID, "Updated LED Grid Layout",
			nil,
			map[string]any{"led_count": totalLedCount})

		return nil
	})

	return totalLedCount, err
}

func (s *service) SaveStandaloneGrid(ctx context.Context, containerID int64, gridDataJSON string, configJSON string) (int64, error) {
	inputBins, inputContainers, err := decodeGridPayloads(gridDataJSON, configJSON)
	if err != nil {
		return 0, err
	}
	if len(inputContainers) != 1 {
		return 0, fmt.Errorf("%w: standalone configuration must describe exactly 1 container", ErrContainerOwnership)
	}

	c := inputContainers[0]
	if c.ID != nil && *c.ID != containerID {
		return 0, fmt.Errorf("%w: container %d does not match standalone container %d", ErrContainerOwnership, *c.ID, containerID)
	}

	var totalLedCount int64
	err = s.store.ExecTx(ctx, func(q db.Querier) error {
		existing, err := q.GetContainer(ctx, containerID)
		if err != nil {
			return err
		}
		if existing.ControllerID.Valid {
			return fmt.Errorf("%w: container %d belongs to controller %d", ErrContainerOwnership, containerID, existing.ControllerID.Int64)
		}

		configBytes, _ := json.Marshal(c.Config)
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = existing.Name
		}

		if err := q.UpdateContainerConfig(ctx, db.UpdateContainerConfigParams{
			ID:            containerID,
			Name:          name,
			SegmentID:     c.SegmentID,
			PositionIndex: existing.PositionIndex,
			ConfigJson:    sql.NullString{String: string(configBytes), Valid: true},
		}); err != nil {
			return fmt.Errorf("failed to update standalone container %d: %w", containerID, err)
		}

		maxLED, err := syncContainerBins(ctx, q, containerID, binsForContainer(inputBins, 0))
		if err != nil {
			return err
		}
		totalLedCount = maxLED

		audit.Log(ctx, q, "UPDATE", "HARDWARE", containerID, "Updated Standalone Container Grid",
			nil,
			map[string]any{"container_id": containerID, "led_count": totalLedCount})

		return nil
	})

	return totalLedCount, err
}

func decodeGridPayloads(gridDataJSON, configJSON string) ([]binInJSON, []containerInJSON, error) {
	var inputBins []binInJSON
	if err := json.Unmarshal([]byte(gridDataJSON), &inputBins); err != nil {
		return nil, nil, fmt.Errorf("invalid grid json: %w", err)
	}

	var inputContainers []containerInJSON
	if err := json.Unmarshal([]byte(configJSON), &inputContainers); err != nil {
		return nil, nil, fmt.Errorf("invalid config json: %w", err)
	}

	return inputBins, inputContainers, nil
}

func binsForContainer(inputBins []binInJSON, containerIdx int) []binInJSON {
	var out []binInJSON
	for _, b := range inputBins {
		if b.ContainerIndex == containerIdx {
			out = append(out, b)
		}
	}
	return out
}

func syncContainerBins(ctx context.Context, q db.Querier, containerDBID int64, containerInputBins []binInJSON) (int64, error) {
	existingBins, err := q.GetBinsByContainer(ctx, containerDBID)
	if err != nil {
		return 0, err
	}

	// Clear all LED indices first to avoid SQLite UNIQUE(container_id, led_index) conflicts during reorder/swap.
	if err := q.ClearContainerBinLedIndices(ctx, containerDBID); err != nil {
		return 0, fmt.Errorf("failed to clear bin indices: %w", err)
	}

	existingByPos := make(map[string]db.Bin, len(existingBins))
	for _, bin := range existingBins {
		if bin.GridX.Valid && bin.GridY.Valid {
			existingByPos[fmt.Sprintf("%d,%d", bin.GridX.Int64, bin.GridY.Int64)] = bin
		}
	}

	processedBinIDs := make(map[int64]bool, len(containerInputBins))
	var maxLEDCount int64

	for _, b := range containerInputBins {
		width := b.Width
		if width < 1 {
			width = 1
		}

		ledNull := sql.NullInt64{Valid: false}
		if b.LedIndex != nil {
			if *b.LedIndex < 0 {
				return 0, fmt.Errorf("invalid negative led_index %d for bin %q", *b.LedIndex, b.Name)
			}
			ledNull = sql.NullInt64{Int64: int64(*b.LedIndex), Valid: true}
			if end := int64(*b.LedIndex) + int64(width); end > maxLEDCount {
				maxLEDCount = end
			}
		}

		key := fmt.Sprintf("%d,%d", b.X, b.Y)
		if existing, found := existingByPos[key]; found {
			err := q.UpdateBin(ctx, db.UpdateBinParams{
				ID:       existing.ID,
				Name:     b.Name,
				LedIndex: ledNull,
				Width:    sql.NullInt64{Int64: int64(width), Valid: true},
				GridX:    sql.NullInt64{Int64: int64(b.X), Valid: true},
				GridY:    sql.NullInt64{Int64: int64(b.Y), Valid: true},
			})
			if err != nil {
				return 0, err
			}
			processedBinIDs[existing.ID] = true
		} else {
			_, err := q.CreateBin(ctx, db.CreateBinParams{
				Name:        b.Name,
				ContainerID: containerDBID,
				LedIndex:    ledNull,
				Width:       sql.NullInt64{Int64: int64(width), Valid: true},
				GridX:       sql.NullInt64{Int64: int64(b.X), Valid: true},
				GridY:       sql.NullInt64{Int64: int64(b.Y), Valid: true},
			})
			if err != nil {
				return 0, err
			}
		}
	}

	for _, bin := range existingBins {
		if !processedBinIDs[bin.ID] {
			if err := q.DeleteBin(ctx, bin.ID); err != nil {
				return 0, err
			}
		}
	}

	return maxLEDCount, nil
}
