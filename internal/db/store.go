package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
)

// Store defines all functions to execute db queries and transactions
type Store interface {
	Querier
	ExecTx(ctx context.Context, fn func(Querier) error) error
}

// SQLStore provides all functions to execute SQL queries and transactions
type SQLStore struct {
	db   *sql.DB
	txMu sync.Mutex
	*Queries
}

// NewStore creates a new store
func NewStore(dbConn *sql.DB) Store {
	return &SQLStore{
		db:      dbConn,
		Queries: New(dbConn),
	}
}

// ExecTx executes a function within a database transaction.
// In-process write transactions are serialized with txMu and combined with
// _txlock=immediate / busy_timeout to eliminate SQLITE_BUSY under contention.
func (s *SQLStore) ExecTx(ctx context.Context, fn func(Querier) error) error {
	s.txMu.Lock()
	defer s.txMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	q := New(tx)
	err = fn(q)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("tx err: %v, rb err: %v", err, rbErr)
		}
		return err
	}

	return tx.Commit()
}
