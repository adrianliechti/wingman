package toolsearch

import (
	"encoding/json"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// Payload uses the existing tool-search result catalog format. Keeping named
// definitions here lets providers with references and providers with inline
// schemas share ToolResult without carrying an opaque provider response.
func Payload(tools []provider.Tool) ([]byte, error) {
	items := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		item := map[string]any{"type": "function", "name": tool.Name, "defer_loading": true}
		if tool.Description != "" {
			item["description"] = tool.Description
		}
		if tool.Parameters != nil {
			item["parameters"] = tool.Parameters
		}
		if tool.Strict != nil {
			item["strict"] = *tool.Strict
		}
		if len(tool.Tools) > 0 {
			children, err := Payload(tool.Tools)
			if err != nil {
				return nil, err
			}
			item["type"], item["tools"] = "namespace", json.RawMessage(children)
			delete(item, "defer_loading")
		}
		items = append(items, item)
	}
	return json.Marshal(items)
}

func Resolve(tools, catalog []provider.Tool) []provider.Tool {
	definitions := map[string]provider.Tool{}
	for _, tool := range provider.FlattenTools(catalog) {
		definitions[tool.Name] = tool
	}
	result := make([]provider.Tool, len(tools))
	for i, tool := range tools {
		if len(tool.Tools) > 0 {
			tool.Tools = Resolve(tool.Tools, catalog)
		} else if tool.Parameters == nil {
			if definition, ok := definitions[tool.Name]; ok {
				tool = definition
			}
		}
		result[i] = tool
	}
	return result
}

func ResolveResults(messages []provider.Message, catalog []provider.Tool) ([]provider.Message, error) {
	definitions := map[string]provider.Tool{}
	for _, tool := range provider.FlattenTools(catalog) {
		definitions[tool.Name] = tool
	}
	aliases := provider.ToolAliases(catalog)
	namespaces := map[string]provider.Tool{}
	for _, tool := range catalog {
		if len(tool.Tools) > 0 {
			namespaces[tool.Name] = tool
		}
	}
	result := make([]provider.Message, len(messages))
	for i, message := range messages {
		message.Content = append([]provider.Content(nil), message.Content...)
		for j, part := range message.Content {
			if output := part.ToolResult; output != nil && output.Kind == provider.ToolKindToolSearch && !output.IsError {
				copy := *output
				var items []map[string]any
				if err := json.Unmarshal(output.Payload, &items); err != nil {
					return nil, err
				}
				// Expand references and restore namespaces flattened by other
				// providers. Complete definitions keep their original fields.
				changed := false
				normalized := make([]map[string]any, 0, len(items))
				groups := map[string]map[string]any{}
				for _, item := range items {
					if item["type"] != "function" {
						normalized = append(normalized, item)
						continue
					}
					name, _ := item["name"].(string)
					if definition, ok := definitions[name]; ok && item["parameters"] == nil {
						item["parameters"] = definition.Parameters
						item["description"] = definition.Description
						if definition.Strict != nil {
							item["strict"] = *definition.Strict
						}
						item["defer_loading"] = true
						changed = true
					}
					if alias, ok := aliases[name]; ok {
						item["name"] = alias.Name
						group := groups[alias.Namespace]
						if group == nil {
							description := namespaces[alias.Namespace].Description
							if description == "" {
								description = "Tools in the " + alias.Namespace + " namespace."
							}
							group = map[string]any{"type": "namespace", "name": alias.Namespace, "description": description, "tools": []any{}}
							groups[alias.Namespace] = group
							normalized = append(normalized, group)
						}
						group["tools"] = append(group["tools"].([]any), item)
						changed = true
					} else {
						normalized = append(normalized, item)
					}
				}
				if changed {
					var err error
					copy.Payload, err = json.Marshal(normalized)
					if err != nil {
						return nil, err
					}
				}
				message.Content[j].ToolResult = &copy
			}
		}
		result[i] = message
	}
	return result, nil
}

// SplitResults exposes hosted results as separate items for APIs whose input
// distinguishes assistant calls from tool results, retaining their order.
func SplitResults(message provider.Message) []provider.Message {
	var result []provider.Message
	current := message
	current.Content = nil
	for _, part := range message.Content {
		if output := part.ToolResult; output != nil && output.Kind == provider.ToolKindToolSearch && output.Execution != "client" {
			if len(current.Content) > 0 {
				result = append(result, current)
				current.Content = nil
			}
			result = append(result, provider.Message{Role: provider.MessageRoleUser, Content: []provider.Content{part}})
		} else {
			current.Content = append(current.Content, part)
		}
	}
	if len(current.Content) > 0 {
		result = append(result, current)
	}
	return result
}

// Inline is the portable fallback for APIs without hosted discovery: expose
// the catalog as ordinary tools and remove already-executed search events.
// Client-executed search stays a normal function call with a JSON result.
func Inline(messages []provider.Message, options *provider.CompleteOptions) ([]provider.Message, *provider.CompleteOptions) {
	if options == nil {
		return messages, &provider.CompleteOptions{}
	}
	cloned := *options
	cloned.Tools = nil
	defined := map[string]bool{}
	for _, tool := range options.Tools {
		if tool.Kind == provider.ToolKindToolSearch {
			if tool.Execution != "client" {
				continue
			}
			tool = FunctionTool(tool)
		}
		cloned.Tools = append(cloned.Tools, tool)
		for _, flat := range provider.FlattenTools([]provider.Tool{tool}) {
			defined[flat.Name] = true
		}
	}
	result := make([]provider.Message, 0, len(messages))
	for _, message := range messages {
		var content []provider.Content
		for _, part := range message.Content {
			if call := part.ToolCall; call != nil && call.Kind == provider.ToolKindToolSearch {
				if call.Execution != "client" {
					continue
				}
				copy := *call
				copy.Kind, copy.Name = provider.ToolKindFunction, Name
				part.ToolCall = &copy
			}
			if output := part.ToolResult; output != nil && output.Kind == provider.ToolKindToolSearch {
				for _, tool := range provider.FlattenTools(Resolve(Tools(output.Payload), options.Tools)) {
					if !defined[tool.Name] && tool.Parameters != nil {
						cloned.Tools = append(cloned.Tools, tool)
						defined[tool.Name] = true
					}
				}
				if output.Execution != "client" {
					continue
				}
				copy := *output
				copy.Kind = provider.ToolKindFunction
				copy.Parts = []provider.Part{{Text: string(output.Payload)}}
				part.ToolResult = &copy
			}
			content = append(content, part)
		}
		if len(content) > 0 {
			message.Content = content
			result = append(result, message)
		}
	}
	return result, &cloned
}
