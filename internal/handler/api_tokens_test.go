package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/apitoken"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/i18n"
	"github.com/tuxedocurly/wledger/internal/uierror"
)

func setupAPITokenHandlerEnv(t *testing.T) (db.Store, apitoken.Service, *APITokenHandler) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "api_tokens_handler.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	if err := i18n.InitWithDir("../../locales"); err != nil {
		t.Fatalf("init i18n: %v", err)
	}
	t.Cleanup(func() { _ = i18n.Init() })

	store := db.NewStore(conn)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokenSvc := apitoken.NewService(store, apitoken.WithLogger(logger))
	t.Cleanup(tokenSvc.Close)

	h := NewAPITokenHandler(store, tokenSvc, uierror.New(logger))
	return store, tokenSvc, h
}

func seedHandlerUser(t *testing.T, store db.Store, email, role string) auth.User {
	t.Helper()
	row, err := store.CreateUser(context.Background(), db.CreateUserParams{
		Email:        email,
		PasswordHash: "hashed",
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create user (%s): %v", email, err)
	}
	return auth.User{
		ID:    row.ID,
		Email: row.Email,
		Role:  row.Role,
	}
}

func withRequestUser(req *http.Request, user auth.User) *http.Request {
	ctx := auth.WithUser(req.Context(), user)
	ctx = i18n.WithLocalizer(ctx, "en")
	return req.WithContext(ctx)
}

func extractRevealedPlaintext(t *testing.T, body string) string {
	t.Helper()
	const marker = `data-token="`
	idx := strings.Index(body, marker)
	if idx == -1 {
		t.Fatalf("expected data-token reveal attribute in response body, got:\n%s", body)
	}
	rest := body[idx+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end == -1 {
		t.Fatalf("unterminated data-token attribute in response body")
	}
	return rest[:end]
}

func TestAPITokenHandlerCreateListAndRevoke(t *testing.T) {
	store, tokenSvc, h := setupAPITokenHandlerEnv(t)

	editor := seedHandlerUser(t, store, "editor@example.com", "editor")
	viewer := seedHandlerUser(t, store, "viewer@example.com", "viewer")

	r := chi.NewRouter()
	r.Get("/settings/tokens", h.HandleListAPITokens)
	r.Post("/settings/tokens", h.HandleCreateAPIToken)
	r.Post("/settings/tokens/{id}/delete", h.HandleRevokeAPIToken)
	r.Delete("/settings/tokens/{id}", h.HandleRevokeAPIToken)

	// Unauthenticated requests are rejected cleanly.
	unauthReq := httptest.NewRequest(http.MethodPost, "/settings/tokens", strings.NewReader("name=CI&scope=read"))
	unauthReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unauthRR := httptest.NewRecorder()
	r.ServeHTTP(unauthRR, unauthReq)
	if unauthRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for anonymous request, got %d", unauthRR.Code)
	}

	// Create token with expiry as editor.
	form := url.Values{}
	form.Set("name", "Home Assistant")
	form.Set("scope", "write")
	form.Set("expires_days", "30")
	createReq := httptest.NewRequest(http.MethodPost, "/settings/tokens", strings.NewReader(form.Encode()))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createReq.Header.Set("HX-Request", "true")
	createReq = withRequestUser(createReq, editor)

	createRR := httptest.NewRecorder()
	r.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", createRR.Code, createRR.Body.String())
	}

	createBody := createRR.Body.String()
	if !strings.Contains(createBody, "api-token-reveal") || !strings.Contains(createBody, "window.copyTextSafe") {
		t.Fatalf("expected one-time reveal banner with copyTextSafe, got:\n%s", createBody)
	}
	if !strings.Contains(createBody, "Home Assistant") {
		t.Fatalf("expected created token row in rendered card")
	}

	plaintext := extractRevealedPlaintext(t, createBody)
	if !strings.HasPrefix(plaintext, apitoken.TokenPrefix) {
		t.Fatalf("expected revealed token to start with %q, got %q", apitoken.TokenPrefix, plaintext)
	}
	if _, err := tokenSvc.Authenticate(context.Background(), plaintext); err != nil {
		t.Fatalf("newly created token failed authentication: %v", err)
	}

	// GET /settings/tokens must NOT re-display plaintext token.
	listReq := httptest.NewRequest(http.MethodGet, "/settings/tokens", nil)
	listReq = withRequestUser(listReq, editor)
	listRR := httptest.NewRecorder()
	r.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on list, got %d", listRR.Code)
	}
	listBody := listRR.Body.String()
	if strings.Contains(listBody, plaintext) {
		t.Fatal("plaintext token must never be re-displayed on subsequent list requests")
	}
	if !strings.Contains(listBody, plaintext[:apitoken.VisiblePrefixLen]) {
		t.Fatalf("expected visible token prefix %q in list view", plaintext[:apitoken.VisiblePrefixLen])
	}

	tokens, err := tokenSvc.ListTokensByUser(context.Background(), editor.ID)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("expected 1 persisted token, got len=%d err=%v", len(tokens), err)
	}
	tokenID := tokens[0].ID

	// Another user attempting to revoke editor's token gets 404.
	crossReq := httptest.NewRequest(http.MethodDelete, "/settings/tokens/"+strconv.FormatInt(tokenID, 10), nil)
	crossReq.Header.Set("HX-Request", "true")
	crossReq = withRequestUser(crossReq, viewer)
	crossRR := httptest.NewRecorder()
	r.ServeHTTP(crossRR, crossReq)
	if crossRR.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when non-owner tries to revoke token, got %d", crossRR.Code)
	}

	// Owner revoking via HTMX POST /settings/tokens/{id}/delete succeeds and refreshes card.
	revokeReq := httptest.NewRequest(http.MethodPost, "/settings/tokens/"+strconv.FormatInt(tokenID, 10)+"/delete", nil)
	revokeReq.Header.Set("HX-Request", "true")
	revokeReq = withRequestUser(revokeReq, editor)
	revokeRR := httptest.NewRecorder()
	r.ServeHTTP(revokeRR, revokeReq)
	if revokeRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on HTMX revoke, got %d: %s", revokeRR.Code, revokeRR.Body.String())
	}
	if !strings.Contains(revokeRR.Body.String(), "No API tokens created yet.") {
		t.Fatalf("expected empty token card after revoking last token, got:\n%s", revokeRR.Body.String())
	}
	if _, err := tokenSvc.Authenticate(context.Background(), plaintext); err == nil {
		t.Fatal("revoked token must no longer authenticate")
	}

	// Non-HTMX POST revoke redirects to /settings.
	second, err := tokenSvc.CreateToken(context.Background(), apitoken.CreateTokenRequest{
		UserID: editor.ID,
		Name:   "Non-HTMX",
		Scopes: "read",
	})
	if err != nil {
		t.Fatalf("CreateToken second: %v", err)
	}
	nonHTMXRevoke := httptest.NewRequest(http.MethodPost, "/settings/tokens/"+strconv.FormatInt(second.Token.ID, 10)+"/delete", nil)
	nonHTMXRevoke = withRequestUser(nonHTMXRevoke, editor)
	nonHTMXRR := httptest.NewRecorder()
	r.ServeHTTP(nonHTMXRR, nonHTMXRevoke)
	if nonHTMXRR.Code != http.StatusSeeOther || nonHTMXRR.Header().Get("Location") != "/settings" {
		t.Fatalf("expected 303 redirect to /settings, got %d (%s)", nonHTMXRR.Code, nonHTMXRR.Header().Get("Location"))
	}
}

