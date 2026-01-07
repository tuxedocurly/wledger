package i18n

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestI18n(t *testing.T) {
	// Create a temporary locales directory
	tmpLocales, err := os.MkdirTemp("", "locales")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpLocales)

	enFile := filepath.Join(tmpLocales, "active.en.json")
	enContent := `{
		"TestMessage": {
			"other": "Test English"
		}
	}`
	if err := os.WriteFile(enFile, []byte(enContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	esFile := filepath.Join(tmpLocales, "active.es.json")
	esContent := `{
		"TestMessage": {
			"other": "Test Spanish"
		}
	}`
	if err := os.WriteFile(esFile, []byte(esContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if err := InitWithDir(tmpLocales); err != nil {
		t.Fatalf("InitWithDir failed: %v", err)
	}

	ctx := context.Background()

	t.Run("Default English", func(t *testing.T) {
		msg := T(ctx, "TestMessage")
		if msg != "Test English" {
			t.Errorf("expected 'Test English', got '%s'", msg)
		}
	})

	t.Run("Spanish Context", func(t *testing.T) {
		ctxEs := WithLocalizer(ctx, "es")
		msg := T(ctxEs, "TestMessage")
		if msg != "Test Spanish" {
			t.Errorf("expected 'Test Spanish', got '%s'", msg)
		}
	})

	t.Run("Fallback to English", func(t *testing.T) {
		ctxFr := WithLocalizer(ctx, "fr")
		msg := T(ctxFr, "TestMessage")
		if msg != "Test English" {
			t.Errorf("expected 'Test English' (fallback), got '%s'", msg)
		}
	})

	t.Run("Non-existent ID", func(t *testing.T) {
		msg := T(ctx, "NonExistent")
		if msg != "NonExistent" {
			t.Errorf("expected ID itself for non-existent message, got '%s'", msg)
		}
	})
}