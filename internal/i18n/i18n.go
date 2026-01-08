package i18n

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/tuxedocurly/wledger/internal/config"
	"golang.org/x/text/language"
)

type contextKey string

const (
	localizerContextKey contextKey = "localizer"
	langContextKey      contextKey = "lang"
)

var bundle *i18n.Bundle

// Init initializes the i18n bundle by loading all translation files from the default locales directory.
func Init() error {
	return InitWithDir(config.DirLocales)
}

// InitWithDir initializes the i18n bundle by loading all translation files from the specified directory.
func InitWithDir(dir string) error {
	bundle = i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	// Load all translation files
	files, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read locales directory: %w", err)
	}

	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
			path := filepath.Join(dir, file.Name())
			_, err := bundle.LoadMessageFile(path)
			if err != nil {
				return fmt.Errorf("failed to load message file %s: %w", file.Name(), err)
			}
		}
	}

	return nil
}

// WithLocalizer returns a new context with the localizer attached.
func WithLocalizer(ctx context.Context, lang string) context.Context {
	localizer := i18n.NewLocalizer(bundle, lang, language.English.String())
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
	if bundle == nil {
		return nil
	}
	return i18n.NewLocalizer(bundle, language.English.String())
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