package parts

import (
	"bytes"
	"html/template"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"wledger/internal/models"
)

// Local mock
type mockStore struct {
	GetPartsFunc            func() ([]models.Part, error)
	DeletePartFunc          func(id int) error
	CreatePartFunc          func(p *models.Part) error
	GetPartByIDFunc         func(id int) (models.Part, error)
	UpdatePartFunc          func(p *models.Part) error
	UpdatePartImagePathFunc func(partID int, imagePath string) error
	SearchPartsFunc         func(searchTerm string) ([]models.Part, error)

	GetPartLocationsFunc       func(partID int) ([]models.PartLocation, error)
	GetAvailableBinsFunc       func(partID int) ([]models.Bin, error)
	GetURLsByPartIDFunc        func(partID int) ([]models.PartURL, error)
	GetDocumentsByPartIDFunc   func(partID int) ([]models.PartDocument, error)
	GetCategoriesByPartIDFunc  func(partID int) ([]models.Category, error)
	GetCategoriesFunc          func() ([]models.Category, error)
	CreatePartURLFunc          func(partID int, url string, description string) error
	DeletePartURLFunc          func(urlID int) error
	CreatePartDocumentFunc     func(doc *models.PartDocument) error
	GetDocumentByIDFunc        func(docID int) (models.PartDocument, error)
	DeletePartDocumentFunc     func(docID int) error
	CreateCategoryFunc         func(name string) (models.Category, error)
	AssignCategoryToPartFunc   func(partID int, categoryID int) error
	RemoveCategoryFromPartFunc func(partID int, categoryID int) error

	// Unused stubs
	GetBinLocationCountFunc       func(partID int) (int, error)
	CleanupOrphanedCategoriesFunc func() error
}

// Implementations
func (m *mockStore) GetParts() ([]models.Part, error) {
	if m.GetPartsFunc != nil {
		return m.GetPartsFunc()
	}
	return nil, nil
}
func (m *mockStore) DeletePart(id int) error {
	if m.DeletePartFunc != nil {
		return m.DeletePartFunc(id)
	}
	return nil
}
func (m *mockStore) CreatePart(p *models.Part) error {
	if m.CreatePartFunc != nil {
		return m.CreatePartFunc(p)
	}
	return nil
}
func (m *mockStore) GetPartByID(id int) (models.Part, error) {
	if m.GetPartByIDFunc != nil {
		return m.GetPartByIDFunc(id)
	}
	return models.Part{}, nil
}
func (m *mockStore) UpdatePart(p *models.Part) error {
	if m.UpdatePartFunc != nil {
		return m.UpdatePartFunc(p)
	}
	return nil
}
func (m *mockStore) UpdatePartImagePath(partID int, imagePath string) error {
	if m.UpdatePartImagePathFunc != nil {
		return m.UpdatePartImagePathFunc(partID, imagePath)
	}
	return nil
}
func (m *mockStore) SearchParts(searchTerm string) ([]models.Part, error) {
	if m.SearchPartsFunc != nil {
		return m.SearchPartsFunc(searchTerm)
	}
	return nil, nil
}
func (m *mockStore) GetPartLocations(partID int) ([]models.PartLocation, error) {
	if m.GetPartLocationsFunc != nil {
		return m.GetPartLocationsFunc(partID)
	}
	return nil, nil
}
func (m *mockStore) GetAvailableBins(partID int) ([]models.Bin, error) {
	if m.GetAvailableBinsFunc != nil {
		return m.GetAvailableBinsFunc(partID)
	}
	return nil, nil
}
func (m *mockStore) GetURLsByPartID(partID int) ([]models.PartURL, error) {
	if m.GetURLsByPartIDFunc != nil {
		return m.GetURLsByPartIDFunc(partID)
	}
	return nil, nil
}
func (m *mockStore) GetDocumentsByPartID(partID int) ([]models.PartDocument, error) {
	if m.GetDocumentsByPartIDFunc != nil {
		return m.GetDocumentsByPartIDFunc(partID)
	}
	return nil, nil
}
func (m *mockStore) GetCategoriesByPartID(partID int) ([]models.Category, error) {
	if m.GetCategoriesByPartIDFunc != nil {
		return m.GetCategoriesByPartIDFunc(partID)
	}
	return nil, nil
}
func (m *mockStore) GetCategories() ([]models.Category, error) {
	if m.GetCategoriesFunc != nil {
		return m.GetCategoriesFunc()
	}
	return nil, nil
}
func (m *mockStore) CreatePartURL(partID int, url string, description string) error {
	if m.CreatePartURLFunc != nil {
		return m.CreatePartURLFunc(partID, url, description)
	}
	return nil
}
func (m *mockStore) DeletePartURL(urlID int) error {
	if m.DeletePartURLFunc != nil {
		return m.DeletePartURLFunc(urlID)
	}
	return nil
}
func (m *mockStore) CreatePartDocument(doc *models.PartDocument) error {
	if m.CreatePartDocumentFunc != nil {
		return m.CreatePartDocumentFunc(doc)
	}
	return nil
}
func (m *mockStore) GetDocumentByID(docID int) (models.PartDocument, error) {
	if m.GetDocumentByIDFunc != nil {
		return m.GetDocumentByIDFunc(docID)
	}
	return models.PartDocument{}, nil
}
func (m *mockStore) DeletePartDocument(docID int) error {
	if m.DeletePartDocumentFunc != nil {
		return m.DeletePartDocumentFunc(docID)
	}
	return nil
}
func (m *mockStore) CreateCategory(name string) (models.Category, error) {
	if m.CreateCategoryFunc != nil {
		return m.CreateCategoryFunc(name)
	}
	return models.Category{}, nil
}
func (m *mockStore) AssignCategoryToPart(partID int, categoryID int) error {
	if m.AssignCategoryToPartFunc != nil {
		return m.AssignCategoryToPartFunc(partID, categoryID)
	}
	return nil
}
func (m *mockStore) RemoveCategoryFromPart(partID int, categoryID int) error {
	if m.RemoveCategoryFromPartFunc != nil {
		return m.RemoveCategoryFromPartFunc(partID, categoryID)
	}
	return nil
}

