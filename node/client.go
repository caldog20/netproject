package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"calnet_server/keys"
	"calnet_server/types"
)

// Client represents a client for interacting with the control server
type Client struct {
	baseURL    string
	httpClient *http.Client
	nodeKey    keys.PublicKey
}

// NewClient creates a new client for interacting with the control server
func NewClient(baseURL string, nodeKey keys.PublicKey) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		nodeKey: nodeKey,
	}
}

// Login attempts to log in to the control server
func (c *Client) Login() (*types.LoginResponse, error) {
	req := types.LoginRequest{
		NodeKey: c.nodeKey,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal login request: %w", err)
	}

	resp, err := c.httpClient.Post(
		fmt.Sprintf("%s/login", c.baseURL),
		"application/json",
		bytes.NewBuffer(reqBody),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to make login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(body))
	}

	var loginResp types.LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return nil, fmt.Errorf("failed to decode login response: %w", err)
	}

	return &loginResp, nil
}

// Update requests an update from the control server
func (c *Client) Update(fullUpdate bool) (*types.UpdateResponse, error) {
	req := types.UpdateRequest{
		NodeKey:    c.nodeKey,
		FullUpdate: fullUpdate,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal update request: %w", err)
	}

	resp, err := c.httpClient.Post(
		fmt.Sprintf("%s/update", c.baseURL),
		"application/json",
		bytes.NewBuffer(reqBody),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to make update request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("update failed with status %d: %s", resp.StatusCode, string(body))
	}

	var updateResp types.UpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&updateResp); err != nil {
		return nil, fmt.Errorf("failed to decode update response: %w", err)
	}

	return &updateResp, nil
}

// Health checks the health status of the control server
func (c *Client) Health() (map[string]map[string]string, error) {
	resp, err := c.httpClient.Get(fmt.Sprintf("%s/health", c.baseURL))
	if err != nil {
		return nil, fmt.Errorf("failed to make health request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("health check failed with status %d: %s", resp.StatusCode, string(body))
	}

	var healthResp map[string]map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&healthResp); err != nil {
		return nil, fmt.Errorf("failed to decode health response: %w", err)
	}

	return healthResp, nil
}
