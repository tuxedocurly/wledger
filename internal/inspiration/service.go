package inspiration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/tuxedocurly/wledger/internal/db"
)

type Service interface {
	ConstructPrompt(ctx context.Context, templateID int64, tagFilters []string) (string, error)
	GetAllTemplates(ctx context.Context) ([]db.InspirationTemplate, error)
	GetTemplate(ctx context.Context, id int64) (db.InspirationTemplate, error)
	CreateTemplate(ctx context.Context, title, content string) (db.InspirationTemplate, error)
	UpdateTemplate(ctx context.Context, id int64, title, content string) error
	DeleteTemplate(ctx context.Context, id int64) error
	SeedTemplates(ctx context.Context) error
}

type service struct {
	store db.Store
}

func NewService(store db.Store) Service {
	return &service{
		store: store,
	}
}

func (s *service) GetAllTemplates(ctx context.Context) ([]db.InspirationTemplate, error) {
	return s.store.GetAllInspirationTemplates(ctx)
}

func (s *service) GetTemplate(ctx context.Context, id int64) (db.InspirationTemplate, error) {
	return s.store.GetInspirationTemplate(ctx, id)
}

func (s *service) CreateTemplate(ctx context.Context, title, content string) (db.InspirationTemplate, error) {
	return s.store.CreateInspirationTemplate(ctx, db.CreateInspirationTemplateParams{
		Title:           title,
		TemplateContent: content,
	})
}

func (s *service) UpdateTemplate(ctx context.Context, id int64, title, content string) error {
	return s.store.UpdateInspirationTemplate(ctx, db.UpdateInspirationTemplateParams{
		Title:           title,
		TemplateContent: content,
		ID:              id,
	})
}

func (s *service) DeleteTemplate(ctx context.Context, id int64) error {
	return s.store.DeleteInspirationTemplate(ctx, id)
}

type inspirationPartRow struct {
	Name          string
	PartNumber    sql.NullString
	TotalQuantity int64
}

func normalizeTagFilters(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	cleaned := make([]string, 0, len(tags))
	for _, t := range tags {
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		cleaned = append(cleaned, trimmed)
	}
	return cleaned
}

func (s *service) ConstructPrompt(ctx context.Context, templateID int64, tagFilters []string) (string, error) {
	// Fetch template
	tmpl, err := s.store.GetInspirationTemplate(ctx, templateID)
	if err != nil {
		return "", fmt.Errorf("failed to get template: %w", err)
	}

	cleanedTags := normalizeTagFilters(tagFilters)
	var parts []inspirationPartRow

	if len(cleanedTags) > 0 {
		rows, err := s.store.GetPartsForInspirationFiltered(ctx, cleanedTags)
		if err != nil {
			return "", fmt.Errorf("failed to fetch filtered parts: %w", err)
		}
		parts = make([]inspirationPartRow, len(rows))
		for i, r := range rows {
			parts[i] = inspirationPartRow{r.Name, r.PartNumber, r.TotalQuantity}
		}
	} else {
		rows, err := s.store.GetPartsForInspirationAll(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to fetch all parts: %w", err)
		}
		parts = make([]inspirationPartRow, len(rows))
		for i, r := range rows {
			parts[i] = inspirationPartRow{r.Name, r.PartNumber, r.TotalQuantity}
		}
	}

	// Format into a markdown table
	var sb strings.Builder
	sb.WriteString(tmpl.TemplateContent)
	sb.WriteString("\n\n| Qty | Name | Part # |\n")
	sb.WriteString("| --- | --- | --- |\n")

	for _, p := range parts {
		pn := ""
		if p.PartNumber.Valid {
			pn = p.PartNumber.String
		}
		// Escape pipes in name or part number to avoid breaking table
		cleanName := strings.ReplaceAll(p.Name, "|", "\\|")
		cleanPN := strings.ReplaceAll(pn, "|", "\\|")

		sb.WriteString(fmt.Sprintf("| %d | %s | %s |\n", p.TotalQuantity, cleanName, cleanPN))
	}

	return sb.String(), nil
}