// Unused stubs (required by interface)
func (m *mockStore) CleanupOrphanedCategories() error            { return nil }
func (m *mockStore) GetBinLocationCount(partID int) (int, error) { return 0, nil }

// Test setup helper
func setupTest(t *testing.T) (*Handler, *mockStore) {
	t.Helper()
	ms := &mockStore{}

	tmpl, _ := template.ParseGlob("../../../ui/templates/*.html")
	if tmpl == nil {
		tmpl, _ = template.ParseGlob("ui/templates/*.html")
	}

	tempDir := t.TempDir()

	h := New(ms, tmpl, tempDir)
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

func TestHandleCreatePart(t *testing.T) {
	h, ms := setupTest(t)

	called := false
	ms.CreatePartFunc = func(p *models.Part) error {
		called = true
		if p.Name != "New Part" {
			t.Errorf("expected name 'New Part', got %s", p.Name)
		}
		if p.StockTracking != true {
			t.Error("expected stock tracking to be true")
		}
		return nil
	}

	// Simulate Form Data
	form := url.Values{}
	form.Set("name", "New Part")
	form.Set("stock_tracking_enabled", "on")
	form.Set("min_stock", "5")

	req := httptest.NewRequest("POST", "/parts", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.handleCreatePart(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got status %d, want 303", rr.Code)
	}
	if !called {
		t.Error("CreatePart was not called")
	}
}

func TestHandleUpdatePartDetails(t *testing.T) {
	h, ms := setupTest(t)

	// Mock GetPartByID (Required before update)
	ms.GetPartByIDFunc = func(id int) (models.Part, error) {
		return models.Part{ID: 1, Name: "Old Name"}, nil
	}

	// Mock UpdatePart
	updated := false
	ms.UpdatePartFunc = func(p *models.Part) error {
		updated = true
		if p.Name != "New Name" {
			t.Errorf("expected name 'New Name', got %s", p.Name)
		}
		return nil
	}

	form := url.Values{}
	form.Set("name", "New Name")

	req := httptest.NewRequest("POST", "/part/1/details", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	// Wrap in router for URL param extraction
	r := chi.NewRouter()
	r.Post("/part/{id}/details", h.handleUpdatePartDetails)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got status %d, want 303", rr.Code)
	}
	if !updated {
		t.Error("UpdatePart was not called")
	}
}

func TestHandlePartImageUpload(t *testing.T) {
	h, ms := setupTest(t)

	// Mock GetPartByID
	ms.GetPartByIDFunc = func(id int) (models.Part, error) {
		return models.Part{ID: 1, Name: "Part"}, nil
	}

	// Mock UpdatePartImagePath
	pathUpdated := false
	ms.UpdatePartImagePathFunc = func(id int, path string) error {
		pathUpdated = true
		if !strings.Contains(path, ".png") {
			t.Errorf("expected png extension, got %s", path)
		}
		return nil
	}

	// Create Multipart Form Request
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file field
	part, _ := writer.CreateFormFile("part_image", "test.png")
	// Write a fake PNG header so http.DetectContentType works
	part.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})

	writer.Close()

	req := httptest.NewRequest("POST", "/part/1/image/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	// Run Handler via Router
	r := chi.NewRouter()
	r.Post("/part/{id}/image/upload", h.handlePartImageUpload)
	r.ServeHTTP(rr, req)

	// Assert
	if rr.Code != http.StatusSeeOther {
		t.Errorf("got status %d, want 303", rr.Code)
	}
	if !pathUpdated {
		t.Error("UpdatePartImagePath was not called")
	}
}

func TestHandleSearchParts(t *testing.T) {
	h, ms := setupTest(t)
	ms.SearchPartsFunc = func(searchTerm string) ([]models.Part, error) {
		return []models.Part{{Name: "Search Result"}}, nil
	}

	req := httptest.NewRequest("POST", "/parts/search", strings.NewReader("search=test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.handleSearchParts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Search Result") {
		t.Error("Response missing search result")
	}
}

func TestHandleShowPartDetails(t *testing.T) {
	h, ms := setupTest(t)

	// Mock ALL the data calls required for this page
	ms.GetPartByIDFunc = func(id int) (models.Part, error) { return models.Part{Name: "Detail Part"}, nil }
	ms.GetPartLocationsFunc = func(id int) ([]models.PartLocation, error) { return nil, nil }
	ms.GetAvailableBinsFunc = func(id int) ([]models.Bin, error) { return nil, nil }
	ms.GetURLsByPartIDFunc = func(id int) ([]models.PartURL, error) { return nil, nil }
	ms.GetDocumentsByPartIDFunc = func(id int) ([]models.PartDocument, error) { return nil, nil }
	ms.GetCategoriesByPartIDFunc = func(id int) ([]models.Category, error) { return nil, nil }
	ms.GetCategoriesFunc = func() ([]models.Category, error) { return nil, nil }

	req := httptest.NewRequest("GET", "/part/1", nil)
	rr := httptest.NewRecorder()

	// Wrap in router for URL param
	r := chi.NewRouter()
	r.Get("/part/{id}", h.handleShowPartDetails)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Detail Part") {
		t.Error("Response missing part name")
	}
}

func TestHandleAddPartURL(t *testing.T) {
	h, ms := setupTest(t)
	called := false
	ms.CreatePartURLFunc = func(pid int, url, desc string) error {
		called = true
		return nil
	}

	form := url.Values{}
	form.Set("part_id", "1")
	form.Set("url", "http://example.com")

	req := httptest.NewRequest("POST", "/part/urls", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.handleAddPartURL(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got status %d, want 303", rr.Code)
	}
	if !called {
		t.Error("CreatePartURL not called")
	}
}

func TestHandleDeletePartURL(t *testing.T) {
	h, ms := setupTest(t)
	called := false
	ms.DeletePartURLFunc = func(id int) error {
		called = true
		return nil
	}

	req := httptest.NewRequest("DELETE", "/part/urls/1", nil)
	rr := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Delete("/part/urls/{url_id}", h.handleDeletePartURL)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rr.Code)
	}
	if !called {
		t.Error("DeletePartURL not called")
	}
}

