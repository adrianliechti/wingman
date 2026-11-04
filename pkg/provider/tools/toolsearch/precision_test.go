package toolsearch

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestCatalogSchemaNumbers(t *testing.T) {
	const schema = `{"enum":[9007199254740993,1.234567890123456789,1e400,null]}`
	for _, payload := range []string{
		`[{"type":"function","name":"lookup","parameters":` + schema + `}]`,
		`[{"type":"namespace","name":"project","tools":[{"type":"function","name":"lookup","parameters":` + schema + `}]}]`,
	} {
		tools := provider.FlattenTools(Tools([]byte(payload)))
		if len(tools) != 1 {
			t.Fatalf("missing tool: %s", payload)
		}
		data, err := json.Marshal(tools[0].Parameters, json.Deterministic(true))
		if err != nil || string(data) != schema {
			t.Fatalf("schema changed: %s (%v)", data, err)
		}
	}
	// Resolving another reference rewrites the catalog; complete definitions
	// must retain their original numbers through that rewrite.
	payload := []byte(`[{"type":"function","name":"lookup","parameters":` + schema + `},{"type":"function","name":"other"}]`)
	messages := []provider.Message{{Content: []provider.Content{provider.ToolResultContent(provider.ToolResult{Kind: provider.ToolKindToolSearch, Payload: payload})}}}
	resolved, err := ResolveResults(messages, []provider.Tool{{Name: "other", Parameters: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatal(err)
	}
	var items []struct {
		Parameters jsontext.Value `json:"parameters"`
	}
	if err := json.Unmarshal(resolved[0].Content[0].ToolResult.Payload, &items); err != nil {
		t.Fatal(err)
	}
	if string(items[0].Parameters) != schema || string(items[1].Parameters) != `{"type":"object"}` {
		t.Fatalf("catalog changed: %+v", items)
	}
	if got := Tools(append(payload, []byte(`[]`)...)); got != nil {
		t.Fatal("accepted trailing JSON")
	}
}
