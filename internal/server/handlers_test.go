package server

import (
	"database/sql"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"wledger/internal/models"
	"wledger/internal/store"
)

// Mock WLED Client
type MockWLEDClient struct {
	SendCommandFunc func(ipAddress string, state models.WLEDState) error
	PingFunc        func(ipAddress string) bool
}

func (m *MockWLEDClient) SendCommand(ipAddress string, state models.WLEDState) error {
	if m.SendCommandFunc != nil {
		return m.SendCommandFunc(ipAddress, state)
	}
	return nil
}
func (m *MockWLEDClient) Ping(ipAddress string) bool {
	if m.PingFunc != nil {
		return m.PingFunc(ipAddress)
	}
	return false
}

// Mock Stores

// mockPartStore
type mockPartStore struct {
	GetPartByIDFunc               func(id int) (models.Part, error)
	GetPartsFunc                  func() ([]models.Part, error)
	SearchPartsFunc               func(searchTerm string) ([]models.Part, error)
	CreatePartFunc                func(p *models.Part) error
	UpdatePartFunc                func(p *models.Part) error
	DeletePartFunc                func(id int) error
	UpdatePartImagePathFunc       func(partID int, imagePath string) error
	GetBinLocationCountFunc       func(partID int) (int, error)
	GetCategoriesFunc             func() ([]models.Category, error)
	GetCategoriesByPartIDFunc     func(partID int) ([]models.Category, error)
	CreateCategoryFunc            func(name string) (models.Category, error)
	AssignCategoryToPartFunc      func(partID int, categoryID int) error
	RemoveCategoryFromPartFunc    func(partID int, categoryID int) error
	CleanupOrphanedCategoriesFunc func() error
	GetURLsByPartIDFunc           func(partID int) ([]models.PartURL, error)
	CreatePartURLFunc             func(partID int, url string, description string) error
	DeletePartURLFunc             func(urlID int) error
	GetDocumentsByPartIDFunc      func(partID int) ([]models.PartDocument, error)
	GetDocumentByIDFunc           func(docID int) (models.PartDocument, error)
	CreatePartDocumentFunc        func(doc *models.PartDocument) error
	DeletePartDocumentFunc        func(docID int) error
}

