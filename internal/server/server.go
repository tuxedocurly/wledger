// internal/server/server.go
package server

import (
	"database/sql"
	"html/template"

	"wledger/internal/models"
	"wledger/internal/store"
	"wledger/internal/wled"
)

// --- Interfaces (Defined by the Consumer) ---

type PartStore interface {
	GetPartByID(id int) (models.Part, error)
	GetParts() ([]models.Part, error)
	SearchParts(searchTerm string) ([]models.Part, error)
	CreatePart(p *models.Part) error
	UpdatePart(p *models.Part) error
	DeletePart(id int) error
	UpdatePartImagePath(partID int, imagePath string) error
	GetBinLocationCount(partID int) (int, error)

	GetCategories() ([]models.Category, error)
	GetCategoriesByPartID(partID int) ([]models.Category, error)
	CreateCategory(name string) (models.Category, error)
	AssignCategoryToPart(partID int, categoryID int) error
	RemoveCategoryFromPart(partID int, categoryID int) error
	CleanupOrphanedCategories() error

	GetURLsByPartID(partID int) ([]models.PartURL, error)
	CreatePartURL(partID int, url string, description string) error
	DeletePartURL(urlID int) error

	GetDocumentsByPartID(partID int) ([]models.PartDocument, error)
	GetDocumentByID(docID int) (models.PartDocument, error)
	CreatePartDocument(doc *models.PartDocument) error
	DeletePartDocument(docID int) error
}

type LocationStore interface {
	GetPartLocationByID(locationID int) (models.PartLocation, error)
	GetPartLocations(partID int) ([]models.PartLocation, error)
	CreatePartLocation(partID, binID, quantity int) error
	UpdatePartLocation(locationID, quantity int) error
	DeletePartLocation(locationID int) error
}

type BinStore interface {
	GetBins() ([]models.Bin, error)
	GetAvailableBins(partID int) ([]models.Bin, error)
	CreateBin(name string, controllerID, segmentID, ledIndex int) error
	CreateBinsBulk(controllerID, segmentID, ledCount int, namePrefix string) error
	DeleteBin(id int) error
	GetPartNamesInBin(binID int) ([]string, error)
}

type ControllerStore interface {
	GetControllers() ([]models.WLEDController, error)
	GetControllerByID(id int) (models.WLEDController, error)
	CreateController(name, ipAddress string) error
	DeleteController(id int) error
	GetAllControllersForHealthCheck() ([]models.WLEDController, error)
	UpdateControllerStatus(id int, status string, lastSeen sql.NullTime) error
}

type DashboardStore interface {
	GetDashboardBinData() ([]models.DashboardBinData, error)
	GetPartLocationsForLocate(partID int) ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
	GetPartLocationsForStop(partID int) ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
	GetAllBinLocationsForStopAll() ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
}

type WLEDClientInterface interface {
	SendCommand(ipAddress string, state models.WLEDState) error
	Ping(ipAddress string) bool
}

// --- App Struct ---

type App struct {
	Templates *template.Template
	Wled      WLEDClientInterface

	PartStore PartStore
	LocStore  LocationStore
	BinStore  BinStore
	CtrlStore ControllerStore
	DashStore DashboardStore
}

// NewApp creates a new server application
func NewApp(t *template.Template, w *wled.WLEDClient, s *store.Store) *App {
	return &App{
		Templates: t,
		Wled:      w,
		PartStore: s,
		LocStore:  s,
		BinStore:  s,
		CtrlStore: s,
		DashStore: s,
	}
}
