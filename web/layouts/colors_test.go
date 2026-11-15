package layouts

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
)

func TestStockColors_SanitizationAndContext(t *testing.T) {
	defaults := DefaultStockColors()
	if defaults.Ok != "#00FF00" || defaults.Low != "#FFA500" || defaults.Critical != "#FF0000" || defaults.Locate != "#0000FF" {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}

	if got := SanitizeHexColor("#1a2B3c", "#000000"); got != "#1A2B3C" {
		t.Errorf("valid hex rejected: got %q", got)
	}

	invalidCases := []string{
		"",
		"#FFF",
		"00FF00",
		"#GGGGGG",
		"#00FF00; } body { display: none; }/*",
		"red",
	}
	for _, tc := range invalidCases {
		if got := SanitizeHexColor(tc, "#123456"); got != "#123456" {
			t.Errorf("SanitizeHexColor(%q) = %q, want fallback #123456", tc, got)
		}
	}

	ctx := WithStockColors(context.Background(), StockColors{
		Ok:       "#11AA22",
		Low:      "not-a-color",
		Critical: "#CC0011</style><script>",
		Locate:   "#0099FF",
	})
	got := GetStockColors(ctx)
	if got.Ok != "#11AA22" || got.Low != defaults.Low || got.Critical != defaults.Critical || got.Locate != "#0099FF" {
		t.Errorf("GetStockColors returned unexpected sanitized values: %+v", got)
	}
}

func TestBase_OfflineAssetsHTMXConfigAndDynamicColors(t *testing.T) {
	ctx := WithStockColors(context.Background(), StockColors{
		Ok:       "#22CC44",
		Low:      "#EEAA00",
		Critical: "#DD1122",
		Locate:   "#3366FF",
	})

	var buf bytes.Buffer
	if err := Base("Inventory Test", auth.User{}).Render(ctx, &buf); err != nil {
		t.Fatalf("Base.Render failed: %v", err)
	}
	html := buf.String()

	if strings.Contains(html, "unpkg.com") {
		t.Error("Base layout still contains unpkg.com CDN reference")
	}

	wantSubstrings := []string{
		`<script src="/static/vendor/htmx.min.js"></script>`,
		`<script src="/static/vendor/alpine.min.js" defer></script>`,
		`<script src="/static/vendor/Sortable.min.js"></script>`,
		`<script src="/static/vendor/html5-qrcode.min.js"></script>`,
		`name="htmx-config"`,
		`&#34;[45]..&#34;`,
		`&#34;swap&#34;:true`,
		`--color-stock-ok: #22CC44;`,
		`--color-stock-low: #EEAA00;`,
		`--color-stock-critical: #DD1122;`,
		`--color-locate: #3366FF;`,
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(html, sub) {
			t.Errorf("rendered Base HTML missing %q", sub)
		}
	}

	htmxIdx := strings.Index(html, "/static/vendor/htmx.min.js")
	alpineIdx := strings.Index(html, "/static/vendor/alpine.min.js")
	if htmxIdx == -1 || alpineIdx == -1 || htmxIdx > alpineIdx {
		t.Errorf("expected htmx.min.js before alpine.min.js (htmx=%d, alpine=%d)", htmxIdx, alpineIdx)
	}
}
