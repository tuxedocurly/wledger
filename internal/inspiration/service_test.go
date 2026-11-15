package inspiration

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestConstructPrompt_MultiTagExactQuantitiesAndSpecialChars(t *testing.T) {
	s, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("InitSettings failed: %v", err)
	}

	svc := NewService(s)
	tmpl, err := svc.CreateTemplate(ctx, "Filtered Test", "Build something with:")
	if err != nil {
		t.Fatalf("CreateTemplate failed: %v", err)
	}

	// Container & bins so part_assignments reference valid bins
	contID, err := s.CreateContainer(ctx, db.CreateContainerParams{
		Name:   "Shelf",
		ConfigJson: sql.NullString{String: `{"rows":1,"cols":2}`, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	bin1ID, err := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: contID})
	if err != nil {
		t.Fatalf("CreateBin 1 failed: %v", err)
	}
	bin2ID, err := s.CreateBin(ctx, db.CreateBinParams{Name: "A2", ContainerID: contID})
	if err != nil {
		t.Fatalf("CreateBin 2 failed: %v", err)
	}

	// Part 1 in two bins (3 + 7 = 10), tagged with multiple matching tags including special characters
	p1ID, err := s.CreatePart(ctx, db.CreatePartParams{
		Name:       "ESP32-S3",
		PartNumber: sql.NullString{String: "ESP32-100", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart 1 failed: %v", err)
	}
	if err := s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   p1ID,
		BinID:    sql.NullInt64{Int64: bin1ID, Valid: true},
		Quantity: 3,
	}); err != nil {
		t.Fatalf("AssignPart 1a failed: %v", err)
	}
	if err := s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   p1ID,
		BinID:    sql.NullInt64{Int64: bin2ID, Valid: true},
		Quantity: 7,
	}); err != nil {
		t.Fatalf("AssignPart 1b failed: %v", err)
	}

	// Part 2 in one bin (qty = 6), tagged with special-character tags
	p2ID, err := s.CreatePart(ctx, db.CreatePartParams{
		Name:       "Level Shifter",
		PartNumber: sql.NullString{String: "TXS0108E", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart 2 failed: %v", err)
	}
	if err := s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   p2ID,
		BinID:    sql.NullInt64{Int64: bin1ID, Valid: true},
		Quantity: 6,
	}); err != nil {
		t.Fatalf("AssignPart 2 failed: %v", err)
	}

	// Part 3 excluded by filter (tag = "mechanical")
	p3ID, err := s.CreatePart(ctx, db.CreatePartParams{
		Name:       "M3 Screw",
		PartNumber: sql.NullString{String: "SCR-M3", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart 3 failed: %v", err)
	}
	if err := s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   p3ID,
		BinID:    sql.NullInt64{Int64: bin2ID, Valid: true},
		Quantity: 99,
	}); err != nil {
		t.Fatalf("AssignPart 3 failed: %v", err)
	}

	// Create tags including &, +, #, /
	tagNames := []string{"r&d", "c++", "#1", "3.3v/5v", "mechanical"}
	tagIDs := make(map[string]int64)
	for _, name := range tagNames {
		tag, err := s.CreateTag(ctx, name)
		if err != nil {
			t.Fatalf("CreateTag %q failed: %v", name, err)
		}
		tagIDs[name] = tag.ID
	}

	// Attach THREE matching tags to Part 1 to prove quantity is NOT multiplied by 3!
	for _, name := range []string{"r&d", "c++", "#1"} {
		if err := s.AddTagToPart(ctx, db.AddTagToPartParams{PartID: p1ID, TagID: tagIDs[name]}); err != nil {
			t.Fatalf("AddTagToPart p1 %q failed: %v", name, err)
		}
	}
	// Attach TWO matching tags to Part 2
	for _, name := range []string{"3.3v/5v", "#1"} {
		if err := s.AddTagToPart(ctx, db.AddTagToPartParams{PartID: p2ID, TagID: tagIDs[name]}); err != nil {
			t.Fatalf("AddTagToPart p2 %q failed: %v", name, err)
		}
	}
	if err := s.AddTagToPart(ctx, db.AddTagToPartParams{PartID: p3ID, TagID: tagIDs["mechanical"]}); err != nil {
		t.Fatalf("AddTagToPart p3 failed: %v", err)
	}

	prompt, err := svc.ConstructPrompt(ctx, tmpl.ID, []string{"r&d", " c++ ", "#1", "3.3v/5v", "", "r&d"})
	if err != nil {
		t.Fatalf("ConstructPrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "| 10 | ESP32-S3 | ESP32-100 |") {
		t.Errorf("expected unmultiplied qty 10 for ESP32-S3 in prompt, got:\n%s", prompt)
	}
	if strings.Count(prompt, "ESP32-S3") != 1 {
		t.Errorf("expected ESP32-S3 once in prompt, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "| 6 | Level Shifter | TXS0108E |") {
		t.Errorf("expected exact qty 6 for Level Shifter in prompt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "M3 Screw") {
		t.Errorf("expected excluded part M3 Screw omitted from filtered prompt, got:\n%s", prompt)
	}

	// Whitespace-only tag filter falls back to all in-stock parts
	allPrompt, err := svc.ConstructPrompt(ctx, tmpl.ID, []string{"   ", ""})
	if err != nil {
		t.Fatalf("ConstructPrompt empty filter fallback failed: %v", err)
	}
	if !strings.Contains(allPrompt, "| 99 | M3 Screw | SCR-M3 |") {
		t.Errorf("expected empty tag filter to return all parts including M3 Screw, got:\n%s", allPrompt)
	}
}
