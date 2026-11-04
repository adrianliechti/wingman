package openai

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestRequestSchemasPreserveNumbers(t *testing.T) {
	const values = `[9007199254740993,1.234567890123456789,1e400,null]`
	var schema map[string]any
	if err := jsonutil.Unmarshal([]byte(`{"type":"object","properties":{"id":{"enum":`+values+`}}}`), &schema); err != nil {
		t.Fatal(err)
	}
	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{Name: "lookup", Parameters: schema},
			{Kind: provider.ToolKindToolSearch, Execution: "client", Parameters: map[string]any{"type": "object", "properties": map[string]any{"maximum": jsonutil.Number("10"), "enum": []any{jsonutil.Number("1"), jsonutil.Number("2")}}}},
		},
		Schema: &provider.Schema{Properties: schema},
	}
	c, _ := NewCompleter("http://localhost", "test")
	chat, err := c.convertCompletionRequest(nil, &provider.CompleteOptions{Tools: options.Tools[:1], Schema: options.Schema})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := NewResponder("http://localhost", "test")
	response, err := r.convertResponsesRequest(nil, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []any{chat, response} {
		data, err := json.Marshal(request, json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(data), values) != 2 {
			t.Fatalf("schema numbers changed: %s", data)
		}
	}
	responseData, err := json.Marshal(response, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(responseData), `"maximum":10`) || !strings.Contains(string(responseData), `"enum":[1,2]`) {
		t.Fatalf("tool_search schema numbers encoded as strings: %s", responseData)
	}
}

func TestIncomingToolSearchNumbers(t *testing.T) {
	const args = `{"id":9007199254740993,"values":[1.234567890123456789,1e400,null]}`
	const tools = `[{"type":"function","name":"lookup","parameters":{"enum":[9007199254740993,1e400]}}]`
	var wire strings.Builder
	for _, event := range []string{
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"tool_search_call","id":"search_1","execution":"server","arguments":` + args + `}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"tool_search_output","execution":"server","tools":` + tools + `}}`,
		`{"type":"response.completed","response":{"status":"completed","output":[]}}`,
	} {
		wire.WriteString("data: " + event + "\n\n")
	}
	client := &http.Client{Transport: responderLifecycleTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire.String())), Request: r}, nil
	})}
	r, _ := NewResponder("http://upstream.test", "test", WithClient(client))
	var acc provider.CompletionAccumulator
	for delta, err := range r.Complete(t.Context(), nil, nil) {
		if err != nil {
			t.Fatal(err)
		}
		acc.Add(*delta)
	}
	parts := acc.Result().Message.Content
	if len(parts) != 2 || parts[0].ToolCall == nil || parts[0].ToolCall.Arguments != args || parts[1].ToolResult == nil || string(parts[1].ToolResult.Payload) != tools {
		t.Fatalf("search data changed: %+v", parts)
	}
}

func TestToolSearchReplayPreservesJSONNumbers(t *testing.T) {
	const arguments = `{"id":9007199254740993,"values":[1.234567890123456789,1e400,null,false]}`
	r := &Responder{}
	input, err := r.convertResponsesInput([]provider.Message{{Role: provider.MessageRoleAssistant, Content: []provider.Content{
		provider.ToolCallContent(provider.ToolCall{ID: "call_1", Kind: provider.ToolKindToolSearch, Execution: "client", Arguments: arguments}),
	}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(input.OfInputItemList[0].OfToolSearchCall.Arguments, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != arguments {
		t.Fatalf("arguments changed: %s", data)
	}
}

func TestToolSearchOutputPreservesSchemaNumbers(t *testing.T) {
	const payload = `[{"type":"function","name":"lookup","parameters":{"enum":[9007199254740993,1.234567890123456789,1e400,null]}}]`
	r := &Responder{}
	input, err := r.convertResponsesInput([]provider.Message{{Role: provider.MessageRoleUser, Content: []provider.Content{
		provider.ToolResultContent(provider.ToolResult{ID: "call_1", Kind: provider.ToolKindToolSearch, Execution: "client", Payload: []byte(payload)}),
	}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(input.OfInputItemList[0].OfToolSearchOutput.Tools, json.Deterministic(true))
	if err != nil || string(data) != payload {
		t.Fatalf("schema changed: %s (%v)", data, err)
	}
}
