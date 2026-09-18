package bedrock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/require"
)

func TestDictionarySchemaStreamsAsText(t *testing.T) {
	var body bytes.Buffer
	encoder := eventstream.NewEncoder()
	for _, event := range []struct{ kind, payload string }{
		{"messageStart", `{"role":"assistant"}`},
		{"contentBlockStart", `{"contentBlockIndex":0,"start":{"toolUse":{"toolUseId":"schema_1","name":"labels"}}}`},
		{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"toolUse":{"input":"{\"colour\":\"blue\"}"}}}`},
		{"contentBlockStop", `{"contentBlockIndex":0}`},
		{"messageStop", `{"stopReason":"tool_use"}`},
		{"metadata", `{"usage":{"inputTokens":2,"outputTokens":3,"totalTokens":5},"metrics":{"latencyMs":1}}`},
	} {
		require.NoError(t, encoder.Encode(&body, eventstream.Message{
			Headers: eventstream.Headers{
				{Name: ":message-type", Value: eventstream.StringValue("event")},
				{Name: ":event-type", Value: eventstream.StringValue(event.kind)},
				{Name: ":content-type", Value: eventstream.StringValue("application/json")},
			},
			Payload: []byte(event.payload),
		}))
	}
	client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
		var request map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Contains(t, request, "toolConfig")
		require.NotContains(t, request, "outputConfig")
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(body.Bytes())), Request: r}, nil
	})}
	c := &Completer{
		Config: &Config{model: "anthropic.claude-sonnet-4-6"},
		client: bedrockruntime.New(bedrockruntime.Options{
			Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
			HTTPClient: client, BaseEndpoint: aws.String("http://bedrock.test"),
		}),
	}
	options := &provider.CompleteOptions{Schema: &provider.Schema{
		Name: "labels", Properties: map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
	}}
	var acc provider.CompletionAccumulator
	for delta, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("Return labels as JSON")}, options) {
		require.NoError(t, err)
		acc.Add(*delta)
	}
	result := acc.Result()
	require.JSONEq(t, `{"colour":"blue"}`, result.Text())
	require.Empty(t, result.Message.ToolCalls(), "schema emulation must not expose a caller tool")
}

func TestConvertConverseInput_DictionarySchemaFallback(t *testing.T) {
	for name, raw := range map[string]string{
		"dictionary":  `{"type":"object","properties":{},"additionalProperties":{"type":"string"}}`,
		"open object": `{"type":"object","properties":{},"additionalProperties":true}`,
		"nested":      `{"type":"object","properties":{"labels":{"type":"object","additionalProperties":{"type":"string"}}}}`,
		"array items": `{"type":"object","properties":{"labels":{"type":"array","items":{"type":"object","additionalProperties":{"type":"string"}}}}}`,
		"definitions": `{"type":"object","properties":{"labels":{"$ref":"#/$defs/labels"}},"$defs":{"labels":{"type":"object","additionalProperties":{"type":"string"}}}}`,
		"union":       `{"type":"object","properties":{"labels":{"anyOf":[{"type":"null"},{"type":"object","additionalProperties":{"type":"string"}}]}}}`,
	} {
		strictFalse := false
		for i, strict := range []*bool{nil, &strictFalse} {
			t.Run(fmt.Sprintf("%s/strict=%d", name, i), func(t *testing.T) {
				var schema map[string]any
				require.NoError(t, json.Unmarshal([]byte(raw), &schema))
				c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-4-6"}}
				opts := &provider.CompleteOptions{
					Schema:           &provider.Schema{Name: "labels", Strict: strict, Properties: schema},
					ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive},
				}
				req, err := c.convertConverseInput([]provider.Message{provider.UserMessage("Return labels as JSON")}, opts)
				require.NoError(t, err)
				require.Nil(t, req.OutputConfig, "dictionaries cannot use the native grammar")
				require.NotNil(t, req.ToolConfig)
				require.Len(t, req.ToolConfig.Tools, 1)
				spec := req.ToolConfig.Tools[0].(*types.ToolMemberToolSpec).Value
				require.Nil(t, spec.Strict)
				data, err := spec.InputSchema.(*types.ToolInputSchemaMemberJson).Value.MarshalSmithyDocument()
				require.NoError(t, err)
				require.JSONEq(t, raw, string(data), "fallback must preserve arbitrary keys and values")
				choice, ok := req.ToolConfig.ToolChoice.(*types.ToolChoiceMemberTool)
				require.True(t, ok, "schema tool must be forced")
				require.Equal(t, "labels", aws.ToString(choice.Value.Name))
				_, thinking := c.converseAdditionalFields(nil, opts)
				require.False(t, thinking.Enabled, "forced tools cannot use adaptive thinking")
				after, err := json.Marshal(schema)
				require.NoError(t, err)
				require.JSONEq(t, raw, string(after), "source schema was mutated")
			})
		}
	}
}

func TestConvertConverseInput_SchemaAnnotationsKeepNativeOutput(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"object",
		"properties":{"additionalProperties":{"type":"string"}},
		"additionalProperties":false,
		"examples":[{"additionalProperties":"example value"}]
	}`), &schema))
	c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-4-6"}}
	req, err := c.convertConverseInput([]provider.Message{provider.UserMessage("Return JSON")}, &provider.CompleteOptions{
		Schema: &provider.Schema{Name: "result", Properties: schema},
	})
	require.NoError(t, err)
	require.NotNil(t, req.OutputConfig)
	require.Nil(t, req.ToolConfig)
}

func TestDictionaryFallbackDoesNotDisableExplicitStrict(t *testing.T) {
	strict := true
	c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-4-6"}}
	req, err := c.convertConverseInput([]provider.Message{provider.UserMessage("Return JSON")}, &provider.CompleteOptions{
		Schema: &provider.Schema{Name: "labels", Strict: &strict, Properties: map[string]any{
			"type": "object", "additionalProperties": map[string]any{"type": "string"},
		}},
	})
	require.NoError(t, err)
	// Strict requests keep upstream grammar validation; they must never
	// silently degrade to a non-strict tool to accommodate an invalid schema.
	require.NotNil(t, req.OutputConfig)
	require.Nil(t, req.ToolConfig)
}
