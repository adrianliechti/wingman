package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/agent/react"
	"github.com/adrianliechti/wingman/pkg/provider"
	claude "github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/bedrock"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/stretchr/testify/require"
)

type observingCompleter struct {
	provider.Completer
	after func(*provider.Completion)
}

func (c observingCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		for delta, err := range c.Completer.Complete(ctx, messages, options) {
			if !yield(delta, err) {
				return
			}
			if delta != nil {
				c.after(delta)
			}
		}
	}
}

func TestNativeThinkingStreamsBeforeItemCompletion(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("AWS_REGION", "eu-central-1")
	for _, backend := range []string{"anthropic", "bedrock"} {
		for _, text := range []string{"", "still thinking"} {
			t.Run(backend+"/"+text, func(t *testing.T) {
				wire := strings.Replace(claudeFeatureStream, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\"", fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":%q}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\"", text), 1)
				if backend == "bedrock" {
					var buffer bytes.Buffer
					for _, e := range [][2]string{{"messageStart", `{"role":"assistant"}`}, {"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":""}}}`}, {"contentBlockDelta", fmt.Sprintf(`{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":%q}}}`, text)}, {"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"signature":"signed-state"}}}`}, {"contentBlockStop", `{"contentBlockIndex":0}`}, {"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"text":"done"}}`}, {"contentBlockStop", `{"contentBlockIndex":1}`}, {"messageStop", `{"stopReason":"end_turn"}`}} {
						require.NoError(t, eventstream.NewEncoder().Encode(&buffer, eventstream.Message{Headers: eventstream.Headers{{Name: ":message-type", Value: eventstream.StringValue("event")}, {Name: ":event-type", Value: eventstream.StringValue(e[0])}, {Name: ":content-type", Value: eventstream.StringValue("application/json")}}, Payload: []byte(e[1])}))
					}
					wire = buffer.String()
				}
				client := featureClient(t, wire, func(*http.Request, map[string]any) {})
				var p provider.Completer
				var err error
				if backend == "anthropic" {
					p, err = claude.NewCompleter("https://upstream.invalid", "claude-sonnet-4-6", claude.WithClient(client))
				} else {
					p, err = bedrock.NewCompleter("eu.anthropic.claude-sonnet-4-6", bedrock.WithClient(client))
				}
				require.NoError(t, err)
				rec := httptest.NewRecorder()
				observed := false
				p = observingCompleter{p, func(delta *provider.Completion) {
					if delta.Message == nil {
						return
					}
					for _, c := range delta.Message.Content {
						if c.Reasoning != nil && c.Reasoning.SummaryText() != "" {
							observed = true
							require.Contains(t, rec.Body.String(), "event: response.reasoning_summary_text.delta", "thinking was held until completion")
							require.NotContains(t, rec.Body.String(), "event: response.completed")
						}
					}
				}}
				featureRouter(p).ServeHTTP(rec, httptest.NewRequest("POST", "/responses", strings.NewReader(`{"model":"target","input":"think","stream":true,"reasoning":{"summary":"auto"},"include":["reasoning.encrypted_content"]}`)))
				require.Equal(t, 200, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "event: response.completed")
				if text != "" {
					require.True(t, observed)
				} else {
					require.NotContains(t, rec.Body.String(), "event: response.reasoning_summary_part.added")
					require.NotContains(t, rec.Body.String(), "event: response.reasoning_content_part.added")
				}
			})
		}
	}
}

func TestChatToolsWithTrailingTextAcrossEndpoints(t *testing.T) {
	var wire strings.Builder
	for _, delta := range []string{
		`{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"lookup","arguments":"{"}}]}`,
		`{"tool_calls":[{"index":1,"id":"c2","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`,
		`{"tool_calls":[{"index":0,"function":{"arguments":"} "}}]}`,
		`{"content":"\n"}`,
	} {
		fmt.Fprintf(&wire, "data: {\"id\":\"chat1\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":null}]}\n\n", delta)
	}
	wire.WriteString("data: {\"id\":\"chat1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	for _, api := range []string{"messages", "responses"} {
		t.Run(api, func(t *testing.T) {
			backend, err := openai.NewCompleter("https://upstream.invalid", "compatible-model", openai.WithClient(featureClient(t, wire.String(), func(*http.Request, map[string]any) {})))
			require.NoError(t, err)
			body := map[string]any{"model": "target", "stream": true}
			if api == "messages" {
				body["messages"], body["max_tokens"] = []any{map[string]any{"role": "user", "content": "check"}}, 1024
			} else {
				body["input"] = "check"
			}
			rec := featurePost(t, featureRouter(backend), "/"+api, body)
			require.Equal(t, 200, rec.Code, rec.Body.String())
			events, err := harness.ParseSSE(rec.Body)
			require.NoError(t, err)
			calls, terminal := 0, false
			open := map[float64]bool{}
			for _, event := range events {
				require.NotEqual(t, "error", event.Event, event.Raw)
				switch event.Event {
				case "content_block_start":
					open[event.Data["index"].(float64)] = true
					if event.Data["content_block"].(map[string]any)["type"] == "tool_use" {
						calls++
					}
				case "content_block_stop":
					delete(open, event.Data["index"].(float64))
				case "content_block_delta":
					require.True(t, open[event.Data["index"].(float64)], "late delta: %s", event.Raw)
				case "response.output_item.done":
					if event.Data["item"].(map[string]any)["type"] == "function_call" {
						calls++
					}
				case "message_stop", "response.completed":
					terminal = true
				}
			}
			require.True(t, terminal)
			require.Equal(t, 2, calls)
			require.Empty(t, open)
		})
	}
}

