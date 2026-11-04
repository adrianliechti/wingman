package errors

import (
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	provideranthropic "github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/server/anthropic"
	"github.com/adrianliechti/wingman/server/openai/chat"
	"github.com/adrianliechti/wingman/server/openai/responses"
	"github.com/adrianliechti/wingman/test/harness"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/go-chi/chi/v5"
)

type anthropicErrorTransport struct {
	status      int
	contentType string
	body        []byte
}

func (tr anthropicErrorTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: tr.status, Request: r,
		Header: http.Header{"Content-Type": {tr.contentType}, "Retry-After": {"2.25"}},
		Body:   io.NopCloser(bytes.NewReader(tr.body)),
	}, nil
}

// Run real Anthropic SDK decoding and provider conversion through each handler.
// A failure before downstream output must retain a failure HTTP status, even
// when the upstream accepted the stream with HTTP 200.
func TestAnthropicErrorsEndToEnd(t *testing.T) {
	const message = "Bedrock is unable to process your request."
	for _, tc := range []struct {
		name, kind, errorType     string
		status, wantStatus        int
		openaiType, anthropicType string
	}{
		{"Bedrock HTTP unavailable", "http", "", 503, 503, "service_unavailable_error", "api_error"},
		{"Bedrock HTTP throttled", "http", "", 429, 429, "rate_limit_exceeded", "rate_limit_error"},
		{"Bedrock stream unavailable", "bedrock", "serviceUnavailableException", 200, 503, "service_unavailable_error", "api_error"},
		{"Bedrock stream throttled", "bedrock", "throttlingException", 200, 429, "rate_limit_exceeded", "rate_limit_error"},
		{"Anthropic stream overloaded", "sse", "overloaded_error", 200, 529, "service_unavailable_error", "overloaded_error"},
		{"Anthropic unknown stream error", "sse", "future_error", 200, 502, "server_error", "api_error"},
	} {
		for _, progress := range []bool{false, true} {
			if tc.kind == "http" && progress {
				continue
			}
			for _, stream := range []bool{false, true} {
				for _, path := range []string{"/chat/completions", "/responses", "/messages"} {
					t.Run(fmt.Sprintf("%s/progress=%t/stream=%t%s", tc.name, progress, stream, path), func(t *testing.T) {
						tr := anthropicErrorTransport{status: tc.status, contentType: "application/json", body: []byte(`{"message":"` + message + `"}`)}
						url := "https://bedrock-runtime.eu-west-1.amazonaws.com"
						if tc.kind != "http" {
							tr.body = anthropicErrorStream(t, tc.kind, tc.errorType, message, progress)
							tr.contentType = "application/vnd.amazon.eventstream"
							if tc.kind == "sse" {
								url = "https://anthropic.test"
								tr.contentType = "text/event-stream"
							}
						}
						completer, err := provideranthropic.NewCompleter(url, "claude-test", provideranthropic.WithToken("test"), provideranthropic.WithClient(&http.Client{Transport: tr}), provideranthropic.WithMaxRetries(0))
						if err != nil {
							t.Fatal(err)
						}
						cfg := &config.Config{Policy: noop.New()}
						cfg.RegisterCompleter("test-model", completer)
						router := chi.NewRouter()
						chat.New(cfg).Attach(router)
						responses.New(cfg).Attach(router)
						anthropic.New(cfg).Attach(router)
						body := fmt.Sprintf(`{"model":"test-model","stream":%t,"messages":[{"role":"user","content":"hello"}],"max_tokens":32}`, stream)
						if path == "/responses" {
							body = fmt.Sprintf(`{"model":"test-model","stream":%t,"input":"hello"}`, stream)
						}
						rec := httptest.NewRecorder()
						router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
						wantType := tc.openaiType
						if path == "/messages" {
							wantType = tc.anthropicType
						}
						if !stream || !progress {
							wantStatus := tc.wantStatus
							if path != "/messages" && wantStatus == 529 {
								wantStatus = http.StatusServiceUnavailable
							}
							if rec.Code != wantStatus || rec.Header().Get("Retry-After") != "3" || rec.Header().Get("Content-Type") != "application/json" {
								t.Fatalf("status=%d headers=%v body=%s", rec.Code, rec.Header(), rec.Body)
							}
							var payload errorResponse
							if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
								t.Fatal(err)
							}
							if payload.Error.Type != wantType || payload.Error.Message != message {
								t.Fatalf("error=%+v", payload.Error)
							}
							if path != "/messages" && payload.Error.Code != tc.errorType {
								t.Fatalf("lost provider error type: %+v", payload.Error)
							}
							return
						}
						if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") || !strings.Contains(rec.Body.String(), "retry: 2250\n") {
							t.Fatalf("status=%d headers=%v body=%s", rec.Code, rec.Header(), rec.Body)
						}
						events, err := harness.ParseSSE(rec.Body)
						if err != nil {
							t.Fatal(err)
						}
						foundError, foundFailed := false, false
						for _, event := range events {
							if event.Event == "response.completed" || event.Event == "message_stop" {
								t.Fatal("failed stream reported success")
							}
							if event.Event == "response.failed" {
								foundFailed = true
								response := event.Data["response"].(map[string]any)
								failure := response["error"].(map[string]any)
								wantCode := "server_error"
								if tc.wantStatus == 429 {
									wantCode = "rate_limit_exceeded"
								}
								if response["status"] != "failed" || failure["code"] != wantCode || failure["message"] != message || failure["headers"].(map[string]any)["Retry-After"] != "3" {
									t.Fatalf("response failure=%v", failure)
								}
								continue
							}
							failure, ok := event.Data["error"].(map[string]any)
							key := "type"
							wantValue := wantType
							if path == "/responses" && event.Event == "error" {
								failure, ok, key = event.Data, true, "code"
								wantValue = tc.errorType
							}
							if ok {
								foundError = true
								if failure[key] != wantValue || failure["message"] != message {
									t.Fatalf("stream error=%v", failure)
								}
							}
						}
						if !foundError || path == "/responses" && !foundFailed {
							t.Fatal("missing terminal stream error")
						}
					})
				}
			}
		}
	}
}

func anthropicErrorStream(t *testing.T, kind, errorType, message string, progress bool) []byte {
	t.Helper()
	var body bytes.Buffer
	writeFrame := func(frame eventstream.Message) {
		t.Helper()
		if err := eventstream.NewEncoder().Encode(&body, frame); err != nil {
			t.Fatal(err)
		}
	}
	if progress {
		for _, data := range []string{
			`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"model":"claude-test","usage":{"input_tokens":1,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		} {
			if kind == "sse" {
				var event struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&body, "event: %s\ndata: %s\n\n", event.Type, data)
			} else {
				payload, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(data))})
				frame := eventstream.Message{Payload: payload}
				frame.Headers.Set(":message-type", eventstream.StringValue("event"))
				frame.Headers.Set(":event-type", eventstream.StringValue("chunk"))
				writeFrame(frame)
			}
		}
	}
	if kind == "sse" {
		payload, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": errorType, "message": message}})
		fmt.Fprintf(&body, "event: error\ndata: %s\n\n", payload)
	} else {
		payload, _ := json.Marshal(map[string]string{"message": message})
		frame := eventstream.Message{Payload: payload}
		frame.Headers.Set(":message-type", eventstream.StringValue("exception"))
		frame.Headers.Set(":exception-type", eventstream.StringValue(errorType))
		writeFrame(frame)
	}
	return body.Bytes()
}
