package inventory

import (
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

// Local mock
type mockStore struct {
	GetBinByIDFunc          func(id int) (models.Bin, error)
	GetControllersFunc      func() ([]models.WLEDController, error)
	CreateBinFunc           func(name string, controllerID, segmentID, ledIndex int) error
	CreateBinsBulkFunc      func(controllerID, segmentID, ledCount int, namePrefix string) error
	UpdateBinFunc           func(b *models.Bin) error
	DeleteBinFunc           func(id int) error
	CreatePartLocationFunc  func(partID, binID, quantity int) error
	GetPartLocationByIDFunc func(locationID int) (models.PartLocation, error)
	UpdatePartLocationFunc  func(locationID, quantity int) error
	DeletePartLocationFunc  func(locationID int) error
}

func (m *mockStore) GetBinByID(id int) (models.Bin, error) {
	if m.GetBinByIDFunc != nil {
		return m.GetBinByIDFunc(id)
	}
	return models.Bin{}, nil
}
func (m *mockStore) GetControllers() ([]models.WLEDController, error) {
	if m.GetControllersFunc != nil {
		return m.GetControllersFunc()
	}
	return nil, nil
}
func (m *mockStore) CreateBin(name string, cid, sid, led int) error {
	if m.CreateBinFunc != nil {
		return m.CreateBinFunc(name, cid, sid, led)
	}
	return nil
}
func (m *mockStore) CreateBinsBulk(cid, sid, count int, prefix string) error {
	if m.CreateBinsBulkFunc != nil {
		return m.CreateBinsBulkFunc(cid, sid, count, prefix)
	}
	return nil
}
func (m *mockStore) UpdateBin(b *models.Bin) error {
	if m.UpdateBinFunc != nil {
		return m.UpdateBinFunc(b)
	}
	return nil
}
func (m *mockStore) DeleteBin(id int) error {
	if m.DeleteBinFunc != nil {
		return m.DeleteBinFunc(id)
	}
	return nil
}
func (m *mockStore) CreatePartLocation(pid, bid, qty int) error {
	if m.CreatePartLocationFunc != nil {
		return m.CreatePartLocationFunc(pid, bid, qty)
	}
	return nil
}
func (m *mockStore) GetPartLocationByID(id int) (models.PartLocation, error) {
	if m.GetPartLocationByIDFunc != nil {
		return m.GetPartLocationByIDFunc(id)
	}
	return models.PartLocation{}, nil
}
func (m *mockStore) UpdatePartLocation(id, qty int) error {
	if m.UpdatePartLocationFunc != nil {
		return m.UpdatePartLocationFunc(id, qty)
	}
	return nil
}
func (m *mockStore) DeletePartLocation(id int) error {
	if m.DeletePartLocationFunc != nil {
		return m.DeletePartLocationFunc(id)
	}
	return nil
}

// Test setup helper
func setupTest(t *testing.T) (*Handler, *mockStore) {
	t.Helper()
	ms := &mockStore{}
	// Path relative to this file: ../../../ui/templates
	tmpl, _ := template.ParseGlob("../../../ui/templates/*.html")
	h := New(ms, tmpl)
	return h, ms
}

// Tests

func TestHandleCreateBin_Duplicate(t *testing.T) {
	h, ms := setupTest(t)

	ms.CreateBinFunc = func(name string, cid, sid, led int) error {
		return store.ErrUniqueConstraint
	}

	formData := url.Values{}
	formData.Set("name", "A1-1")
	formData.Set("controller_id", "1")
	formData.Set("segment_id", "0")
	formData.Set("led_index", "1")

	req := httptest.NewRequest("POST", "/settings/bins", strings.NewReader(formData.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.handleCreateBin(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusConflict)
	}
}

func TestHandleUpdateBin(t *testing.T) {
	h, ms := setupTest(t)

	binUpdated := false
	ms.UpdateBinFunc = func(b *models.Bin) error {
		if b.ID == 1 && b.Name == "Updated Name" {
			binUpdated = true
		}
		return nil
	}
	ms.GetBinByIDFunc = func(id int) (models.Bin, error) {
		return models.Bin{ID: 1, Name: "Updated Name"}, nil
	}

	formData := url.Values{}
	formData.Set("name", "Updated Name")
	formData.Set("controller_id", "2")
	formData.Set("segment_id", "0")
	formData.Set("led_index", "5")

	req := httptest.NewRequest("PUT", "/settings/bins/1", strings.NewReader(formData.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	r.Put("/settings/bins/{id}", h.handleUpdateBin)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}
	if !binUpdated {
		t.Error("UpdateBin was not called with expected values")
	}
}
