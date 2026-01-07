package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tuxedocurly/wledger/internal/i18n"
)

func TestI18nMiddleware(t *testing.T) {
	// Initialize i18n (empty is fine for this test as we are checking context population)
	_ = i18n.InitWithDir(".") // Use current dir, might fail but we just need bundle to exist

	m := &Manager{}

	t.Run("Language from Query Param", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/?lang=es", nil)
		rr := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Localizer should be in context
			localizer := i18n.GetLocalizer(r.Context())
			if localizer == nil {
				t.Error("expected localizer in context, got nil")
			}
		})

		handler := m.I18n(nextHandler)
		handler.ServeHTTP(rr, req)

		// Should set cookie
		cookies := rr.Result().Cookies()
		found := false
		for _, c := range cookies {
			if c.Name == "lang" && c.Value == "es" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected lang=es cookie to be set")
		}
	})

	t.Run("Language from Cookie", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(&http.Cookie{Name: "lang", Value: "fr"})
		rr := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localizer := i18n.GetLocalizer(r.Context())
			if localizer == nil {
				t.Error("expected localizer in context, got nil")
			}
		})

		handler := m.I18n(nextHandler)
		handler.ServeHTTP(rr, req)
	})

	t.Run("Language from Accept-Language Header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Accept-Language", "de-DE, de;q=0.9, en;q=0.8")
		rr := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localizer := i18n.GetLocalizer(r.Context())
			if localizer == nil {
				t.Error("expected localizer in context, got nil")
			}
		})

		handler := m.I18n(nextHandler)
		handler.ServeHTTP(rr, req)
	})
}
