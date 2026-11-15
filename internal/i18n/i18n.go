package i18n

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sync"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/tuxedocurly/wledger/locales"
	"golang.org/x/text/language"
)

type contextKey string

const (
	localizerContextKey contextKey = "localizer"
	langContextKey      contextKey = "lang"
)

var (
	bundleMu sync.RWMutex
	bundle   *i18n.Bundle
)

// Init initializes the i18n bundle from the embedded LocaleFS so binaries and tests
// work regardless of working directory.
func Init() error {
	return InitWithFS(locales.LocaleFS)
}

// InitWithFS initializes the i18n bundle by loading all JSON translation files
// from the root of the provided fs.FS.
func InitWithFS(fsys fs.FS) error {
	if fsys == nil {
		return errors.New("locales filesystem must not be nil")
	}

	newBundle := i18n.NewBundle(language.English)
	newBundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("failed to read locales directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && path.Ext(entry.Name()) == ".json" {
			data, err := fs.ReadFile(fsys, entry.Name())
			if err != nil {
				return fmt.Errorf("failed to read message file %s: %w", entry.Name(), err)
			}
			if _, err := newBundle.ParseMessageFileBytes(data, entry.Name()); err != nil {
				return fmt.Errorf("failed to load message file %s: %w", entry.Name(), err)
			}
		}
	}

	bundleMu.Lock()
	bundle = newBundle
	bundleMu.Unlock()

	return nil
}

// InitWithDir initializes the i18n bundle by loading all translation files from the specified directory.
func InitWithDir(dir string) error {
	return InitWithFS(os.DirFS(dir))
}

// WithLocalizer returns a new context with the localizer attached.
func WithLocalizer(ctx context.Context, lang string) context.Context {
	bundleMu.RLock()
	b := bundle
	bundleMu.RUnlock()

	localizer := i18n.NewLocalizer(b, lang, language.English.String())
	ctx = context.WithValue(ctx, localizerContextKey, localizer)
	return context.WithValue(ctx, langContextKey, lang)
}

// GetLocalizer retrieves the localizer from the context.
func GetLocalizer(ctx context.Context) *i18n.Localizer {
	val := ctx.Value(localizerContextKey)
	if localizer, ok := val.(*i18n.Localizer); ok {
		return localizer
	}
	// Fallback to English localizer if none found in context
	bundleMu.RLock()
	b := bundle
	bundleMu.RUnlock()
	if b == nil {
		return nil
	}
	return i18n.NewLocalizer(b, language.English.String())
}

// GetLanguage retrieves the current language tag from the context.
func GetLanguage(ctx context.Context) string {
	val := ctx.Value(langContextKey)
	if lang, ok := val.(string); ok && lang != "" {
		return lang
	}
	return "en"
}

// T translates a message by its ID.
func T(ctx context.Context, messageID string) string {
	localizer := GetLocalizer(ctx)
	if localizer == nil {
		return messageID
	}
	msg, err := localizer.Localize(&i18n.LocalizeConfig{
		MessageID: messageID,
	})
	if err != nil {
		return messageID
	}
	return msg
}

// TD translates a message with data for template variables.
func TD(ctx context.Context, messageID string, templateData interface{}) string {
	localizer := GetLocalizer(ctx)
	if localizer == nil {
		return messageID
	}
	msg, err := localizer.Localize(&i18n.LocalizeConfig{
		MessageID:    messageID,
		TemplateData: templateData,
	})
	if err != nil {
		return messageID
	}
	return msg
}