func TestAPITokenHandlerValidationAndScopeCeiling(t *testing.T) {
	store, _, h := setupAPITokenHandlerEnv(t)
	viewer := seedHandlerUser(t, store, "viewer2@example.com", "viewer")

	r := chi.NewRouter()
	r.Post("/settings/tokens", h.HandleCreateAPIToken)
	r.Post("/settings/tokens/{id}/delete", h.HandleRevokeAPIToken)

	cases := []struct {
		name     string
		form     url.Values
		wantCode int
	}{
		{
			name: "missing token name",
			form: url.Values{
				"name":  {"   "},
				"scope": {"read"},
			},
			wantCode: http.StatusBadRequest,
		},
		{
			name: "invalid scope",
			form: url.Values{
				"name":  {"Bad"},
				"scope": {"root"},
			},
			wantCode: http.StatusBadRequest,
		},
		{
			name: "invalid expiry days",
			form: url.Values{
				"name":         {"BadExpiry"},
				"scope":        {"read"},
				"expires_days": {"0"},
			},
			wantCode: http.StatusBadRequest,
		},
		{
			name: "viewer exceeds role ceiling",
			form: url.Values{
				"name":  {"Escalated"},
				"scope": {"write"},
			},
			wantCode: http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/settings/tokens", strings.NewReader(tc.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withRequestUser(req, viewer)
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, rr.Code, rr.Body.String())
			}
		})
	}

	badIDReq := httptest.NewRequest(http.MethodPost, "/settings/tokens/not-a-number/delete", nil)
	badIDReq = withRequestUser(badIDReq, viewer)
	badIDRR := httptest.NewRecorder()
	r.ServeHTTP(badIDRR, badIDReq)
	if badIDRR.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-numeric token ID, got %d", badIDRR.Code)
	}
}
