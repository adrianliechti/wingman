package errors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	openaiProvider "github.com/adrianliechti/wingman/pkg/provider/openai"
	anthropicHandler "github.com/adrianliechti/wingman/server/anthropic"
	geminiHandler "github.com/adrianliechti/wingman/server/gemini"
	chatHandler "github.com/adrianliechti/wingman/server/openai/chat"
	"github.com/adrianliechti/wingman/server/openai/responses"
	"github.com/adrianliechti/wingman/server/openai/shared"

	"github.com/go-chi/chi/v5"
)

// mockOpenAIError represents an OpenAI API error response body.
type mockOpenAIError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// newMockOpenAIServer creates a chi-based mock server that returns the
// given status code and error body for POST /responses.
func newMockOpenAIServer(statusCode int, retryAfter string, errBody mockOpenAIError) *httptest.Server {
	r := chi.NewRouter()

	r.Post("/responses", func(w http.ResponseWriter, r *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(errBody)
	})

	return httptest.NewServer(r)
}

// newResponder creates a Responder pointing at the mock server with retries disabled.
func newResponder(mockURL string, client *http.Client) *openaiProvider.Responder {
	r, err := openaiProvider.NewResponder(mockURL, "test-model",
		openaiProvider.WithClient(client),
		openaiProvider.WithMaxRetries(0),
	)
	if err != nil {
		panic(err)
	}
	return r
}

// errorCompleter is a mock Completer that always returns the given error.
type errorCompleter struct {
	err error
}

func (c *errorCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		yield(nil, c.err)
	}
}

// midStreamErrorCompleter yields one successful chunk, then an error.
// This simulates errors that occur after streaming headers are already sent.
type midStreamErrorCompleter struct {
	err error
}

func (c *midStreamErrorCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		// First chunk succeeds — this causes headers to be sent
		chunk := &provider.Completion{
			Model: "test-model",
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					{Text: "Hello"},
				},
			},
		}

		if !yield(chunk, nil) {
			return
		}

		// Second iteration returns the error (mid-stream)
		yield(nil, c.err)
	}
}

// newWingmanServer creates a minimal wingman responses handler backed by
// the provided completer, mounted on a chi router at /v1/responses.
func newWingmanServer(completer provider.Completer, modelID string) *httptest.Server {
	cfg := &config.Config{
		Policy: noop.New(),
	}
	cfg.RegisterCompleter(modelID, completer)

	handler := responses.New(cfg)

	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		handler.Attach(r)
	})

	return httptest.NewServer(r)
}

func newChatServer(completer provider.Completer, modelID string) *httptest.Server {
	cfg := &config.Config{
		Policy: noop.New(),
	}
	cfg.RegisterCompleter(modelID, completer)

	handler := chatHandler.New(cfg)

	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		handler.Attach(r)
	})

	return httptest.NewServer(r)
}

func newAnthropicServer(completer provider.Completer, modelID string) *httptest.Server {
	cfg := &config.Config{
		Policy: noop.New(),
	}
	cfg.RegisterCompleter(modelID, completer)

	handler := anthropicHandler.New(cfg)

	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		handler.Attach(r)
	})

	return httptest.NewServer(r)
}

func newGeminiServer(completer provider.Completer, modelID string) *httptest.Server {
	cfg := &config.Config{
		Policy: noop.New(),
	}
	cfg.RegisterCompleter(modelID, completer)

	handler := geminiHandler.New(cfg)

	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		handler.Attach(r)
	})

	return httptest.NewServer(r)
}

// errorResponse matches the OpenAI error envelope returned by wingman.
type errorResponse = shared.ErrorResponse

// --- Provider-level tests ---

