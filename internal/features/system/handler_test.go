package system

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"wledger/internal/models"
)

// Local mock
type mockStore struct {
	GetAllDataForBackupFunc       func() (models.BackupData, error)
	RestoreFromBackupFunc         func(data models.BackupData) error
	CleanupOrphanedCategoriesFunc func() error
}

func (m *mockStore) GetAllDataForBackup() (models.BackupData, error) {
	if m.GetAllDataForBackupFunc != nil {
		return m.GetAllDataForBackupFunc()
	}
	return models.BackupData{}, nil
}
func (m *mockStore) RestoreFromBackup(data models.BackupData) error {
	if m.RestoreFromBackupFunc != nil {
		return m.RestoreFromBackupFunc(data)
	}
	return nil
}
func (m *mockStore) CleanupOrphanedCategories() error {
	if m.CleanupOrphanedCategoriesFunc != nil {
		return m.CleanupOrphanedCategoriesFunc()
	}
	return nil
}

// Test Setup Helper
func setupTest(t *testing.T) (*Handler, *mockStore) {
	t.Helper()
	ms := &mockStore{}
	tempDir := t.TempDir()
	h := New(ms, tempDir)
	return h, ms
}

// Tests
func TestHandleCleanupCategories(t *testing.T) {
	h, ms := setupTest(t)

	called := false
	ms.CleanupOrphanedCategoriesFunc = func() error {
		called = true
		return nil
	}

	req := httptest.NewRequest("POST", "/settings/categories/cleanup", nil)
	rr := httptest.NewRecorder()

	h.handleCleanupCategories(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if !called {
		t.Error("CleanupOrphanedCategories was not called")
	}
}

func TestHandleDownloadBackup(t *testing.T) {
	h, ms := setupTest(t)

	ms.GetAllDataForBackupFunc = func() (models.BackupData, error) {
		return models.BackupData{Version: 1}, nil
	}

	req := httptest.NewRequest("GET", "/settings/backup/download", nil)
	rr := httptest.NewRecorder()

	h.handleDownloadBackup(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}
	if rr.Header().Get("Content-Type") != "application/zip" {
		t.Errorf("got content type %q, want application/zip", rr.Header().Get("Content-Type"))
	}
}
