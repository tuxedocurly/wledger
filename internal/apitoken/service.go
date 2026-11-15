package apitoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
)

const (
	// TokenPrefix identifies WLEDger Personal Access Tokens.
	TokenPrefix = "wlg_"

	// TokenRandomBytes defines the entropy of generated tokens (16 bytes -> 32 hex chars).
	TokenRandomBytes = 16

	// VisiblePrefixLen is the stored visible prefix length ("wlg_" + 8 hex chars).
	VisiblePrefixLen = 12

	// MaxTokenNameLength bounds token labels created by users.
	MaxTokenNameLength = 100

	// DefaultLastUsedThrottle avoids hammering SQLite on every concurrent API read.
	DefaultLastUsedThrottle = time.Minute

	ScopeRead  = "read"
	ScopeWrite = "write"
	ScopeAdmin = "admin"
)

var (
	ErrInvalidToken      = errors.New("invalid api token")
	ErrExpiredToken      = errors.New("api token expired")
	ErrTokenNotFound     = errors.New("api token not found")
	ErrInvalidTokenName  = errors.New("token name is required")
	ErrInvalidScope      = errors.New("invalid token scope")
	ErrScopeExceedsRole  = errors.New("requested scope exceeds user role permissions")
	ErrInvalidExpiration = errors.New("expiration time must be in the future")
	ErrUserNotFound      = errors.New("user not found")
)

// CreateTokenRequest configures a newly issued Personal Access Token.
type CreateTokenRequest struct {
	UserID    int64
	Name      string
	Scopes    string
	ExpiresAt *time.Time
}

// CreatedToken is returned once upon creation and contains the one-time plaintext token.
type CreatedToken struct {
	Token     db.ApiToken
	Plaintext string
}

// Principal represents an authenticated API token paired with its owning user and effective scopes.
type Principal struct {
	TokenID     int64
	TokenName   string
	TokenPrefix string
	Scopes      []string
	ExpiresAt   sql.NullTime
	User        auth.User
}

// HasScope reports whether BOTH the token's granted scopes and the user's current
// DB role permit the required scope ("read", "write", or "admin").
func (p Principal) HasScope(required string) bool {
	reqLevel := scopeLevel(required)
	if reqLevel == 0 {
		return false
	}
	if roleCeiling(p.User.Role) < reqLevel {
		return false
	}
	return maxScopeLevel(p.Scopes) >= reqLevel
}

// Service manages Personal Access Token lifecycle and authentication.
type Service interface {
	CreateToken(ctx context.Context, req CreateTokenRequest) (CreatedToken, error)
	Authenticate(ctx context.Context, rawToken string) (Principal, error)
	ListTokensByUser(ctx context.Context, userID int64) ([]db.ListAPITokensByUserRow, error)
	RevokeTokenForUser(ctx context.Context, tokenID, userID int64) error
	RevokeToken(ctx context.Context, tokenID int64) error
	Flush()
	Close()
}

// Option customizes Service behavior (primarily for testing and logging).
type Option func(*service)

