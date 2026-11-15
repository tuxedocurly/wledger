package pages

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/i18n"
)

func TestPartCards_ConsistentHeightAndClamping(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatalf("i18n.Init failed: %v", err)
	}
	ctx := i18n.WithLocalizer(context.Background(), "en")

	longName := strings.Repeat("SuperLongComponentNameWithoutSpaces", 4)
	longMPN := strings.Repeat("GRM188R61C106MA73D-", 4) + "END"
	longDesc := "Line 1 description\nLine 2 description\nLine 3 overflow description that exceeds two lines easily"

	sampleParts := []PartView{
		{
			ID:         1,
			Name:       "10k Resistor",
			TotalStock: 5,
			ValidStock: 5,
		},
		{
			ID:            2,
			Name:          longName,
			Description:   sql.NullString{String: longDesc, Valid: true},
			PartNumber:    sql.NullString{String: longMPN, Valid: true},
			IsFavorite:    sql.NullBool{Bool: true, Valid: true},
			TotalStock:    42,
			ValidStock:    40,
			OrphanedStock: 2,
		},
	}

	var listBuf bytes.Buffer
	if err := PartsList(auth.User{Role: "admin"}, sampleParts, "", 1, nil).Render(ctx, &listBuf); err != nil {
		t.Fatalf("render PartsList failed: %v", err)
	}
	listHTML := listBuf.String()
	if !strings.Contains(listHTML, `id="parts-grid"`) || !strings.Contains(listHTML, `items-stretch`) {
		t.Errorf("expected parts-grid to stretch row heights with items-stretch")
	}

	var cardsBuf bytes.Buffer
	if err := PartCards(sampleParts, "", 1, nil).Render(ctx, &cardsBuf); err != nil {
		t.Fatalf("render PartCards failed: %v", err)
	}
	html := cardsBuf.String()

	required := []string{
		`flex flex-col h-full`,
		`h-48 shrink-0`,
		`flex-1 flex flex-col min-w-0`,
		`min-h-[3rem]`,
		`line-clamp-2 break-words min-w-0`,
		`title="` + longName + `"`,
		`line-clamp-2 min-h-[2.5rem] break-words`,
		`mt-auto pt-3`,
		`truncate block h-5`,
		`title="` + longMPN + `"`,
		`# ` + longMPN,
	}
	for _, needle := range required {
		if !strings.Contains(html, needle) {
			t.Errorf("expected rendered part cards to contain %q", needle)
		}
	}

	// Empty PartNumber should not render a dangling "# " prefix.
	if strings.Contains(html, `># </span>`) {
		t.Errorf("expected empty PartNumber not to render dangling '# ' prefix")
	}
}