func TestRateLimitError_ProviderLevel(t *testing.T) {
	errBody := mockOpenAIError{}
	errBody.Error.Message = "Rate limit reached for model"
	errBody.Error.Type = "rate_limit_exceeded"
	errBody.Error.Code = "rate_limit_exceeded"

	mock := newMockOpenAIServer(http.StatusTooManyRequests, "30", errBody)
	defer mock.Close()

	responder := newResponder(mock.URL, mock.Client())

	messages := []provider.Message{provider.UserMessage("hello")}
	var gotErr error

	for _, err := range responder.Complete(t.Context(), messages, nil) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Fatal("expected error from provider, got nil")
	}

	statusCode := provider.CodeFromError(gotErr, 0)
	if statusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", statusCode)
	}

	retryAfter := provider.RetryAfterFromError(gotErr)
	if retryAfter.Seconds() != 30 {
		t.Errorf("expected Retry-After 30s, got %v", retryAfter)
	}
}

func TestServerError_ProviderLevel(t *testing.T) {
	errBody := mockOpenAIError{}
	errBody.Error.Message = "The server had an error"
	errBody.Error.Type = "server_error"
	errBody.Error.Code = "server_error"

	mock := newMockOpenAIServer(http.StatusInternalServerError, "", errBody)
	defer mock.Close()

	responder := newResponder(mock.URL, mock.Client())

	var gotErr error
	for _, err := range responder.Complete(t.Context(), []provider.Message{provider.UserMessage("hello")}, nil) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Fatal("expected error from provider, got nil")
	}

	// Upstream 500 → ProviderError maps to 502 (bad gateway).
	statusCode := provider.CodeFromError(gotErr, 0)
	if statusCode != http.StatusBadGateway {
		t.Errorf("expected status 502, got %d", statusCode)
	}
}

func TestAuthError_ProviderLevel(t *testing.T) {
	errBody := mockOpenAIError{}
	errBody.Error.Message = "Incorrect API key provided"
	errBody.Error.Type = "authentication_error"
	errBody.Error.Code = "invalid_api_key"

	mock := newMockOpenAIServer(http.StatusUnauthorized, "", errBody)
	defer mock.Close()

	responder := newResponder(mock.URL, mock.Client())

	var gotErr error
	for _, err := range responder.Complete(t.Context(), []provider.Message{provider.UserMessage("hello")}, nil) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Fatal("expected error from provider, got nil")
	}

	statusCode := provider.CodeFromError(gotErr, 0)
	if statusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", statusCode)
	}
}

// --- Handler-level (e2e) tests ---

func TestRateLimitError_HandlerLevel(t *testing.T) {
	errBody := mockOpenAIError{}
	errBody.Error.Message = "Rate limit reached for model"
	errBody.Error.Type = "rate_limit_exceeded"
	errBody.Error.Code = "rate_limit_exceeded"

	mock := newMockOpenAIServer(http.StatusTooManyRequests, "60", errBody)
	defer mock.Close()

	responder := newResponder(mock.URL, mock.Client())

	srv := newWingmanServer(responder, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model": "test-model",
		"input": "hello",
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/responses", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/responses: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 429, got %d; body: %s", resp.StatusCode, body)
	}

	if ra := resp.Header.Get("Retry-After"); ra != "60" {
		t.Errorf("expected Retry-After: 60, got %q", ra)
	}

	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	if errResp.Error.Type != "rate_limit_exceeded" {
		t.Errorf("expected error type rate_limit_exceeded, got %q", errResp.Error.Type)
	}
}

func TestServerError_HandlerLevel(t *testing.T) {
	errBody := mockOpenAIError{}
	errBody.Error.Message = "The server had an error while processing your request"
	errBody.Error.Type = "server_error"
	errBody.Error.Code = "server_error"

	mock := newMockOpenAIServer(http.StatusInternalServerError, "", errBody)
	defer mock.Close()

	responder := newResponder(mock.URL, mock.Client())

	srv := newWingmanServer(responder, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model": "test-model",
		"input": "hello",
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/responses", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/responses: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 502, got %d; body: %s", resp.StatusCode, body)
	}

	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	if errResp.Error.Type != "server_error" {
		t.Errorf("expected error type server_error, got %q", errResp.Error.Type)
	}
}

