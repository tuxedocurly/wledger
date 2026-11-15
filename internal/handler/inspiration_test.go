package handler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/inspiration"
	"github.com/tuxedocurly/wledger/internal/uierror"
)

func TestHandleInspirationGenerate_URLEncodedSpecialTags(t *testing.T) {
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	inspSvc := inspiration.NewService(s)
	h := &Handler{
		Logger:      logger,
		Queries:     s,
		Database:    dbConn,
		UIError:     uierror.New(logger),
		Inspiration: inspSvc,
	}

	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("InitSettings failed: %v", err)
	}
	tmpl, err := inspSvc.CreateTemplate(ctx, "Special Tags", "Use these parts:")
	if err != nil {
		t.Fatalf("CreateTemplate failed: %v", err)
	}

	contID, err := s.CreateContainer(ctx, db.CreateContainerParams{
		Name:       "Box",
		ConfigJson: sql.NullString{String: `{"rows":1,"cols":1}`, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	binID, err := s.CreateBin(ctx, db.CreateBinParams{Name: "B1", ContainerID: contID})
	if err != nil {
		t.Fatalf("CreateBin failed: %v", err)
	}

	partID, err := s.CreatePart(ctx, db.CreatePartParams{
		Name:       "STM32F4",
		PartNumber: sql.NullString{String: "STM32-01", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}
	if err := s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   partID,
		BinID:    sql.NullInt64{Int64: binID, Valid: true},
		Quantity: 5,
	}); err != nil {
		t.Fatalf("CreatePartAssignment failed: %v", err)
	}

	for _, name := range []string{"r&d", "c++", "#1", "3.3v/5v"} {
		tag, err := s.CreateTag(ctx, name)
		if err != nil {
			t.Fatalf("CreateTag %q failed: %v", name, err)
		}
		if err := s.AddTagToPart(ctx, db.AddTagToPartParams{PartID: partID, TagID: tag.ID}); err != nil {
			t.Fatalf("AddTagToPart %q failed: %v", name, err)
		}
	}

	router := chi.NewRouter()
	router.Get("/inspiration/{id}/generate", h.HandleInspirationGenerate)

	q := url.Values{}
	q.Set("tags", "r&d,c++,#1,3.3v/5v")
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/inspiration/%d/generate?%s", tmpl.ID, q.Encode()), nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "| 5 | STM32F4 | STM32-01 |") {
		t.Errorf("expected exact unmultiplied qty 5 for special-char tags, got:\n%s", body)
	}

	// Also verify repeated ?tags=... parameters decode cleanly
	qRepeated := url.Values{}
	qRepeated.Add("tags", "r&d")
	qRepeated.Add("tags", "3.3v/5v")
	req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/inspiration/%d/generate?%s", tmpl.ID, qRepeated.Encode()), nil)
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK || !strings.Contains(rr2.Body.String(), "| 5 | STM32F4 | STM32-01 |") {
		t.Errorf("expected repeated tags params to work cleanly, got %d:\n%s", rr2.Code, rr2.Body.String())
	}
}
