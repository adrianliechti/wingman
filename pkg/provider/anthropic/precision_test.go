package anthropic

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestRequestSchemasPreserveNumbers(t *testing.T) {
	const enum = `[9007199254740993,1.234567890123456789,1e400,null]`
	var schema map[string]any
	if err := jsonutil.UnmarshalRead(strings.NewReader(`{"type":"object","properties":{"id":{"enum":`+enum+`}}}`), &schema); err != nil {
		t.Fatal(err)
	}
	c, _ := NewCompleter("http://localhost", "claude-test")
	for _, strict := range []bool{false, true} {
		req, err := c.convertMessageRequest(nil, &provider.CompleteOptions{
			Tools:  []provider.Tool{{Name: "lookup", Parameters: schema, Strict: &strict}},
			Schema: &provider.Schema{Properties: schema},
		})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(req, json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(data), `"enum":`+enum) != 2 {
			t.Fatalf("schema numbers changed: %s", data)
		}
	}
}
