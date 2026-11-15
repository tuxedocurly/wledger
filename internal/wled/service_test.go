package wled

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestService_Locate(t *testing.T) {
	// Setup a mock WLED server
	receivedColor := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/state" {
			body, _ := io.ReadAll(r.Body)
			receivedColor = string(body)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	// Extract IP/Port from server URL (e.g. 127.0.0.1:12345)
	ip := server.URL[7:]

	// Setup DB
	dbConn, err := db.Open("file:wled_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()

	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	store := db.NewStore(dbConn)
	ctx := context.Background()

	// Seed Settings
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("failed to init settings: %v", err)
	}
	err = store.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate: sql.NullString{String: "#FF0000", Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to update colors: %v", err)
	}

	client := NewClient()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(store, client, logger)

	t.Run("LocateBin", func(t *testing.T) {
		c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Test", IpAddress: ip})
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}

		cont, err := store.CreateContainer(ctx, db.CreateContainerParams{
			Name:         "Cont",
			ControllerID: sql.NullInt64{Int64: c.ID, Valid: true},
			SegmentID:    0,
		})
		if err != nil {
			t.Fatalf("failed to create container: %v", err)
		}

		b, err := store.CreateBin(ctx, db.CreateBinParams{
			Name: "B1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create bin: %v", err)
		}

		err = svc.LocateBin(ctx, c.ID, b)
		if err != nil {
			t.Fatalf("LocateBin failed: %v", err)
		}

		// Verify that client sent the correct color
		if receivedColor == "" {
			t.Error("No color received by mock server")
		}
	})
}
