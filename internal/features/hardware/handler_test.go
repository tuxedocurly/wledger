package hardware

import (
	"database/sql"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"wledger/internal/models"

	"github.com/go-chi/chi/v5"
)

// Local Mocks
type mockStore struct {
	FailOps bool

	GetControllerByIDFunc      func(id int) (models.WLEDController, error)
	GetControllersFunc         func() ([]models.WLEDController, error)
	CreateControllerFunc       func(name, ipAddress string) error
	UpdateControllerFunc       func(c *models.WLEDController) error
	DeleteControllerFunc       func(id int) error
	UpdateControllerStatusFunc func(id int, status string, lastSeen sql.NullTime) error
	MigrateBinsFunc            func(oldID, newID int) error
	GetBinsFunc                func() ([]models.Bin, error)
}

func (m *mockStore) retErr() error {
	if m.FailOps {
		return errors.New("db error")
	}
	return nil
}

func (m *mockStore) GetControllerByID(id int) (models.WLEDController, error) {
	if m.FailOps {
		return models.WLEDController{}, errors.New("db error")
	}
	if m.GetControllerByIDFunc != nil {
		return m.GetControllerByIDFunc(id)
	}
	return models.WLEDController{}, nil
}
func (m *mockStore) GetControllers() ([]models.WLEDController, error) {
	if m.FailOps {
		return nil, errors.New("db error")
	}
	if m.GetControllersFunc != nil {
		return m.GetControllersFunc()
	}
	return nil, nil
}
func (m *mockStore) CreateController(name, ip string) error {
	if err := m.retErr(); err != nil {
		return err
	}
	if m.CreateControllerFunc != nil {
		return m.CreateControllerFunc(name, ip)
	}
	return nil
}
func (m *mockStore) UpdateController(c *models.WLEDController) error {
	if err := m.retErr(); err != nil {
		return err
	}
	if m.UpdateControllerFunc != nil {
		return m.UpdateControllerFunc(c)
	}
	return nil
}
func (m *mockStore) DeleteController(id int) error {
	if m.DeleteControllerFunc != nil {
		return m.DeleteControllerFunc(id)
	}
	return m.retErr()
}
func (m *mockStore) UpdateControllerStatus(id int, status string, lastSeen sql.NullTime) error {
	// Special handling for refresh test:
	// We want to test "Update fails but flow continues" vs "DB Error generally"
	if m.UpdateControllerStatusFunc != nil {
		return m.UpdateControllerStatusFunc(id, status, lastSeen)
	}
	if err := m.retErr(); err != nil {
		return err
	}
	return nil
}
func (m *mockStore) MigrateBins(oldID, newID int) error {
	if err := m.retErr(); err != nil {
		return err
	}
	if m.MigrateBinsFunc != nil {
		return m.MigrateBinsFunc(oldID, newID)
	}
	return nil
}
func (m *mockStore) GetBins() ([]models.Bin, error) {
	if m.FailOps {
		return nil, errors.New("db error")
	}
	if m.GetBinsFunc != nil {
		return m.GetBinsFunc()
	}
	return nil, nil
}

type mockWLED struct {
	PingFunc func(ipAddress string) bool
}

func (m *mockWLED) Ping(ip string) bool {
	if m.PingFunc != nil {
		return m.PingFunc(ip)
	}
	return false
}

// Test Setup Helper
func setupTest(t *testing.T) (*Handler, *mockStore, *mockWLED) {
	t.Helper()
	ms := &mockStore{}
	mw := &mockWLED{}
	// Fallback for template path
	tmpl, _ := template.ParseGlob("../../../ui/templates/*.html")
	if tmpl == nil {
		tmpl, _ = template.ParseGlob("ui/templates/*.html")
	}
	h := New(ms, mw, tmpl)
	return h, ms, mw
}

// --- TESTS ---

