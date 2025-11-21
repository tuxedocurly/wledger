package settings

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"testing"

	"wledger/internal/models"
)

// Local mock
type mockStore struct {
	GetControllersFunc func() ([]models.WLEDController, error)
	GetBinsFunc        func() ([]models.Bin, error)
}

func (m *mockStore) GetControllers() ([]models.WLEDController, error) {
	if m.GetControllersFunc != nil {
		return m.GetControllersFunc()
	}
	return nil, nil
}
func (m *mockStore) GetBins() ([]models.Bin, error) {
	if m.GetBinsFunc != nil {
		return m.GetBinsFunc()
	}
	return nil, nil
}

// Test Setup Helper
func setupTest(t *testing.T) (*Handler, *mockStore) {
	t.Helper()
	ms := &mockStore{}
	tmpl, _ := template.ParseGlob("../../../ui/templates/*.html")
	h := New(ms, tmpl)
	return h, ms
}

func TestHandleShowSettings(t *testing.T) {
	h, ms := setupTest(t)

	// Mock data
	ms.GetControllersFunc = func() ([]models.WLEDController, error) {
		return []models.WLEDController{{Name: "Test Ctrl"}}, nil
	}
	ms.GetBinsFunc = func() ([]models.Bin, error) {
		return []models.Bin{{Name: "Test Bin"}}, nil
	}

	req := httptest.NewRequest("GET", "/settings", nil)
	rr := httptest.NewRecorder()

	h.handleShowSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}
}