func TestAuthenticationError_HandlerLevel(t *testing.T) {
	errBody := mockOpenAIError{}
	errBody.Error.Message = "Incorrect API key provided"
	errBody.Error.Type = "authentication_error"
	errBody.Error.Code = "invalid_api_key"

	mock := newMockOpenAIServer(http.StatusUnauthorized, "", errBody)
	defer mock.Close()

	responder := newResponder(mock.URL, mock.Client())

	srv := newWingmanServer(responder, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model": "test-model",
		"input": "hello",
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/responses", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/responses: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 401, got %d; body: %s", resp.StatusCode, body)
	}

	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	if errResp.Error.Type != "authentication_error" {
		t.Errorf("expected error type authentication_error, got %q", errResp.Error.Type)
	}
}

// --- Streaming pre-stream error tests ---
// These test that when the upstream provider returns an error before any
// data is streamed, the server returns a proper HTTP error status code
// (not 200) with a JSON error body — matching the real API behavior.

func TestStreamingPreStreamError_Responses(t *testing.T) {
	errBody := mockOpenAIError{}
	errBody.Error.Message = "Rate limit reached"
	errBody.Error.Type = "rate_limit_exceeded"
	errBody.Error.Code = "rate_limit_exceeded"

	mock := newMockOpenAIServer(http.StatusTooManyRequests, "10", errBody)
	defer mock.Close()

	responder := newResponder(mock.URL, mock.Client())

	srv := newWingmanServer(responder, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model":  "test-model",
		"input":  "hello",
		"stream": true,
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/responses", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/responses: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 429, got %d; body: %s", resp.StatusCode, body)
	}

	if ra := resp.Header.Get("Retry-After"); ra != "10" {
		t.Errorf("expected Retry-After: 10, got %q", ra)
	}

	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	if errResp.Error.Type != "rate_limit_exceeded" {
		t.Errorf("expected error type rate_limit_exceeded, got %q", errResp.Error.Type)
	}
}

func TestStreamingPreStreamError_Chat(t *testing.T) {
	provErr := &provider.ProviderError{
		Code:    http.StatusTooManyRequests,
		Message: "Rate limit reached",
	}

	srv := newChatServer(&errorCompleter{err: provErr}, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model":  "test-model",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/chat/completions", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 429, got %d; body: %s", resp.StatusCode, body)
	}

	// Verify it's a JSON error response, not SSE
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	if errResp.Error.Type != "rate_limit_exceeded" {
		t.Errorf("expected error type rate_limit_exceeded, got %q", errResp.Error.Type)
	}
}

func TestStreamingPreStreamError_Chat_ServerError(t *testing.T) {
	provErr := &provider.ProviderError{
		Code:    http.StatusInternalServerError,
		Message: "Internal server error",
	}

	srv := newChatServer(&errorCompleter{err: provErr}, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model":  "test-model",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/chat/completions", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()

	// Upstream 500 → 502 Bad Gateway
	if resp.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 502, got %d; body: %s", resp.StatusCode, body)
	}
}

func TestStreamingPreStreamError_Anthropic(t *testing.T) {
	provErr := &provider.ProviderError{
		Code:    http.StatusTooManyRequests,
		Message: "Rate limit reached",
	}

	srv := newAnthropicServer(&errorCompleter{err: provErr}, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model":      "test-model",
		"stream":     true,
		"max_tokens": 100,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/messages", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/messages: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 429, got %d; body: %s", resp.StatusCode, body)
	}

	// Verify it's a JSON error response, not SSE
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	// Verify Anthropic error envelope
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	if raw["type"] != "error" {
		t.Errorf("expected type 'error', got %q", raw["type"])
	}

	errObj, ok := raw["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errObj["type"] != "rate_limit_error" {
		t.Errorf("expected error type rate_limit_error, got %q", errObj["type"])
	}
}

