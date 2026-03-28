package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RawResponse holds the unmarshalled JSON body plus HTTP metadata.
type RawResponse struct {
	StatusCode int
	Headers    http.Header
	Body       map[string]any // parsed JSON
	RawBody    []byte
}

// SSEEvent represents a single server-sent event.
type SSEEvent struct {
	Event string
	Data  map[string]any // parsed JSON from data field
	Raw   string         // raw data string
}

// Client is a thin, SDK-free HTTP client for the OpenAI-compatible API.
type Client struct {
	HTTP    *http.Client
	Timeout time.Duration
}

// NewClient creates a Client with sensible defaults.
func NewClient() *Client {
	return &Client{
		HTTP:    &http.Client{},
		Timeout: 60 * time.Second,
	}
}

// Post sends a JSON POST request and returns the parsed response.
func (c *Client) Post(ctx context.Context, ep Endpoint, path string, body any) (*RawResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := ep.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ep.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request to %s: %w", ep.Name, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", ep.Name, err)
	}

	result := &RawResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		RawBody:    raw,
	}

	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result.Body); err != nil {
			return result, fmt.Errorf("unmarshal response from %s: %w\nbody: %s", ep.Name, err, string(raw))
		}
	}

	return result, nil
}

// PostSSE sends a streaming POST request and collects all SSE events.
func (c *Client) PostSSE(ctx context.Context, ep Endpoint, path string, body any) ([]*SSEEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := ep.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ep.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request to %s: %w", ep.Name, err)
	}
	defer resp.Body.Close()

	return ParseSSE(resp.Body)
}
