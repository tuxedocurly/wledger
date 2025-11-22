// internal/features/dashboard/handler_test.go
package dashboard

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
)

// Local Mocks
type mockStore struct {
	FailOps bool

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
	GetPartsFunc func() ([]models.Part, error) // Not used here anymore, but safe to keep stubbed if interface requires it? No, interface doesn't require it here anymore.
}

func (m *mockStore) GetDashboardBinData() ([]models.DashboardBinData, error) {
	if m.FailOps {
		return nil, errors.New("db error")
	}
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
	if m.FailOps {
		return nil, errors.New("db error")
	}
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
	if m.FailOps {
		return nil, errors.New("db error")
	}
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
	if m.FailOps {
		return nil, errors.New("db error")
	}
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

// Setup
func setupTest(t *testing.T) (*Handler, *mockStore, *mockWLED) {
	t.Helper()
	ms := &mockStore{}
	mw := &mockWLED{}

	tmpl, err := template.ParseGlob("../../../ui/templates/*.html")
	if err != nil {
		tmpl, err = template.ParseGlob("ui/templates/*.html")
		if err != nil {
			t.Fatalf("Failed to parse templates: %v", err)
		}
	}

	h := New(ms, mw, tmpl)
	return h, ms, mw
}

// Tests

func TestHandleShowDashboard(t *testing.T) {
	h, _, _ := setupTest(t)
	req := httptest.NewRequest("GET", "/dashboard", nil)
	rr := httptest.NewRecorder()

	h.handleShowDashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rr.Code)
	}
}

func TestHandleShowStockStatus(t *testing.T) {
	h, ms, _ := setupTest(t)

	// Setup data: 3 bins (Red, Yellow, Green)
	ms.GetDashboardBinDataFunc = func() ([]models.DashboardBinData, error) {
		return []models.DashboardBinData{
			{BinQuantity: 0, MinStock: 5, ReorderPoint: 10, BinIP: "1.1.1.1", BinSegmentID: 0, BinLEDIndex: 0},  // Red
			{BinQuantity: 8, MinStock: 5, ReorderPoint: 10, BinIP: "1.1.1.1", BinSegmentID: 0, BinLEDIndex: 1},  // Yellow
			{BinQuantity: 50, MinStock: 5, ReorderPoint: 10, BinIP: "1.1.1.1", BinSegmentID: 0, BinLEDIndex: 2}, // Green
		}, nil
	}

	// Test Level "all"
	form := url.Values{"level": {"all"}}
	req := httptest.NewRequest("POST", "/api/v1/stock-status", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.handleShowStockStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Lit 3 bins") {
		t.Errorf("Expected 3 lit, got response: %s", rr.Body.String())
	}

	// Test Level "critical" (Should only light 1)
	form = url.Values{"level": {"critical"}}
	req = httptest.NewRequest("POST", "/api/v1/stock-status", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()

	h.handleShowStockStatus(rr, req)
	if !strings.Contains(rr.Body.String(), "Lit 1 bins") {
		t.Errorf("Expected 1 lit (Critical), got response: %s", rr.Body.String())
	}

	// DB Error Path
	ms.FailOps = true
	req = httptest.NewRequest("POST", "/api/v1/stock-status", nil)
	rr = httptest.NewRecorder()
	h.handleShowStockStatus(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleStopAll(t *testing.T) {
	h, ms, _ := setupTest(t)
	ms.GetAllBinLocationsForStopAllFunc = func() ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error) {
		return []struct {
			IP       string
			SegID    int
			LEDIndex int
		}{{IP: "1.1", SegID: 0, LEDIndex: 0}}, nil
	}

	req := httptest.NewRequest("POST", "/api/v1/stop-all", nil)
	rr := httptest.NewRecorder()

	h.handleStopAll(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d", rr.Code)
	}

	// Error
	ms.FailOps = true
	rr = httptest.NewRecorder()
	h.handleStopAll(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleLocatePart(t *testing.T) {
	h, ms, mw := setupTest(t)

	// Setup common mocks
	ms.GetPartLocationsForLocateFunc = func(id int) ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error) {
		return []struct {
			IP       string
			SegID    int
			LEDIndex int
		}{{IP: "1.1", SegID: 0, LEDIndex: 0}}, nil
	}

	// happy path
	mw.SendCommandFunc = func(ip string, s models.WLEDState) error { return nil }

	req := httptest.NewRequest("POST", "/locate/part/1", nil)
	rr := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Post("/locate/part/{id}", h.handleLocatePart)

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// Offline (WLED Error)
	mw.SendCommandFunc = func(ip string, s models.WLEDState) error { return errors.New("offline") }

	// Create a FRESH request/recorder
	req = httptest.NewRequest("POST", "/locate/part/1", nil)
	rr = httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	// Assert we got the START button back
	if !strings.Contains(rr.Body.String(), `hx-post="/locate/part/1"`) {
		t.Errorf("Did not return start button on failure. Got: %s", rr.Body.String())
	}

	// DB Error
	ms.FailOps = true
	req = httptest.NewRequest("POST", "/locate/part/1", nil)
	rr = httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleStopLocate(t *testing.T) {
	h, ms, _ := setupTest(t)

	ms.GetPartLocationsForStopFunc = func(id int) ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error) {
		return []struct {
			IP       string
			SegID    int
			LEDIndex int
		}{{IP: "1.1", SegID: 0, LEDIndex: 0}}, nil
	}

	req := httptest.NewRequest("POST", "/locate/stop/1", nil)
	rr := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Post("/locate/stop/{id}", h.handleStopLocate)
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Happy: got %d", rr.Code)
	}

	// DB Error
	ms.FailOps = true
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("DB Error: got %d", rr.Code)
	}
}

func TestHandleGetLocateButton(t *testing.T) {
	h, _, _ := setupTest(t)
	req := httptest.NewRequest("GET", "/locate/button/1", nil)
	rr := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Get("/locate/button/{id}", h.handleGetLocateButton)
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d", rr.Code)
	}
}
