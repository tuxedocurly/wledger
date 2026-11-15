package uierror

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func setupResponderTest(t *testing.T) (*Responder, func()) {
	dbConn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	r := New(logger)

	return r, func() {
		dbConn.Close()
	}
}

func TestResponder_Respond(t *testing.T) {
	r, cleanup := setupResponderTest(t)
	defer cleanup()

	t.Run("Standard Response (non-HTMX)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()

		testErr := errors.New("something went wrong")
		r.Respond(rr, req, testErr, "Failed to load page", http.StatusInternalServerError)

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("expected status 500, got %d", rr.Code)
		}
	})

	t.Run("HTMX Response 4xx", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("HX-Request", "true")
		rr := httptest.NewRecorder()

		testErr := errors.New("htmx error")
		r.Respond(rr, req, testErr, "HTMX failed", http.StatusBadRequest)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rr.Code)
		}
		if got := rr.Header().Get("HX-Reswap"); got != "none" {
			t.Errorf("expected HX-Reswap=none, got %q", got)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("expected text/html Content-Type, got %q", ct)
		}
		body := rr.Body.String()
		for _, want := range []string{`id="toast-container"`, `hx-swap-oob="beforeend"`, `alert-error`, `role="alert"`, `HTMX failed`} {
			if !strings.Contains(body, want) {
				t.Errorf("HTMX error body missing %q; got %s", want, body)
			}
		}
	})

	t.Run("HTMX Response 5xx", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/locate", nil)
		req.Header.Set("HX-Request", "true")
		rr := httptest.NewRecorder()

		r.Respond(rr, req, errors.New("controller offline"), "Hardware unreachable", http.StatusInternalServerError)

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("expected status 500, got %d", rr.Code)
		}
		if got := rr.Header().Get("HX-Reswap"); got != "none" {
			t.Errorf("expected HX-Reswap=none, got %q", got)
		}
		body := rr.Body.String()
		if !strings.Contains(body, `id="toast-container"`) || !strings.Contains(body, `hx-swap-oob="beforeend"`) || !strings.Contains(body, "Hardware unreachable") {
			t.Errorf("expected OOB error toast in 5xx HTMX response, got: %s", body)
		}
	})
}

func TestResponder_HTMXEscapesMessageAndNilLogger(t *testing.T) {
	r := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	nilLoggerResponder := New(nil)

	for _, resp := range []*Responder{r, nilLoggerResponder} {
		req := httptest.NewRequest("POST", "/test", nil)
		req.Header.Set("HX-Request", "true")
		rr := httptest.NewRecorder()

		payload := `<script>alert("xss")</script>`
		resp.Respond(rr, req, errors.New("boom"), payload, http.StatusUnprocessableEntity)

		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected status 422, got %d", rr.Code)
		}
		body := rr.Body.String()
		if strings.Contains(body, "<script>") {
			t.Errorf("unescaped script tag in OOB toast response: %s", body)
		}
		if !strings.Contains(body, "&lt;script&gt;") {
			t.Errorf("expected escaped message in OOB toast response: %s", body)
		}
	}
}