// WithLogger sets the logger used for background last_used_at write errors.
func WithLogger(logger *slog.Logger) Option {
	return func(s *service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// WithNowFunc overrides time.Now for deterministic expiration/debounce tests.
func WithNowFunc(fn func() time.Time) Option {
	return func(s *service) {
		if fn != nil {
			s.now = fn
		}
	}
}

// WithLastUsedThrottle configures how frequently last_used_at updates are persisted per token.
func WithLastUsedThrottle(d time.Duration) Option {
	return func(s *service) {
		if d >= 0 {
			s.lastUsedThrottle = d
		}
	}
}

type service struct {
	store            db.Store
	logger           *slog.Logger
	now              func() time.Time
	lastUsedThrottle time.Duration

	bgCtx  context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	closed      bool
	draining    bool
	lastUpdated map[int64]time.Time
	pending     map[int64]struct{}
	wg          sync.WaitGroup
}

// NewService constructs an apitoken.Service backed by db.Store.
func NewService(store db.Store, opts ...Option) Service {
	bgCtx, cancel := context.WithCancel(context.Background())
	svc := &service{
		store:            store,
		logger:           slog.New(slog.DiscardHandler),
		now:              func() time.Time { return time.Now().UTC() },
		lastUsedThrottle: DefaultLastUsedThrottle,
		bgCtx:            bgCtx,
		cancel:           cancel,
		lastUpdated:      make(map[int64]time.Time),
		pending:          make(map[int64]struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	return svc
}

// HashToken returns the lowercase SHA-256 hex digest of a raw token.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ParseScopes parses and validates a comma-separated list of token scopes ("read", "write", "admin").
func ParseScopes(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, ErrInvalidScope
	}

	parts := strings.Split(trimmed, ",")
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		scope := strings.ToLower(strings.TrimSpace(part))
		if scopeLevel(scope) == 0 {
			return nil, fmt.Errorf("%w: %q", ErrInvalidScope, part)
		}
		seen[scope] = true
	}

	canonicalOrder := []string{ScopeRead, ScopeWrite, ScopeAdmin}
	out := make([]string, 0, len(seen))
	for _, sc := range canonicalOrder {
		if seen[sc] {
			out = append(out, sc)
		}
	}
	return out, nil
}

// RoleAllowsScope reports whether a user role ("admin", "editor", "viewer") permits the given scope.
func RoleAllowsScope(role, scope string) bool {
	reqLevel := scopeLevel(strings.ToLower(strings.TrimSpace(scope)))
	if reqLevel == 0 {
		return false
	}
	return roleCeiling(role) >= reqLevel
}

func (s *service) CreateToken(ctx context.Context, req CreateTokenRequest) (CreatedToken, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return CreatedToken{}, ErrInvalidTokenName
	}
	if len(name) > MaxTokenNameLength {
		name = name[:MaxTokenNameLength]
	}

	scopes, err := ParseScopes(req.Scopes)
	if err != nil {
		return CreatedToken{}, err
	}

	user, err := s.store.GetUser(ctx, req.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CreatedToken{}, ErrUserNotFound
		}
		return CreatedToken{}, fmt.Errorf("lookup token owner: %w", err)
	}

	if roleCeiling(user.Role) < maxScopeLevel(scopes) {
		return CreatedToken{}, ErrScopeExceedsRole
	}

	var expiresAt sql.NullTime
	if req.ExpiresAt != nil && !req.ExpiresAt.IsZero() {
		expUTC := req.ExpiresAt.UTC()
		if !expUTC.After(s.now()) {
			return CreatedToken{}, ErrInvalidExpiration
		}
		expiresAt = sql.NullTime{Time: expUTC, Valid: true}
	}

	plaintext, err := generatePlaintextToken()
	if err != nil {
		return CreatedToken{}, err
	}
	prefix := plaintext[:VisiblePrefixLen]
	hash := HashToken(plaintext)

	var created db.ApiToken
	err = s.store.ExecTx(ctx, func(q db.Querier) error {
		var txErr error
		created, txErr = q.CreateAPIToken(ctx, db.CreateAPITokenParams{
			UserID:      req.UserID,
			Name:        name,
			TokenHash:   hash,
			TokenPrefix: prefix,
			Scopes:      strings.Join(scopes, ","),
			ExpiresAt:   expiresAt,
		})
		if txErr != nil {
			return txErr
		}

		audit.Log(ctx, q, "CREATE", "USER", req.UserID, "Created API token "+name, nil, map[string]any{
			"token_id":     created.ID,
			"token_name":   created.Name,
			"token_prefix": created.TokenPrefix,
			"scopes":       created.Scopes,
		})
		return nil
	})
	if err != nil {
		return CreatedToken{}, fmt.Errorf("create api token: %w", err)
	}

	return CreatedToken{
		Token:     created,
		Plaintext: plaintext,
	}, nil
}

func (s *service) Authenticate(ctx context.Context, rawToken string) (Principal, error) {
	if !strings.HasPrefix(rawToken, TokenPrefix) || len(rawToken) <= len(TokenPrefix) {
		return Principal{}, ErrInvalidToken
	}

	hash := HashToken(rawToken)
	row, err := s.store.GetAPITokenWithUserByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Principal{}, ErrInvalidToken
		}
		return Principal{}, fmt.Errorf("authenticate api token: %w", err)
	}

	if row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(s.now()) {
		return Principal{}, ErrExpiredToken
	}

	if roleCeiling(row.UserRole) == 0 {
		return Principal{}, ErrInvalidToken
	}

	scopes, err := ParseScopes(row.Scopes)
	if err != nil {
		return Principal{}, ErrInvalidToken
	}

	s.recordLastUsed(row.ID)

	return Principal{
		TokenID:     row.ID,
		TokenName:   row.Name,
		TokenPrefix: row.TokenPrefix,
		Scopes:      scopes,
		ExpiresAt:   row.ExpiresAt,
		User: auth.User{
			ID:      row.UserID,
			Email:   row.UserEmail,
			Role:    row.UserRole,
			IsGuest: false,
		},
	}, nil
}

