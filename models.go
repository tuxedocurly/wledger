package main

import (
	"database/sql"
	"time"
)

// Part struct (catalog item)
type Part struct {
	ID            int
	Name          string
	Description   sql.NullString
	PartNumber    sql.NullString
	DatasheetURL  sql.NullString
	CreatedAt     time.Time
	UpdatedAt     time.Time
	TotalQuantity int // Calculated on the fly, weeeeeee
	ImagePath     sql.NullString
	Manufacturer  sql.NullString
	Supplier      sql.NullString
	UnitCost      sql.NullFloat64
	Status        sql.NullString
	StockTracking bool
	ReorderPoint  int
	MinStock      int
}

// WLEDController struct to hold WLED controller data
type WLEDController struct {
	ID        int
	Name      string
	IPAddress string
	Status    string
	LastSeen  sql.NullTime
}

// Bin struct (a single LED)
type Bin struct {
	ID                 int
	Name               string
	WLEDControllerID   int
	WLEDSegmentID      int
	LEDIndex           int
	WLEDControllerName sql.NullString
}

// PartLocation holds detailed info about a single part's inventory
type PartLocation struct {
	LocationID   int
	PartID       int
	BinID        int
	Quantity     int
	BinName      string
	SegmentID    int
	LEDIndex     int
	ControllerID int
}

// WLEDState represents the state to send to WLED
type WLEDState struct {
	Segments []WLEDSegment `json:"seg"`
}

// WLEDSegment defines a single segment's (LED Strip) state
type WLEDSegment struct {
	ID     int           `json:"id"`
	On     bool          `json:"on"`
	Effect int           `json:"fx,omitempty"`
	Color  []int         `json:"col,omitempty"` // [R, G, B]
	I      []interface{} `json:"i,omitempty"`   // For individual LEDs
}

type PartURL struct {
	ID          int
	PartID      int
	URL         string
	Description sql.NullString
}

// PartDocument represents a single uploaded file for a part
type PartDocument struct {
	ID          int
	PartID      int
	Filename    string
	Filepath    string
	Description sql.NullString
	Mimetype    string
}

// Category represents a tag/category for a part
type Category struct {
	ID   int
	Name string
}

// DashboardBinData holds aggregated data for dashboard display
type DashboardBinData struct {
	ReorderPoint int
	MinStock     int
	BinQuantity  int
	BinIP        string
	BinSegmentID int
	BinLEDIndex  int
}