func TestHandleAssignCategory(t *testing.T) {
	h, ms := setupTest(t)

	ms.CreateCategoryFunc = func(name string) (models.Category, error) {
		return models.Category{ID: 5, Name: name}, nil
	}

	assigned := false
	ms.AssignCategoryToPartFunc = func(pid, cid int) error {
		if pid == 1 && cid == 5 {
			assigned = true
		}
		return nil
	}

	form := url.Values{}
	form.Set("part_id", "1")
	form.Set("category_name", "NewTag")

	req := httptest.NewRequest("POST", "/part/categories", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.handleAssignCategoryToPart(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got status %d, want 303", rr.Code)
	}
	if !assigned {
		t.Error("AssignCategoryToPart not called correctly")
	}
}

func TestHandleRemoveCategory(t *testing.T) {
	h, ms := setupTest(t)
	called := false
	ms.RemoveCategoryFromPartFunc = func(pid, cid int) error {
		called = true
		return nil
	}

	req := httptest.NewRequest("DELETE", "/part/1/categories/5", nil)
	rr := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Delete("/part/{part_id}/categories/{cat_id}", h.handleRemoveCategoryFromPart)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rr.Code)
	}
	if !called {
		t.Error("RemoveCategoryFromPart not called")
	}
}

func TestHandleUploadDocument(t *testing.T) {
	h, ms := setupTest(t)
	called := false
	ms.CreatePartDocumentFunc = func(doc *models.PartDocument) error {
		called = true
		if doc.PartID != 1 || doc.Filename != "test.txt" {
			t.Error("Document struct mismatch")
		}
		return nil
	}

	// Create Multipart form with a file
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("part_document", "test.txt")
	part.Write([]byte("test content"))
	writer.Close()

	req := httptest.NewRequest("POST", "/part/1/document/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	r.Post("/part/{id}/document/upload", h.handleUploadDocument)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got status %d, want 303", rr.Code)
	}
	if !called {
		t.Error("CreatePartDocument not called")
	}
}