func TestStreamingPreStreamError_Anthropic_AuthError(t *testing.T) {
	provErr := &provider.ProviderError{
		Code:    http.StatusUnauthorized,
		Message: "Invalid API key",
	}

	srv := newAnthropicServer(&errorCompleter{err: provErr}, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model":      "test-model",
		"stream":     true,
		"max_tokens": 100,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/messages", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/messages: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 401, got %d; body: %s", resp.StatusCode, body)
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	errObj, _ := raw["error"].(map[string]any)
	if errObj["type"] != "authentication_error" {
		t.Errorf("expected error type authentication_error, got %q", errObj["type"])
	}
}

func TestStreamingPreStreamError_Gemini(t *testing.T) {
	provErr := &provider.ProviderError{
		Code:    http.StatusTooManyRequests,
		Message: "Rate limit reached",
	}

	srv := newGeminiServer(&errorCompleter{err: provErr}, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{"text": "hello"},
				},
			},
		},
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/models/test-model:streamGenerateContent?alt=sse", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST streamGenerateContent: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 429, got %d; body: %s", resp.StatusCode, body)
	}

	// Verify it's a JSON error response, not SSE
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	// Verify Gemini error envelope
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	errObj, ok := raw["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errObj["status"] != "RESOURCE_EXHAUSTED" {
		t.Errorf("expected status RESOURCE_EXHAUSTED, got %q", errObj["status"])
	}
}

func TestStreamingPreStreamError_Gemini_BadRequest(t *testing.T) {
	provErr := &provider.ProviderError{
		Code:    http.StatusBadRequest,
		Message: "Invalid request",
	}

	srv := newGeminiServer(&errorCompleter{err: provErr}, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{"text": "hello"},
				},
			},
		},
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/models/test-model:streamGenerateContent?alt=sse", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST streamGenerateContent: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 400, got %d; body: %s", resp.StatusCode, body)
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	errObj, _ := raw["error"].(map[string]any)
	if errObj["status"] != "INVALID_ARGUMENT" {
		t.Errorf("expected status INVALID_ARGUMENT, got %q", errObj["status"])
	}
}

// --- Mid-stream error tests ---
// These test that when the upstream provider returns an error AFTER streaming
// has already started (headers sent), the error event in the stream contains
// the correct error type (not a hardcoded generic type).

// readSSEEvents reads all SSE events from a response body.
func readSSEEvents(body io.Reader) []map[string]string {
	var events []map[string]string
	scanner := bufio.NewScanner(body)

	current := map[string]string{}

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			if len(current) > 0 {
				events = append(events, current)
				current = map[string]string{}
			}
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			current["event"] = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			current["data"] = strings.TrimPrefix(line, "data: ")
		}
	}

	if len(current) > 0 {
		events = append(events, current)
	}

	return events
}

func TestMidStreamError_Chat_RateLimit(t *testing.T) {
	provErr := &provider.ProviderError{
		Code:       http.StatusTooManyRequests,
		Message:    "Rate limit reached",
		RetryAfter: 30 * time.Second,
	}

	srv := newChatServer(&midStreamErrorCompleter{err: provErr}, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model":  "test-model",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/chat/completions", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()

	// Status is 200 because headers were already sent with first chunk
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200 (streaming already started), got %d; body: %s", resp.StatusCode, body)
	}

	// Read all SSE events and find the error event
	body, _ := io.ReadAll(resp.Body)
	events := readSSEEvents(bytes.NewReader(body))

	var errorEvent map[string]any
	for _, ev := range events {
		data := ev["data"]
		if data == "" || data == "[DONE]" {
			continue
		}

		var parsed map[string]any
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			continue
		}

		if errObj, ok := parsed["error"].(map[string]any); ok {
			errorEvent = errObj
			break
		}
	}

	if errorEvent == nil {
		t.Fatalf("expected error event in stream, got events: %s", body)
	}

	if errorEvent["type"] != "rate_limit_exceeded" {
		t.Errorf("expected error type rate_limit_exceeded, got %q", errorEvent["type"])
	}
}

