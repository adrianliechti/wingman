package harness

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

// RawResponse holds the unmarshalled JSON body plus HTTP metadata.
type RawResponse struct {
	StatusCode int            `json:"statusCode"`
	Headers    http.Header    `json:"headers"`
	Body       map[string]any `json:"body"` // parsed JSON
	RawBody    []byte         `json:"rawBody"`
}

// SSEEvent represents a single server-sent event.
type SSEEvent struct {
	Event string         `json:"event"`
	Data  map[string]any `json:"data"` // parsed JSON from data field
	Raw   string         `json:"raw"`  // raw data string
}

// Client is a thin, SDK-free HTTP client for the OpenAI-compatible API.
type Client struct {
	HTTP    *http.Client  `json:"http"`
	Timeout time.Duration `json:"timeout"`
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

// PostRaw sends a JSON POST request and tolerates a non-JSON response body,
// which audio endpoints return. Body is parsed when the payload happens to be
// JSON, so error responses stay inspectable.
func (c *Client) PostRaw(ctx context.Context, ep Endpoint, path string, body any) (*RawResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.BaseURL+path, bytes.NewReader(payload))
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
		_ = json.Unmarshal(raw, &result.Body)
	}

	return result, nil
}

// MultipartField is a single form value. Repeat a name to send an array field.
type MultipartField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MultipartFile is a file part of a multipart request.
type MultipartFile struct {
	Name        string `json:"name"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Content     []byte `json:"content"`
}

// PostMultipart sends a multipart/form-data POST request. Unlike Post it
// tolerates non-JSON bodies, which the text, srt and vtt formats return.
func (c *Client) PostMultipart(ctx context.Context, ep Endpoint, path string, fields []MultipartField, files []MultipartFile) (*RawResponse, error) {
	resp, err := c.doMultipart(ctx, ep, path, fields, files)
	if err != nil {
		return nil, err
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
		_ = json.Unmarshal(raw, &result.Body)
	}

	return result, nil
}

// PostMultipartSSE sends a multipart/form-data POST request and collects all
// SSE events.
func (c *Client) PostMultipartSSE(ctx context.Context, ep Endpoint, path string, fields []MultipartField, files []MultipartFile) ([]*SSEEvent, error) {
	resp, err := c.doMultipart(ctx, ep, path, fields, files)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s returned status %d: %s", ep.Name, resp.StatusCode, string(raw))
	}

	return ParseSSE(resp.Body)
}

func (c *Client) doMultipart(ctx context.Context, ep Endpoint, path string, fields []MultipartField, files []MultipartFile) (*http.Response, error) {
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)

	for _, field := range fields {
		if err := writer.WriteField(field.Name, field.Value); err != nil {
			return nil, fmt.Errorf("write field %q: %w", field.Name, err)
		}
	}

	for _, file := range files {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, file.Name, file.Filename))

		if file.ContentType != "" {
			header.Set("Content-Type", file.ContentType)
		}

		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, fmt.Errorf("create part %q: %w", file.Name, err)
		}

		if _, err := part.Write(file.Content); err != nil {
			return nil, fmt.Errorf("write part %q: %w", file.Name, err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.Timeout)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.BaseURL+path, &payload)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+ep.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("do request to %s: %w", ep.Name, err)
	}

	resp.Body = &cancelReader{ReadCloser: resp.Body, cancel: cancel}

	return resp, nil
}

type cancelReader struct {
	io.ReadCloser

	cancel context.CancelFunc
}

func (r *cancelReader) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()

	return err
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