func (m *mockPartStore) GetPartByID(id int) (models.Part, error) { return m.GetPartByIDFunc(id) }
func (m *mockPartStore) GetParts() ([]models.Part, error)        { return m.GetPartsFunc() }
func (m *mockPartStore) SearchParts(searchTerm string) ([]models.Part, error) {
	return m.SearchPartsFunc(searchTerm)
}
func (m *mockPartStore) CreatePart(p *models.Part) error { return m.CreatePartFunc(p) }
func (m *mockPartStore) UpdatePart(p *models.Part) error { return m.UpdatePartFunc(p) }
func (m *mockPartStore) DeletePart(id int) error         { return m.DeletePartFunc(id) }
func (m *mockPartStore) UpdatePartImagePath(partID int, imagePath string) error {
	return m.UpdatePartImagePathFunc(partID, imagePath)
}
func (m *mockPartStore) GetBinLocationCount(partID int) (int, error) {
	return m.GetBinLocationCountFunc(partID)
}
func (m *mockPartStore) GetCategories() ([]models.Category, error) {
	if m.GetCategoriesFunc != nil {
		return m.GetCategoriesFunc()
	}
	return nil, nil
}
func (m *mockPartStore) GetCategoriesByPartID(partID int) ([]models.Category, error) {
	if m.GetCategoriesByPartIDFunc != nil {
		return m.GetCategoriesByPartIDFunc(partID)
	}
	return nil, nil
}
func (m *mockPartStore) CreateCategory(name string) (models.Category, error) {
	if m.CreateCategoryFunc != nil {
		return m.CreateCategoryFunc(name)
	}
	return models.Category{}, nil
}
func (m *mockPartStore) AssignCategoryToPart(partID int, categoryID int) error {
	if m.AssignCategoryToPartFunc != nil {
		return m.AssignCategoryToPartFunc(partID, categoryID)
	}
	return nil
}
func (m *mockPartStore) RemoveCategoryFromPart(partID int, categoryID int) error {
	if m.RemoveCategoryFromPartFunc != nil {
		return m.RemoveCategoryFromPartFunc(partID, categoryID)
	}
	return nil
}
func (m *mockPartStore) CleanupOrphanedCategories() error {
	if m.CleanupOrphanedCategoriesFunc != nil {
		return m.CleanupOrphanedCategoriesFunc()
	}
	return nil
}
func (m *mockPartStore) GetURLsByPartID(partID int) ([]models.PartURL, error) {
	if m.GetURLsByPartIDFunc != nil {
		return m.GetURLsByPartIDFunc(partID)
	}
	return nil, nil
}
func (m *mockPartStore) CreatePartURL(partID int, url string, description string) error {
	if m.CreatePartURLFunc != nil {
		return m.CreatePartURLFunc(partID, url, description)
	}
	return nil
}
func (m *mockPartStore) DeletePartURL(urlID int) error {
	if m.DeletePartURLFunc != nil {
		return m.DeletePartURLFunc(urlID)
	}
	return nil
}
func (m *mockPartStore) GetDocumentsByPartID(partID int) ([]models.PartDocument, error) {
	if m.GetDocumentsByPartIDFunc != nil {
		return m.GetDocumentsByPartIDFunc(partID)
	}
	return nil, nil
}
func (m *mockPartStore) GetDocumentByID(docID int) (models.PartDocument, error) {
	if m.GetDocumentByIDFunc != nil {
		return m.GetDocumentByIDFunc(docID)
	}
	return models.PartDocument{}, nil
}
func (m *mockPartStore) CreatePartDocument(doc *models.PartDocument) error {
	if m.CreatePartDocumentFunc != nil {
		return m.CreatePartDocumentFunc(doc)
	}
	return nil
}
func (m *mockPartStore) DeletePartDocument(docID int) error {
	if m.DeletePartDocumentFunc != nil {
		return m.DeletePartDocumentFunc(docID)
	}
	return nil
}

// mockControllerStore
type mockControllerStore struct {
	GetControllersFunc                  func() ([]models.WLEDController, error)
	GetControllerByIDFunc               func(id int) (models.WLEDController, error)
	CreateControllerFunc                func(name, ipAddress string) error
	DeleteControllerFunc                func(id int) error
	GetAllControllersForHealthCheckFunc func() ([]models.WLEDController, error)
	UpdateControllerStatusFunc          func(id int, status string, lastSeen sql.NullTime) error
}

func (m *mockControllerStore) GetControllers() ([]models.WLEDController, error) {
	return m.GetControllersFunc()
}
func (m *mockControllerStore) GetControllerByID(id int) (models.WLEDController, error) {
	return m.GetControllerByIDFunc(id)
}
func (m *mockControllerStore) CreateController(name, ipAddress string) error {
	return m.CreateControllerFunc(name, ipAddress)
}
func (m *mockControllerStore) DeleteController(id int) error { return m.DeleteControllerFunc(id) }
func (m *mockControllerStore) GetAllControllersForHealthCheck() ([]models.WLEDController, error) {
	return m.GetAllControllersForHealthCheckFunc()
}
func (m *mockControllerStore) UpdateControllerStatus(id int, status string, lastSeen sql.NullTime) error {
	return m.UpdateControllerStatusFunc(id, status, lastSeen)
}

// mockBinStore
type mockBinStore struct {
	GetBinsFunc           func() ([]models.Bin, error)
	GetAvailableBinsFunc  func(partID int) ([]models.Bin, error)
	CreateBinFunc         func(name string, controllerID, segmentID, ledIndex int) error
	CreateBinsBulkFunc    func(controllerID, segmentID, ledCount int, namePrefix string) error
	DeleteBinFunc         func(id int) error
	GetPartNamesInBinFunc func(binID int) ([]string, error)
}

