package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestOpen_PragmasConfigured(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pragmas.db")
	dbConn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	var fk int
	if err := dbConn.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys failed: %v", err)
	}
	if fk != 1 {
		t.Errorf("expected foreign_keys=1, got %d", fk)
	}

	var busyTimeout int
	if err := dbConn.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout failed: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("expected busy_timeout=5000, got %d", busyTimeout)
	}

	var journalMode string
	if err := dbConn.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode failed: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Errorf("expected journal_mode=wal, got %q", journalMode)
	}

	var synchronous int
	if err := dbConn.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("PRAGMA synchronous failed: %v", err)
	}
	if synchronous != 1 {
		t.Errorf("expected synchronous=1 (NORMAL), got %d", synchronous)
	}
}

func TestConcurrentExecTx_NoSQLITEBUSY(t *testing.T) {
	cases := []struct {
		name string
		dsn  func(t *testing.T) string
	}{
		{
			name: "FileWAL",
			dsn: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "stress.db")
			},
		},
		{
			name: "InMemory",
			dsn: func(t *testing.T) string {
				return "file::memory:?cache=shared"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbConn, err := Open(tc.dsn(t))
			if err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			defer dbConn.Close()

			if err := Migrate(dbConn); err != nil {
				t.Fatalf("Migrate failed: %v", err)
			}

			store := NewStore(dbConn)
			ctx := context.Background()

			ctrl, err := store.CreateController(ctx, CreateControllerParams{
				Name:      "StressCtrl",
				IpAddress: "127.0.0.1",
			})
			if err != nil {
				t.Fatalf("CreateController failed: %v", err)
			}
			contID, err := store.CreateContainer(ctx, CreateContainerParams{
				Name:         "StressCont",
				ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
				SegmentID:    0,
			})
			if err != nil {
				t.Fatalf("CreateContainer failed: %v", err)
			}
			binID, err := store.CreateBin(ctx, CreateBinParams{
				Name:        "S1",
				ContainerID: contID,
				LedIndex:    sql.NullInt64{Int64: 0, Valid: true},
			})
			if err != nil {
				t.Fatalf("CreateBin failed: %v", err)
			}

			const writers = 16
			const writesPerWriter = 15
			const readers = 10
			const readsPerReader = 20

			errCh := make(chan error, writers*writesPerWriter+readers*readsPerReader)
			var wg sync.WaitGroup

			for w := 0; w < writers; w++ {
				wg.Add(1)
				go func(worker int) {
					defer wg.Done()
					for i := 0; i < writesPerWriter; i++ {
						err := store.ExecTx(ctx, func(q Querier) error {
							pID, err := q.CreatePart(ctx, CreatePartParams{
								Name: fmt.Sprintf("StressPart-%d-%d", worker, i),
							})
							if err != nil {
								return err
							}
							if err := q.CreatePartAssignment(ctx, CreatePartAssignmentParams{
								PartID:   pID,
								BinID:    sql.NullInt64{Int64: binID, Valid: true},
								Quantity: 3,
							}); err != nil {
								return err
							}
							return q.CreateAuditLog(ctx, CreateAuditLogParams{
								ActionType: "CREATE",
								EntityType: "PART",
								EntityID:   pID,
							})
						})
						if err != nil {
							errCh <- fmt.Errorf("writer %d iter %d: %w", worker, i, err)
						}
					}
				}(w)
			}

			for r := 0; r < readers; r++ {
				wg.Add(1)
				go func(reader int) {
					defer wg.Done()
					for i := 0; i < readsPerReader; i++ {
						if _, err := store.ListParts(ctx, ListPartsParams{Limit: 20, Offset: 0}); err != nil {
							errCh <- fmt.Errorf("reader %d ListParts iter %d: %w", reader, i, err)
						}
						if _, err := store.GetDashboardStats(ctx); err != nil {
							errCh <- fmt.Errorf("reader %d GetDashboardStats iter %d: %w", reader, i, err)
						}
					}
				}(r)
			}

			wg.Wait()
			close(errCh)

			for err := range errCh {
				t.Errorf("concurrent operation failed: %v", err)
			}

			parts, err := store.GetAllParts(ctx)
			if err != nil {
				t.Fatalf("GetAllParts failed: %v", err)
			}
			expected := writers * writesPerWriter
			if len(parts) != expected {
				t.Fatalf("expected %d parts committed, got %d", expected, len(parts))
			}
		})
	}
}
