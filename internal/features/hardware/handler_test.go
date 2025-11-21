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

	"github.com/go-chi/chi/v5"

	"wledger/internal/models"
)

// Local Mocks
type mockStore struct {
	GetControllerByIDFunc      func(id int) (models.WLEDController, error)
	GetControllersFunc         func() ([]models.WLEDController, error)
	CreateControllerFunc       func(name, ipAddress string) error
	UpdateControllerFunc       func(c *models.WLEDController) error
	DeleteControllerFunc       func(id int) error
	UpdateControllerStatusFunc func(id int, status string, lastSeen sql.NullTime) error
	MigrateBinsFunc            func(oldID, newID int) error
}

// Implement interface methods
func (m *mockStore) GetControllerByID(id int) (models.WLEDController, error) {
	if m.GetControllerByIDFunc != nil {
		return m.GetControllerByIDFunc(id)
	}
	return models.WLEDController{}, nil
}
func (m *mockStore) GetControllers() ([]models.WLEDController, error) {
	if m.GetControllersFunc != nil {
		return m.GetControllersFunc()
	}
	return nil, nil
}
func (m *mockStore) CreateController(name, ipAddress string) error {
	if m.CreateControllerFunc != nil {
		return m.CreateControllerFunc(name, ipAddress)
	}
	return nil
}
func (m *mockStore) UpdateController(c *models.WLEDController) error {
	if m.UpdateControllerFunc != nil {
		return m.UpdateControllerFunc(c)
	}
	return nil
}
func (m *mockStore) DeleteController(id int) error {
	if m.DeleteControllerFunc != nil {
		return m.DeleteControllerFunc(id)
	}
	return nil
}
func (m *mockStore) UpdateControllerStatus(id int, status string, lastSeen sql.NullTime) error {
	if m.UpdateControllerStatusFunc != nil {
		return m.UpdateControllerStatusFunc(id, status, lastSeen)
	}
	return nil
}
func (m *mockStore) MigrateBins(oldID, newID int) error {
	if m.MigrateBinsFunc != nil {
		return m.MigrateBinsFunc(oldID, newID)
	}
	return nil
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

// Test setup helper
func setupTest(t *testing.T) (*Handler, *mockStore) {
	t.Helper()
	ms := &mockStore{}
	mw := &mockWLED{}
	// need templates for some handlers
	// Path is relative to this test file: ../../../ui/templates
	tmpl, _ := template.ParseGlob("../../../ui/templates/*.html")

	h := New(ms, mw, tmpl)
	return h, ms
}

// Tests

func TestHandleDeleteController_InUse(t *testing.T) {
	h, ms := setupTest(t)

	ms.DeleteControllerFunc = func(id int) error {
		// Simulate the store returning a foreign key error
		return errors.New("foreign key constraint violation")
	}

	req := httptest.NewRequest("DELETE", "/settings/controllers/1", nil)
	rr := httptest.NewRecorder()

	// Wrap in router for URL params
	r := chi.NewRouter()
	r.Delete("/settings/controllers/{id}", h.handleDeleteController)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusConflict)
	}
}

func TestHandleMigrateController(t *testing.T) {
	h, ms := setupTest(t)

	migrated := false
	ms.MigrateBinsFunc = func(oldID, newID int) error {
		if oldID == 1 && newID == 2 {
			migrated = true
		}
		return nil
	}
	ms.GetControllerByIDFunc = func(id int) (models.WLEDController, error) {
		return models.WLEDController{ID: 1, Name: "Source", BinCount: 0}, nil
	}

	formData := url.Values{}
	formData.Set("new_controller_id", "2")
	req := httptest.NewRequest("POST", "/settings/controllers/1/migrate", strings.NewReader(formData.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	r.Post("/settings/controllers/{id}/migrate", h.handleMigrateController)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}
	if !migrated {
		t.Error("MigrateBins was not called with expected IDs")
	}
}
