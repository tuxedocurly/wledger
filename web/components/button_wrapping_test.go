package components

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/i18n"
)

func TestAPITokensAndGridPainter_WrappingProtection(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatalf("i18n.Init failed: %v", err)
	}
	ctx := i18n.WithLocalizer(context.Background(), "fr")

	tokens := []db.ListAPITokensByUserRow{
		{
			ID:          1,
			Name:        "Home Assistant Integration",
			TokenPrefix: "wld_1234",
			Scopes:      "read",
			ExpiresAt:   sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true},
		},
	}

	var tokBuf bytes.Buffer
	if err := APITokens(tokens, "").Render(ctx, &tokBuf); err != nil {
		t.Fatalf("render APITokens failed: %v", err)
	}
	tokHTML := tokBuf.String()
	tokRequired := []string{
		`sm:grid-cols-2 xl:grid-cols-4`,
		`class="btn btn-primary btn-sm w-full gap-1.5 whitespace-nowrap"`,
		`class="badge badge-xs badge-error ml-1 whitespace-nowrap"`,
		`class="text-right whitespace-nowrap"`,
		`class="inline-block shrink-0"`,
		`class="btn btn-xs btn-outline btn-error gap-1 whitespace-nowrap"`,
	}
	for _, needle := range tokRequired {
		if !strings.Contains(tokHTML, needle) {
			t.Errorf("expected APITokens HTML to contain %q", needle)
		}
	}

	var gridBuf bytes.Buffer
	if err := GridPainter(db.Controller{ID: 1, Name: "Ctrl"}, nil, nil, true).Render(ctx, &gridBuf); err != nil {
		t.Fatalf("render GridPainter failed: %v", err)
	}
	gridHTML := gridBuf.String()
	gridRequired := []string{
		`class="min-w-0"`,
		`shrink-0`,
		`sm:w-auto sm:min-w-32 whitespace-nowrap`,
		`md:min-w-48 whitespace-nowrap`,
	}
	for _, needle := range gridRequired {
		if !strings.Contains(gridHTML, needle) {
			t.Errorf("expected GridPainter HTML to contain %q", needle)
		}
	}
	if strings.Contains(gridHTML, `md:w-48 whitespace-nowrap`) {
		t.Errorf("expected GridPainter Save button not to use rigid md:w-48")
	}
}

func TestOutputCSS_ButtonAndBadgeHeightGuards(t *testing.T) {
	raw, err := os.ReadFile("../static/css/output.css")
	if err != nil {
		t.Fatalf("read ../static/css/output.css failed: %v", err)
	}
	css := string(raw)

	required := []string{
		`.btn:not(.btn-circle):not(.btn-square)`,
		`.badge:not(:empty)`,
		`height:auto;min-height:var(--size)`,
	}
	for _, needle := range required {
		if !strings.Contains(css, needle) {
			t.Errorf("expected output.css to contain %q", needle)
		}
	}
}
