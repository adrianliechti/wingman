package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/toolid"
	"github.com/adrianliechti/wingman/pkg/provider/tools/toolsearch"
	"github.com/anthropics/anthropic-sdk-go"
)

// ParseToolSearchResult converts Claude references into the shared catalog.
// The request's tool declarations supply schemas; references without a catalog
// can be resolved later at the target provider boundary.
func ParseToolSearchResult(id string, data []byte, catalog []provider.Tool) (provider.ToolResult, error) {
	var content anthropic.BetaToolSearchToolResultBlockContentUnion
	if err := json.Unmarshal(data, &content); err != nil {
		return provider.ToolResult{}, err
	}
	result := provider.ToolResult{ID: id, Kind: provider.ToolKindToolSearch, Execution: "server"}
	switch content.Type {
	case "tool_search_tool_search_result":
		var tools []provider.Tool
		for _, reference := range content.ToolReferences {
			tools = append(tools, provider.Tool{Name: reference.ToolName})
		}
		payload, err := toolsearch.Payload(toolsearch.Resolve(tools, catalog))
		result.Payload = payload
		return result, err
	case "tool_search_tool_result_error":
		result.IsError = true
		result.Payload, _ = json.Marshal(map[string]string{"code": string(content.ErrorCode), "message": content.ErrorMessage})
		result.Parts = []provider.Part{{Text: content.ErrorMessage}}
		return result, nil
	default:
		return result, fmt.Errorf("unsupported tool search result %q", content.Type)
	}
}

func ToolSearchResultContent(result provider.ToolResult) anthropic.BetaToolSearchToolResultBlockParamContentUnion {
	if result.IsError {
		var detail struct{ Code, Message string }
		json.Unmarshal(result.Payload, &detail)
		if detail.Code == "" {
			detail.Code = "unavailable"
		}
		error := &anthropic.BetaToolSearchToolResultErrorParam{ErrorCode: anthropic.BetaToolSearchToolResultErrorParamErrorCode(detail.Code)}
		if detail.Message != "" {
			error.ErrorMessage = anthropic.String(detail.Message)
		}
		return anthropic.BetaToolSearchToolResultBlockParamContentUnion{OfRequestToolSearchToolResultError: error}
	}
	references := []anthropic.BetaToolReferenceBlockParam{}
	for _, tool := range provider.FlattenTools(toolsearch.Tools(result.Payload)) {
		references = append(references, anthropic.BetaToolReferenceBlockParam{ToolName: tool.Name})
	}
	return anthropic.BetaToolSearchToolResultBlockParamContentUnion{OfRequestToolSearchToolSearchResultBlock: &anthropic.BetaToolSearchToolSearchResultBlockParam{ToolReferences: references}}
}

func toolSearchCallName(call provider.ToolCall, tools []provider.Tool) string {
	if strings.HasPrefix(call.Name, "tool_search_tool_") {
		return call.Name
	}
	for _, tool := range tools {
		if tool.Kind == provider.ToolKindToolSearch && strings.Contains(tool.Name, "bm25") {
			return "tool_search_tool_bm25"
		}
	}
	return "tool_search_tool_regex"
}

func toolSearchResultBlock(result provider.ToolResult) anthropic.BetaContentBlockParamUnion {
	return anthropic.BetaContentBlockParamUnion{OfToolSearchToolResult: &anthropic.BetaToolSearchToolResultBlockParam{
		ToolUseID: toolSearchID(result.ID), Content: ToolSearchResultContent(result),
	}}
}

func toolSearchID(id string) string {
	if strings.HasPrefix(id, "srvtoolu_") {
		return id
	}
	return "srvtoolu_" + strings.ReplaceAll(toolid.Sanitize(id, 119), "-", "_")
}
