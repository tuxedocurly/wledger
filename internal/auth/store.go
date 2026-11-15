package auth

import (
	"context"
	"database/sql"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

// SQLCStore implements the scs.Store interface using db.Store
type SQLCStore struct {
	store db.Store
}

func NewStore(s db.Store) *SQLCStore {
	return &SQLCStore{store: s}
}

// Find returns the data for a given session token from the SQLCStore instance
// If the session token is not found or is expired, the returned exists flag will
// be set to false
func (s *SQLCStore) Find(token string) (b []byte, exists bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	session, err := s.store.GetSession(ctx, token)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	// check expiry manually
	if session.Expiry < float64(time.Now().Unix()) {
		return nil, false, nil
	}

	return session.Data, true, nil
}

// Commit adds a session token and data to the SQLCStore instance with the
// given expiry time. If the session token already exists, then data and
// expiry time are updated.
func (s *SQLCStore) Commit(token string, b []byte, expiry time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Convert expiry time to Unix timestamp (float64) for SQLite REAL column
	expiryFloat := float64(expiry.Unix())

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		err := q.DeleteSession(ctx, token)
		if err != nil {
			return err
		}

		return q.CreateSession(ctx, db.CreateSessionParams{
			Token:  token,
			Data:   b,
			Expiry: expiryFloat,
		})
	})
}

// Delete removes a sesssion token and corresponding data from the SQLCStore
// instance
func (s *SQLCStore) Delete(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return s.store.DeleteSession(ctx, token)
}

// CleanupExpired deletes all expired sessions from the backing database.
func (s *SQLCStore) CleanupExpired(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	now := float64(time.Now().Unix())
	return s.store.ExecTx(cctx, func(q db.Querier) error {
		return q.CleanupSessions(cctx, now)
	})
}
