package dashboard

import (
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"wledger/internal/models"
)

// Local mock
type mockStore struct {
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

func (m *mockStore) GetDashboardBinData() ([]models.DashboardBinData, error) {
	if m.GetDashboardBinDataFunc != nil {
		return m.GetDashboardBinDataFunc()
	}
	return nil, nil
}
func (m *mockStore) GetPartLocationsForLocate(id int) ([]struct {
	IP       string
	SegID    int
	LEDIndex int
}, error) {
	if m.GetPartLocationsForLocateFunc != nil {
		return m.GetPartLocationsForLocateFunc(id)
	}
	return nil, nil
}
func (m *mockStore) GetPartLocationsForStop(id int) ([]struct {
	IP       string
	SegID    int
	LEDIndex int
}, error) {
	if m.GetPartLocationsForStopFunc != nil {
		return m.GetPartLocationsForStopFunc(id)
	}
	return nil, nil
}
func (m *mockStore) GetAllBinLocationsForStopAll() ([]struct {
	IP       string
	SegID    int
	LEDIndex int
}, error) {
	if m.GetAllBinLocationsForStopAllFunc != nil {
		return m.GetAllBinLocationsForStopAllFunc()
	}
	return nil, nil
}

type mockWLED struct {
	SendCommandFunc func(ipAddress string, state models.WLEDState) error
}

func (m *mockWLED) SendCommand(ip string, state models.WLEDState) error {
	if m.SendCommandFunc != nil {
		return m.SendCommandFunc(ip, state)
	}
	return nil
}

// Test setup helper
func setupTest(t *testing.T) (*Handler, *mockStore, *mockWLED) {
	t.Helper()
	ms := &mockStore{}
	mw := &mockWLED{}
	tmpl, _ := template.ParseGlob("../../../ui/templates/*.html")
	h := New(ms, mw, tmpl)
	return h, ms, mw
}

// Tests

func TestHandleLocatePart_Offline(t *testing.T) {
	h, ms, mw := setupTest(t)

	ms.GetPartLocationsForLocateFunc = func(partID int) ([]struct {
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
	mw.SendCommandFunc = func(ipAddress string, state models.WLEDState) error {
		return errors.New("timeout: controller is offline")
	}

	req := httptest.NewRequest("POST", "/locate/part/1", nil)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	r.Post("/locate/part/{id}", h.handleLocatePart)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), `hx-post="/locate/part/1"`) {
		t.Errorf("response body does not contain the 'Locate' (start) button")
	}
}
