package components

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/i18n"
)

func TestDashboardContainerCard_ModalScrollableLayout(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatalf("i18n.Init failed: %v", err)
	}
	ctx := i18n.WithLocalizer(context.Background(), "en")

	var bins []DashboardBin
	var id int64 = 1
	for y := 0; y < 16; y++ {
		for x := 0; x < 12; x++ {
			bins = append(bins, DashboardBin{
				ID:    id,
				Name:  fmt.Sprintf("B%02d", id),
				GridX: x,
				GridY: y,
			})
			id++
		}
	}

	container := DashboardContainer{
		ID:               42,
		Name:             "Tall Storage Rack",
		ControllerName:   "Rack WLED",
		ControllerOnline: true,
		Bins:             bins,
	}

	var buf bytes.Buffer
	if err := DashboardContainerCard(container).Render(ctx, &buf); err != nil {
		t.Fatalf("render DashboardContainerCard failed: %v", err)
	}
	html := buf.String()

	required := []string{
		`id="container_modal_42"`,
		`@click.stop`,
		`max-h-[85vh] flex flex-col`,
		`flex-col flex-1 min-h-0`,
		`overflow-auto flex-1 min-h-0`,
		`tabindex="0"`,
		`flex-wrap gap-4`,
	}
	for _, needle := range required {
		if !strings.Contains(html, needle) {
			t.Errorf("expected rendered container modal to contain %q", needle)
		}
	}
}
