package mapper

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/tuxedocurly/wledger/internal/db"
)

type ContainerConfig struct {
	Type        string    `json:"type"`                   // "linear", "grid", "compound", "xy"
	Rows        int       `json:"rows"`                   // for grid, xy
	Cols        int       `json:"cols"`                   // for grid, xy
	Total       int       `json:"total"`                  // for linear
	StartCorner string    `json:"start_corner,omitempty"` // UI start corner
	Sections    []Section `json:"sections,omitempty"`     // for compound
	XStartLed   *int      `json:"x_start_led,omitempty"`  // for xy (default 0)
	XReverse    bool      `json:"x_reverse,omitempty"`    // for xy
	YStartLed   *int      `json:"y_start_led,omitempty"`  // for xy (default x_start_led + cols)
	YReverse    bool      `json:"y_reverse,omitempty"`    // for xy
}

type Section struct {
	Rows int `json:"rows"`
	Cols int `json:"cols"`
}

// MappedRange represents a contiguous LED span to illuminate for a bin on a WLED segment.
type MappedRange struct {
	SegmentID  int64
	StartIndex int64
	Width      int64
}

// CalculateGlobalIndex determines the WLED segment and absolute LED index for a bin.
// It assumes the `containers` slice is sorted in physical wiring order.
func CalculateGlobalIndex(containers []db.Container, targetBin db.Bin) (int64, int64, error) {
	// Find Target Container
	targetContainer, found := findContainer(containers, targetBin.ContainerID)
	if !found {
		return 0, 0, fmt.Errorf("target container %d not found in provided list", targetBin.ContainerID)
	}

	if !targetBin.LedIndex.Valid {
		return 0, 0, fmt.Errorf("target bin has no LED index")
	}

	return targetContainer.SegmentID, targetBin.LedIndex.Int64, nil
}

// ResolveBinRanges resolves one or more WLED LED ranges for a bin.
// Standard 1-LED-per-bin layouts return a single range matching CalculateGlobalIndex;
// "xy" crosshair containers return two width-1 ranges corresponding to [X, Y].
func ResolveBinRanges(containers []db.Container, targetBin db.Bin) ([]MappedRange, error) {
	targetContainer, found := findContainer(containers, targetBin.ContainerID)
	if !found {
		return nil, fmt.Errorf("target container %d not found in provided list", targetBin.ContainerID)
	}

	var cfg ContainerConfig
	if targetContainer.ConfigJson.Valid && targetContainer.ConfigJson.String != "" {
		if err := json.Unmarshal([]byte(targetContainer.ConfigJson.String), &cfg); err != nil {
			return nil, fmt.Errorf("invalid container %d config_json: %w", targetContainer.ID, err)
		}
	}

	if cfg.Type != "xy" {
		segID, globalIdx, err := CalculateGlobalIndex(containers, targetBin)
		if err != nil {
			return nil, err
		}
		width := targetBin.Width.Int64
		if width < 1 {
			width = 1
		}
		return []MappedRange{{
			SegmentID:  segID,
			StartIndex: globalIdx,
			Width:      width,
		}}, nil
	}

	if cfg.Rows < 1 || cfg.Cols < 1 {
		return nil, fmt.Errorf("xy container %d has invalid dimensions %dx%d", targetContainer.ID, cfg.Cols, cfg.Rows)
	}
	if !targetBin.GridX.Valid || !targetBin.GridY.Valid {
		return nil, fmt.Errorf("target xy bin has no grid coordinates")
	}

	gx := int(targetBin.GridX.Int64)
	gy := int(targetBin.GridY.Int64)
	if gx < 0 || gx >= cfg.Cols || gy < 0 || gy >= cfg.Rows {
		return nil, fmt.Errorf("xy bin coordinates (%d, %d) out of bounds for %dx%d container", gx, gy, cfg.Cols, cfg.Rows)
	}

	xStart, yStart := ResolveXYStarts(cfg)
	xOffset := gx
	if cfg.XReverse {
		xOffset = cfg.Cols - 1 - gx
	}
	yOffset := gy
	if cfg.YReverse {
		yOffset = cfg.Rows - 1 - gy
	}

	return []MappedRange{
		{
			SegmentID:  targetContainer.SegmentID,
			StartIndex: int64(xStart + xOffset),
			Width:      1,
		},
		{
			SegmentID:  targetContainer.SegmentID,
			StartIndex: int64(yStart + yOffset),
			Width:      1,
		},
	}, nil
}

