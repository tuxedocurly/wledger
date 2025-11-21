package inventory

import (
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

// Local mock
type mockStore struct {
	FailOps bool // Flag to trigger DB errors

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

// Helper to return error if FailOps is true
func (m *mockStore) retErr() error {
	if m.FailOps {
		return errors.New("db error")
	}
	return nil
}

func (m *mockStore) GetBinByID(id int) (models.Bin, error) {
	if m.FailOps {
		return models.Bin{}, errors.New("db error")
	}
	if m.GetBinByIDFunc != nil {
		return m.GetBinByIDFunc(id)
	}
	return models.Bin{}, nil
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
func (m *mockStore) CreateBin(name string, cid, sid, led int) error {
	// Allow specific override to take precedence for unique constraint tests
	if m.CreateBinFunc != nil {
		return m.CreateBinFunc(name, cid, sid, led)
	}
	return m.retErr()
}
func (m *mockStore) CreateBinsBulk(cid, sid, count int, prefix string) error {
	if m.CreateBinsBulkFunc != nil {
		return m.CreateBinsBulkFunc(cid, sid, count, prefix)
	}
	return m.retErr()
}
func (m *mockStore) UpdateBin(b *models.Bin) error {
	if m.UpdateBinFunc != nil {
		return m.UpdateBinFunc(b)
	}
	return m.retErr()
}
func (m *mockStore) DeleteBin(id int) error {
	if m.DeleteBinFunc != nil {
		return m.DeleteBinFunc(id)
	}
	return m.retErr()
}
func (m *mockStore) CreatePartLocation(pid, bid, qty int) error {
	if m.CreatePartLocationFunc != nil {
		return m.CreatePartLocationFunc(pid, bid, qty)
	}
	return m.retErr()
}
func (m *mockStore) GetPartLocationByID(id int) (models.PartLocation, error) {
	if m.FailOps {
		return models.PartLocation{}, errors.New("db error")
	}
	if m.GetPartLocationByIDFunc != nil {
		return m.GetPartLocationByIDFunc(id)
	}
	return models.PartLocation{}, nil
}
func (m *mockStore) UpdatePartLocation(id, qty int) error {
	if m.UpdatePartLocationFunc != nil {
		return m.UpdatePartLocationFunc(id, qty)
	}
	return m.retErr()
}
func (m *mockStore) DeletePartLocation(id int) error {
	if m.DeletePartLocationFunc != nil {
		return m.DeletePartLocationFunc(id)
	}
	return m.retErr()
}

// Test setup Helper
func setupTest(t *testing.T) (*Handler, *mockStore) {
	t.Helper()
	ms := &mockStore{}
	tmpl, _ := template.ParseGlob("../../../ui/templates/*.html")
	// Fallback if running from root
	if tmpl == nil {
		tmpl, _ = template.ParseGlob("ui/templates/*.html")
	}
	h := New(ms, tmpl)
	return h, ms
}

// Tests

func TestHandleCreateBin(t *testing.T) {
	h, ms := setupTest(t)

	// Happy Path
	form := url.Values{"name": {"B1"}, "controller_id": {"1"}, "segment_id": {"0"}, "led_index": {"0"}}
	req := httptest.NewRequest("POST", "/settings/bins", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.handleCreateBin(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// Duplicate Error (Specific)
	ms.CreateBinFunc = func(n string, c, s, l int) error { return store.ErrUniqueConstraint }
	req = httptest.NewRequest("POST", "/settings/bins", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	h.handleCreateBin(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("Duplicate: got %d", rr.Code)
	}

	// Reset func
	ms.CreateBinFunc = nil

	// DB Error (Generic)
	ms.FailOps = true
	req = httptest.NewRequest("POST", "/settings/bins", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	h.handleCreateBin(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}

	// Missing Data (Validation)
	ms.FailOps = false
	form = url.Values{"name": {""}}
	req = httptest.NewRequest("POST", "/settings/bins", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	h.handleCreateBin(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("Missing Data: got %d", rr.Code)
	}
}

func TestHandleCreateBinsBulk(t *testing.T) {
	h, ms := setupTest(t)

	// Happy
	form := url.Values{"controller_id": {"1"}, "segment_id": {"0"}, "led_count": {"10"}, "name_prefix": {"A-"}}
	req := httptest.NewRequest("POST", "/settings/bins/bulk", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.handleCreateBinsBulk(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// Bad Request (Missing count)
	form = url.Values{"controller_id": {"1"}}
	req = httptest.NewRequest("POST", "/settings/bins/bulk", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	h.handleCreateBinsBulk(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("Bad Request: got %d", rr.Code)
	}

	// DB Error
	ms.FailOps = true
	form = url.Values{"controller_id": {"1"}, "segment_id": {"0"}, "led_count": {"10"}, "name_prefix": {"A-"}}
	req = httptest.NewRequest("POST", "/settings/bins/bulk", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	h.handleCreateBinsBulk(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleUpdateBin(t *testing.T) {
	h, ms := setupTest(t)

	// Setup for Happy Path
	ms.GetBinByIDFunc = func(id int) (models.Bin, error) {
		return models.Bin{ID: 1, Name: "Old"}, nil
	}

	// Happy
	form := url.Values{"name": {"New"}, "controller_id": {"1"}}
	req := httptest.NewRequest("PUT", "/settings/bins/1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Need router for ID
	r := chi.NewRouter()
	r.Put("/settings/bins/{id}", h.handleUpdateBin)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// DB Error
	ms.FailOps = true
	// send valid data to pass validation check
	form = url.Values{"name": {"New"}, "controller_id": {"1"}}
	req = httptest.NewRequest("PUT", "/settings/bins/1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleDeleteBin(t *testing.T) {
	h, ms := setupTest(t)
	r := chi.NewRouter()
	r.Delete("/settings/bins/{id}", h.handleDeleteBin)

	// Happy
	req := httptest.NewRequest("DELETE", "/settings/bins/1", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// DB Error
	ms.FailOps = true
	req = httptest.NewRequest("DELETE", "/settings/bins/1", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleGetBinRows(t *testing.T) {
	h, ms := setupTest(t)
	r := chi.NewRouter()
	r.Get("/settings/bins/{id}", h.handleGetBinRow)
	r.Get("/settings/bins/{id}/edit", h.handleGetBinEditRow)

	ms.GetBinByIDFunc = func(id int) (models.Bin, error) { return models.Bin{Name: "Bin1"}, nil }

	// Display Row
	req := httptest.NewRequest("GET", "/settings/bins/1", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Display: got %d", rr.Code)
	}

	// Edit Row
	req = httptest.NewRequest("GET", "/settings/bins/1/edit", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Edit: got %d", rr.Code)
	}

	// Not Found
	ms.FailOps = true
	req = httptest.NewRequest("GET", "/settings/bins/1", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("Not Found: got %d", rr.Code)
	}
}

func TestHandleCreatePartLocation(t *testing.T) {
	h, ms := setupTest(t)

	// Happy
	form := url.Values{"part_id": {"1"}, "bin_id": {"2"}, "quantity": {"10"}}
	req := httptest.NewRequest("POST", "/part/locations", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.handleCreatePartLocation(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// DB Error
	ms.FailOps = true
	req = httptest.NewRequest("POST", "/part/locations", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	h.handleCreatePartLocation(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleUpdatePartLocation(t *testing.T) {
	h, ms := setupTest(t)
	r := chi.NewRouter()
	r.Put("/part/location/{loc_id}", h.handleUpdatePartLocation)

	ms.GetPartLocationByIDFunc = func(id int) (models.PartLocation, error) {
		return models.PartLocation{Quantity: 100}, nil
	}

	// Happy
	form := url.Values{"quantity": {"50"}}
	req := httptest.NewRequest("PUT", "/part/location/1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// DB Error
	ms.FailOps = true
	req = httptest.NewRequest("PUT", "/part/location/1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleDeletePartLocation(t *testing.T) {
	h, ms := setupTest(t)
	r := chi.NewRouter()
	r.Delete("/part/location/{loc_id}", h.handleDeletePartLocation)

	// Happy
	req := httptest.NewRequest("DELETE", "/part/location/1", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// DB Error
	ms.FailOps = true
	req = httptest.NewRequest("DELETE", "/part/location/1", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}