func TestHandleDownloadDocument(t *testing.T) {
	h, ms := setupTest(t)
	// Create a dummy file to serve
	docsDir := filepath.Join(h.uploadDir, "documents")
	os.MkdirAll(docsDir, 0755)
	os.WriteFile(filepath.Join(docsDir, "testfile.txt"), []byte("hello"), 0644)

	ms.GetDocumentByIDFunc = func(id int) (models.PartDocument, error) {
		return models.PartDocument{
			Filename: "real.txt",
			Filepath: "documents/testfile.txt",
			Mimetype: "text/plain",
		}, nil
	}

	req := httptest.NewRequest("GET", "/part/document/1", nil)
	rr := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Get("/part/document/{doc_id}", h.handleDownloadDocument)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rr.Code)
	}
	if rr.Body.String() != "hello" {
		t.Error("File content mismatch")
	}
}

func TestHandleDeleteDocument(t *testing.T) {
	h, ms := setupTest(t)
	// Create a dummy file to delete
	docsDir := filepath.Join(h.uploadDir, "documents")
	os.MkdirAll(docsDir, 0755)
	os.WriteFile(filepath.Join(docsDir, "testfile.txt"), []byte("hello"), 0644)

	// Mock GetDocumentByID to return our dummy file path
	ms.GetDocumentByIDFunc = func(id int) (models.PartDocument, error) {
		return models.PartDocument{
			ID:       1,
			Filepath: "documents/delete_me.txt",
		}, nil
	}

	// Mock DeletePartDocument
	deleted := false
	ms.DeletePartDocumentFunc = func(id int) error {
		deleted = true
		return nil
	}

	req := httptest.NewRequest("DELETE", "/part/document/1", nil)
	rr := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Delete("/part/document/{doc_id}", h.handleDeleteDocument)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rr.Code)
	}
	if !deleted {
		t.Error("DeletePartDocument was not called")
	}

	// Verify file is gone
	if _, err := os.Stat("data/uploads/documents/delete_me.txt"); !os.IsNotExist(err) {
		t.Error("File was not deleted from filesystem")
	}
}
