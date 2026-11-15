package layouts

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/a-h/templ"
)

type stockColorsContextKey struct{}

// StockColors holds the hex colors injected as CSS custom properties into :root.
type StockColors struct {
	Locate   string
	Ok       string
	Low      string
	Critical string
}

const (
	defaultColorLocate   = "#0000FF"
	defaultColorStockOk  = "#00FF00"
	defaultColorStockLow = "#FFA500"
	defaultColorCritical = "#FF0000"
)

var hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// DefaultStockColors returns the default locate and stock-level hex colors.
func DefaultStockColors() StockColors {
	return StockColors{
		Locate:   defaultColorLocate,
		Ok:       defaultColorStockOk,
		Low:      defaultColorStockLow,
		Critical: defaultColorCritical,
	}
}

// SanitizeHexColor validates a strict 6-digit hex color (#RRGGBB) to prevent CSS injection,
// returning fallback if the input is empty or invalid.
func SanitizeHexColor(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if hexColorPattern.MatchString(trimmed) {
		return strings.ToUpper(trimmed)
	}
	return fallback
}

// WithStockColors attaches sanitized StockColors to ctx.
func WithStockColors(ctx context.Context, colors StockColors) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	defaults := DefaultStockColors()
	sanitized := StockColors{
		Locate:   SanitizeHexColor(colors.Locate, defaults.Locate),
		Ok:       SanitizeHexColor(colors.Ok, defaults.Ok),
		Low:      SanitizeHexColor(colors.Low, defaults.Low),
		Critical: SanitizeHexColor(colors.Critical, defaults.Critical),
	}
	return context.WithValue(ctx, stockColorsContextKey{}, sanitized)
}

// GetStockColors retrieves sanitized StockColors from ctx, falling back to defaults.
func GetStockColors(ctx context.Context) StockColors {
	defaults := DefaultStockColors()
	if ctx == nil {
		return defaults
	}
	colors, ok := ctx.Value(stockColorsContextKey{}).(StockColors)
	if !ok {
		return defaults
	}
	return StockColors{
		Locate:   SanitizeHexColor(colors.Locate, defaults.Locate),
		Ok:       SanitizeHexColor(colors.Ok, defaults.Ok),
		Low:      SanitizeHexColor(colors.Low, defaults.Low),
		Critical: SanitizeHexColor(colors.Critical, defaults.Critical),
	}
}

// DynamicColorsStyle renders the :root CSS variables inside <style id="dynamic-colors">
// using strictly validated 6-digit hex strings.
func DynamicColorsStyle(ctx context.Context) templ.Component {
	colors := GetStockColors(ctx)
	return templ.ComponentFunc(func(renderCtx context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(
			w,
			`<style id="dynamic-colors">
            :root {
                --color-locate: %s;
                --color-stock-ok: %s;
                --color-stock-low: %s;
                --color-stock-critical: %s;
            }
            [x-cloak] { display: none !important; }
            .custom-scrollbar::-webkit-scrollbar { width: 4px; }
            .custom-scrollbar::-webkit-scrollbar-track { background: transparent; }
            .custom-scrollbar::-webkit-scrollbar-thumb { background: color-mix(in oklab, var(--color-base-content) 20%%, transparent); border-radius: 4px; }
            .custom-scrollbar::-webkit-scrollbar-thumb:hover { background: color-mix(in oklab, var(--color-base-content) 40%%, transparent); }

            /* SortableJS Classes */
            .sortable-ghost {
                opacity: 0.4;
                background: color-mix(in oklab, var(--color-primary) 10%%, transparent) !important;
                border: 2px dashed var(--color-primary) !important;
            }
            .sortable-chosen {
                background: var(--color-base-100) !important;
            }
            .sortable-drag {
                opacity: 0.9;
                cursor: grabbing !important;
                box-shadow: 0 10px 15px -3px rgb(0 0 0 / 0.1), 0 4px 6px -4px rgb(0 0 0 / 0.1) !important;
            }
            </style>`,
			colors.Locate,
			colors.Ok,
			colors.Low,
			colors.Critical,
		)
		return err
	})
}
