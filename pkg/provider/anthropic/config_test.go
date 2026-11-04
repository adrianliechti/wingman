package anthropic

import (
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
)

func TestEndpointRouting(t *testing.T) {
	for _, tc := range []struct {
		name, url, model, token, wantAuth, wantPath, version string
		runtime                                              bool
	}{
		{name: "default", token: "explicit", wantAuth: "explicit", wantPath: "/v1/messages"},
		{name: "custom", url: "https://gateway.example/api", token: "explicit", wantAuth: "explicit", wantPath: "/api/v1/messages"},
		{name: "lookalike", url: "https://bedrock-runtime.eu-north-1.amazonaws.com.example", token: "explicit", wantAuth: "explicit", wantPath: "/v1/messages"},
		{name: "direct without token", wantPath: "/v1/messages"},
		{name: "runtime explicit", url: "https://bedrock-runtime.eu-north-1.amazonaws.com", token: "explicit", wantAuth: "explicit", runtime: true},
		{name: "runtime environment", url: "https://bedrock-runtime.eu-north-1.amazonaws.com", wantAuth: "bedrock-env", runtime: true},
		{name: "runtime native URL", url: "https://bedrock-runtime.eu-north-1.amazonaws.com/anthropic", token: "explicit", wantAuth: "explicit", runtime: true},
		{name: "runtime legacy", url: "https://bedrock-runtime.eu-north-1.amazonaws.com", model: "eu.anthropic.claude-haiku-4-5-20251001-v1:0", token: "explicit", wantAuth: "explicit", runtime: true, version: "bedrock-2023-05-31"},
		{name: "mantle explicit", url: "https://bedrock-mantle.eu-north-1.api.aws", token: "explicit", wantAuth: "explicit", wantPath: "/anthropic/v1/messages"},
		{name: "mantle environment", url: "https://bedrock-mantle.eu-north-1.api.aws/anthropic/", wantAuth: "bedrock-env", wantPath: "/anthropic/v1/messages"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "anthropic-env")
			t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "bedrock-env")
			t.Setenv("AWS_REGION", "us-east-1")
			model := tc.model
			if model == "" {
				model = "eu.anthropic.claude-haiku-5-5"
			}
			calls := 0
			client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				wantAuth := tc.wantAuth
				if wantAuth != "" {
					wantAuth = "Bearer " + wantAuth
				}
				if r.Header.Get("Authorization") != wantAuth || r.Header.Get("X-Api-Key") != "" {
					t.Error("incorrect authentication source")
				}
				var body map[string]any
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Fatal(err)
				}
				path := tc.wantPath
				contentType := "text/event-stream"
				data := []byte(lifecycleEvents("end_turn", true))
				if tc.runtime {
					path = "/model/" + model + "/invoke-with-response-stream"
					version := tc.version
					if version == "" {
						version = "2026-10-21"
					}
					if body["anthropic_version"] != version || body["model"] != nil || body["stream"] != nil || r.Header.Get("anthropic-version") != "" {
						t.Fatalf("incorrect Runtime request: %v", body)
					}
					contentType = "application/vnd.amazon.eventstream"
					data = bedrockEventStream(t, string(data))
				} else if body["model"] != model || body["stream"] != true || r.Header.Get("anthropic-version") != "2026-10-21" {
					t.Fatalf("incorrect Messages request: %v", body)
				}
				if r.URL.Path != path {
					t.Errorf("path = %q, want %q", r.URL.Path, path)
				}
				if (tc.runtime || strings.Contains(tc.name, "mantle")) && !strings.Contains(r.URL.Hostname(), ".eu-north-1.") {
					t.Errorf("endpoint region changed: %s", r.URL.Hostname())
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}
			c, err := NewCompleter(tc.url, model, WithToken(tc.token), WithClient(client), WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			var acc provider.CompletionAccumulator
			for chunk, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("hello")}, nil) {
				if err != nil {
					t.Fatal(err)
				}
				acc.Add(*chunk)
			}
			if result := acc.Result(); calls != 1 || result.Text() != "progress" || result.StopReason != provider.StopReasonEndTurn {
				t.Fatalf("calls=%d result=%+v", calls, result)
			}
		})
	}
}

func TestBedrockAWSCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "unused-anthropic-key")
	t.Setenv("ANTHROPIC_AWS_API_KEY", "")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_SESSION_TOKEN", "test-session-token")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
	for _, tc := range []struct{ url, service string }{
		{"https://bedrock-runtime.eu-north-1.amazonaws.com", "bedrock"},
		{"https://bedrock-mantle.eu-north-1.api.aws", "bedrock-mantle"},
	} {
		t.Run(tc.service, func(t *testing.T) {
			called := false
			client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
				called = true
				auth := r.Header.Get("Authorization")
				if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=test-access-key/") || !strings.Contains(auth, "/eu-north-1/"+tc.service+"/aws4_request") || r.Header.Get("X-Amz-Security-Token") != "test-session-token" {
					t.Error("request was not signed with the configured AWS credentials and endpoint region")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))}, nil
			})}
			c, err := NewCompleter(tc.url, "anthropic.claude-sonnet-5", WithClient(client), WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.messages.New(t.Context(), anthropic.BetaMessageNewParams{Model: "anthropic.claude-sonnet-5", MaxTokens: 16, Messages: []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("OK"))}})
			if err != nil || !called {
				t.Fatalf("called=%t error=%v", called, err)
			}
		})
	}
}

func bedrockEventStream(t *testing.T, sse string) []byte {
	t.Helper()
	var body bytes.Buffer
	for line := range strings.SplitSeq(sse, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		payload, err := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(data))})
		if err != nil {
			t.Fatal(err)
		}
		message := eventstream.Message{Payload: payload}
		message.Headers.Set(":message-type", eventstream.StringValue("event"))
		message.Headers.Set(":event-type", eventstream.StringValue("chunk"))
		if err := eventstream.NewEncoder().Encode(&body, message); err != nil {
			t.Fatal(err)
		}
	}
	return body.Bytes()
}
