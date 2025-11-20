package wled

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"
	"wledger/internal/models"
)

type WLEDClientInterface interface {
	SendCommand(ipAddress string, state models.WLEDState) error
	Ping(ipAddress string) bool
}

var _ WLEDClientInterface = (*WLEDClient)(nil)

// WLEDClient for communicating with WLED controllers
type WLEDClient struct {
	client *http.Client
}

// Creates a new WLED client
func NewWLEDClient() *WLEDClient {
	return &WLEDClient{
		client: &http.Client{
			// define default timeout
			Timeout: 5 * time.Second,
		},
	}
}

// SendCommand sends a JSON state command to a WLED controller IP
func (c *WLEDClient) SendCommand(ipAddress string, state models.WLEDState) error {
	jsonData, err := json.Marshal(state)
	if err != nil {
		return err
	}

	url := "http://" + ipAddress + "/json/state"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	// Use the client's internal http.Client
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// Ping sends a GET request to a WLED controller
func (c *WLEDClient) Ping(ipAddress string) bool {
	// Use a shorter timeout for pings
	pingClient := &http.Client{Timeout: 2 * time.Second}

	resp, err := pingClient.Get("http://" + ipAddress + "/json/info")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