func (m *mockBinStore) GetBins() ([]models.Bin, error) { return m.GetBinsFunc() }
func (m *mockBinStore) GetAvailableBins(partID int) ([]models.Bin, error) {
	return m.GetAvailableBinsFunc(partID)
}
func (m *mockBinStore) CreateBin(name string, controllerID, segmentID, ledIndex int) error {
	return m.CreateBinFunc(name, controllerID, segmentID, ledIndex)
}
func (m *mockBinStore) CreateBinsBulk(controllerID, segmentID, ledCount int, namePrefix string) error {
	return m.CreateBinsBulkFunc(controllerID, segmentID, ledCount, namePrefix)
}
func (m *mockBinStore) DeleteBin(id int) error { return m.DeleteBinFunc(id) }
func (m *mockBinStore) GetPartNamesInBin(binID int) ([]string, error) {
	return m.GetPartNamesInBinFunc(binID)
}

// mockDashboardStore
type mockDashboardStore struct {
	GetDashboardBinDataFunc       func() ([]models.DashboardBinData, error)
	GetPartLocationsForLocateFunc func(partID int) ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
	GetPartLocationsForStopFunc func(partID int) ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
	GetAllBinLocationsForStopAllFunc func() ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
}

func (m *mockDashboardStore) GetDashboardBinData() ([]models.DashboardBinData, error) {
	if m.GetDashboardBinDataFunc != nil {
		return m.GetDashboardBinDataFunc()
	}
	return nil, nil
}
func (m *mockDashboardStore) GetPartLocationsForLocate(partID int) ([]struct {
	IP       string
	SegID    int
	LEDIndex int
}, error) {
	if m.GetPartLocationsForLocateFunc != nil {
		return m.GetPartLocationsForLocateFunc(partID)
	}
	return nil, nil
}
func (m *mockDashboardStore) GetPartLocationsForStop(partID int) ([]struct {
	IP       string
	SegID    int
	LEDIndex int
}, error) {
	if m.GetPartLocationsForStopFunc != nil {
		return m.GetPartLocationsForStopFunc(partID)
	}
	return nil, nil
}
func (m *mockDashboardStore) GetAllBinLocationsForStopAll() ([]struct {
	IP       string
	SegID    int
	LEDIndex int
}, error) {
	if m.GetAllBinLocationsForStopAllFunc != nil {
		return m.GetAllBinLocationsForStopAllFunc()
	}
	return nil, nil
}

// TESTS

func TestHandleShowParts(t *testing.T) {
	mockStore := &mockPartStore{}
	mockStore.GetPartsFunc = func() ([]models.Part, error) {
		return []models.Part{{Name: "Fake Part", TotalQuantity: 10}}, nil
	}

	templates, err := template.ParseGlob("../../ui/templates/*.html")
	if err != nil {
		t.Fatalf("Failed to parse templates: %v", err)
	}

	app := &App{
		PartStore: mockStore,
		Templates: templates,
	}

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	app.handleShowParts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "Fake Part") {
		t.Errorf("response body does not contain 'Fake Part'")
	}
}

func TestHandleDeletePart(t *testing.T) {
	mockStore := &mockPartStore{}
	var deletedID int

	mockStore.DeletePartFunc = func(id int) error {
		deletedID = id
		return nil
	}

	app := &App{
		PartStore: mockStore,
	}

	req := httptest.NewRequest("DELETE", "/parts/5", nil)
	rr := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Delete("/parts/{id}", app.handleDeletePart)

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}
	if deletedID != 5 {
		t.Errorf("handler deleted ID %d, want %d", deletedID, 5)
	}
}