func (s *service) ListTokensByUser(ctx context.Context, userID int64) ([]db.ListAPITokensByUserRow, error) {
	rows, err := s.store.ListAPITokensByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return []db.ListAPITokensByUserRow{}, nil
	}
	return rows, nil
}

func (s *service) RevokeTokenForUser(ctx context.Context, tokenID, userID int64) error {
	tok, err := s.store.GetAPIToken(ctx, tokenID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTokenNotFound
		}
		return fmt.Errorf("lookup api token: %w", err)
	}
	if tok.UserID != userID {
		return ErrTokenNotFound
	}

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		if err := q.DeleteAPITokenForUser(ctx, db.DeleteAPITokenForUserParams{
			ID:     tokenID,
			UserID: userID,
		}); err != nil {
			return err
		}
		audit.Log(ctx, q, "DELETE", "USER", userID, "Revoked API token "+tok.Name, map[string]any{
			"token_id":     tok.ID,
			"token_name":   tok.Name,
			"token_prefix": tok.TokenPrefix,
		}, nil)
		return nil
	})
}

func (s *service) RevokeToken(ctx context.Context, tokenID int64) error {
	tok, err := s.store.GetAPIToken(ctx, tokenID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTokenNotFound
		}
		return fmt.Errorf("lookup api token: %w", err)
	}

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		if err := q.DeleteAPIToken(ctx, tokenID); err != nil {
			return err
		}
		audit.Log(ctx, q, "DELETE", "USER", tok.UserID, "Revoked API token "+tok.Name, map[string]any{
			"token_id":     tok.ID,
			"token_name":   tok.Name,
			"token_prefix": tok.TokenPrefix,
		}, nil)
		return nil
	})
}

// Flush waits for any queued asynchronous last_used_at updates to finish.
func (s *service) Flush() {
	s.wg.Wait()
}

// Close cancels pending background last_used_at updates and waits for worker shutdown.
func (s *service) Close() {
	s.mu.Lock()
	s.closed = true
	s.pending = nil
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

func (s *service) recordLastUsed(tokenID int64) {
	now := s.now()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if last, ok := s.lastUpdated[tokenID]; ok && s.lastUsedThrottle > 0 && now.Sub(last) < s.lastUsedThrottle {
		s.mu.Unlock()
		return
	}
	s.lastUpdated[tokenID] = now
	s.pending[tokenID] = struct{}{}
	if !s.draining {
		s.draining = true
		s.wg.Add(1)
		go s.drainLastUsed()
	}
	s.mu.Unlock()
}

func (s *service) drainLastUsed() {
	defer s.wg.Done()

	for {
		s.mu.Lock()
		if s.closed || len(s.pending) == 0 {
			s.draining = false
			s.mu.Unlock()
			return
		}
		ids := make([]int64, 0, len(s.pending))
		for id := range s.pending {
			ids = append(ids, id)
		}
		s.pending = make(map[int64]struct{})
		s.mu.Unlock()

		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

		for _, id := range ids {
			if s.bgCtx.Err() != nil {
				s.mu.Lock()
				s.draining = false
				s.mu.Unlock()
				return
			}
			ctx, cancel := context.WithTimeout(s.bgCtx, 2*time.Second)
			err := s.store.ExecTx(ctx, func(q db.Querier) error {
				return q.UpdateAPITokenLastUsed(ctx, id)
			})
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Debug("failed to update api token last_used_at", "token_id", id, "err", err)
			}
		}
	}
}

func generatePlaintextToken() (string, error) {
	var raw [TokenRandomBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate token entropy: %w", err)
	}
	return TokenPrefix + hex.EncodeToString(raw[:]), nil
}

func scopeLevel(scope string) int {
	switch scope {
	case ScopeRead:
		return 1
	case ScopeWrite:
		return 2
	case ScopeAdmin:
		return 3
	default:
		return 0
	}
}

func roleCeiling(role string) int {
	switch role {
	case "admin":
		return 3
	case "editor":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

func maxScopeLevel(scopes []string) int {
	maxLvl := 0
	for _, sc := range scopes {
		if lvl := scopeLevel(sc); lvl > maxLvl {
			maxLvl = lvl
		}
	}
	return maxLvl
}
