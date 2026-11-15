package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tuxedocurly/wledger/internal/apitoken"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/stock"
	"github.com/tuxedocurly/wledger/web/pages"
)

var (
	// ErrProjectShortage indicates a BOM build cannot proceed due to insufficient stock.
	ErrProjectShortage = errors.New("insufficient stock to build project")

	// ErrNotImplemented indicates an optional subsystem (Wave 3A/3B) is not wired yet.
	ErrNotImplemented = errors.New("endpoint not implemented")
)

// TokenAuthenticator validates Bearer Personal Access Tokens.
type TokenAuthenticator interface {
	Authenticate(ctx context.Context, rawToken string) (apitoken.Principal, error)
}

// PartManager abstracts inventory part lifecycle operations.
type PartManager interface {
	CreatePart(ctx context.Context, req parts.CreatePartRequest) (int64, error)
	UpdatePart(ctx context.Context, req parts.UpdatePartRequest) error
	DeletePart(ctx context.Context, id int64) error
	GetPart(ctx context.Context, id int64) (db.Part, error)
	GetPartDetail(ctx context.Context, id int64) (parts.PartDetail, error)
	ListPartsFiltered(ctx context.Context, filter parts.ListPartsFilter) ([]pages.PartView, error)
}

// StockManager abstracts bin stock assignments and adjustments.
type StockManager interface {
	AssignStock(ctx context.Context, req stock.AssignStockRequest) error
	AdjustStock(ctx context.Context, assignmentID int64, delta int) error
}

// HardwareManager abstracts WLED controllers, containers, and bins.
type HardwareManager interface {
	ListControllers(ctx context.Context) ([]db.Controller, error)
	GetController(ctx context.Context, id int64) (db.Controller, error)
	GetBinsByController(ctx context.Context, id int64) ([]db.Bin, error)
	ListAllContainersWithController(ctx context.Context) ([]db.GetAllContainersWithControllerRow, error)
	GetBinsByContainer(ctx context.Context, containerID int64) ([]db.Bin, error)
}

// WLEDController triggers LED locate and global-off actions.
type WLEDController interface {
	LocatePart(ctx context.Context, partID int64) error
	LocateBin(ctx context.Context, controllerID, binID int64) error
	GlobalOff(ctx context.Context) error
}

// BarcodeParser parses raw 1D/2D supplier barcodes (Wave 3A).
type BarcodeParser interface {
	ParseBarcode(ctx context.Context, raw string) (ParsedBarcodeResult, error)
}

// ProjectManager abstracts projects and BOM build operations (Wave 3B).
type ProjectManager interface {
	ListProjects(ctx context.Context) ([]ProjectSummary, error)
	LocateProject(ctx context.Context, projectID int64) error
	BuildProject(ctx context.Context, projectID int64, boardCount int) (ProjectBuildResult, error)
}

// Dependencies holds the services required by the JSON REST API v1 router.
type Dependencies struct {
	Store    db.Store
	Tokens   TokenAuthenticator
	Parts    PartManager
	Stock    StockManager
	Hardware HardwareManager
	WLED     WLEDController
	Barcodes BarcodeParser
	Projects ProjectManager
	Logger   *slog.Logger
	Version  string
}

// ErrorResponse is the canonical error envelope returned by /api/v1 endpoints.
type ErrorResponse struct {
	Error string `json:"error"`
}

// HealthResponse describes API health and version metadata.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// PartResponse serializes an inventory part and its stock aggregates.
type PartResponse struct {
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	Description       string     `json:"description,omitempty"`
	PartNumber        string     `json:"part_number,omitempty"`
	Manufacturer      string     `json:"manufacturer,omitempty"`
	Supplier          string     `json:"supplier,omitempty"`
	BarcodeData       string     `json:"barcode_data,omitempty"`
	UnitCost          float64    `json:"unit_cost"`
	ReorderLevel      int64      `json:"reorder_level"`
	MinStockThreshold int64      `json:"min_stock_threshold"`
	ImagePath         string     `json:"image_path,omitempty"`
	IsFavorite        bool       `json:"is_favorite"`
	Tags              []string   `json:"tags"`
	TotalStock        int64      `json:"total_stock"`
	ValidStock        int64      `json:"valid_stock"`
	OrphanedStock     int64      `json:"orphaned_stock"`
	StockStatus       string     `json:"stock_status"`
	CreatedAt         *time.Time `json:"created_at,omitempty"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
}

// PartListResponse wraps paginated part listings.
type PartListResponse struct {
	Parts  []PartResponse `json:"parts"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
	Count  int            `json:"count"`
}

// PartAssignmentResponse describes stock stored in a specific bin/controller.
type PartAssignmentResponse struct {
	ID             int64  `json:"id"`
	BinID          *int64 `json:"bin_id,omitempty"`
	BinName        string `json:"bin_name,omitempty"`
	ContainerID    *int64 `json:"container_id,omitempty"`
	ContainerName  string `json:"container_name,omitempty"`
	ControllerID   *int64 `json:"controller_id,omitempty"`
	ControllerName string `json:"controller_name,omitempty"`
	ControllerIP   string `json:"controller_ip,omitempty"`
	LedIndex       *int64 `json:"led_index,omitempty"`
	Width          *int64 `json:"width,omitempty"`
	Quantity       int64  `json:"quantity"`
}

// PartLinkResponse represents an external datasheet/product link on a part.
type PartLinkResponse struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

// PartDocResponse represents an attached document on a part.
type PartDocResponse struct {
	ID       int64  `json:"id"`
	FileName string `json:"file_name"`
	FilePath string `json:"file_path"`
}