func TestHandleDeleteController_InUse(t *testing.T) {
	mockStore := &mockControllerStore{}
	mockStore.DeleteControllerFunc = func(id int) error {
		// Use the store package error
		return store.ErrForeignKeyConstraint
	}

	app := &App{
		CtrlStore: mockStore,
	}

	req := httptest.NewRequest("DELETE", "/settings/controllers/1", nil)
	rr := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Delete("/settings/controllers/{id}", app.handleDeleteController)

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusConflict)
	}
	if !strings.Contains(rr.Body.String(), "Cannot delete controller") {
		t.Errorf("response body does not contain correct error message: got %q", rr.Body.String())
	}
}

func TestHandleCreateBin_Duplicate(t *testing.T) {
	mockStore := &mockBinStore{}
	mockStore.CreateBinFunc = func(name string, controllerID, segmentID, ledIndex int) error {
		// Use the store package error
		return store.ErrUniqueConstraint
	}

	app := &App{
		BinStore: mockStore,
	}

	formData := url.Values{}
	formData.Set("name", "A1-1")
	formData.Set("controller_id", "1")
	formData.Set("segment_id", "0")
	formData.Set("led_index", "1")

	req := httptest.NewRequest("POST", "/settings/bins", strings.NewReader(formData.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	app.handleCreateBin(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusConflict)
	}
	if !strings.Contains(rr.Body.String(), "already exists") {
		t.Errorf("response body does not contain correct error message: got %q", rr.Body.String())
	}
}

func TestHandleLocatePart_Offline(t *testing.T) {
	mockStore := &mockDashboardStore{}
	mockWLED := &MockWLEDClient{}

	mockStore.GetPartLocationsForLocateFunc = func(partID int) ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error) {
		return []struct {
			IP       string
			SegID    int
			LEDIndex int
		}{{IP: "1.2.3.4", SegID: 0, LEDIndex: 1}}, nil
	}
	mockWLED.SendCommandFunc = func(ipAddress string, state models.WLEDState) error {
		return errors.New("timeout: controller is offline")
	}

	templates, err := template.ParseGlob("../../ui/templates/*.html")
	if err != nil {
		t.Fatalf("Failed to parse templates: %v", err)
	}

	app := &App{
		DashStore: mockStore,
		Wled:      mockWLED,
		Templates: templates,
	}

	req := httptest.NewRequest("POST", "/locate/part/1", nil)
	rr := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Post("/locate/part/{id}", app.handleLocatePart)

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), `hx-post="/locate/part/1"`) {
		t.Errorf("response body does not contain the 'Locate' (start) button")
	}
	if strings.Contains(rr.Body.String(), `hx-post="/locate/stop/1"`) {
		t.Errorf("response body *incorrectly* contains the 'Stop' button")
	}
}

func TestHandleShowInspiration(t *testing.T) {
	mockStore := &mockPartStore{}

	mockStore.GetPartsFunc = func() ([]models.Part, error) {
		return []models.Part{
			{
				Name:          "Test Resistor",
				PartNumber:    sql.NullString{String: "R-100", Valid: true},
				TotalQuantity: 50,
			},
			{
				Name:          "Test Capacitor",
				TotalQuantity: 0,
			},
		}, nil
	}

	templates, err := template.ParseGlob("../../ui/templates/*.html")
	if err != nil {
		t.Fatalf("Failed to parse templates: %v", err)
	}

	app := &App{
		PartStore: mockStore,
		Templates: templates,
	}

	req := httptest.NewRequest("GET", "/inspiration", nil)
	rr := httptest.NewRecorder()

	app.handleShowInspiration(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}

	expectedInStock := "- Test Resistor (R-100): 50 in stock"
	if !strings.Contains(rr.Body.String(), expectedInStock) {
		t.Errorf("response body does not contain the in-stock part.\nWant: %s\nGot:\n%s", expectedInStock, rr.Body.String())
	}

	unexpectedOutOfStock := "Test Capacitor"
	if strings.Contains(rr.Body.String(), unexpectedOutOfStock) {
		t.Errorf("response body *incorrectly* contains the out-of-stock part: %s", unexpectedOutOfStock)
	}
}
