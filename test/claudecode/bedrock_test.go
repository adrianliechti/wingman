package claudecode

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"slices"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/bedrock"
	"github.com/adrianliechti/wingman/server/anthropic"
	"github.com/adrianliechti/wingman/test/harness"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/go-chi/chi/v5"
)

type bedrockTransport func(*http.Request) (*http.Response, error)

func (f bedrockTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Run the real CLI, Wingman handler and Bedrock adapter with a trailing
// system instruction. Its presence must not depend on which CLI version is
// installed; Converse must receive it at the top level and end on the user turn.
func TestClaudeCodeOfflineBedrock(t *testing.T) {
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("Claude Code is not installed")
	}
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")

	var wire bytes.Buffer
	encoder := eventstream.NewEncoder()
	for _, event := range []struct{ kind, payload string }{
		{"messageStart", `{"role":"assistant"}`},
		{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"WINGMAN_E2E_OK"}}`},
		{"contentBlockStop", `{"contentBlockIndex":0}`},
		{"messageStop", `{"stopReason":"end_turn"}`},
		{"metadata", `{"usage":{"inputTokens":10,"outputTokens":5,"totalTokens":15},"metrics":{"latencyMs":1}}`},
	} {
		if err := encoder.Encode(&wire, eventstream.Message{
			Headers: eventstream.Headers{
				{Name: ":message-type", Value: eventstream.StringValue("event")},
				{Name: ":event-type", Value: eventstream.StringValue(event.kind)},
				{Name: ":content-type", Value: eventstream.StringValue("application/json")},
			},
			Payload: []byte(event.payload),
		}); err != nil {
			t.Fatal(err)
		}
	}

	var systemTexts []string
	client := &http.Client{Transport: bedrockTransport(func(r *http.Request) (*http.Response, error) {
		defer r.Body.Close()
		var request struct {
			System []struct {
				Text string `json:"text"`
			} `json:"system"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.UnmarshalRead(r.Body, &request); err != nil {
			return nil, err
		}
		if len(request.Messages) == 0 || request.Messages[len(request.Messages)-1].Role != "user" {
			t.Error("Bedrock conversation must end with a user message")
		}
		for _, message := range request.Messages {
			if message.Role == "system" {
				t.Error("Converse does not support mid-conversation system messages")
			}
		}
		for _, block := range request.System {
			systemTexts = append(systemTexts, block.Text)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(wire.Bytes())), Request: r}, nil
	})}
	p, err := bedrock.NewCompleter("eu.anthropic.claude-opus-5", bedrock.WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	const model = "claude-opus-5"
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, p)
	router := chi.NewRouter()
	router.Route("/v1", anthropic.New(cfg).Attach)
	const trailingInstruction = "WINGMAN_E2E_SYSTEM_GUIDANCE"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			var request map[string]jsontext.Value
			if err := json.UnmarshalRead(r.Body, &request); err != nil {
				t.Error(err)
				http.Error(w, "invalid CLI request", http.StatusBadRequest)
				return
			}
			r.Body.Close()
			var messages []jsontext.Value
			if err := json.Unmarshal(request["messages"], &messages); err != nil {
				t.Error(err)
				http.Error(w, "invalid CLI messages", http.StatusBadRequest)
				return
			}
			instruction, _ := json.Marshal(map[string]any{"role": "system", "content": []map[string]string{{"type": "text", "text": trailingInstruction}}})
			request["messages"], _ = json.Marshal(append(messages, instruction))
			body, _ := json.Marshal(request)
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(upstream.Close)
	answer, exchanges, _ := runClaude(t, binary, harness.Endpoint{Name: "offline", BaseURL: upstream.URL + "/v1", APIKey: "test"}, model, "Reply with WINGMAN_E2E_OK.", "", "")
	if answer != "WINGMAN_E2E_OK" {
		t.Errorf("unexpected CLI output: %q", answer)
	}
	checkExchanges(t, exchanges, model)

	for _, exchange := range exchanges {
		u, err := url.Parse(exchange.Path)
		if err != nil || u.Path != "/v1/messages" {
			continue
		}
		var request struct {
			Messages []struct {
				Role    string         `json:"role"`
				Content jsontext.Value `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(exchange.Request), &request); err != nil {
			t.Fatal(err)
		}
		for _, message := range request.Messages {
			if message.Role != "system" {
				continue
			}
			var blocks []struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(message.Content, &blocks); err != nil {
				t.Fatal(err)
			}
			for _, block := range blocks {
				if !slices.Contains(systemTexts, block.Text) {
					t.Errorf("lost CLI system instruction %q", block.Text)
				}
			}
		}
	}
	if !slices.Contains(systemTexts, trailingInstruction) {
		t.Fatal("lost trailing system instruction")
	}
}