// PartDetailResponse returns full part metadata alongside assignments, links, and docs.
type PartDetailResponse struct {
	PartResponse
	Assignments []PartAssignmentResponse `json:"assignments"`
	Links       []PartLinkResponse       `json:"links"`
	Docs        []PartDocResponse        `json:"docs"`
}

// LinkPayload specifies a reference link in create/update requests.
type LinkPayload struct {
	ID    int64  `json:"id,omitempty"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

// CreatePartPayload is the JSON request body for POST /api/v1/parts.
type CreatePartPayload struct {
	Name              string        `json:"name"`
	Description       string        `json:"description"`
	PartNumber        string        `json:"part_number"`
	Manufacturer      string        `json:"manufacturer"`
	Supplier          string        `json:"supplier"`
	BarcodeData       string        `json:"barcode_data"`
	UnitCost          float64       `json:"unit_cost"`
	ReorderLevel      int           `json:"reorder_level"`
	MinStockThreshold int           `json:"min_stock_threshold"`
	Tags              []string      `json:"tags"`
	Links             []LinkPayload `json:"links"`
}

// UpdatePartPayload is the JSON request body for PUT /api/v1/parts/{id}.
// Pointer fields allow both full replacement and partial updates without zeroing omitted fields.
type UpdatePartPayload struct {
	Name              *string        `json:"name"`
	Description       *string        `json:"description"`
	PartNumber        *string        `json:"part_number"`
	Manufacturer      *string        `json:"manufacturer"`
	Supplier          *string        `json:"supplier"`
	BarcodeData       *string        `json:"barcode_data"`
	UnitCost          *float64       `json:"unit_cost"`
	ReorderLevel      *int           `json:"reorder_level"`
	MinStockThreshold *int           `json:"min_stock_threshold"`
	Tags              *[]string      `json:"tags"`
	Links             *[]LinkPayload `json:"links"`
}

// StockAssignPayload is the JSON request body for POST /api/v1/stock/assign.
type StockAssignPayload struct {
	PartID   int64 `json:"part_id"`
	BinID    int64 `json:"bin_id"`
	Quantity int   `json:"quantity"`
}

// StockAdjustPayload is the JSON request body for POST /api/v1/stock/{assignmentID}/adjust.
type StockAdjustPayload struct {
	Delta int `json:"delta"`
}

// BarcodeParsePayload is the JSON request body for POST /api/v1/barcodes/parse.
type BarcodeParsePayload struct {
	Barcode string `json:"barcode"`
}

// ParsedBarcodeResult is the parsed barcode payload returned by POST /api/v1/barcodes/parse.
type ParsedBarcodeResult struct {
	Supplier           string `json:"supplier,omitempty"`
	PartNumber         string `json:"part_number,omitempty"`
	SupplierPartNumber string `json:"supplier_part_number,omitempty"`
	Manufacturer       string `json:"manufacturer,omitempty"`
	Quantity           int    `json:"quantity,omitempty"`
	OrderNumber        string `json:"order_number,omitempty"`
	RawBarcode         string `json:"raw_barcode"`
	Format             string `json:"format"`
	ExistingPartID     *int64 `json:"existing_part_id,omitempty"`
	ExistingPartName   string `json:"existing_part_name,omitempty"`
}

// BinResponse serializes a physical storage bin within a container.
type BinResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ContainerID int64  `json:"container_id"`
	LedIndex    *int64 `json:"led_index,omitempty"`
	Width       *int64 `json:"width,omitempty"`
	GridX       *int64 `json:"grid_x,omitempty"`
	GridY       *int64 `json:"grid_y,omitempty"`
}

// ContainerResponse serializes a container with optional WLED controller binding and bins.
type ContainerResponse struct {
	ID                 int64         `json:"id"`
	Name               string        `json:"name"`
	ControllerID       *int64        `json:"controller_id,omitempty"`
	ControllerName     string        `json:"controller_name,omitempty"`
	ControllerIP       string        `json:"controller_ip,omitempty"`
	ControllerIsOnline *bool         `json:"controller_is_online,omitempty"`
	SegmentID          int64         `json:"segment_id"`
	PositionIndex      int64         `json:"position_index"`
	Bins               []BinResponse `json:"bins"`
}

// ControllerResponse serializes a WLED controller.
type ControllerResponse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	IPAddress  string `json:"ip_address"`
	Port       int64  `json:"port"`
	MacAddress string `json:"mac_address,omitempty"`
	IsOnline   bool   `json:"is_online"`
	LedCount   int64  `json:"led_count"`
	UseUDP     bool   `json:"use_udp"`
	UDPPort    int64  `json:"udp_port"`
}

// LocateControllerPayload optionally specifies which bin on a controller to illuminate.
type LocateControllerPayload struct {
	BinID *int64 `json:"bin_id,omitempty"`
}

// ProjectSummary represents a hardware project with BOM item count.
type ProjectSummary struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Description  string     `json:"description,omitempty"`
	Status       string     `json:"status"`
	BOMItemCount int64      `json:"bom_item_count"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

// ProjectBuildPayload configures POST /api/v1/projects/{id}/build.
type ProjectBuildPayload struct {
	BoardCount *int `json:"board_count"`
}

// ProjectBuildResult describes the stock deduction outcome of building a project.
type ProjectBuildResult struct {
	ProjectID     int64  `json:"project_id"`
	ProjectName   string `json:"project_name,omitempty"`
	BoardCount    int    `json:"board_count"`
	ItemsBuilt    int    `json:"items_built,omitempty"`
	TotalDeducted int    `json:"total_deducted,omitempty"`
	Message       string `json:"message,omitempty"`
}
