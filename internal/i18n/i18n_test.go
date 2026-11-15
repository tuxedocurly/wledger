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

func TestInitEmbedded_AfterChdirToTempDir(t *testing.T) {
	t.Cleanup(func() {
		_ = Init()
	})

	t.Chdir(t.TempDir())

	if err := Init(); err != nil {
		t.Fatalf("Init() after Chdir to empty temp dir failed: %v", err)
	}

	cases := []struct {
		lang string
		key  string
		want string
	}{
		{lang: "en", key: "Dashboard", want: "Dashboard"},
		{lang: "en", key: "DashboardStatus", want: "Visual inventory status."},
		{lang: "zh", key: "Dashboard", want: "仪表板"},
		{lang: "pt-BR", key: "Dashboard", want: "Painel"},
		{lang: "de", key: "Dashboard", want: "Dashboard"},
		{lang: "fr", key: "Dashboard", want: "Tableau de bord"},
		{lang: "ru", key: "Dashboard", want: "Панель управления"},
		{lang: "it", key: "Dashboard", want: "Dashboard"},
	}

	for _, tc := range cases {
		ctx := WithLocalizer(context.Background(), tc.lang)
		if got := T(ctx, tc.key); got != tc.want {
			t.Errorf("T(%q, %q) = %q, want %q", tc.lang, tc.key, got, tc.want)
		}
	}
}

func TestInitWithFS_Nil(t *testing.T) {
	if err := InitWithFS(nil); err == nil {
		t.Fatal("expected InitWithFS(nil) to return an error, got nil")
	}
}
