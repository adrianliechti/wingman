package tool

import (
	"encoding/json"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TextResult(text string) provider.ToolResult {
	return provider.ToolResult{Parts: []provider.Part{{Text: text}}}
}

// RenderResult preserves provider-specific content and passes strings verbatim.
// Other values are encoded as JSON for the model.
func RenderResult(p Provider, name string, value any) (provider.ToolResult, error) {
	if r, ok := p.(Resulter); ok {
		return r.Result(name, value), nil
	}
	if text, ok := value.(string); ok {
		return TextResult(text), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return provider.ToolResult{}, err
	}
	return TextResult(string(data)), nil
}
