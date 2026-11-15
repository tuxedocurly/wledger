package i18n

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

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
	t.Cleanup(func() {
		_ = Init()
	})

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
	localesDir := filepath.Join(repoRoot(t), "locales")

	testBundle := i18n.NewBundle(language.English)
	testBundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	checks := []struct {
		lang string
		want string
	}{
		{lang: "zh", want: "仪表板"},
		{lang: "pt-BR", want: "Painel"},
		{lang: "it", want: "Dashboard"},
		{lang: "de", want: "Dashboard"},
		{lang: "fr", want: "Tableau de bord"},
		{lang: "ru", want: "Панель управления"},
	}

	for _, tc := range checks {
		file := filepath.Join(localesDir, "active."+tc.lang+".json")
		if _, err := os.Stat(file); os.IsNotExist(err) {
			t.Fatalf("active.%s.json does not exist", tc.lang)
		}

		if _, err := testBundle.LoadMessageFile(file); err != nil {
			t.Fatalf("Failed to load active.%s.json: %v", tc.lang, err)
		}

		localizer := i18n.NewLocalizer(testBundle, tc.lang)
		msg, err := localizer.Localize(&i18n.LocalizeConfig{MessageID: "Dashboard"})
		if err != nil {
			t.Fatalf("Failed to localize Dashboard in %s: %v", tc.lang, err)
		}
		if msg != tc.want {
			t.Errorf("%s Dashboard: expected %q, got %q", tc.lang, tc.want, msg)
		}
	}
}

type localeEntry struct {
	Description string `json:"description,omitempty"`
	Other       string `json:"other"`
}

func loadLocaleMap(t *testing.T, path string) map[string]localeEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}

	var entries map[string]localeEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("invalid JSON in %s: %v", path, err)
	}
	if len(entries) == 0 {
		t.Fatalf("locale file %s contains no entries", path)
	}
	return entries
}

var templateVarRe = regexp.MustCompile(`\{\{[^}]+\}\}`)

func extractTemplateVars(s string) string {
	vars := templateVarRe.FindAllString(s, -1)
	sort.Strings(vars)
	return strings.Join(vars, ",")
}

func TestAllLocalesParity(t *testing.T) {
	localesDir := filepath.Join(repoRoot(t), "locales")
	expectedLangs := []string{"de", "en", "es", "fr", "it", "pt-BR", "ru", "zh"}

	matches, err := filepath.Glob(filepath.Join(localesDir, "active.*.json"))
	if err != nil {
		t.Fatalf("glob locales failed: %v", err)
	}

	var foundLangs []string
	for _, m := range matches {
		base := filepath.Base(m)
		lang := strings.TrimSuffix(strings.TrimPrefix(base, "active."), ".json")
		foundLangs = append(foundLangs, lang)
	}
	sort.Strings(foundLangs)

	if strings.Join(foundLangs, ",") != strings.Join(expectedLangs, ",") {
		t.Fatalf("locale files mismatch: got %v, want %v", foundLangs, expectedLangs)
	}

	bundle := i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	allLocales := make(map[string]map[string]localeEntry, len(expectedLangs))
	for _, lang := range expectedLangs {
		path := filepath.Join(localesDir, "active."+lang+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if _, err := bundle.ParseMessageFileBytes(data, filepath.Base(path)); err != nil {
			t.Fatalf("go-i18n failed to parse %s: %v", path, err)
		}
		allLocales[lang] = loadLocaleMap(t, path)
	}

	enEntries := allLocales["en"]
	for _, lang := range expectedLangs {
		entries := allLocales[lang]

		var missing []string
		for key := range enEntries {
			entry, ok := entries[key]
			if !ok {
				missing = append(missing, key)
				continue
			}
			if strings.TrimSpace(entry.Other) == "" {
				t.Errorf("locale %s key %q has empty 'other' translation", lang, key)
			}
			if gotVars, wantVars := extractTemplateVars(entry.Other), extractTemplateVars(enEntries[key].Other); gotVars != wantVars {
				t.Errorf("locale %s key %q template vars = [%s], want [%s]", lang, key, gotVars, wantVars)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("locale %s is missing %d keys present in en: %v", lang, len(missing), missing)
		}

		var extra []string
		for key := range entries {
			if _, ok := enEntries[key]; !ok {
				extra = append(extra, key)
			}
		}
		sort.Strings(extra)
		if len(extra) > 0 {
			t.Errorf("locale %s has %d extra keys absent from en: %v", lang, len(extra), extra)
		}
	}

	requiredWave4Keys := []string{
		"Projects", "NavProjects", "APITokens", "StandaloneContainers",
		"DiscoverWLED", "ParseBarcode", "CreateToken", "RevokeToken",
		"BuildProject", "LocateAll", "PickMode", "ImportBOM",
		"Shortage", "TokenCreatedWarning",
	}
	for _, key := range requiredWave4Keys {
		if _, ok := enEntries[key]; !ok {
			t.Errorf("missing pre-populated Wave 3/4 key %q in active.en.json", key)
		}
	}
}

var i18nLiteralCallRe = regexp.MustCompile(`(?s)\bi18n\.TD?\s*\(\s*[^,]+,\s*("(?:[^"\\]|\\.)*")`)

func TestAllCodeKeysPresentInEnglish(t *testing.T) {
	root := repoRoot(t)
	enEntries := loadLocaleMap(t, filepath.Join(root, "locales", "active.en.json"))

	scanDirs := []string{
		filepath.Join(root, "web"),
		filepath.Join(root, "internal"),
	}

	referenced := make(map[string][]string)
	for _, dir := range scanDirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".templ") {
				return nil
			}
			if strings.HasSuffix(name, "_templ.go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			relPath, _ := filepath.Rel(root, path)
			matches := i18nLiteralCallRe.FindAllStringSubmatch(string(raw), -1)
			for _, m := range matches {
				key, err := strconv.Unquote(m[1])
				if err != nil {
					t.Errorf("%s: failed to unquote key literal %s: %v", relPath, m[1], err)
					continue
				}
				referenced[key] = append(referenced[key], relPath)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("failed scanning %s: %v", dir, err)
		}
	}

	if len(referenced) == 0 {
		t.Fatal("no i18n.T / i18n.TD calls found during code scan")
	}

	var missing []string
	for key := range referenced {
		if _, ok := enEntries[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		t.Errorf("i18n key %q referenced in %v is missing from locales/active.en.json", key, referenced[key])
	}

	// Verify dynamic role keys passed to i18n.T(ctx, user.Role) also exist in active.en.json.
	for _, role := range []string{"admin", "editor", "viewer"} {
		if _, ok := enEntries[role]; !ok {
			t.Errorf("dynamic role key %q missing from locales/active.en.json", role)
		}
	}
}