// ResolveXYStarts returns the resolved 0-based start LED indices for the X and Y strips
// of an "xy" container configuration.
func ResolveXYStarts(cfg ContainerConfig) (int, int) {
	xStart := 0
	if cfg.XStartLed != nil && *cfg.XStartLed >= 0 {
		xStart = *cfg.XStartLed
	}
	yStart := xStart + cfg.Cols
	if cfg.YStartLed != nil && *cfg.YStartLed >= 0 {
		yStart = *cfg.YStartLed
	}
	return xStart, yStart
}

// ContainerMaxLEDExtent returns the highest exclusive LED index used by an "xy" container
// (max(xStart+cols, yStart+rows)), or 0 for non-"xy" or invalid configurations.
func ContainerMaxLEDExtent(cfg ContainerConfig) int64 {
	if cfg.Type != "xy" || cfg.Rows < 1 || cfg.Cols < 1 {
		return 0
	}
	xStart, yStart := ResolveXYStarts(cfg)
	xEnd := int64(xStart + cfg.Cols)
	yEnd := int64(yStart + cfg.Rows)
	if yEnd > xEnd {
		return yEnd
	}
	return xEnd
}

// IsXYConfig reports whether configJSON describes a valid "xy" crosshair container.
func IsXYConfig(configJSON sql.NullString) bool {
	if !configJSON.Valid || configJSON.String == "" {
		return false
	}
	var cfg ContainerConfig
	if err := json.Unmarshal([]byte(configJSON.String), &cfg); err != nil {
		return false
	}
	return cfg.Type == "xy" && cfg.Rows >= 1 && cfg.Cols >= 1
}

// IsAssignmentLocatable returns true when an assignment row references a mounted bin on a
// configured controller that can be addressed either directly via led_index or via an "xy" container.
func IsAssignmentLocatable(binIDValid, ledIndexValid, gridXValid, gridYValid, controllerIDValid bool, controllerIP, containerConfig sql.NullString) bool {
	if !binIDValid || !controllerIDValid || !controllerIP.Valid || controllerIP.String == "" {
		return false
	}
	if ledIndexValid {
		return true
	}
	return IsXYConfig(containerConfig) && gridXValid && gridYValid
}

func findContainer(containers []db.Container, containerID int64) (db.Container, bool) {
	for _, c := range containers {
		if c.ID == containerID {
			return c, true
		}
	}
	return db.Container{}, false
}

func GetContainerLength(c db.Container) (int64, error) {
	if !c.ConfigJson.Valid {
		return 0, nil
	}

	var config ContainerConfig
	if err := json.Unmarshal([]byte(c.ConfigJson.String), &config); err != nil {
		return 0, err
	}

	switch config.Type {
	case "linear":
		return int64(config.Total), nil
	case "grid":
		return int64(config.Rows * config.Cols), nil
	case "xy":
		if config.Rows > 0 && config.Cols > 0 {
			return int64(config.Cols + config.Rows), nil
		}
		return 0, nil
	case "compound":
		var total int64
		for _, s := range config.Sections {
			total += int64(s.Rows * s.Cols)
		}
		return total, nil
	default:
		// Fallback for legacy or untyped grid configs
		if config.Rows > 0 && config.Cols > 0 {
			return int64(config.Rows * config.Cols), nil
		}
		return 0, nil
	}
}