func TestMidStreamError_Chat_ServerError(t *testing.T) {
	provErr := &provider.ProviderError{
		Code:    http.StatusInternalServerError,
		Message: "Internal server error",
	}

	srv := newChatServer(&midStreamErrorCompleter{err: provErr}, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model":  "test-model",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/chat/completions", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	events := readSSEEvents(bytes.NewReader(body))

	var errorEvent map[string]any
	for _, ev := range events {
		data := ev["data"]
		if data == "" || data == "[DONE]" {
			continue
		}

		var parsed map[string]any
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			continue
		}

		if errObj, ok := parsed["error"].(map[string]any); ok {
			errorEvent = errObj
			break
		}
	}

	if errorEvent == nil {
		t.Fatalf("expected error event in stream, got events: %s", body)
	}

	// 500 → mapped to 502 by StatusCodeFromError → "server_error"
	if errorEvent["type"] != "server_error" {
		t.Errorf("expected error type server_error, got %q", errorEvent["type"])
	}
}

func TestMidStreamError_Anthropic_RateLimit(t *testing.T) {
	provErr := &provider.ProviderError{
		Code:       http.StatusTooManyRequests,
		Message:    "Rate limit reached",
		RetryAfter: 30 * time.Second,
	}

	srv := newAnthropicServer(&midStreamErrorCompleter{err: provErr}, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"model":      "test-model",
		"stream":     true,
		"max_tokens": 100,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/messages", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/messages: %v", err)
	}
	defer resp.Body.Close()

	// Streaming already started, so status is 200
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d; body: %s", resp.StatusCode, body)
	}

	// Read SSE events and find the error event
	body, _ := io.ReadAll(resp.Body)
	events := readSSEEvents(bytes.NewReader(body))

	var errorEvent map[string]any
	for _, ev := range events {
		if ev["event"] != "error" {
			continue
		}

		var parsed map[string]any
		if err := json.Unmarshal([]byte(ev["data"]), &parsed); err != nil {
			continue
		}

		if errObj, ok := parsed["error"].(map[string]any); ok {
			errorEvent = errObj
			break
		}
	}

	if errorEvent == nil {
		t.Fatalf("expected error event in stream, got events: %s", body)
	}

	if errorEvent["type"] != "rate_limit_error" {
		t.Errorf("expected error type rate_limit_error, got %q", errorEvent["type"])
	}
}

func TestMidStreamError_Gemini_RateLimit(t *testing.T) {
	provErr := &provider.ProviderError{
		Code:    http.StatusTooManyRequests,
		Message: "Rate limit reached",
	}

	srv := newGeminiServer(&midStreamErrorCompleter{err: provErr}, "test-model")
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{"text": "hello"},
				},
			},
		},
	})

	resp, err := srv.Client().Post(srv.URL+"/v1/models/test-model:streamGenerateContent?alt=sse", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST streamGenerateContent: %v", err)
	}
	defer resp.Body.Close()

	// Streaming already started, so status is 200
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d; body: %s", resp.StatusCode, body)
	}

	// Read SSE events and find one with an error field
	body, _ := io.ReadAll(resp.Body)
	events := readSSEEvents(bytes.NewReader(body))

	var errorObj map[string]any
	for _, ev := range events {
		data := ev["data"]
		if data == "" {
			continue
		}

		var parsed map[string]any
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			continue
		}

		if errField, ok := parsed["error"].(map[string]any); ok {
			errorObj = errField
			break
		}
	}

	if errorObj == nil {
		t.Fatalf("expected error in stream, got events: %s", body)
	}

	if errorObj["status"] != "RESOURCE_EXHAUSTED" {
		t.Errorf("expected status RESOURCE_EXHAUSTED, got %q", errorObj["status"])
	}
}
