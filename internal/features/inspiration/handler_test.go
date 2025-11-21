package inspiration

import (
	"database/sql"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wledger/internal/models"
)

// Local Mock
type mockStore struct {
	GetPartsFunc func() ([]models.Part, error)
}

func (m *mockStore) GetParts() ([]models.Part, error) {
	if m.GetPartsFunc != nil {
		return m.GetPartsFunc()
	}
	return nil, nil
}

// Test setup helper
func setupTest(t *testing.T) (*Handler, *mockStore) {
	t.Helper()
	ms := &mockStore{}
	tmpl, _ := template.ParseGlob("../../../ui/templates/*.html")
	h := New(ms, tmpl)
	return h, ms
}

func TestHandleShowInspiration(t *testing.T) {
	h, ms := setupTest(t)

	ms.GetPartsFunc = func() ([]models.Part, error) {
		return []models.Part{
			{Name: "Test Resistor", TotalQuantity: 50, PartNumber: sql.NullString{String: "R-100", Valid: true}},
			{Name: "Test Capacitor", TotalQuantity: 0},
		}, nil
	}

	req := httptest.NewRequest("GET", "/inspiration", nil)
	rr := httptest.NewRecorder()

	h.handleShowInspiration(rr, req)

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
