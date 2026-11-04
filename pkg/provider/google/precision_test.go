package google

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestInteractionRequestNumbers(t *testing.T) {
	const values = `[9007199254740993,1.234567890123456789,null]`
	var schema map[string]any
	if err := jsonutil.UnmarshalRead(strings.NewReader(`{"type":"object","properties":{"id":{"enum":`+values+`}}}`), &schema); err != nil {
		t.Fatal(err)
	}
	req, err := convertInteraction("gemini-3.8-flash", []provider.Message{{Role: provider.MessageRoleAssistant, Content: []provider.Content{
		provider.ToolCallContent(provider.ToolCall{ID: "c1", Name: "lookup", Arguments: `{"id":` + values + `}`}),
	}}}, &provider.CompleteOptions{Tools: []provider.Tool{{Name: "lookup", Parameters: schema}}, Schema: &provider.Schema{Properties: schema}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(req, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), values) != 3 {
		t.Fatalf("numbers changed: %s", data)
	}
}