func TestReactToolsOnGeminiAndNonClaudeBedrock(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("AWS_REGION", "eu-central-1")
	for _, model := range []string{"gemini-test", "amazon.nova-pro-v1:0", "arn:aws:bedrock:eu-central-1:123456789012:application-inference-profile/test"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				requests := 0
				client := &http.Client{Transport: featureTransport(func(r *http.Request) (*http.Response, error) {
					requests++
					body, err := io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}
					if requests == 2 {
						require.Contains(t, string(body), "READY")
					}
					if requests > 2 {
						return nil, fmt.Errorf("unexpected third agent request")
					}
					var wire []byte
					if strings.HasPrefix(model, "gemini") {
						step := `{"type":"function_call","id":"c1","name":"lookup","arguments":{}}`
						if requests == 2 {
							step = `{"type":"model_output","content":[{"type":"text","text":"done"}]}`
						}
						wire = []byte(fmt.Sprintf("data: {\"event_type\":\"interaction.created\",\"interaction\":{\"id\":\"i1\",\"model\":\"gemini-test\",\"status\":\"in_progress\"}}\n\ndata: {\"event_type\":\"step.start\",\"index\":0,\"step\":%s}\n\ndata: {\"event_type\":\"step.stop\",\"index\":0}\n\ndata: {\"event_type\":\"interaction.completed\",\"interaction\":{\"id\":\"i1\",\"status\":\"completed\"}}\n\n", step))
					} else {
						var sent map[string]any
						require.NoError(t, json.Unmarshal(body, &sent))
						if fields, ok := sent["additionalModelRequestFields"].(map[string]any); ok {
							require.NotContains(t, fields, "tool_choice")
						}
						events := [][2]string{{"messageStart", `{"role":"assistant"}`}, {"contentBlockStart", `{"contentBlockIndex":0,"start":{"toolUse":{"toolUseId":"c1","name":"lookup"}}}`}, {"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"toolUse":{"input":"{}"}}}`}, {"contentBlockStop", `{"contentBlockIndex":0}`}, {"messageStop", `{"stopReason":"tool_use"}`}}
						if requests == 2 {
							events = [][2]string{{"messageStart", `{"role":"assistant"}`}, {"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"done"}}`}, {"contentBlockStop", `{"contentBlockIndex":0}`}, {"messageStop", `{"stopReason":"end_turn"}`}}
						}
						var buffer bytes.Buffer
						for _, event := range events {
							require.NoError(t, eventstream.NewEncoder().Encode(&buffer, eventstream.Message{Headers: eventstream.Headers{{Name: ":message-type", Value: eventstream.StringValue("event")}, {Name: ":event-type", Value: eventstream.StringValue(event[0])}, {Name: ":content-type", Value: eventstream.StringValue("application/json")}}, Payload: []byte(event[1])}))
						}
						wire = buffer.Bytes()
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(bytes.NewReader(wire)), Request: r}, nil
				})}
				var backend provider.Completer
				var err error
				if strings.HasPrefix(model, "gemini") {
					backend, err = google.NewCompleter(model, google.WithClient(client), google.WithToken("test"))
				} else {
					backend, err = bedrock.NewCompleter(model, bedrock.WithClient(client), bedrock.WithRegion("eu-central-1"))
				}
				require.NoError(t, err)
				lookup := &agentLookup{}
				agent, err := react.New("target", react.WithCompleter(backend), react.WithTools(lookup))
				require.NoError(t, err)
				rec := featurePost(t, featureRouter(agent), "/responses", map[string]any{"model": "target", "input": "Use lookup", "stream": stream})
				require.Equal(t, 200, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "done")
				require.Equal(t, 2, requests)
				require.Equal(t, 1, lookup.calls)
			})
		}
	}
}
