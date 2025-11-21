package core

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerError(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	err := errors.New("database went boom")

	ServerError(rr, req, err)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Internal Server Error") {
		t.Errorf("body mismatch")
	}
}

func TestClientError(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	err := errors.New("bad input")

	ClientError(rr, req, http.StatusBadRequest, "Invalid Input", err)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Invalid Input") {
		t.Errorf("body mismatch")
	}
}
