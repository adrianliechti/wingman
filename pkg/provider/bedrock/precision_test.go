package bedrock

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestToolReplayPreservesJSONNumbers(t *testing.T) {
	const arguments = `{"id":9007199254740993,"values":[1.234567890123456789,1e400,null,false]}`
	c := &Completer{Config: &Config{model: "anthropic.claude-test"}}
	req, err := c.convertConverseInput([]provider.Message{{Role: provider.MessageRoleAssistant, Content: []provider.Content{
		provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "lookup", Arguments: arguments}),
	}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	use := req.Messages[0].Content[0].(*types.ContentBlockMemberToolUse)
	data, err := use.Value.Input.MarshalSmithyDocument()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["id"]) != "9007199254740993" || string(got["values"]) != "[1.234567890123456789,1e400,null,false]" {
		t.Fatalf("tool input changed: %s", data)
	}
}

func TestToolSchemaPreservesJSONNumbers(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-test"}}
	schema := map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"enum": []any{json.Number("9007199254740993")}}}}
	config, err := c.convertToolConfig([]provider.Tool{{Name: "lookup", Parameters: schema}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := config.Tools[0].(*types.ToolMemberToolSpec).Value.InputSchema.(*types.ToolInputSchemaMemberJson)
	data, err := spec.Value.MarshalSmithyDocument()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Properties map[string]struct{ Enum []json.RawMessage }
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Properties["id"].Enum[0]) != "9007199254740993" {
		t.Fatalf("schema number changed: %s", data)
	}
	if _, ok := schema["properties"].(map[string]any)["id"].(map[string]any)["enum"].([]any)[0].(json.Number); !ok {
		t.Fatal("mutated caller schema")
	}
}

func TestDocumentTypedCollectionsPreserveNumbers(t *testing.T) {
	data, err := newDocument(map[string]any{
		"enum":  []json.Number{"9007199254740993", "1e400"},
		"items": []map[string]any{{"const": json.Number("1.234567890123456789")}},
	}).MarshalSmithyDocument()
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["enum"]) != `[9007199254740993,1e400]` || string(fields["items"]) != `[{"const":1.234567890123456789}]` {
		t.Fatalf("numbers changed: %s", data)
	}
}

func TestThinkingSignatureFragments(t *testing.T) {
	for _, stops := range []bool{true, false} {
		t.Run(fmt.Sprint(stops), func(t *testing.T) {
			events := [][2]string{
				{"messageStart", `{"role":"assistant"}`},
				{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"first"}}}`},
				{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"signature":"SIG_"}}}`},
				{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"signature":"1"}}}`},
				{"contentBlockStop", `{"contentBlockIndex":0}`},
				{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"reasoningContent":{"text":"second"}}}`},
				{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"reasoningContent":{"signature":"SIG_"}}}`},
				{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"reasoningContent":{"signature":"2"}}}`},
			}
			if stops {
				events = append(events, [2]string{"contentBlockStop", `{"contentBlockIndex":1}`})
			}
			events = append(events, [2]string{"messageStop", `{"stopReason":"end_turn"}`})
			var acc provider.CompletionAccumulator
			for delta, err := range scriptedCompleter(t, events...).Complete(t.Context(), nil, nil) {
				if err != nil {
					t.Fatal(err)
				}
				acc.Add(*delta)
			}
			parts := acc.Result().Message.Content
			if len(parts) != 2 {
				t.Fatalf("expected 2 thinking blocks, got %+v", parts)
			}
			for i, want := range []string{"first", "second"} {
				if r := parts[i].Reasoning; r == nil || r.ID != "" || r.SummaryText() != "" || r.Text() != want || r.Signature != fmt.Sprintf("SIG_%d", i+1) {
					t.Errorf("block %d: %+v", i, r)
				}
			}
		})
	}
}
