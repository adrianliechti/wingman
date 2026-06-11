// Package toolsearch bridges the tool-search dialects: OpenAI's tool_search
// tool (server- or client-executed, with defer_loading on tools) and
// Anthropic's tool search tool (tool_search_tool_regex/bm25, server-executed).
//
// On a backend with native server-side search (OpenAI hosted mode, Anthropic),
// the search runs transparently inside the turn — the client only sees the
// eventual call of a discovered tool. Client-executed search is emulated as a
// plain function tool; the tools the client returns in tool_search_output are
// merged back into the toolset on subsequent turns.
package toolsearch

import (
	"encoding/json"

	"github.com/adrianliechti/wingman/pkg/provider"
)

const Name = "tool_search"

// FunctionTool renders a client-executed tool_search tool as a plain function
// tool, for backends without a native equivalent.
func FunctionTool(t provider.Tool) provider.Tool {
	description := t.Description

	if description == "" {
		description = "Search for additional tools that are available but not yet loaded. " +
			"Returns tool definitions that can be called afterwards."
	}

	parameters := t.Parameters

	if len(parameters) == 0 {
		parameters = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "What kind of tool is needed.",
				},
			},
			"required": []string{"query"},
		}
	}

	return provider.Tool{
		Name:        Name,
		Description: description,
		Parameters:  parameters,
	}
}

// Tools decodes a tool_search_output payload (a list of tools in the OpenAI
// Responses wire format) into provider tools.
func Tools(payload []byte) []provider.Tool {
	var raw []struct {
		Type        string         `json:"type"`
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`

		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
		} `json:"tools"`
	}

	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}

	var result []provider.Tool

	for _, t := range raw {
		if t.Type == "namespace" {
			namespace := provider.Tool{
				Name:        t.Name,
				Description: t.Description,
			}

			for _, inner := range t.Tools {
				namespace.Tools = append(namespace.Tools, provider.Tool{
					Name:        inner.Name,
					Description: inner.Description,
					Parameters:  inner.Parameters,
				})
			}

			if len(namespace.Tools) > 0 {
				result = append(result, namespace)
			}

			continue
		}

		if t.Name == "" {
			continue
		}

		result = append(result, provider.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}

	return result
}
