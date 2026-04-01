package test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	openaiProvider "github.com/adrianliechti/wingman/pkg/provider/openai"
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

	statusCode := provider.StatusCodeFromError(gotErr, 0)
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
	statusCode := provider.StatusCodeFromError(gotErr, 0)
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

	statusCode := provider.StatusCodeFromError(gotErr, 0)
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

func TestStreamingRateLimitError_HandlerLevel(t *testing.T) {
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

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// For streaming, the handler writes SSE headers (200) then emits
	// a response.failed event when the completer returns an error.
	if resp.StatusCode == http.StatusOK {
		if !bytes.Contains(body, []byte("response.failed")) {
			t.Errorf("expected response.failed event in SSE stream, got: %s", bodyStr)
		}
		return
	}

	// Some implementations may return the error status directly if
	// the error occurs before any streaming output is written.
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected status 200 or 429, got %d; body: %s", resp.StatusCode, bodyStr)
	}
}