func TestHandleCreateController(t *testing.T) {
	h, ms, _ := setupTest(t)

	// Happy
	form := url.Values{"name": {"C1"}, "ip_address": {"1.1.1.1"}}
	req := httptest.NewRequest("POST", "/settings/controllers", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.handleCreateController(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// Missing Data
	form = url.Values{"name": {""}}
	req = httptest.NewRequest("POST", "/settings/controllers", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	h.handleCreateController(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("Missing Data: got %d", rr.Code)
	}

	// DB Error
	ms.FailOps = true
	form = url.Values{"name": {"C1"}, "ip_address": {"1.1.1.1"}}
	req = httptest.NewRequest("POST", "/settings/controllers", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	h.handleCreateController(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleDeleteController(t *testing.T) {
	h, ms, _ := setupTest(t)
	r := chi.NewRouter()
	r.Delete("/settings/controllers/{id}", h.handleDeleteController)

	// Happy
	req := httptest.NewRequest("DELETE", "/settings/controllers/1", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// Constraint Error
	ms.DeleteControllerFunc = func(id int) error { return errors.New("foreign key constraint violation") }
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("Conflict: got %d", rr.Code)
	}

	// DB Error
	ms.DeleteControllerFunc = nil
	ms.FailOps = true
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleRefreshControllerStatus(t *testing.T) {
	h, ms, mw := setupTest(t)
	r := chi.NewRouter()
	r.Post("/settings/controllers/{id}/refresh", h.handleRefreshControllerStatus)

	// Mock Controller
	ms.GetControllerByIDFunc = func(id int) (models.WLEDController, error) {
		return models.WLEDController{ID: 1, IPAddress: "1.1.1.1"}, nil
	}

	// Mock WLED Ping (Online)
	mw.PingFunc = func(ip string) bool { return true }

	// Happy Path (Online)
	req := httptest.NewRequest("POST", "/settings/controllers/1/refresh", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// Test Offline Logic: "Update fails but flow continues"
	// Simulate update returning an error. Logic should LOG it, then continue
	ms.UpdateControllerStatusFunc = func(id int, s string, l sql.NullTime) error { return errors.New("fail") }

	// need GetControllerByID to succeed so the handler finishes
	ms.GetControllerByIDFunc = func(id int) (models.WLEDController, error) {
		return models.WLEDController{ID: 1, IPAddress: "1.1.1.1"}, nil
	}

	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Update Fail check: got %d", rr.Code)
	}
}

func TestHandleUpdateController(t *testing.T) {
	h, ms, _ := setupTest(t)
	r := chi.NewRouter()
	r.Put("/settings/controllers/{id}", h.handleUpdateController)

	ms.GetControllerByIDFunc = func(id int) (models.WLEDController, error) {
		return models.WLEDController{Name: "New"}, nil
	}

	// Happy
	form := url.Values{"name": {"New"}, "ip_address": {"2.2.2.2"}}
	req := httptest.NewRequest("PUT", "/settings/controllers/1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Happy: got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "New") {
		t.Error("Body missing updated name")
	}

	// DB Error
	ms.FailOps = true
	form = url.Values{"name": {"New"}, "ip_address": {"2.2.2.2"}}
	req = httptest.NewRequest("PUT", "/settings/controllers/1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleMigrateController(t *testing.T) {
	h, ms, _ := setupTest(t)
	r := chi.NewRouter()
	r.Post("/settings/controllers/{id}/migrate", h.handleMigrateController)

	ms.GetControllerByIDFunc = func(id int) (models.WLEDController, error) {
		return models.WLEDController{Name: "Source"}, nil
	}

	// Happy
	form := url.Values{"new_controller_id": {"2"}}
	req := httptest.NewRequest("POST", "/settings/controllers/1/migrate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// DB Error
	ms.FailOps = true
	form = url.Values{"new_controller_id": {"2"}}
	req = httptest.NewRequest("POST", "/settings/controllers/1/migrate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleGetControllerRows(t *testing.T) {
	h, ms, _ := setupTest(t)
	r := chi.NewRouter()
	r.Get("/settings/controllers/{id}", h.handleGetControllerRow)
	r.Get("/settings/controllers/{id}/edit", h.handleGetControllerEditRow)
	r.Get("/settings/controllers/{id}/migrate", h.handleGetControllerMigrateRow)

	ms.GetControllerByIDFunc = func(id int) (models.WLEDController, error) { return models.WLEDController{}, nil }
	ms.GetControllersFunc = func() ([]models.WLEDController, error) { return []models.WLEDController{{ID: 2}}, nil }

	// Row
	req := httptest.NewRequest("GET", "/settings/controllers/1", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Row: got %d", rr.Code)
	}

	// Edit
	req = httptest.NewRequest("GET", "/settings/controllers/1/edit", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Edit: got %d", rr.Code)
	}

	// Migrate
	req = httptest.NewRequest("GET", "/settings/controllers/1/migrate", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Migrate: got %d", rr.Code)
	}
}
