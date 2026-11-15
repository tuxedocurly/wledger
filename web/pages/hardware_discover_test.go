package pages

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/i18n"
)

func TestDiscoverResults_ButtonAndBadgeWrappingProtection(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatalf("i18n.Init failed: %v", err)
	}
	ctx := i18n.WithLocalizer(context.Background(), "en")

	items := []DiscoveryResultItem{
		{
			Name:     "WLED",
			IP:       "192.168.1.233",
			Port:     80,
			MAC:      "3c:8a:1f:e0:e8:24",
			LedCount: 106,
			Status:   "new",
		},
		{
			Name:           "Workbench WLED",
			IP:             "192.168.1.97",
			Port:           80,
			MAC:            "2c:bc:bb:da:8e:ec",
			LedCount:       128,
			Status:         "ip_updated",
			ControllerName: "Workbench",
			PreviousIP:     "192.168.1.50",
		},
		{
			Name:     "Shelf WLED",
			IP:       "192.168.1.44",
			Port:     80,
			MAC:      "aa:bb:cc:dd:ee:ff",
			LedCount: 64,
			Status:   "online",
		},
	}

	var buf bytes.Buffer
	if err := DiscoverResults(items).Render(ctx, &buf); err != nil {
		t.Fatalf("render DiscoverResults failed: %v", err)
	}
	html := buf.String()

	required := []string{
		`flex flex-col min-w-0 flex-1`,
		`<form action="/hardware" method="POST" class="shrink-0">`,
		`class="btn btn-sm btn-primary whitespace-nowrap">Add Controller</button>`,
		`class="btn btn-sm btn-ghost shrink-0 whitespace-nowrap">Refresh</a>`,
		`<span class="badge badge-info badge-xs shrink-0 whitespace-nowrap">New</span>`,
		`<span class="badge badge-warning badge-xs shrink-0 whitespace-nowrap">IP Auto-Healed</span>`,
		`<span class="badge badge-success badge-xs shrink-0 whitespace-nowrap">Configured</span>`,
		`192.168.1.233:80`,
		`3c:8a:1f:e0:e8:24`,
		`106 LEDs`,
	}
	for _, needle := range required {
		if !strings.Contains(html, needle) {
			t.Errorf("expected rendered DiscoverResults HTML to contain %q, got:\n%s", needle, html)
		}
	}

	containers := []db.GetAllContainersWithControllerRow{
		{
			ID:   7,
			Name: "Office Bins (Main Rack Overflow Container)",
		},
	}

	var pageBuf bytes.Buffer
	if err := Hardware(auth.User{ID: 1, Role: "admin"}, nil, containers).Render(ctx, &pageBuf); err != nil {
		t.Fatalf("render Hardware page failed: %v", err)
	}
	pageHTML := pageBuf.String()
	pageRequired := []string{
		`class="btn btn-xs btn-outline shrink-0 whitespace-nowrap"`,
		`class="min-w-0 flex-1"`,
		`class="font-bold text-base break-words"`,
		`class="badge badge-ghost badge-xs font-mono shrink-0 whitespace-nowrap"`,
		`class="btn btn-xs btn-ghost whitespace-nowrap"`,
	}
	for _, needle := range pageRequired {
		if !strings.Contains(pageHTML, needle) {
			t.Errorf("expected rendered Hardware page HTML to contain %q", needle)
		}
	}
}
