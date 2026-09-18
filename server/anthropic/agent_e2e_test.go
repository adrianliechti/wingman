package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/agent/assistant"
	"github.com/adrianliechti/wingman/pkg/agent/react"
	"github.com/adrianliechti/wingman/pkg/provider"
	claude "github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/stretchr/testify/require"
)

type agentLookup struct {
	calls int
}

func (p *agentLookup) Tools(context.Context) ([]provider.Tool, error) {
	deferred := true
	return []provider.Tool{{Name: "lookup", Deferred: &deferred, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}, nil
}

func (p *agentLookup) Execute(context.Context, string, map[string]any) (any, error) {
	p.calls++
	return "READY", nil
}

func TestMessagesAgentEffortDefaultE2E(t *testing.T) {
	for _, kind := range []string{"assistant", "react"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", kind, stream), func(t *testing.T) {
				var sent map[string]any
				backend := featureBackend(t, "claude", func(_ *http.Request, body map[string]any) { sent = body })
				var agent provider.Completer
				var err error
				if kind == "assistant" {
					agent, err = assistant.New("target", assistant.WithCompleter(backend), assistant.WithEffort(provider.EffortLow))
				} else {
					agent, err = react.New("target", react.WithCompleter(backend), react.WithEffort(provider.EffortLow))
				}
				require.NoError(t, err)
				rec := featurePost(t, featureRouter(agent), "/messages", map[string]any{
					"model": "target", "max_tokens": 1024, "stream": stream,
					"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
				})
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "done")
				output, _ := sent["output_config"].(map[string]any)
				require.Equal(t, "low", output["effort"], "configured agent effort was lost")
			})
		}
	}
}

func TestHostedSearchThenAgentToolE2E(t *testing.T) {
	for _, backendName := range []string{"claude", "openai"} {
		for _, api := range []string{"messages", "responses"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", backendName, api, stream), func(t *testing.T) {
					var requests []map[string]any
					client := &http.Client{Transport: featureTransport(func(r *http.Request) (*http.Response, error) {
						defer r.Body.Close()
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							return nil, err
						}
						requests = append(requests, body)
						if len(requests) > 2 {
							return nil, fmt.Errorf("agent made an unexpected third request")
						}
						wire := searchStream(backendName)
						if len(requests) == 2 {
							wire = claudeFeatureStream
							if backendName == "openai" {
								wire = responsesFeatureStream
							}
						}
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire)), Request: r}, nil
					})}
					var backend provider.Completer
					var err error
					if backendName == "claude" {
						backend, err = claude.NewCompleter("https://upstream.invalid", "claude-fable-5-1", claude.WithClient(client), claude.WithMaxRetries(0))
					} else {
						backend, err = openai.NewResponder("https://upstream.invalid", "gpt-5.4", openai.WithClient(client), openai.WithMaxRetries(0))
					}
					require.NoError(t, err)
					lookup := &agentLookup{}
					agent, err := react.New("target", react.WithCompleter(backend), react.WithTools(lookup))
					require.NoError(t, err)
					body := map[string]any{"model": "target", "stream": stream}
					history := []any{map[string]any{"role": "user", "content": "Use lookup"}}
					if api == "messages" {
						body["max_tokens"], body["messages"] = 1024, history
						body["tools"] = []any{map[string]any{"type": "tool_search_tool_regex_20251119", "name": "tool_search_tool_regex"}}
					} else {
						body["max_output_tokens"], body["input"] = 1024, history
						body["tools"] = []any{map[string]any{"type": "tool_search"}}
					}
					rec := featurePost(t, featureRouter(agent), "/"+api, body)
					require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
					require.Contains(t, rec.Body.String(), "done")
					require.Equal(t, 1, lookup.calls)
					require.Len(t, requests, 2)
					replayed, err := json.Marshal(requests[1])
					require.NoError(t, err)
					require.Contains(t, string(replayed), "READY")
					if backendName == "claude" {
						require.Contains(t, string(replayed), `"type":"server_tool_use"`)
						require.Contains(t, string(replayed), `"type":"tool_search_tool_result"`)
					} else {
						require.Contains(t, string(replayed), `"type":"tool_search_call"`)
						require.Contains(t, string(replayed), `"type":"tool_search_output"`)
					}
				})
			}
		}
	}
}
