package wled

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wledger/internal/models"
)

func TestWLEDClient_SendCommand(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("got method %s, want POST", r.Method)
		}

		body, _ := io.ReadAll(r.Body)
		expectedJSON := `{"seg":[{"id":0,"on":true,"i":[5,"FF0000"]}]}`
		if string(body) != expectedJSON {
			t.Errorf("got body %s, want %s", string(body), expectedJSON)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := NewWLEDClient()

	state := models.WLEDState{
		Segments: []models.WLEDSegment{
			{
				ID: 0,
				On: true,
				I:  []interface{}{5, "FF0000"},
			},
		},
	}

	err := client.SendCommand(strings.TrimPrefix(ts.URL, "http://"), state)

	if err != nil {
		t.Fatalf("SendCommand() failed: %v", err)
	}
}

func TestWLEDClient_Ping(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/info" {
			t.Errorf("got path %s, want /json/info", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := NewWLEDClient()

	ip := strings.TrimPrefix(ts.URL, "http://")
	online := client.Ping(ip)

	if !online {
		t.Error("client.Ping() returned false, want true")
	}

	ts.Close()
	online = client.Ping(ip)
	if online {
		t.Error("client.Ping() returned true for a dead server, want false")
	}
}
