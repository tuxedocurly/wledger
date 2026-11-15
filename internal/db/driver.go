package db

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func isInMemoryDSN(dsn string) bool {
	return strings.Contains(dsn, ":memory:") || strings.Contains(dsn, "mode=memory")
}

func appendQueryParam(dsn, kv, matchPrefix string) string {
	if strings.Contains(dsn, matchPrefix) {
		return dsn
	}
	if !strings.Contains(dsn, "?") {
		return dsn + "?" + kv
	}
	return dsn + "&" + kv
}

// Open opens a pure-Go SQLite database connection configured for WAL, foreign keys,
// busy timeout, synchronous=NORMAL, and immediate write transactions.
func Open(dsn string) (*sql.DB, error) {
	inMemory := isInMemoryDSN(dsn)

	dsn = appendQueryParam(dsn, "_pragma=foreign_keys(1)", "_pragma=foreign_keys")
	dsn = appendQueryParam(dsn, "_pragma=busy_timeout(5000)", "_pragma=busy_timeout")
	if !inMemory {
		dsn = appendQueryParam(dsn, "_pragma=journal_mode(WAL)", "_pragma=journal_mode")
	}
	dsn = appendQueryParam(dsn, "_pragma=synchronous(NORMAL)", "_pragma=synchronous")
	dsn = appendQueryParam(dsn, "_txlock=immediate", "_txlock=")

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	if inMemory {
		// Keep :memory: databases on a single connection so tables and locks
		// stay consistent without SQLite shared-cache table lock contention.
		db.SetMaxOpenConns(1)
	}

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	slog.Info("SQLite database connection opened", "dsn", dsn)

	return db, nil
}
