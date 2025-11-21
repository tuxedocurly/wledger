package parts

import (
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
	GetPartsFunc   func() ([]models.Part, error)
	DeletePartFunc func(id int) error
	// TODO: This mock could probably be generated automatically.
	// For now, implement empty stubs for the methods not being tested yet
}

// Implement Interface (partial implementation for now)
func (m *mockStore) GetParts() ([]models.Part, error) { return m.GetPartsFunc() }
func (m *mockStore) DeletePart(id int) error          { return m.DeletePartFunc(id) }

// Stubs to satisfy the interface
func (m *mockStore) GetPartByID(id int) (models.Part, error)                        { return models.Part{}, nil }
func (m *mockStore) SearchParts(searchTerm string) ([]models.Part, error)           { return nil, nil }
func (m *mockStore) CreatePart(p *models.Part) error                                { return nil }
func (m *mockStore) UpdatePart(p *models.Part) error                                { return nil }
func (m *mockStore) UpdatePartImagePath(partID int, imagePath string) error         { return nil }
func (m *mockStore) GetBinLocationCount(partID int) (int, error)                    { return 0, nil }
func (m *mockStore) GetPartLocations(partID int) ([]models.PartLocation, error)     { return nil, nil }
func (m *mockStore) GetAvailableBins(partID int) ([]models.Bin, error)              { return nil, nil }
func (m *mockStore) GetURLsByPartID(partID int) ([]models.PartURL, error)           { return nil, nil }
func (m *mockStore) CreatePartURL(partID int, url string, description string) error { return nil }
func (m *mockStore) DeletePartURL(urlID int) error                                  { return nil }
func (m *mockStore) GetDocumentsByPartID(partID int) ([]models.PartDocument, error) { return nil, nil }
func (m *mockStore) GetDocumentByID(docID int) (models.PartDocument, error) {
	return models.PartDocument{}, nil
}
func (m *mockStore) CreatePartDocument(doc *models.PartDocument) error           { return nil }
func (m *mockStore) DeletePartDocument(docID int) error                          { return nil }
func (m *mockStore) GetCategories() ([]models.Category, error)                   { return nil, nil }
func (m *mockStore) GetCategoriesByPartID(partID int) ([]models.Category, error) { return nil, nil }
func (m *mockStore) CreateCategory(name string) (models.Category, error) {
	return models.Category{}, nil
}
func (m *mockStore) AssignCategoryToPart(partID int, categoryID int) error   { return nil }
func (m *mockStore) RemoveCategoryFromPart(partID int, categoryID int) error { return nil }

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

func TestHandleShowParts(t *testing.T) {
	h, ms := setupTest(t)

	ms.GetPartsFunc = func() ([]models.Part, error) {
		return []models.Part{{Name: "Fake Part", TotalQuantity: 10}}, nil
	}

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	h.handleShowParts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "Fake Part") {
		t.Errorf("response body does not contain 'Fake Part'")
	}
}

func TestHandleDeletePart(t *testing.T) {
	h, ms := setupTest(t)
	var deletedID int

	ms.DeletePartFunc = func(id int) error {
		deletedID = id
		return nil
	}

	req := httptest.NewRequest("DELETE", "/parts/5", nil)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	r.Delete("/parts/{id}", h.handleDeletePart)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}
	if deletedID != 5 {
		t.Errorf("handler deleted ID %d, want %d", deletedID, 5)
	}
}
