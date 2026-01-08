package i18n

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

func TestNewLanguages(t *testing.T) {
	// Create a temporary locales directory
	tmpLocales, err := os.MkdirTemp("", "locales_new")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpLocales)

	enFile := filepath.Join(tmpLocales, "active.en.json")
	enContent := `{
		"Dashboard": {
			"other": "Dashboard"
		}
	}`
	if err := os.WriteFile(enFile, []byte(enContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	languages := []string{"zh", "pt-BR", "it", "de", "fr", "ru"}
	for _, lang := range languages {
		content := `{
			"Dashboard": {
				"other": "Dashboard-` + lang + `"
			}
		}`
		file := filepath.Join(tmpLocales, "active."+lang+".json")
		if err := os.WriteFile(file, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write test file for %s: %v", lang, err)
		}
	}

	if err := InitWithDir(tmpLocales); err != nil {
		t.Fatalf("InitWithDir failed: %v", err)
	}

	ctx := context.Background()

	for _, lang := range languages {
		t.Run("Language "+lang, func(t *testing.T) {
			ctxLang := WithLocalizer(ctx, lang)
			msg := T(ctxLang, "Dashboard")
			expected := "Dashboard-" + lang
			if msg != expected {
				t.Errorf("expected '%s', got '%s'", expected, msg)
			}
		})
	}
}

func TestRealLocaleFiles(t *testing.T) {
	// Point to the actual project locales directory
	localesDir := "../../locales"

	// Create a new bundle for this test
	testBundle := i18n.NewBundle(language.English)
	testBundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	// Try to load the new language file
	file := filepath.Join(localesDir, "active.zh.json")
	if _, err := os.Stat(file); os.IsNotExist(err) {
		t.Fatalf("active.zh.json does not exist")
	}

	_, err := testBundle.LoadMessageFile(file)
	if err != nil {
		t.Fatalf("Failed to load active.zh.json: %v", err)
	}

	// Simple check to ensure a key exists (e.g., "Dashboard")
	localizer := i18n.NewLocalizer(testBundle, "zh")
	msg, err := localizer.Localize(&i18n.LocalizeConfig{
		MessageID: "Dashboard",
	})
	if err != nil {
		t.Fatalf("Failed to localize Dashboard in zh: %v", err)
	}

	if msg != "仪表板" {
		t.Errorf("Expected '仪表板', got '%s'", msg)
	}

	// Check for Portuguese (Brazil)
	filePT := filepath.Join(localesDir, "active.pt-BR.json")
	if _, err := os.Stat(filePT); os.IsNotExist(err) {
		t.Fatalf("active.pt-BR.json does not exist")
	}

	_, err = testBundle.LoadMessageFile(filePT)
	if err != nil {
		t.Fatalf("Failed to load active.pt-BR.json: %v", err)
	}

	localizerPT := i18n.NewLocalizer(testBundle, "pt-BR")
	msgPT, err := localizerPT.Localize(&i18n.LocalizeConfig{
		MessageID: "Dashboard",
	})
	if err != nil {
		t.Fatalf("Failed to localize Dashboard in pt-BR: %v", err)
	}
	if msgPT != "Painel" {
		t.Errorf("Expected 'Painel', got '%s'", msgPT)
	}

	// Check for Italian
	fileIT := filepath.Join(localesDir, "active.it.json")
	if _, err := os.Stat(fileIT); os.IsNotExist(err) {
		t.Fatalf("active.it.json does not exist")
	}

	_, err = testBundle.LoadMessageFile(fileIT)
	if err != nil {
		t.Fatalf("Failed to load active.it.json: %v", err)
	}

	localizerIT := i18n.NewLocalizer(testBundle, "it")
	msgIT, err := localizerIT.Localize(&i18n.LocalizeConfig{
		MessageID: "Dashboard",
	})
	if err != nil {
		t.Fatalf("Failed to localize Dashboard in it: %v", err)
	}
	if msgIT != "Dashboard" {
		t.Errorf("Expected 'Dashboard', got '%s'", msgIT)
	}

	// Check for German
	fileDE := filepath.Join(localesDir, "active.de.json")
	if _, err := os.Stat(fileDE); os.IsNotExist(err) {
		t.Fatalf("active.de.json does not exist")
	}

	_, err = testBundle.LoadMessageFile(fileDE)
	if err != nil {
		t.Fatalf("Failed to load active.de.json: %v", err)
	}

	localizerDE := i18n.NewLocalizer(testBundle, "de")
	msgDE, err := localizerDE.Localize(&i18n.LocalizeConfig{
		MessageID: "Dashboard",
	})
	if err != nil {
		t.Fatalf("Failed to localize Dashboard in de: %v", err)
	}
	if msgDE != "Dashboard" {
		t.Errorf("Expected 'Dashboard', got '%s'", msgDE)
	}

	// Check for French
	fileFR := filepath.Join(localesDir, "active.fr.json")
	if _, err := os.Stat(fileFR); os.IsNotExist(err) {
		t.Fatalf("active.fr.json does not exist")
	}

	_, err = testBundle.LoadMessageFile(fileFR)
	if err != nil {
		t.Fatalf("Failed to load active.fr.json: %v", err)
	}

	localizerFR := i18n.NewLocalizer(testBundle, "fr")
	msgFR, err := localizerFR.Localize(&i18n.LocalizeConfig{
		MessageID: "Dashboard",
	})
	if err != nil {
		t.Fatalf("Failed to localize Dashboard in fr: %v", err)
	}
	if msgFR != "Tableau de bord" {
		t.Errorf("Expected 'Tableau de bord', got '%s'", msgFR)
	}

	// Check for Russian
	fileRU := filepath.Join(localesDir, "active.ru.json")
	if _, err := os.Stat(fileRU); os.IsNotExist(err) {
		t.Fatalf("active.ru.json does not exist")
	}

	_, err = testBundle.LoadMessageFile(fileRU)
	if err != nil {
		t.Fatalf("Failed to load active.ru.json: %v", err)
	}

	localizerRU := i18n.NewLocalizer(testBundle, "ru")
	msgRU, err := localizerRU.Localize(&i18n.LocalizeConfig{
		MessageID: "Dashboard",
	})
	if err != nil {
		t.Fatalf("Failed to localize Dashboard in ru: %v", err)
	}
	if msgRU != "Панель управления" {
		t.Errorf("Expected 'Панель управления', got '%s'", msgRU)
	}
}
